import { spawn, type ChildProcess } from "node:child_process";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import type { Plugin } from "vite";

function stopProcess(child: ChildProcess) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const processId = child.pid;
  if (process.platform === "win32" || processId === undefined) {
    child.kill("SIGTERM");
    return;
  }
  try {
    process.kill(-processId, "SIGTERM");
  } catch (error) {
    if (!child.killed) child.kill("SIGTERM");
    if (child.exitCode === null && child.signalCode === null)
      console.warn("Unable to stop Go fixture process", error);
  }
}

export async function startFixtureServer(): Promise<{
  target: string;
  plugin: Plugin;
}> {
  const root = fileURLToPath(new URL("../../..", import.meta.url));
  const child = spawn(
    "go",
    [
      "run",
      "./cmd/servediff",
      "--fixture",
      "test/fixtures/sample.diff",
      "--state",
      "memory",
      "--no-browser",
      "--port",
      "0",
    ],
    {
      cwd: root,
      detached: process.platform !== "win32",
      env: { ...process.env, SERVEDIFF_EXIT_ON_STDIN_CLOSE: "1" },
      stdio: ["pipe", "pipe", "inherit"],
    },
  );
  const output = child.stdout;
  if (!output) {
    stopProcess(child);
    throw new Error("Unable to read Go fixture server address");
  }
  const target = await new Promise<string>((resolve, reject) => {
    let settled = false;
    const lines = createInterface({ input: output });
    const timer = setTimeout(() => {
      fail(new Error("Timed out starting Go fixture server"));
    }, 30_000);
    const fail = (error: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      lines.close();
      stopProcess(child);
      reject(error);
    };
    child.once("error", fail);
    child.once("exit", (code, signal) => {
      fail(
        new Error(
          `Go fixture server exited before startup (${signal ?? code ?? "unknown"})`,
        ),
      );
    });
    lines.on("line", (line) => {
      const port = /url:\s+http:\/\/127\.0\.0\.1:(\d+)/.exec(line)?.[1];
      if (!port || settled) return;
      settled = true;
      clearTimeout(timer);
      lines.close();
      resolve(`http://127.0.0.1:${port}`);
    });
  });
  const stop = () => stopProcess(child);
  const interrupt = () => {
    stop();
    process.exit(130);
  };
  const terminate = () => {
    stop();
    process.exit(143);
  };
  process.once("exit", stop);
  process.once("SIGINT", interrupt);
  process.once("SIGTERM", terminate);
  return {
    target,
    plugin: {
      name: "servediff-fixture",
      configureServer(server) {
        server.httpServer?.once("close", () => {
          process.off("exit", stop);
          process.off("SIGINT", interrupt);
          process.off("SIGTERM", terminate);
          stop();
        });
      },
    },
  };
}
