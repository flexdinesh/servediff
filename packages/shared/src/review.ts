import type {
  AnnotationSide,
  FileDiffMetadata,
  SelectedLineRange,
} from "@pierre/diffs";
import type { DiffMode, RepositoryDiff } from "./index.ts";

export interface ReviewComment {
  id: string;
  path: string;
  scope: DiffMode;
  fingerprint: string;
  side: AnnotationSide;
  start: number;
  end: number;
  code: string;
  body: string;
  status: "open" | "resolved";
  createdAt: number;
  origin?: ReviewOrigin;
}

export interface ReviewOrigin {
  source: "local" | "stdin";
  repository: string;
  branch: string;
  head: string | null;
  revision: string;
  file: { status: string; oldPath: string | null };
}

export interface ReviewRound {
  key: string;
  comments: ReviewComment[];
  current: boolean;
}

export type CommentApplicability =
  | "anchored"
  | "stale"
  | "other-scope"
  | "unknown";

export function lineContext(
  diff: FileDiffMetadata,
  side: AnnotationSide,
  lineNumber: number,
): string | null {
  const lines = side === "additions" ? diff.additionLines : diff.deletionLines;
  for (const hunk of diff.hunks) {
    const start =
      side === "additions" ? hunk.additionStart : hunk.deletionStart;
    const count =
      side === "additions" ? hunk.additionCount : hunk.deletionCount;
    if (lineNumber < start || lineNumber >= start + count) continue;
    const offset = lineNumber - start;
    const index = diff.isPartial
      ? (side === "additions"
          ? hunk.additionLineIndex
          : hunk.deletionLineIndex) + offset
      : lineNumber - 1;
    const content = lines[index];
    if (content === undefined) return null;
    let remaining = offset;
    let prefix = " ";
    for (const segment of hunk.hunkContent) {
      const length =
        segment.type === "context"
          ? segment.lines
          : side === "additions"
            ? segment.additions
            : segment.deletions;
      if (remaining < length) {
        prefix =
          segment.type === "context" ? " " : side === "additions" ? "+" : "-";
        break;
      }
      remaining -= length;
    }
    return `${prefix} ${content.replace(/\r?\n$/, "")}`;
  }
  const content = !diff.isPartial ? lines[lineNumber - 1] : undefined;
  return content === undefined ? null : `  ${content.replace(/\r?\n$/, "")}`;
}

export function commentContext(
  diff: FileDiffMetadata,
  range: SelectedLineRange,
) {
  const side = range.endSide ?? range.side;
  if (side !== "additions" && side !== "deletions") return null;
  const crossSide = range.side !== undefined && range.side !== side;
  const start = crossSide ? range.end : Math.min(range.start, range.end);
  const end = crossSide ? range.end : Math.max(range.start, range.end);
  if (start < 1 || end - start > 199) return null;
  const code: string[] = [];
  for (let line = start; line <= end; line++) {
    const content = lineContext(diff, side, line);
    if (content === null) return null;
    code.push(content);
  }
  return { side, start, end, code: code.join("\n") };
}

export interface ReviewMark {
  fileId: string;
  fileVersion: string;
  scope: DiffMode;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function diffMode(value: unknown): value is DiffMode {
  return value === "all" || value === "staged" || value === "unstaged";
}

function validOrigin(value: unknown): value is ReviewOrigin {
  if (!record(value) || !record(value.file)) return false;
  return (
    (value.source === "local" || value.source === "stdin") &&
    typeof value.repository === "string" &&
    typeof value.branch === "string" &&
    (value.head === null || typeof value.head === "string") &&
    typeof value.revision === "string" &&
    typeof value.file.status === "string" &&
    (value.file.oldPath === null || typeof value.file.oldPath === "string")
  );
}

export function validComment(value: unknown): value is ReviewComment {
  return (
    record(value) &&
    typeof value.id === "string" &&
    typeof value.path === "string" &&
    diffMode(value.scope) &&
    typeof value.fingerprint === "string" &&
    (value.side === "additions" || value.side === "deletions") &&
    typeof value.start === "number" &&
    Number.isInteger(value.start) &&
    value.start > 0 &&
    typeof value.end === "number" &&
    Number.isInteger(value.end) &&
    value.end >= value.start &&
    value.end - value.start < 200 &&
    typeof value.code === "string" &&
    typeof value.body === "string" &&
    value.body.trim().length > 0 &&
    (value.status === "open" || value.status === "resolved") &&
    typeof value.createdAt === "number" &&
    Number.isFinite(value.createdAt) &&
    (value.origin === undefined || validOrigin(value.origin))
  );
}

export function parseComments(raw: string | null): ReviewComment[] {
  try {
    const value: unknown = JSON.parse(raw ?? "[]");
    return Array.isArray(value) ? value.filter(validComment) : [];
  } catch {
    return [];
  }
}

function xml(value: string) {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&apos;");
}

function attribute(value: string) {
  return xml(value)
    .replaceAll("\n", "&#10;")
    .replaceAll("\r", "&#13;")
    .replaceAll("\t", "&#9;");
}

function changeName(status: string) {
  switch (status) {
    case "A":
      return "added";
    case "M":
      return "modified";
    case "D":
      return "deleted";
    case "R":
      return "renamed";
    case "C":
      return "copied";
    case "T":
      return "type-changed";
    case "U":
      return "conflicted";
    case "?":
      return "untracked";
    default:
      return status;
  }
}

function snapshotKey(origin: ReviewOrigin | undefined) {
  return origin
    ? JSON.stringify([
        origin.source,
        origin.repository,
        origin.branch,
        origin.head,
        origin.revision,
      ])
    : "unknown";
}

export function formatComments(
  comments: readonly ReviewComment[],
  includeResolved: boolean,
  repositories: readonly RepositoryDiff[] = [],
): string {
  const applicability = (comment: ReviewComment) => {
    const repository = repositories.find(
      (candidate) => candidate.mode === comment.scope,
    );
    return commentApplicability(comment, repository ?? null);
  };
  const selected = includeResolved
    ? comments
    : comments.filter(
        (comment) =>
          comment.status === "open" && applicability(comment) !== "stale",
      );
  if (selected.length === 0) return "";
  const reviews = new Map<
    string,
    {
      origin: ReviewOrigin | undefined;
      files: Map<string, Array<{ id: string; comment: ReviewComment }>>;
    }
  >();
  for (const [index, comment] of selected.entries()) {
    const key = snapshotKey(comment.origin);
    const review = reviews.get(key) ?? {
      origin: comment.origin,
      files: new Map<string, Array<{ id: string; comment: ReviewComment }>>(),
    };
    const file = review.files.get(comment.path) ?? [];
    file.push({ id: `C${index + 1}`, comment });
    review.files.set(comment.path, file);
    reviews.set(key, review);
  }
  const instruction = [
    includeResolved
      ? "Address every anchored open review comment."
      : "Address every unresolved review comment.",
    "Inspect the current working tree before editing because code and line numbers describe the reviewed snapshot.",
    "Preserve unrelated changes.",
    ...(includeResolved
      ? [
          "Resolved and stale comments are context only; do not act on them.",
          "If an anchored open comment cannot be applied, report it using its comment ID.",
        ]
      : []),
  ].join(" ");
  const lines = [
    '<code-review-comments version="2">',
    `  <instructions>${xml(instruction)}</instructions>`,
  ];
  for (const review of reviews.values()) {
    const origin = review.origin;
    if (origin) {
      const head = origin.head ? ` head="${attribute(origin.head)}"` : "";
      lines.push(
        `  <review source="${origin.source}" repository="${attribute(origin.repository)}" branch="${attribute(origin.branch)}"${head} revision="${attribute(origin.revision)}">`,
      );
    } else {
      lines.push('  <review origin="unknown">');
    }
    for (const [path, commentsForFile] of review.files) {
      const fileOrigin = commentsForFile[0]?.comment.origin?.file;
      const oldPath = fileOrigin?.oldPath
        ? ` old-path="${attribute(fileOrigin.oldPath)}"`
        : "";
      const change = fileOrigin
        ? ` change="${attribute(changeName(fileOrigin.status))}"`
        : "";
      lines.push(`    <file path="${attribute(path)}"${oldPath}${change}>`);
      for (const { id, comment } of commentsForFile) {
        const selection =
          comment.start === comment.end ? "single-line" : "range";
        const anchorState = applicability(comment);
        lines.push(
          `      <comment id="${id}" selection="${selection}" line="${comment.start}" end-line="${comment.end}" side="${comment.side}" scope="${comment.scope}" status="${comment.status}" applicability="${anchorState}">`,
          `        <code>${xml(comment.code)}</code>`,
          `        <body>${xml(comment.body)}</body>`,
          "      </comment>",
        );
      }
      lines.push("    </file>");
    }
    lines.push("  </review>");
  }
  lines.push("</code-review-comments>");
  return lines.join("\n");
}

export function anchored(
  comment: ReviewComment,
  repository: RepositoryDiff | null,
) {
  return commentApplicability(comment, repository) === "anchored";
}

export function commentApplicability(
  comment: ReviewComment,
  repository: RepositoryDiff | null,
): CommentApplicability {
  if (!repository) return "unknown";
  if (comment.scope !== repository.mode) return "other-scope";
  return repository.files.some(
    (file) =>
      file.path === comment.path && file.fingerprint === comment.fingerprint,
  )
    ? "anchored"
    : "stale";
}

function currentComment(
  comment: ReviewComment,
  repository: RepositoryDiff | null,
) {
  if (comment.scope !== repository?.mode) return false;
  if (comment.origin) return comment.origin.revision === repository.revision;
  return repository.files.some(
    (file) =>
      file.path === comment.path && file.fingerprint === comment.fingerprint,
  );
}

export function reviewRounds(
  comments: readonly ReviewComment[],
  repository: RepositoryDiff | null,
): ReviewRound[] {
  const rounds = new Map<string, ReviewRound>();
  for (const comment of comments) {
    const current = currentComment(comment, repository);
    const key = comment.origin
      ? `snapshot:${comment.scope}:${comment.origin.revision}`
      : `legacy:${current ? "current" : comment.fingerprint}`;
    const round = rounds.get(key) ?? { key, comments: [], current };
    round.comments.push(comment);
    rounds.set(key, round);
  }
  return [...rounds.values()].sort((left, right) => {
    if (left.current !== right.current) return left.current ? -1 : 1;
    return (
      (right.comments[0]?.createdAt ?? 0) - (left.comments[0]?.createdAt ?? 0)
    );
  });
}
