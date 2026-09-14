import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { get } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { type TestContext, test } from "node:test";
import { promisify } from "node:util";
import { openRepository } from "../src/git.ts";
import { startServer } from "../src/server.ts";

const execute = promisify(execFile);
async function fixture(t: TestContext) {
  const root = await mkdtemp(join(tmpdir(), "servediff-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  async function git(...args: string[]) {
    return (
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
  }
  await git("init", "-b", "main");
  const repository = await openRepository(root);
  return {
    root,
    git,
    repository,
    write: (path: string, contents: string | Buffer) =>
      writeFile(join(root, path), contents),
  };
}

test("separates HEAD, index, working tree, and untracked files without writing the index", async (t) => {
  const { root, git, repository, write } = await fixture(t);
  await write("example.ts", "export const value = 'committed';\n");
  await write(".gitignore", "ignored.txt\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await write("example.ts", "export const value = 'staged';\n");
  await git("add", "example.ts");
  await write("example.ts", "export const value = 'working';\n");
  await write("untracked.txt", "new file\n");
  await write("ignored.txt", "ignored\n");
  const indexBefore = await readFile(join(root, ".git/index"));
  for (const mode of ["all", "staged", "unstaged"] satisfies Array<
    "all" | "staged" | "unstaged"
  >) {
    const snapshot = await repository.snapshot(mode);
    assert.equal(
      snapshot.files.some((file) => file.path === "untracked.txt"),
      mode !== "staged",
    );
    assert.equal(
      snapshot.files.some((file) => file.path === "ignored.txt"),
      false,
    );
    const file = snapshot.files.find((file) => file.path === "example.ts");
    assert.ok(file);
    assert.equal(file.indexStatus, "M");
    assert.equal(file.worktreeStatus, "M");
    const { patch } = await repository.patch(mode, file, snapshot.head);
    assert.match(
      patch,
      new RegExp(
        `-${"export const value = '"}${mode === "unstaged" ? "staged" : "committed"}`,
      ),
    );
    assert.deepEqual(await repository.contents(mode, file, snapshot.head), {
      before: `export const value = '${mode === "unstaged" ? "staged" : "committed"}';\n`,
      after: `export const value = '${mode === "staged" ? "staged" : "working"}';\n`,
    });
    assert.match(
      patch,
      new RegExp(
        `\\+export const value = '${mode === "staged" ? "staged" : "working"}`,
      ),
    );
  }
  assert.deepEqual(await readFile(join(root, ".git/index")), indexBefore);
});

test("supports unborn repositories and resolves subdirectories to the working tree root", async (t) => {
  const { root, git, repository, write } = await fixture(t);
  await write("first.txt", "first\n");
  const untracked = await repository.snapshot("all");
  assert.equal(untracked.head, null);
  assert.equal(untracked.files[0]?.status, "?");
  assert.equal(untracked.files[0]?.indexStatus, "?");
  assert.equal(untracked.files[0]?.worktreeStatus, "?");
  const first = untracked.files[0];
  assert.ok(first);
  assert.match((await repository.patch("all", first, null)).patch, /\+first/);
  await git("add", ".");
  const staged = await repository.snapshot("staged");
  assert.equal(staged.files[0]?.status, "A");
  assert.equal(staged.files[0]?.indexStatus, "A");
  assert.equal(staged.files[0]?.worktreeStatus, " ");
  const stagedFile = staged.files[0];
  assert.ok(stagedFile);
  assert.match(
    (await repository.patch("staged", stagedFile, null)).patch,
    /\+first/,
  );
  assert.equal((await repository.snapshot("unstaged")).files.length, 0);
  await mkdir(join(root, "nested"));
  assert.equal(
    (await openRepository(join(root, "nested"))).root,
    repository.root,
  );
});

test("preserves rename pairs, unusual filenames, deletions, binary and mode-only changes", async (t) => {
  const { git, repository, write, root } = await fixture(t);
  await write("old.txt", "keep this line\n");
  await write("delete.txt", "delete\n");
  await write("mode.sh", "echo hello\n");
  await write("binary.bin", Buffer.from([0, 1, 2]));
  await git("add", ".");
  await git("commit", "-m", "Initial");
  const path = "renamed\twith\n雪.txt";
  await git("mv", "old.txt", path);
  await git("rm", "delete.txt");
  await git("update-index", "--chmod=+x", "mode.sh");
  await write("binary.bin", Buffer.from([0, 2, 3]));
  await git("add", "binary.bin");
  const snapshot = await repository.snapshot("staged");
  const renamed = snapshot.files.find((file) => file.path === path);
  assert.ok(renamed);
  assert.equal(renamed.oldPath, "old.txt");
  assert.equal(renamed.status, "R");
  assert.equal(renamed.indexStatus, "R");
  assert.equal(renamed.worktreeStatus, " ");
  assert.deepEqual(
    await repository.contents("staged", renamed, snapshot.head),
    { before: "keep this line\n", after: "keep this line\n" },
  );
  const deleted = snapshot.files.find((file) => file.path === "delete.txt");
  assert.ok(deleted);
  assert.equal(deleted.deletions, 1);
  assert.deepEqual(
    await repository.contents("staged", deleted, snapshot.head),
    { before: "delete\n", after: "" },
  );
  const binary = snapshot.files.find((file) => file.path === "binary.bin");
  assert.ok(binary);
  assert.equal(binary.binary, true);
  assert.match(
    (await repository.patch("staged", binary, snapshot.head)).message ?? "",
    /Binary/,
  );
  await assert.rejects(
    repository.contents("staged", binary, snapshot.head),
    /Full text is unavailable/,
  );
  const mode = snapshot.files.find((file) => file.path === "mode.sh");
  assert.ok(mode);
  assert.match(
    (await repository.patch("staged", mode, snapshot.head)).patch,
    /new mode 100755/,
  );
  await write(":(glob)*.txt", "literal\n");
  const all = await repository.snapshot("all");
  const literal = all.files.find((file) => file.path === ":(glob)*.txt");
  assert.ok(literal);
  assert.match(
    (await repository.patch("all", literal, all.head)).patch,
    /\+literal/,
  );
  await rm(join(root, ":(glob)*.txt"));
});

test("refresh fingerprints detect same-size edits and index-only updates", async (t) => {
  const { git, repository, write } = await fixture(t);
  await write("value.txt", "base\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await write("value.txt", "edit\n");
  const before = await repository.snapshot("all");
  await write("value.txt", "next\n");
  const after = await repository.snapshot("all");
  assert.notEqual(before.revision, after.revision);
  await git("add", ".");
  const stagedBefore = await repository.snapshot("staged");
  await write("value.txt", "more\n");
  await git("add", ".");
  assert.notEqual(
    stagedBefore.revision,
    (await repository.snapshot("staged")).revision,
  );
});

test("does not follow untracked symlinks and limits oversized previews", async (t) => {
  const { root, repository, write } = await fixture(t);
  const secret = join(root, ".git", "secret");
  await writeFile(secret, "DO NOT READ TARGET\n");
  await symlink(secret, join(root, "link.txt"));
  await write("large.txt", "x".repeat(2 * 1024 * 1024 + 1));
  const snapshot = await repository.snapshot("all");
  const link = snapshot.files.find((file) => file.path === "link.txt");
  assert.ok(link);
  const result = await repository.patch("all", link, null);
  assert.doesNotMatch(result.patch, /DO NOT READ TARGET/);
  assert.deepEqual(await repository.contents("all", link, null), {
    before: "",
    after: secret,
  });
  const large = snapshot.files.find((file) => file.path === "large.txt");
  assert.ok(large);
  assert.match(
    (await repository.patch("all", large, null)).message ?? "",
    /2 MiB/,
  );
  await assert.rejects(repository.contents("all", large, null), /2 MiB/);
});

test("reports merge conflicts and detached HEAD", async (t) => {
  const { git, repository, write } = await fixture(t);
  await write("conflict.txt", "base\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await git("checkout", "-b", "other");
  await write("conflict.txt", "other\n");
  await git("commit", "-am", "Other");
  await git("checkout", "main");
  await write("conflict.txt", "main\n");
  await git("commit", "-am", "Main");
  await assert.rejects(git("merge", "other"));
  const snapshot = await repository.snapshot("unstaged");
  const conflict = snapshot.files.find((file) => file.path === "conflict.txt");
  assert.ok(conflict);
  assert.equal(conflict.status, "U");
  assert.equal(conflict.indexStatus, "U");
  assert.equal(conflict.worktreeStatus, "U");
  assert.match(
    (await repository.patch("unstaged", conflict, snapshot.head)).message ?? "",
    /conflict/,
  );
  await git("merge", "--abort");
  await git("checkout", "--detach");
  assert.match((await repository.snapshot("all")).branch, /^detached at/);
});

test("serves browser assets and validates API paths, versions, modes, hosts, origins, methods", async (t) => {
  const { root, repository, write } = await fixture(t);
  await write("hello.ts", "export const hello = true;\n");
  const server = await startServer({ directory: root, port: 0, dev: true });
  t.after(() => server.close());
  assert.match(
    server.addresses.localhost,
    /^http:\/\/localhost:\d+#token=[a-f\d]+$/,
  );
  assert.match(
    server.addresses.all,
    /^http:\/\/0\.0\.0\.0:\d+#token=[a-f\d]+$/,
  );
  if (server.addresses.network) {
    assert.match(
      server.addresses.network,
      /^http:\/\/\d{1,3}(?:\.\d{1,3}){3}:\d+#token=[a-f\d]+$/,
    );
    assert.equal((await fetch(server.addresses.network)).status, 200);
  }
  const snapshot = await repository.snapshot("all");
  const file = snapshot.files[0];
  assert.ok(file);
  const query = new URLSearchParams({
    scope: "all",
    fileVersion: file.fingerprint,
  });
  const fileURL = `${server.url}/api/v1/diffs/${snapshot.revision}/files/${file.id}/patch?${query}`;
  const contentsURL = `${server.url}/api/v1/diffs/${snapshot.revision}/files/${file.id}/contents?${query}`;
  const authorized = { headers: { Authorization: `Bearer ${server.token}` } };
  assert.equal((await fetch(server.url)).status, 200);
  assert.match(await (await fetch(server.url)).text(), /servediff/);
  assert.equal((await fetch(`${server.url}/logo.png`)).status, 404);
  assert.equal(
    (await fetch(`${server.url}/api/v1/diffs/current?scope=all`)).status,
    401,
  );
  assert.equal(
    (await fetch(`${server.url}/api/v1/diffs/current?scope=all`, authorized))
      .status,
    200,
  );
  assert.match(
    await (await fetch(fileURL, authorized)).text(),
    /export const hello/,
  );
  assert.deepEqual(await (await fetch(contentsURL, authorized)).json(), {
    before: "",
    after: "export const hello = true;\n",
  });
  assert.equal(
    (await fetch(`${server.url}/api/v1/diffs/current?scope=bad`, authorized))
      .status,
    400,
  );
  assert.equal(
    (
      await fetch(
        `${server.url}/api/v1/diffs/${snapshot.revision}/files/missing/patch?scope=all&fileVersion=x`,
        authorized,
      )
    ).status,
    404,
  );
  assert.equal(
    (
      await fetch(
        `${server.url}/api/v1/diffs/${snapshot.revision}/files/${file.id}/patch?scope=all&fileVersion=stale`,
        authorized,
      )
    ).status,
    409,
  );
  assert.equal(
    (
      await fetch(
        `${server.url}/api/v1/diffs/stale/files/${file.id}/contents?scope=all&fileVersion=${file.fingerprint}`,
        authorized,
      )
    ).status,
    409,
  );
  const invalidHostStatus = await new Promise<number | undefined>(
    (resolve, reject) => {
      get(
        `${server.url}/api/v1/diffs/current?scope=all`,
        { headers: { Host: "evil.example" } },
        (response) => {
          response.resume();
          resolve(response.statusCode);
        },
      ).on("error", reject);
    },
  );
  assert.equal(invalidHostStatus, 403);
  assert.equal(
    (
      await fetch(`${server.url}/api/v1/diffs/current?scope=all`, {
        headers: {
          Authorization: `Bearer ${server.token}`,
          Origin: "https://evil.example",
        },
      })
    ).status,
    403,
  );
  assert.equal(
    (
      await fetch(`${server.url}/api/v1/diffs/current?scope=all`, {
        ...authorized,
        method: "POST",
      })
    ).status,
    405,
  );
  assert.equal((await fetch(`${server.url}/missing.js`)).status, 404);
});

test("rejects directories outside a Git working tree", async (t) => {
  const root = await mkdtemp(join(tmpdir(), "servediff-not-git-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await assert.rejects(openRepository(root), /Not a Git working tree/);
});

test("ignores Git display settings and external diff drivers", async (t) => {
  const { git, repository, write } = await fixture(t);
  await write("file.txt", "before\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await git("config", "color.ui", "always");
  await git("config", "diff.noprefix", "true");
  await git("config", "diff.outputIndicatorNew", ">");
  await git("config", "diff.external", "this-command-must-not-run");
  await write("file.txt", "after\n");
  const snapshot = await repository.snapshot("all");
  const file = snapshot.files[0];
  assert.ok(file);
  const { patch } = await repository.patch("all", file, snapshot.head);
  assert.match(patch, /--- a\/file.txt/);
  assert.match(patch, /\+after/);
  assert.equal(patch.includes(String.fromCharCode(27)), false);
});

test("supports linked worktrees", async (t) => {
  const { git, write } = await fixture(t);
  await write("file.txt", "before\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  const directory = await mkdtemp(join(tmpdir(), "servediff-worktree-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  await git("worktree", "add", "--detach", directory, "HEAD");
  await writeFile(join(directory, "file.txt"), "linked worktree\n");
  const repository = await openRepository(directory);
  const snapshot = await repository.snapshot("all");
  assert.equal(snapshot.files[0]?.path, "file.txt");
  assert.match(snapshot.branch, /^detached at/);
});

test("All changes retains net HEAD diff when a staged deletion is recreated", async (t) => {
  const { git, repository, write } = await fixture(t);
  await write("file.txt", "original\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await git("rm", "file.txt");
  await write("file.txt", "recreated\n");
  const all = await repository.snapshot("all");
  const file = all.files[0];
  assert.ok(file);
  assert.equal(file.status, "M");
  assert.equal(file.indexStatus, "D");
  assert.equal(file.worktreeStatus, "?");
  const { contents } = await repository.patch("all", file, all.head);
  assert.equal(contents?.before, "original\n");
  assert.equal(contents?.after, "recreated\n");
  assert.equal((await repository.snapshot("staged")).files[0]?.status, "D");
  assert.equal((await repository.snapshot("unstaged")).files[0]?.status, "?");
  await write("file.txt", "original\n");
  assert.equal((await repository.snapshot("all")).files.length, 0);
});

test("reports staging transitions and deleted files independently of the selected diff scope", async (t) => {
  const { git, repository, write, root } = await fixture(t);
  await write("modified.ts", "stable first line\nbefore\nstable last line\n");
  await write("deleted.md", "before\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await write("modified.ts", "stable first line\nafter\nstable last line\n");
  await rm(join(root, "deleted.md"));
  const unstaged = await repository.snapshot("all");
  assert.deepEqual(
    unstaged.files.map(({ path, status, indexStatus, worktreeStatus }) => [
      path,
      status,
      indexStatus,
      worktreeStatus,
    ]),
    [
      ["deleted.md", "D", " ", "D"],
      ["modified.ts", "M", " ", "M"],
    ],
  );
  await git("add", ".");
  const staged = await repository.snapshot("all");
  assert.notEqual(staged.revision, unstaged.revision);
  assert.deepEqual(
    staged.files.map(({ path, status, indexStatus, worktreeStatus }) => [
      path,
      status,
      indexStatus,
      worktreeStatus,
    ]),
    [
      ["deleted.md", "D", "D", " "],
      ["modified.ts", "M", "M", " "],
    ],
  );
  await git("mv", "modified.ts", "renamed\t雪.ts");
  await write(
    "renamed\t雪.ts",
    "stable first line\nworking\nstable last line\n",
  );
  const working = await repository.snapshot("unstaged");
  assert.equal(working.files[0]?.path, "renamed\t雪.ts");
  assert.equal(working.files[0]?.worktreeStatus, "M");
  assert.equal(working.files[0]?.indexStatus, "R");
  await write("modified.ts", "new file at old path\n");
  const recreated = (await repository.snapshot("unstaged")).files.find(
    (file) => file.path === "modified.ts",
  );
  assert.ok(recreated);
  assert.equal(recreated.indexStatus, "?");
  assert.equal(recreated.worktreeStatus, "?");
});

test("does not attribute edits at a rename destination to the deleted source", async (t) => {
  const { git, repository, write } = await fixture(t);
  await write("old.txt", "original contents\n");
  await git("add", ".");
  await git("commit", "-m", "Initial");
  await git("mv", "old.txt", "new.txt");
  await write("new.txt", "entirely rewritten\n");
  const snapshot = await repository.snapshot("all");
  const source = snapshot.files.find((file) => file.path === "old.txt");
  const destination = snapshot.files.find((file) => file.path === "new.txt");
  assert.ok(source && destination);
  assert.equal(source.indexStatus, "D");
  assert.equal(source.worktreeStatus, " ");
  assert.equal(destination.indexStatus, "R");
  assert.equal(destination.worktreeStatus, "M");
});
