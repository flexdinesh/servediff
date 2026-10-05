import { spawn } from "node:child_process";
export function register(pi, request = requestSync) {
    pi.on("agent_settled", (_event, context) => {
        request(context.cwd, context.sessionManager.getSessionId());
    });
}
export default function servediff(pi) {
    register(pi);
}
// The CLI schedules its own detached worker. Do not wait for collection or upload.
function requestSync(directory, sessionID) {
    try {
        const args = [
            "hook",
            "--harness",
            "pi",
            "--path",
            directory,
            "--run-id",
            sessionID,
        ];
        const child = spawn(process.env.SERVEDIFF_BINARY ?? "servediff", args, {
            detached: true,
            stdio: "ignore",
            shell: false,
        });
        // Unref the deadline too: spawn's timeout option keeps Node hosts alive.
        const deadline = setTimeout(() => child.kill(), 5_000);
        deadline.unref();
        // Launch failures must never become unhandled errors or transcript output.
        child.once("error", () => clearTimeout(deadline));
        child.once("exit", () => clearTimeout(deadline));
        child.unref();
    }
    catch {
        // Invalid launch settings cannot disrupt an agent session.
    }
}
