import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { lstat, readFile, readlink } from "node:fs/promises";
import { basename, join, resolve } from "node:path";
import { promisify } from "node:util";
import type {
  ChangedFile,
  DiffMode,
  FilePatch,
  RepositoryDiff,
} from "@servediff/shared";
import { type DiffSource, RequestError } from "./source.ts";

const execute = promisify(execFile);
const MAX_PATCH_BYTES = 2 * 1024 * 1024;
const patchFormat = [
  "--no-color",
  "--src-prefix=a/",
  "--dst-prefix=b/",
  "--output-indicator-new=+",
  "--output-indicator-old=-",
  "--output-indicator-context= ",
];

// Git receives argument arrays and literal pathspecs; filenames never become shell code.
async function git(root: string, args: string[], maxBuffer = 16 * 1024 * 1024) {
  const result = execute(
    "git",
    ["--literal-pathspecs", "-c", "core.quotePath=false", ...args],
    {
      cwd: root,
      encoding: "utf8",
      maxBuffer,
      timeout: 20_000,
      env: {
        ...process.env,
        GIT_OPTIONAL_LOCKS: "0",
        GIT_TERMINAL_PROMPT: "0",
      },
    },
  );
  result.child.stdin?.end();
  return result;
}

function digest(value: string) {
  return createHash("sha256").update(value).digest("hex");
}

function diffArgs(mode: DiffMode, base: string) {
  return [
    "diff",
    "--no-ext-diff",
    "--no-textconv",
    "--ignore-submodules=none",
    "--find-renames",
    ...patchFormat,
    ...(mode === "staged" ? ["--cached", base] : mode === "all" ? [base] : []),
  ];
}

// NUL records preserve spaces, tabs, newlines, Unicode, and rename pairs in Git paths.
function parseRaw(raw: string): Map<string, ChangedFile> {
  const entries = raw.split("\0");
  const files = new Map<string, ChangedFile>();
  for (let index = 0; index < entries.length; index++) {
    const header = entries[index];
    if (!header?.startsWith(":")) continue;
    const status = header.split(" ").at(-1)?.[0] ?? "M";
    const firstPath = entries[++index];
    const path =
      status === "R" || status === "C" ? entries[++index] : firstPath;
    if (path === undefined || firstPath === undefined)
      throw new Error("Invalid Git file record");
    if (files.get(path)?.status === "U") continue;
    files.set(path, {
      id: digest(path),
      path,
      oldPath: path === firstPath ? null : firstPath,
      status,
      indexStatus: " ",
      worktreeStatus: " ",
      additions: 0,
      deletions: 0,
      binary: false,
      fingerprint: header,
    });
  }
  return files;
}

function applyStats(files: Map<string, ChangedFile>, output: string) {
  const entries = output.split("\0");
  for (let index = 0; index < entries.length; index++) {
    const record = entries[index];
    if (!record) continue;
    const first = record.indexOf("\t");
    const second = record.indexOf("\t", first + 1);
    const additions = record.slice(0, first);
    const deletions = record.slice(first + 1, second);
    let path = record.slice(second + 1);
    if (path === "") {
      index++;
      path = entries[++index] ?? "";
    }
    const file = files.get(path);
    if (file) {
      file.binary = additions === "-";
      file.additions = Number.parseInt(additions, 10) || 0;
      file.deletions = Number.parseInt(deletions, 10) || 0;
    }
  }
}

// Porcelain separates index and working-tree changes, including rename pairs.
// A staged deletion followed by recreation produces two records for one path.
function parseStatus(output: string) {
  const renamedPaths = new Map<string, string>();
  const statuses = new Map<
    string,
    { indexStatus: string; worktreeStatus: string }
  >();
  const records = output.split("\0");
  for (let index = 0; index < records.length; index++) {
    const record = records[index];
    if (!record) continue;
    const path = record.slice(3);
    const previous = statuses.get(path);
    const indexStatus = record[0] ?? " ";
    const worktreeStatus = record[1] ?? " ";
    const status = {
      indexStatus:
        indexStatus === "?" ? (previous?.indexStatus ?? "?") : indexStatus,
      worktreeStatus: previous?.worktreeStatus === "?" ? "?" : worktreeStatus,
    };
    statuses.set(path, status);
    if (/[RC]/.test(indexStatus + worktreeStatus)) {
      const oldPath = records[++index];
      if (oldPath !== undefined) renamedPaths.set(oldPath, path);
    }
  }
  for (const [oldPath, path] of renamedPaths) {
    const status = statuses.get(path);
    // A net diff may split a heavily edited rename into delete/add entries.
    // Edits at the destination must not count as unstaged edits at the source.
    if (status && !statuses.has(oldPath))
      statuses.set(oldPath, {
        indexStatus: status.indexStatus === "R" ? "D" : " ",
        worktreeStatus: status.worktreeStatus === "R" ? "D" : " ",
      });
  }
  return statuses;
}

export async function openRepository(directory: string) {
  let root: string;
  try {
    root = (
      await git(resolve(directory), ["rev-parse", "--show-toplevel"])
    ).stdout.replace(/\n$/, "");
  } catch {
    throw new Error(`Not a Git working tree: ${resolve(directory)}`);
  }

  // Git ignores an index-deleted path even after it is recreated on disk.
  // Compare its HEAD contents explicitly, without adding it back to the index.
  async function recreatedContents(
    path: string,
    head: string,
  ): Promise<FilePatch> {
    const absolute = join(root, path);
    const stat = await lstat(absolute);
    if (
      (!stat.isFile() && !stat.isSymbolicLink()) ||
      stat.size > MAX_PATCH_BYTES
    ) {
      return {
        patch: "",
        message:
          "Recreated file exceeds the preview limit or is not a text file.",
      };
    }
    try {
      const before = (
        await git(root, ["show", `${head}:${path}`], MAX_PATCH_BYTES)
      ).stdout;
      const after = stat.isSymbolicLink()
        ? await readlink(absolute)
        : await readFile(absolute, "utf8");
      return {
        patch: "",
        message:
          before.includes("\0") || after.includes("\0")
            ? "Binary file changed. No text preview available."
            : null,
        contents: { before, after },
      };
    } catch {
      return {
        patch: "",
        message:
          "Original file exceeds the 2 MiB preview limit or changed while loading.",
      };
    }
  }

  async function snapshot(mode: DiffMode): Promise<RepositoryDiff> {
    const head = await git(root, ["rev-parse", "--verify", "HEAD"]).then(
      (result) => result.stdout.trim(),
      () => null,
    );
    const base =
      head ??
      (await git(root, ["hash-object", "-t", "tree", "--stdin"])).stdout.trim();
    const args = diffArgs(mode, base);
    const [raw, stats, untracked, branch, statusOutput] = await Promise.all([
      git(root, [...args, "--raw", "--no-abbrev", "-z", "--"]),
      git(root, [...args, "--numstat", "-z", "--"]),
      mode === "staged"
        ? Promise.resolve({ stdout: "" })
        : git(root, ["ls-files", "--others", "--exclude-standard", "-z"]),
      git(root, ["symbolic-ref", "--quiet", "--short", "HEAD"]).then(
        (result) => result.stdout.trim(),
        () => `detached at ${head?.slice(0, 7) ?? "HEAD"}`,
      ),
      git(root, [
        "status",
        "--porcelain=v1",
        "-z",
        "--untracked-files=all",
        "--ignore-submodules=none",
        "--renames",
      ]),
    ]);
    const files = parseRaw(raw.stdout);
    applyStats(files, stats.stdout);
    // A staged deletion recreated on disk is untracked in the index, but still
    // belongs to HEAD. In All changes, retain its net diff (or no diff at all).
    const headPaths =
      mode === "all" && head && untracked.stdout
        ? new Set(
            (
              await git(root, ["ls-tree", "-r", "--name-only", "-z", head])
            ).stdout.split("\0"),
          )
        : new Set<string>();
    for (const path of untracked.stdout.split("\0").filter(Boolean)) {
      if (head && headPaths.has(path)) {
        const original = files.get(path);
        if (!original) continue;
        const preview = await recreatedContents(path, head);
        const stat = await lstat(join(root, path));
        const mode = stat.isSymbolicLink()
          ? "120000"
          : stat.mode & 0o111
            ? "100755"
            : "100644";
        const previousMode = original.fingerprint.split(" ")[0]?.slice(1);
        if (
          preview.contents &&
          preview.contents.before === preview.contents.after &&
          mode === previousMode
        ) {
          files.delete(path);
        } else {
          original.status = "M";
          original.additions = 0;
          original.deletions = 0;
          original.recreated = true;
          original.binary = preview.message?.startsWith("Binary") ?? false;
        }
        continue;
      }
      if (files.has(path)) continue;
      files.set(path, {
        id: digest(path),
        path,
        oldPath: null,
        status: "?",
        indexStatus: " ",
        worktreeStatus: " ",
        additions: 0,
        deletions: 0,
        binary: false,
        fingerprint: "untracked",
      });
    }
    // Worktree metadata detects edits even when a file's diff line counts stay the same.
    await Promise.all(
      [...files.values()].map(async (file) => {
        const {
          indexStatus: _indexStatus,
          worktreeStatus: _worktreeStatus,
          ...contentFile
        } = file;
        const stat =
          mode === "staged"
            ? null
            : await lstat(join(root, file.path)).catch(() => null);
        file.fingerprint = digest(
          JSON.stringify([
            mode,
            head,
            contentFile,
            stat?.size,
            stat?.mtimeMs,
            stat?.ctimeMs,
          ]),
        );
      }),
    );
    // Staging metadata changes the manifest revision, not comment/code identity.
    const statuses = parseStatus(statusOutput.stdout);
    for (const file of files.values()) {
      const status =
        statuses.get(file.path) ??
        (file.oldPath ? statuses.get(file.oldPath) : undefined);
      if (status) Object.assign(file, status);
    }
    const sorted = [...files.values()].sort((a, b) =>
      a.path.localeCompare(b.path),
    );
    return {
      source: "local",
      root,
      name: basename(root),
      branch,
      head,
      mode,
      files: sorted,
      revision: digest(JSON.stringify([head, branch, sorted])),
    };
  }

  async function patch(
    mode: DiffMode,
    file: ChangedFile,
    head: string | null,
  ): Promise<FilePatch> {
    if (file.recreated && head) return recreatedContents(file.path, head);
    if (file.binary)
      return {
        patch: "",
        message: "Binary file changed. No text preview available.",
      };
    if (file.status === "U")
      return {
        patch: "",
        message:
          "Unresolved merge conflict. Resolve this file in your editor; the viewer is read-only.",
      };
    if (mode !== "staged") {
      const stat = await lstat(join(root, file.path)).catch(() => null);
      if (stat?.isDirectory())
        return {
          patch: "",
          message:
            "Submodule or directory changed. Open its repository to review the contents.",
        };
      if (stat && !stat.isFile() && !stat.isSymbolicLink())
        return {
          patch: "",
          message: "Special file. No text preview available.",
        };
      if (stat && stat.size > MAX_PATCH_BYTES)
        return { patch: "", message: "File exceeds the 2 MiB preview limit." };
    }
    const base =
      head ??
      (await git(root, ["hash-object", "-t", "tree", "--stdin"])).stdout.trim();
    const args =
      file.status === "?"
        ? [
            "diff",
            "--no-index",
            "--no-ext-diff",
            "--no-textconv",
            ...patchFormat,
            "--",
            "/dev/null",
            file.path,
          ]
        : [
            ...diffArgs(mode, base),
            "--patch",
            "--unified=5",
            "--",
            ...(file.oldPath ? [file.oldPath] : []),
            file.path,
          ];
    try {
      const { stdout } = await git(root, args, MAX_PATCH_BYTES);
      return {
        patch: stdout,
        message: stdout
          ? null
          : "No text changes (file mode or metadata changed).",
      };
    } catch (error) {
      // git diff --no-index returns 1 when differences exist, which is success here.
      if (
        file.status === "?" &&
        error instanceof Error &&
        "code" in error &&
        error.code === 1 &&
        "stdout" in error &&
        typeof error.stdout === "string"
      ) {
        return { patch: error.stdout, message: null };
      }
      if (
        error instanceof Error &&
        "code" in error &&
        error.code === "ERR_CHILD_PROCESS_STDIO_MAXBUFFER"
      ) {
        return { patch: "", message: "Diff exceeds the 2 MiB preview limit." };
      }
      throw new RequestError(
        409,
        "File changed while loading. Refresh to try again.",
      );
    }
  }
  async function contents(
    mode: DiffMode,
    file: ChangedFile,
    head: string | null,
  ) {
    if (file.binary || file.status === "U")
      throw new RequestError(400, "Full text is unavailable for this file");
    const previousPath = file.oldPath ?? file.path;
    const readBlob = async (source: string, path: string, missing: boolean) => {
      try {
        return (await git(root, ["show", `${source}:${path}`], MAX_PATCH_BYTES))
          .stdout;
      } catch (error) {
        if (missing) return "";
        throw error;
      }
    };
    const readWorking = async () => {
      const stat = await lstat(join(root, file.path));
      if (stat.size > MAX_PATCH_BYTES)
        throw new RequestError(413, "File exceeds the 2 MiB preview limit");
      return stat.isSymbolicLink()
        ? await readlink(join(root, file.path))
        : await readFile(join(root, file.path), "utf8");
    };
    const beforeSource = mode === "unstaged" ? "" : head;
    const before = await readBlob(
      beforeSource ?? "",
      previousPath,
      !beforeSource || file.status === "A" || file.status === "?",
    );
    const after =
      mode === "staged"
        ? await readBlob("", file.path, file.status === "D")
        : file.status === "D"
          ? ""
          : await readWorking();
    if (before.includes("\0") || after.includes("\0"))
      throw new RequestError(400, "Full text is unavailable for binary files");
    return { before, after };
  }
  return {
    root,
    kind: "local",
    scopes: ["all", "staged", "unstaged"],
    live: true,
    snapshot,
    patch,
    contents,
  } satisfies DiffSource;
}

export type Repository = DiffSource;
