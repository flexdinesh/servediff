#!/usr/bin/env node
import { fstatSync } from "node:fs";
import { parseArgs } from "node:util";
import { openBrowser } from "./browser.ts";
import { startServer } from "./server.ts";
import { readPatchInput } from "./stdin.ts";

try {
  const { values, positionals } = parseArgs({
    options: {
      port: { type: "string", short: "p", default: "3333" },
      dev: { type: "boolean", default: false },
      help: { type: "boolean", short: "h" },
    },
    allowPositionals: true,
  });
  if (values.help) {
    console.log(
      "Usage: servediff [directory | -] [--port 3333] [--dev]\n\nExamples:\n  servediff .                 Watch the current repository\n  servediff /path/to/repo     Watch another repository\n  git diff | servediff       Read a patch from stdin\n  git show | servediff       Review a commit\n  servediff - < saved.patch  Read a saved patch\n  servediff . --port 4000     Use a different port\n\nPiped or redirected input takes priority, even when empty.\nWithout input redirection, provide a directory to watch with Git.\nRequires Node 26. Opens your browser automatically on macOS and Linux, except over SSH. Ctrl+C stops the server.",
    );
  } else {
    if (Number(process.versions.node.split(".")[0]) !== 26)
      throw new Error("servediff requires Node 26");
    if (positionals.length > 1)
      throw new Error("Expected at most one repository directory");
    const port = Number(values.port);
    if (!Number.isInteger(port) || port < 0 || port > 65535)
      throw new Error("Port must be between 0 and 65535");
    // An empty pipe is a valid empty diff. A terminal or /dev/null is no input.
    const stdin = fstatSync(0);
    const piped =
      positionals[0] === "-" ||
      stdin.isFIFO() ||
      stdin.isSocket() ||
      stdin.isFile();
    const input = piped ? await readPatchInput(process.stdin) : "";
    if (!piped && (!positionals[0] || positionals[0] === "-"))
      throw new Error(
        "Provide a repository path (servediff .) or pipe a Git diff into servediff. No input received.",
      );
    const server = await startServer({
      directory: positionals[0] ?? process.cwd(),
      port,
      dev: values.dev,
      ...(piped ? { input } : {}),
    });
    const browserError = await openBrowser(server.addresses.localhost);
    console.log(
      `\n  servediff\n  Local    ${server.addresses.localhost}\n  All      ${server.addresses.all}\n  Network  ${server.addresses.network ?? "unavailable"}\n  API token  ${server.token}\n  ${piped ? "Piped diff · fixed snapshot" : server.root}\n\n  Press Ctrl+C to stop.\n`,
    );
    if (browserError) console.warn(`servediff: ${browserError}`);
    let closing = false;
    const close = () => {
      if (!closing) {
        closing = true;
        void server.close();
      }
    };
    process.once("SIGINT", close);
    process.once("SIGTERM", close);
  }
} catch (error) {
  console.error(
    `servediff: ${error instanceof Error ? error.message : String(error)}`,
  );
  process.exitCode = 1;
}
