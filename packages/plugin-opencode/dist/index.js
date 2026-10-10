import { spawn } from "node:child_process";
function record(value) {
    return typeof value === "object" && value !== null;
}
export async function consume(events, directory, request = requestSync) {
    const names = new Map();
    try {
        for await (const event of events) {
            if (!record(event) || !record(event.data))
                continue;
            if (event.type === "session.created" ||
                event.type === "session.renamed") {
                const { sessionID, title } = event.data;
                if (typeof sessionID === "string" && typeof title === "string") {
                    names.delete(sessionID);
                    names.set(sessionID, title);
                    if (names.size > 256) {
                        const oldest = names.keys().next().value;
                        if (oldest !== undefined)
                            names.delete(oldest);
                    }
                }
                continue;
            }
            if (event.type !== "session.status")
                continue;
            const { sessionID, status } = event.data;
            if (typeof sessionID !== "string" ||
                !record(status) ||
                status.type !== "idle")
                continue;
            const path = record(event.location) && typeof event.location.directory === "string"
                ? event.location.directory
                : directory;
            request(path, sessionID, names.get(sessionID));
        }
    }
    catch {
        // A disconnected event stream cannot disrupt host startup or agent work.
    }
}
export default {
    id: "diffx",
    setup(context) {
        const controller = new AbortController();
        void consume(context.event.subscribe({ signal: controller.signal }), context.location.directory);
        return () => controller.abort();
    },
};
// The CLI schedules its own detached worker. Do not wait for collection or upload.
function requestSync(directory, sessionID, sessionName) {
    try {
        const args = [
            "hook",
            "--harness",
            "opencode",
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
