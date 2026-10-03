import { api, errorDetail, type ApiContext } from "@servediff/api";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

type LoadState =
  | { status: "loading" }
  | { status: "error"; detail: string }
  | { status: "ready"; session: ApiContext };

interface Catalog {
  contexts: ApiContext[];
  selectedId: string;
  select: (id: string) => void;
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
    <Select
      value={catalog.selectedId || null}
      items={catalog.contexts.map((context) => ({
        value: context.id,
        label: context.name,
      }))}
      onValueChange={(value) => {
        if (typeof value === "string") catalog.select(value);
      }}
    >
      <SelectTrigger aria-label="Review context" className="context-switcher">
        <SelectValue placeholder="Select a repository or piped diff" />
      </SelectTrigger>
      <SelectContent align="start" alignItemWithTrigger={false}>
        {catalog.contexts.map((context) => (
          <SelectItem key={context.id} value={context.id}>
            <span className="context-option">
              <span>{context.name}</span>
              <span className="context-option-detail">
                {context.kind === "capture" ? "Piped diff" : context.root}
              </span>
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [contexts, setContexts] = useState<ApiContext[]>([]);
  const [selectedId, setSelectedId] = useState(contextFromUrl);
  const [catalogLoaded, setCatalogLoaded] = useState(false);
  const [catalogError, setCatalogError] = useState("");
  const [state, setState] = useState<LoadState>({ status: "loading" });
  const [attempt, setAttempt] = useState(0);
  const select = useCallback((id: string) => {
    if (id === contextFromUrl()) return;
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
    const timer = setInterval(() => void loadCatalog(), 3000);
    const visible = () => {
      if (!document.hidden) void loadCatalog();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      controller.abort();
      clearInterval(timer);
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
    () => ({ contexts, selectedId, select }),
    [contexts, selectedId, select],
  );
  const ready = state.status === "ready" && state.session.id === selectedId;
  const empty = catalogLoaded && !contexts.length && !selectedId;
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
    </CatalogContext>
  );
}

export function useSession() {
  const session = useContext(SessionContext);
  if (!session) throw new Error("Page components require SessionProvider");
  return session;
}
