import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Readable } from "node:stream";
import { type TestContext, test } from "node:test";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { openPatch, readPatchInput } from "../src/stdin.ts";

const execute = promisify(execFile);
const cli = fileURLToPath(new URL("../src/cli.ts", import.meta.url));
const patch = await readFile(
  fileURLToPath(new URL("../../../test/fixtures/sample.diff", import.meta.url)),
  "utf8",
);

async function directory(t: TestContext) {
  const root = await mkdtemp(join(tmpdir(), "serve diff stdin "));
  t.after(() => rm(root, { force: true, recursive: true }));
  return root;
}

test("serves parsed patch contents without consulting a Git repository", async () => {
  const repository = openPatch(patch);
  const data = await repository.snapshot("all");
  assert.equal(data.source, "stdin");
  assert.equal(data.files.length, 12);
  const file = data.files.find((entry) => entry.path === "src/value.ts");
  assert.ok(file);
  assert.equal(file.status, "M");
  assert.equal(file.additions, 1);
  assert.equal(file.deletions, 1);
  assert.equal(file.indexStatus, "");
  assert.equal(file.worktreeStatus, "");
  assert.match(
    (await repository.patch("all", file, null)).patch,
    /-export const value = 1;\n\+export const value = 2;/,
  );
  assert.equal(openPatch(patch).root, repository.root);
  assert.notEqual(
    openPatch(patch.replace("value = 2", "value = 3")).root,
    repository.root,
  );
  await assert.rejects(repository.snapshot("staged"), /no staged or unstaged/);
});

test("handles actual git show ranges without dropping repeated files or mixing their patches", async (t) => {
  const root = await directory(t);
  const git = async (...args: string[]) =>
    (
      await execute("git", args, {
        cwd: root,
        env: {
          ...process.env,
          GIT_AUTHOR_NAME: "Test",
          GIT_AUTHOR_EMAIL: "test@example.com",
          GIT_COMMITTER_NAME: "Test",
          GIT_COMMITTER_EMAIL: "test@example.com",
        },
      })
    ).stdout;
  await git("init", "-b", "main");
  await writeFile(join(root, "value.ts"), "base\n");
  await git("add", ".");
  await git("commit", "-m", "Base");
  await git("checkout", "-b", "feature");
  for (const value of ["one", "two", "tip"]) {
    await writeFile(join(root, "value.ts"), `${value}\n`);
    await git("commit", "-am", value);
  }
  const output = await git("show", "--no-color", "main..HEAD~1");
  const repository = openPatch(output);
  const data = await repository.snapshot("all");
  assert.equal(data.files.length, 2);
  assert.equal(new Set(data.files.map((file) => file.path)).size, 2);
  assert.ok(data.files.every((file) => file.path.endsWith("/value.ts")));
  const first = data.files[0];
  const second = data.files[1];
  assert.ok(first && second);
  assert.match(
    (await repository.patch("all", first, null)).patch,
    /-one\n\+two/,
  );
  assert.match(
    (await repository.patch("all", second, null)).patch,
    /-base\n\+one/,
  );
  const single = await openPatch(
    await git("show", "--color=always", "HEAD"),
  ).snapshot("all");
  assert.equal(single.files[0]?.path, "value.ts");
  const mail = await openPatch(
    await git("format-patch", "--stdout", "main..HEAD~1"),
  ).snapshot("all");
  assert.equal(mail.files.length, 2);
});

test("preserves rename, delete, binary, and mode-only entries", async () => {
  const input = `diff --git a/old.ts b/new.ts
similarity index 100%
rename from old.ts
rename to new.ts
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 1111111..0000000
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-gone
diff --git a/image.bin b/image.bin
index 1111111..2222222 100644
Binary files a/image.bin and b/image.bin differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
`;
  const repository = openPatch(input);
  const data = await repository.snapshot("all");
  assert.deepEqual(
    data.files.map(({ path, status, oldPath }) => [path, status, oldPath]),
    [
      ["new.ts", "R", "old.ts"],
      ["gone.txt", "D", null],
      ["image.bin", "M", null],
      ["run.sh", "M", null],
    ],
  );
  const binary = data.files.find((file) => file.binary);
  assert.ok(binary);
  assert.match(
    (await repository.patch("all", binary, null)).message ?? "",
    /Binary/,
  );
});

test("reads chunked UTF-8, bounds input, and rejects unsupported output clearly", async () => {
  const bytes = Buffer.from("雪\n");
  assert.equal(
    await readPatchInput(
      Readable.from([bytes.subarray(0, 1), bytes.subarray(1)]),
    ),
    "雪\n",
  );
  await assert.rejects(
    readPatchInput(Readable.from([Buffer.alloc(16 * 1024 * 1024 + 1)])),
    /16 MiB/,
  );
  assert.throws(() => openPatch("not a diff"), /No Git patch/);
  assert.throws(() => openPatch("diff --cc file.ts\n"), /Combined merge diffs/);
  assert.throws(
    () => openPatch(patch.replace("@@ -1,3 +1,3 @@", "@@ -20,5 +20,5 @@")),
    /./,
  );
  assert.equal((await openPatch("").snapshot("all")).files.length, 0);
});

for (const input of [patch, ""]) {
  test(`CLI gives ${input ? "populated" : "empty"} stdin priority over a path, serves assets without Git, and shuts down on SIGINT`, async (t) => {
    const root = await directory(t);
    const child = spawn(
      process.execPath,
      [cli, "/nonexistent/repository", "--port", "0", "--dev"],
      {
        cwd: root,
        env: { ...process.env, PATH: "/nonexistent" },
        stdio: "pipe",
      },
    );
    t.after(() => {
      if (child.exitCode === null && child.signalCode === null)
        child.kill("SIGKILL");
    });
    let stderr = "";
    child.stderr.on("data", (chunk) => {
      stderr += String(chunk);
    });
    const ready = new Promise<{ url: string; token: string }>(
      (resolve, reject) => {
        let output = "";
        const timeout = setTimeout(
          () => reject(new Error(`CLI did not start: ${stderr}`)),
          10_000,
        );
        child.once("error", (error) => {
          clearTimeout(timeout);
          reject(error);
        });
        child.once("exit", () => {
          clearTimeout(timeout);
          reject(new Error(stderr));
        });
        child.stdout.on("data", (chunk) => {
          output += String(chunk);
          const url = /http:\/\/localhost:\d+/.exec(output)?.[0];
          const token = /API token\s+([a-f\d]+)/.exec(output)?.[1];
          if (url && token) {
            clearTimeout(timeout);
            resolve({ url, token });
          }
        });
      },
    );
    child.stdin.end(input);
    const { url, token } = await ready;
    const authorized = { headers: { Authorization: `Bearer ${token}` } };
    assert.equal((await fetch(url)).status, 200);
    const data = await (
      await fetch(`${url}/api/v1/diffs/current?scope=all`, authorized)
    ).text();
    assert.match(data, /"source":"stdin"/);
    if (input) assert.match(data, /src\/value.ts/);
    else assert.match(data, /"files":\[\]/);
    assert.equal(
      (await fetch(`${url}/api/v1/diffs/current?scope=staged`, authorized))
        .status,
      400,
    );
    const stopped = once(child, "exit");
    child.kill("SIGINT");
    assert.deepEqual(await stopped, [0, null]);
  });
}

test("CLI requires a directory when stdin is disconnected rather than piped", async (t) => {
  const root = await directory(t);
  const child = spawn(process.execPath, [cli], {
    cwd: root,
    stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => {
    stderr += String(chunk);
  });
  const [code] = await once(child, "exit");
  assert.equal(code, 1);
  assert.match(stderr, /Provide a repository path/);
});
