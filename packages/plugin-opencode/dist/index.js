import { spawn } from "node:child_process";
function record(value) {
    return typeof value === "object" && value !== null;
}
export async function consume(events, directory, request = requestSync) {
    try {
        for await (const event of events) {
            if (!record(event) ||
                event.type !== "session.status" ||
                !record(event.data))
                continue;
            const { sessionID, status } = event.data;
            if (typeof sessionID !== "string" ||
                !record(status) ||
                status.type !== "idle")
                continue;
            const path = record(event.location) && typeof event.location.directory === "string"
                ? event.location.directory
                : directory;
            request(path, sessionID);
        }
    }
    catch {
        // A disconnected event stream cannot disrupt host startup or agent work.
    }
}
export default {
    id: "servediff",
    setup(context) {
        const controller = new AbortController();
        void consume(context.event.subscribe({ signal: controller.signal }), context.location.directory);
        return () => controller.abort();
    },
};
// The CLI schedules its own detached worker. Do not wait for collection or upload.
function requestSync(directory, sessionID) {
    try {
        const args = [
            "hook",
            "--agent",
            "opencode",
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
