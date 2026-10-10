import { spawn } from "node:child_process";
export function register(pi, request = requestSync) {
    pi.on("agent_settled", (_event, context) => {
        request(context.cwd, context.sessionManager.getSessionId(), context.sessionManager.getSessionName?.());
    });
}
export default function diffx(pi) {
    register(pi);
}
// The CLI schedules its own detached worker. Do not wait for collection or upload.
function requestSync(directory, sessionID, sessionName) {
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
        if (sessionName)
            args.push("--session-name", sessionName);
        const child = spawn(process.env.DIFFX_BINARY ?? "diffx", args, {
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
