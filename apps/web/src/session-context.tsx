import { api, errorDetail, type ApiSession } from "@servediff/api";
import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useState,
} from "react";

type LoadState =
  | { status: "loading" }
  | { status: "error"; detail: string }
  | { status: "ready"; session: ApiSession };

const SessionContext = createContext<ApiSession | null>(null);

export function capabilityEnabled(capability: { state: string }) {
  return capability.state === "enabled";
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<LoadState>({ status: "loading" });
  useEffect(() => {
    const controller = new AbortController();
    void api
      .GET("/api/v1/session", { signal: controller.signal })
      .then(({ data, error }) => {
        if (controller.signal.aborted) return;
        if (data) setState({ status: "ready", session: data });
        else
          setState({
            status: "error",
            detail: errorDetail(error, "Unable to load session"),
          });
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted)
          setState({
            status: "error",
            detail: errorDetail(error, "Unable to load session"),
          });
      });
    return () => controller.abort();
  }, []);
  if (state.status !== "ready")
    return (
      <main className="session-status" role="status">
        <h1>
          {state.status === "loading"
            ? "Loading servediff"
            : "Cannot load servediff"}
        </h1>
        <p>
          {state.status === "loading"
            ? "Reading session capabilities…"
            : state.detail}
        </p>
      </main>
    );
  return <SessionContext value={state.session}>{children}</SessionContext>;
}

export function useSession() {
  const session = useContext(SessionContext);
  if (!session) throw new Error("Page components require SessionProvider");
  return session;
}
