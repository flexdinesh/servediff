import { createHash } from "node:crypto";
import type { Readable } from "node:stream";
import { stripVTControlCharacters } from "node:util";
import { parsePatchFiles } from "@pierre/diffs";
import type { ChangedFile, FilePatch, RepositoryDiff } from "@servediff/shared";
import { type DiffSource, RequestError } from "./source.ts";

const MAX_INPUT_BYTES = 16 * 1024 * 1024;
const MAX_FILE_BYTES = 2 * 1024 * 1024;

// Wait for EOF so the viewer represents exactly one bounded command result.
export async function readPatchInput(input: Readable): Promise<string> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of input) {
    if (!Buffer.isBuffer(chunk))
      throw new Error("Expected binary stdin stream");
    size += chunk.length;
    if (size > MAX_INPUT_BYTES)
      throw new Error("Piped diff exceeds the 16 MiB input limit");
    chunks.push(chunk);
  }
  return Buffer.concat(chunks).toString("utf8");
}

// Git headers are unprefixed; hunk contents always start with space, +, or -.
// Split commit boundaries before file boundaries to avoid swallowing later commits.
export function openPatch(input: string): DiffSource {
  if (Buffer.byteLength(input) > MAX_INPUT_BYTES)
    throw new Error("Piped diff exceeds the 16 MiB input limit");
  const data = stripVTControlCharacters(input);
  if (/^diff --(?:cc|combined) /m.test(data))
    throw new Error(
      "Combined merge diffs are not supported. Use git show --diff-merges=separate | servediff instead.",
    );
  const revision = createHash("sha256").update(data).digest("hex");
  const root = `stdin:${revision}`;
  const files: ChangedFile[] = [];
  const previews = new Map<string, FilePatch>();
  const commits = data
    .split(/(?=^(?:commit [a-f\d]{40,64}(?: .*)?|From [a-f\d]{40,64} .*)$)/m)
    .filter((part) => /^diff --git /m.test(part));
  let section = 0;
  for (const commit of commits) {
    section++;
    const commitId = /^(?:commit|From) ([a-f\d]{40,64})/m.exec(commit)?.[1];
    const prefix =
      commits.length > 1
        ? `${section} · ${commitId?.slice(0, 8) ?? "patch"}/`
        : "";
    const chunks = commit
      .split(/(?=^diff --git )/m)
      .filter((part) => part.startsWith("diff --git "));
    for (const patch of chunks) {
      if (Buffer.byteLength(patch) > MAX_FILE_BYTES)
        throw new Error(
          "A file in the piped diff exceeds the 2 MiB preview limit",
        );
      const parsed = parsePatchFiles(patch, undefined, true).flatMap(
        (entry) => entry.files,
      );
      const file = parsed[0];
      if (!file || parsed.length !== 1)
        throw new Error("Could not parse a file in the piped diff");
      const path = `${prefix}${file.name}`;
      if (previews.has(path))
        throw new Error(
          `Repeated file in patch: ${path}. Pipe standard git show or git diff output.`,
        );
      const binary = /^(Binary files .* differ|GIT binary patch)$/m.test(patch);
      const fingerprint = createHash("sha256").update(patch).digest("hex");
      files.push({
        id: createHash("sha256").update(path).digest("hex"),
        path,
        oldPath: file.prevName ? `${prefix}${file.prevName}` : null,
        status:
          file.type === "new"
            ? "A"
            : file.type === "deleted"
              ? "D"
              : file.type.startsWith("rename")
                ? "R"
                : /^copy from /m.test(patch)
                  ? "C"
                  : file.mode &&
                      file.prevMode &&
                      file.mode[0] !== file.prevMode[0]
                    ? "T"
                    : "M",
        indexStatus: "",
        worktreeStatus: "",
        additions: file.hunks.reduce(
          (sum, hunk) => sum + hunk.additionLines,
          0,
        ),
        deletions: file.hunks.reduce(
          (sum, hunk) => sum + hunk.deletionLines,
          0,
        ),
        binary,
        fingerprint,
      });
      previews.set(path, {
        patch: binary ? "" : patch,
        message: binary
          ? "Binary file changed. No text preview available."
          : null,
      });
    }
  }
  if (
    !files.length &&
    data.trim() &&
    !/^(commit |From )[a-f\d]{40,64}/m.test(data)
  )
    throw new Error(
      "No Git patch found on stdin. Pipe git diff or git show output into servediff.",
    );
  const manifest: RepositoryDiff = {
    source: "stdin",
    root,
    name: "Piped diff",
    branch: "stdin",
    head: null,
    mode: "all",
    files,
    revision,
  };
  return {
    root,
    kind: "stdin",
    scopes: ["all"],
    live: false,
    async snapshot(mode) {
      if (mode !== "all")
        throw new RequestError(
          400,
          "Piped diffs have no staged or unstaged scope",
        );
      return manifest;
    },
    async patch(_mode, file) {
      const preview = previews.get(file.path);
      if (!preview)
        throw new RequestError(404, "File is not in the piped diff");
      return preview;
    },
    async contents() {
      throw new RequestError(
        400,
        "Full context is unavailable for piped diffs",
      );
    },
  };
}
