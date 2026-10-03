import { subscribeChanges } from "./change-events.ts";
import {
  api,
  errorDetail,
  type ApiContext,
  type ApiRepository,
} from "@servediff/api";
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

type LoadState =
  | { status: "loading" }
  | { status: "error"; detail: string }
  | { status: "ready"; session: ApiContext };

interface Catalog {
  contexts: ApiContext[];
  repositories: ApiRepository[];
  selectedId: string;
  select: (id: string) => void;
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
  const [repositories, setRepositories] = useState<ApiRepository[]>([]);
  const [catalogEpoch, setCatalogEpoch] = useState(0);
  const [contexts, setContexts] = useState<ApiContext[]>([]);
  const [selectedId, setSelectedId] = useState(contextFromUrl);
  const [catalogLoaded, setCatalogLoaded] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const [pickerOpen, setPickerOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const restorePickerFocus = useRef(false);
  const discovered = useCallback(
    (items: ApiContext[]) =>
      setContexts((current) => [
        ...current.filter((item) => !items.some((next) => next.id === item.id)),
        ...items,
      ]),
    [],
  );
  const openPicker = useCallback(() => {
    setPickerOpen(true);
    setCatalogEpoch((current) => current + 1);
  }, []);
  const select = useCallback((id: string) => {
    if (id === contextFromUrl()) return;
    restorePickerFocus.current = true;
    window.history.pushState(null, "", `/contexts/${encodeURIComponent(id)}`);
    setSelectedId(id);
    setState({ status: "loading" });
  }, []);
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
    async function loadCatalog() {
      if (busy || document.hidden) return;
      busy = true;
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
        const repositoryResult = await api.GET("/api/v2/repositories", {
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        if (!repositoryResult.data)
          throw new Error(
            errorDetail(repositoryResult.error, "Unable to list repositories"),
          );
        const registered = repositoryResult.data.repositories;
        setRepositories(registered);
        setContexts(entries);
        setCatalogLoaded(true);
        setCatalogError("");
        const first = entries[0];
        if (!contextFromUrl() && first) {
          window.history.replaceState(
            null,
            "",
            `/contexts/${encodeURIComponent(first.id)}`,
          );
          setSelectedId(first.id);
        }
      } catch (error: unknown) {
        if (!controller.signal.aborted)
          setCatalogError(errorDetail(error, "Unable to list review contexts"));
      } finally {
        busy = false;
      }
    }
    void loadCatalog();
    const unsubscribe = subscribeChanges((event) => {
      if (event.kind === "catalog" || event.kind === "reconnect")
        void loadCatalog();
    });
    return () => {
      controller.abort();
      unsubscribe();
    };
  }, [attempt, catalogEpoch]);
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
      repositories,
      selectedId,
      select,
      pickerOpen,
      openPicker,
      triggerRef,
    }),
    [contexts, repositories, selectedId, select, pickerOpen, openPicker],
  );
  const ready = state.status === "ready" && state.session.id === selectedId;
  const empty = catalogLoaded && !contexts.length && !selectedId;
  useEffect(() => {
    if (!restorePickerFocus.current || state.status === "loading") return;
    restorePickerFocus.current = false;
    triggerRef.current?.focus();
  }, [state]);
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
          <main className="session-status" role="status">
            <h1>
              {empty
                ? "No review contexts yet"
                : state.status === "error" || catalogError
                  ? "Cannot load servediff"
                  : "Loading servediff"}
            </h1>
            <p>
              {empty
                ? "Run servediff . in a repository, or pipe a diff into servediff."
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
        repositories={repositories}
        onDiscovered={discovered}
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
