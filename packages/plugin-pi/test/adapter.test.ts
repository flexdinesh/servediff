import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { chmod, cp, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout } from "node:timers/promises";
import test from "node:test";
import { register } from "../src/index.ts";

test("settled run queues current checkout/session without registering earlier lifecycle events", () => {
  const requests: string[][] = [];
  register(
    {
      on(event, handler) {
        assert.equal(event, "agent_settled");
        const result = handler(
          {},
          {
            cwd: "/worktree",
            sessionManager: { getSessionId: () => "session-1" },
          },
        );
        assert.equal(result, undefined);
      },
    },
    (path, session) => requests.push([path, session]),
  );
  assert.deepEqual(requests, [["/worktree", "session-1"]]);
});

test("launch preserves argument boundaries and returns before child completes", async () => {
  const temp = await mkdtemp(join(tmpdir(), "servediff-adapter-"));
  const binary = join(temp, "servediff with spaces");
  const output = join(temp, "args.json");
  const previous = process.env.SERVEDIFF_BINARY;
  await writeFile(
    binary,
    `#!/usr/bin/env node
import { writeFileSync } from 'node:fs';
setTimeout(() => writeFileSync(${JSON.stringify(output)}, JSON.stringify(process.argv.slice(2))), 100);
`,
  );
  await chmod(binary, 0o700);
  process.env.SERVEDIFF_BINARY = binary;
  try {
    register({
      on(_event, handler) {
        handler(
          {},
          {
            cwd: "/a checkout/$(touch sentinel)",
            sessionManager: { getSessionId: () => "session ; value" },
          },
        );
      },
    });
    await assert.rejects(readFile(output));
    let actual = "";
    for (let attempt = 0; attempt < 100; attempt++) {
      try {
        actual = await readFile(output, "utf8");
        break;
      } catch {
        await setTimeout(20);
      }
    }
    assert.deepEqual(JSON.parse(actual), [
      "hook",
      "--harness",
      "pi",
      "--path",
      "/a checkout/$(touch sentinel)",
      "--run-id",
      "session ; value",
    ]);
  } finally {
    if (previous === undefined) delete process.env.SERVEDIFF_BINARY;
    else process.env.SERVEDIFF_BINARY = previous;
    await rm(temp, { recursive: true, force: true });
  }
});

test("missing binary never rejects or emits a host error", async () => {
  const previous = process.env.SERVEDIFF_BINARY;
  process.env.SERVEDIFF_BINARY = "/missing/servediff";
  try {
    register({
      on(_event, handler) {
        handler(
          {},
          { cwd: "/worktree", sessionManager: { getSessionId: () => "id" } },
        );
      },
    });
    await setTimeout(20);
  } finally {
    if (previous === undefined) delete process.env.SERVEDIFF_BINARY;
    else process.env.SERVEDIFF_BINARY = previous;
  }
});

test("agent host can exit while detached sync scheduling remains alive", async () => {
  const temp = await mkdtemp(join(tmpdir(), "servediff-detached-"));
  const binary = join(temp, "fake-servediff");
  const completed = join(temp, "completed");
  await writeFile(
    binary,
    `#!/usr/bin/env node\nimport { writeFileSync } from 'node:fs';\nsetTimeout(() => writeFileSync(${JSON.stringify(completed)}, 'done'), 1_000);\n`,
  );
  await chmod(binary, 0o700);
  const script = `import { register } from ${JSON.stringify(new URL("../src/index.ts", import.meta.url).href)};
    register({ on(_event, handler) {
      handler({}, { cwd: '/worktree', sessionManager: { getSessionId: () => 'id' } });
    } });`;
  try {
    const result = await promisify(execFile)(
      process.execPath,
      ["--input-type=module", "-e", script],
      {
        env: { ...process.env, SERVEDIFF_BINARY: binary },
        timeout: 750,
      },
    );
    assert.equal(result.stdout, "");
    assert.equal(result.stderr, "");
    for (let attempt = 0; attempt < 100; attempt++) {
      try {
        assert.equal(await readFile(completed, "utf8"), "done");
        return;
      } catch {
        await setTimeout(20);
      }
    }
    assert.fail("child did not survive host teardown");
  } finally {
    await rm(temp, { recursive: true, force: true });
  }
});

for (const agent of ["pi", "opencode"]) {
  test(`${agent} native package uses runtime binary/config environment without mutating it`, async () => {
    const temp = await mkdtemp(join(tmpdir(), "servediff-configured-"));
    const binary = join(temp, "servediff with spaces");
    const output = join(temp, "args.json");
    const configFile = join(temp, "custom config.json");
    await writeFile(
      binary,
      `#!/usr/bin/env node\nimport { writeFileSync } from 'node:fs';\nwriteFileSync(${JSON.stringify(output)}, JSON.stringify({args: process.argv.slice(2), config: process.env.SERVEDIFF_CONFIG_PATH}));\n`,
    );
    await chmod(binary, 0o700);
    const packageRoot = new URL(`../../plugin-${agent}/`, import.meta.url);
    await cp(new URL("package.json", packageRoot), join(temp, "package.json"));
    await cp(new URL("dist", packageRoot), join(temp, "dist"), {
      recursive: true,
    });
    const source = pathToFileURL(join(temp, "dist/index.js")).href;
    const trigger =
      agent === "pi"
        ? `adapter.register({ on(_event, handler) {
          handler({}, { cwd: '/worktree', sessionManager: { getSessionId: () => 'id' } });
        } });`
        : `await adapter.consume((async function*() {
          yield { type: 'session.status', data: {sessionID: 'id', status: {type: 'idle'}} };
        })(), '/worktree');`;
    const script = `import assert from 'node:assert/strict';
      import * as adapter from ${JSON.stringify(source)};
      const before = JSON.stringify({ ...process.env });
      assert.equal(JSON.stringify({ ...process.env }) === before, true, "host environment changed");
      ${trigger}
      assert.equal(JSON.stringify({ ...process.env }) === before, true, "host environment changed");`;
    try {
      const result = await promisify(execFile)(
        process.execPath,
        ["--input-type=module", "-e", script],
        {
          env: {
            ...process.env,
            SERVEDIFF_BINARY: binary,
            SERVEDIFF_CONFIG_PATH: configFile,
          },
          timeout: 750,
        },
      );
      assert.equal(result.stdout, "");
      assert.equal(result.stderr, "");
      let actual = "";
      for (let attempt = 0; attempt < 100; attempt++) {
        try {
          actual = await readFile(output, "utf8");
          break;
        } catch {
          await setTimeout(20);
        }
      }
      assert.deepEqual(JSON.parse(actual), {
        args: [
          "hook",
          "--harness",
          agent,
          "--path",
          "/worktree",
          "--run-id",
          "id",
        ],
        config: configFile,
      });
    } finally {
      await rm(temp, { recursive: true, force: true });
    }
  });
}
