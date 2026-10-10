import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { promisify } from "node:util";
import test from "node:test";
import plugin, { consume } from "../src/index.ts";

async function* events(values: unknown[]) {
  yield* values;
}

test("session rename metadata reaches settled collection without additional uploads", async () => {
  const requests: (string | undefined)[][] = [];
  await consume(
    events([
      {
        type: "session.created",
        data: { sessionID: "a", title: "Initial title" },
      },
      {
        type: "session.renamed",
        data: { sessionID: "a", title: "Parser review" },
      },
      {
        type: "session.status",
        data: { sessionID: "a", status: { type: "idle" } },
      },
    ]),
    "/worktree",
    (path, session, name) => {
      requests.push([path, session, name]);
    },
  );
  assert.deepEqual(requests, [["/worktree", "a", "Parser review"]]);
});

test("only settled status syncs; event location selects the actual worktree", async () => {
  const requests: string[][] = [];
  await consume(
    events([
      {
        type: "session.status",
        data: { sessionID: "busy", status: { type: "busy" } },
      },
      {
        type: "session.status",
        data: { sessionID: "retry", status: { type: "retry" } },
      },
      { type: "session.idle", data: { sessionID: "legacy-duplicate" } },
      {
        type: "session.status",
        data: { sessionID: "a", status: { type: "idle" } },
      },
      {
        type: "session.status",
        location: { directory: "/other/worktree" },
        data: { sessionID: "b", status: { type: "idle" } },
      },
      {
        type: "session.status",
        data: { sessionID: 3, status: { type: "idle" } },
      },
      null,
    ]),
    "/loaded/worktree",
    (path, session) => requests.push([path, session]),
  );
  assert.deepEqual(requests, [
    ["/loaded/worktree", "a"],
    ["/other/worktree", "b"],
  ]);
});

test("event stream failure is isolated", async () => {
  async function* disconnected() {
    yield null;
    throw new Error("Disconnected");
  }
  await assert.doesNotReject(
    consume(disconnected(), "/worktree", () => assert.fail("unexpected sync")),
  );
});

test("setup returns immediately and cleanup cancels subscription", () => {
  let signal: AbortSignal | undefined;
  const cleanup = plugin.setup({
    location: { directory: "/worktree" },
    event: {
      subscribe(options) {
        signal = options.signal;
        return events([]);
      },
    },
  });
  assert.equal(signal?.aborted, false);
  cleanup();
  assert.equal(signal?.aborted, true);
});

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

for (const agent of ["codex", "claude"]) {
  test(`${agent} native Stop respects runtime settings, stdin, and failure isolation`, async () => {
    const text = await readFile(
      new URL(`../../plugin-${agent}/hooks/hooks.json`, import.meta.url),
      "utf8",
    );
    const config: unknown = JSON.parse(text);
    assert.ok(record(config) && record(config.hooks));
    assert.deepEqual(Object.keys(config.hooks), ["Stop"]);
    const stop = config.hooks.Stop;
    assert.ok(Array.isArray(stop) && record(stop[0]));
    const hooks: unknown = stop[0].hooks;
    assert.ok(Array.isArray(hooks) && record(hooks[0]));
    const command: unknown = hooks[0].command;
    assert.equal(hooks[0].timeout, 5);
    assert.equal(hooks[0].type, "command");
    assert.ok(typeof command === "string");

    const temp = await mkdtemp(join(tmpdir(), "diffx-stop-"));
    const output = join(temp, "capture.json");
    const binary = join(temp, "diffx '$ with spaces");
    const event = JSON.stringify({
      cwd: "/checkout with spaces",
      session_id: "s",
    });
    const configFile = join(temp, "config with spaces.json");
    const script = `#!${process.execPath}
import { readFileSync, writeFileSync } from 'node:fs';
writeFileSync(${JSON.stringify(output)}, JSON.stringify({
  args: process.argv.slice(2), stdin: readFileSync(0, 'utf8'),
  config: process.env.DIFFX_CONFIG_PATH,
}));
console.log('suppressed stdout');
console.error('suppressed stderr');
process.exit(37);
`;
    try {
      await writeFile(binary, script);
      await chmod(binary, 0o700);
      await writeFile(join(temp, "diffx"), script);
      await chmod(join(temp, "diffx"), 0o700);
      for (const override of [undefined, binary]) {
        const env: NodeJS.ProcessEnv = {
          ...process.env,
          DIFFX_CONFIG_PATH: configFile,
        };
        delete env.DIFFX_BINARY;
        if (override !== undefined) env.DIFFX_BINARY = override;
        env.PATH = `${temp}:${process.env.PATH ?? ""}`;
        const result = await new Promise<{ stdout: string; stderr: string }>(
          (resolve, reject) => {
            execFile(
              "sh",
              ["-c", command],
              { env },
              (error, stdout, stderr) => {
                if (error !== null) reject(error);
                else resolve({ stdout, stderr });
              },
            ).stdin?.end(event);
          },
        );
        assert.deepEqual(result, { stdout: "", stderr: "" });
        assert.deepEqual(JSON.parse(await readFile(output, "utf8")), {
          args: ["hook", "--harness", agent],
          stdin: event,
          config: configFile,
        });
      }
      const { stdout, stderr } = await promisify(execFile)(
        "sh",
        ["-c", command],
        {
          env: { ...process.env, DIFFX_BINARY: "/missing/diffx" },
        },
      );
      assert.equal(stdout, "");
      assert.equal(stderr, "");
    } finally {
      await rm(temp, { recursive: true, force: true });
    }
  });
}
