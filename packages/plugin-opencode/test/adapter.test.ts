import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import plugin, { consume } from "../src/index.ts";

async function* events(values: unknown[]) {
  yield* values;
}

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

for (const agent of ["codex", "claude"]) {
  test(`${agent} native Stop only queues CLI sync and cannot block completion`, async () => {
    const text = await readFile(
      new URL(`../../plugin-${agent}/hooks/hooks.json`, import.meta.url),
      "utf8",
    );
    const config: unknown = JSON.parse(text);
    assert.deepEqual(config, {
      hooks: {
        Stop: [
          {
            hooks: [
              {
                type: "command",
                command: `servediff hook --agent ${agent} >/dev/null 2>&1 || true`,
                timeout: 5,
              },
            ],
          },
        ],
      },
    });
  });
}
