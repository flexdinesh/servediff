import { api, errorDetail, type ApiContext } from "@servediff/api";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type RefObject,
} from "react";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { ProjectPicker, ProjectPickerTrigger } from "./ProjectPicker.tsx";
import { contextHasChanges } from "./project-picker.ts";

type LoadState =
  | { status: "loading" }
  | { status: "error"; detail: string }
  | { status: "ready"; session: ApiContext };

interface Catalog {
  contexts: ApiContext[];
  selectedId: string;
  select: (id: string) => void;
  deleteContext: (id: string) => Promise<void>;
  pickerOpen: boolean;
  openPicker: () => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
}
const CatalogContext = createContext<Catalog | null>(null);
const SessionContext = createContext<ApiContext | null>(null);

export function capabilityEnabled(capability: { state: string }) {
  return capability.state === "enabled";
}

function contextFromUrl() {
  const match = /^\/contexts\/([^/]+)\/?$/.exec(window.location.pathname);
  if (!match?.[1]) return "";
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return "";
  }
}

export function ContextSwitcher() {
  const catalog = useContext(CatalogContext);
  if (!catalog || !catalog.contexts.length) return null;
  return (
    <ProjectPickerTrigger
      current={catalog.contexts.find(
        (context) => context.id === catalog.selectedId,
      )}
      open={catalog.pickerOpen}
      onOpen={catalog.openPicker}
      triggerRef={catalog.triggerRef}
    />
  );
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [contexts, setContexts] = useState<ApiContext[]>([]);
  const [selectedId, setSelectedId] = useState(contextFromUrl);
  const [catalogLoaded, setCatalogLoaded] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [pickerOpen, setPickerOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const statusRef = useRef<HTMLElement>(null);
  const catalogGeneration = useRef(0);
  const restorePickerFocus = useRef(false);
  const openPicker = useCallback(() => setPickerOpen(true), []);
  const select = useCallback((id: string) => {
    if (id === contextFromUrl()) {
      window.history.replaceState(
        null,
        "",
        `/contexts/${encodeURIComponent(id)}`,
      );
      return;
    }
    restorePickerFocus.current = true;
    window.history.pushState(null, "", `/contexts/${encodeURIComponent(id)}`);
    setSelectedId(id);
    setState({ status: "loading" });
  }, []);
  const deleteContext = useCallback(
    async (id: string) => {
      const { error, response } = await api.DELETE(
        "/api/v2/contexts/{contextId}",
        {
          params: { path: { contextId: id } },
        },
      );
      if (!response.ok && response.status !== 404)
        throw new Error(
          errorDetail(error, "Unable to delete review context. Try again."),
        );
      catalogGeneration.current++;
      const remaining = contexts.filter((context) => context.id !== id);
      setContexts((current) => current.filter((context) => context.id !== id));
      if (contextFromUrl() === id) {
        const next = remaining.find(contextHasChanges);
        restorePickerFocus.current = true;
        window.history.replaceState(
          null,
          "",
          next ? `/contexts/${encodeURIComponent(next.id)}` : "/",
        );
        setSelectedId(next?.id ?? "");
        setState({ status: "loading" });
      }
      setAttempt((current) => current + 1);
    },
    [contexts],
  );
  useEffect(() => {
    const handleNavigation = () => {
      setSelectedId(contextFromUrl());
      setState({ status: "loading" });
    };
    window.addEventListener("popstate", handleNavigation);
    return () => window.removeEventListener("popstate", handleNavigation);
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    let busy = false;
    let pending = false;
    async function loadCatalog() {
      if (document.hidden) return;
      if (busy) {
        pending = true;
        return;
      }
      busy = true;
      const generation = catalogGeneration.current;
      try {
        const entries: ApiContext[] = [];
        let cursor: string | undefined;
        do {
          const { data, error } = await api.GET("/api/v2/contexts", {
            params: { query: { limit: 100, ...(cursor ? { cursor } : {}) } },
            signal: controller.signal,
          });
          if (!data)
            throw new Error(
              errorDetail(error, "Unable to list review contexts"),
            );
          entries.push(...data.contexts);
          cursor = data.nextCursor ?? undefined;
        } while (cursor && !controller.signal.aborted);
        if (controller.signal.aborted) return;
        if (generation !== catalogGeneration.current) {
          pending = true;
          return;
        }
        setContexts(entries);
        setCatalogLoaded(true);
        setCatalogError("");
        const query = new URLSearchParams(window.location.search);
        const checkout = query.get("watch");
        const source = query.get("source");
        const watching = checkout !== null && source !== null;
        const first = watching
          ? entries.find(
              (entry) =>
                entry.observation?.checkoutKey === checkout &&
                entry.observation.sourceId === source &&
                entry.availability === "available",
            )
          : entries.find(contextHasChanges);
        if (
          first &&
          (watching || !contextFromUrl()) &&
          first.id !== contextFromUrl()
        ) {
          window.history.replaceState(
            null,
            "",
            `/contexts/${encodeURIComponent(first.id)}${watching ? window.location.search : ""}`,
          );
          setSelectedId(first.id);
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted)
          setCatalogError(errorDetail(error, "Unable to list review contexts"));
      } finally {
        busy = false;
        if (pending && !controller.signal.aborted) {
          pending = false;
          void loadCatalog();
        }
      }
    }
    void loadCatalog();
    const events = new EventSource("/api/v2/events");
    const updateCatalog = () => void loadCatalog();
    events.addEventListener("message", updateCatalog);
    events.addEventListener("ingestion", updateCatalog);
    events.addEventListener("open", updateCatalog);
    const visible = () => {
      if (!document.hidden) void loadCatalog();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      controller.abort();
      events.close();
      document.removeEventListener("visibilitychange", visible);
    };
  }, [attempt]);
  useEffect(() => {
    if (!selectedId) return;
    const controller = new AbortController();
    setState({ status: "loading" });
    void api
      .GET("/api/v2/contexts/{contextId}", {
        params: { path: { contextId: selectedId } },
        signal: controller.signal,
      })
      .then(({ data, error }) => {
        if (controller.signal.aborted) return;
        if (data) setState({ status: "ready", session: data });
        else
          setState({
            status: "error",
            detail: errorDetail(error, "Unable to load review context"),
          });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setState({
            status: "error",
            detail: errorDetail(error, "Unable to load review context"),
          });
      });
    return () => controller.abort();
  }, [selectedId, attempt]);
  const catalog = useMemo(
    () => ({
      contexts,
      selectedId,
      select,
      deleteContext,
      pickerOpen,
      openPicker,
      triggerRef,
    }),
    [contexts, selectedId, select, deleteContext, pickerOpen, openPicker],
  );
  const ready = state.status === "ready" && state.session.id === selectedId;
  const empty = catalogLoaded && !selectedId;
  useEffect(() => {
    if (!restorePickerFocus.current || (!empty && state.status === "loading"))
      return;
    restorePickerFocus.current = false;
    if (empty) statusRef.current?.focus();
    else triggerRef.current?.focus();
  }, [state, empty]);
  return (
    <CatalogContext value={catalog}>
      {ready ? (
        <SessionContext value={state.session}>{children}</SessionContext>
      ) : (
        <>
          <header className="topbar">
            <a className="brand" href="/" aria-label="servediff home">
              servediff
            </a>
            <Separator className="header-divider" orientation="vertical" />
            <ContextSwitcher />
          </header>
          <main
            className="session-status"
            role="status"
            ref={statusRef}
            tabIndex={-1}
          >
            <h1>
              {empty
                ? contexts.length
                  ? "No changes to review"
                  : "No review contexts yet"
                : state.status === "error" || catalogError
                  ? "Cannot load servediff"
                  : "Loading servediff"}
            </h1>
            <p>
              {empty
                ? "Run servediff . to watch a checkout, or servediff sync to publish remotely."
                : state.status === "error"
                  ? state.detail
                  : catalogError || "Reading review context…"}
            </p>
            {(state.status === "error" || catalogError) && (
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setState({ status: "loading" });
                  setCatalogError("");
                  setAttempt((current) => current + 1);
                }}
              >
                Retry
              </Button>
            )}
          </main>
        </>
      )}
      <ProjectPicker
        contexts={contexts}
        selectedId={selectedId}
        select={select}
        open={pickerOpen}
        onOpenChange={setPickerOpen}
        triggerRef={triggerRef}
      />
    </CatalogContext>
  );
}

export function useSession() {
  const session = useContext(SessionContext);
  if (!session) throw new Error("Page components require SessionProvider");
  return session;
}

export function usePinContext() {
  const catalog = useContext(CatalogContext);
  if (!catalog) throw new Error("Context pinning requires SessionProvider");
  const { select, selectedId } = catalog;
  return useCallback(() => {
    if (new URLSearchParams(window.location.search).has("watch")) {
      select(selectedId);
    }
  }, [select, selectedId]);
}

export function useDeleteContext() {
  const catalog = useContext(CatalogContext);
  if (!catalog) throw new Error("Context deletion requires SessionProvider");
  return catalog.deleteContext;
}
