import { spawn } from "node:child_process";
import type {
  ExtensionAPI,
  ExtensionContext,
} from "@earendil-works/pi-coding-agent";

type SettledContext = Pick<ExtensionContext, "cwd"> & {
  sessionManager: Pick<ExtensionContext["sessionManager"], "getSessionId">;
};
type PiHost = {
  on(
    event: "agent_settled",
    handler: (event: unknown, context: SettledContext) => void,
  ): unknown;
};

export function register(pi: PiHost, request = requestSync): void {
  pi.on("agent_settled", (_event, context) => {
    request(context.cwd, context.sessionManager.getSessionId());
  });
}

export default function servediff(pi: ExtensionAPI): void {
  register(pi);
}

// The CLI schedules its own detached worker. Do not wait for collection or upload.
function requestSync(directory: string, sessionID: string): void {
  try {
    const child = spawn(
      process.env.SERVEDIFF_BINARY ?? "servediff",
      ["hook", "--agent", "pi", "--path", directory, "--run-id", sessionID],
      { detached: true, stdio: "ignore", shell: false },
    );
    // Unref the deadline too: spawn's timeout option keeps Node hosts alive.
    const deadline = setTimeout(() => child.kill(), 5_000);
    deadline.unref();
    // Launch failures must never become unhandled errors or transcript output.
    child.once("error", () => clearTimeout(deadline));
    child.once("exit", () => clearTimeout(deadline));
    child.unref();
  } catch {
    // Invalid launch settings cannot disrupt an agent session.
  }
}
