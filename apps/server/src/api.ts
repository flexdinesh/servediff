import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import type { IncomingMessage, ServerResponse } from "node:http";
import { fileURLToPath } from "node:url";
import { parseDiffFromFile, parsePatchFiles } from "@pierre/diffs";
import {
  commentApplicability,
  commentContext,
  formatComments,
  isDiffMode,
  validComment,
  type DiffMode,
  type RepositoryDiff,
  type ReviewComment,
} from "@servediff/shared";
import { getProcessMetrics } from "./metrics.ts";
import { ReviewStore } from "./review-store.ts";
import { type DiffSource, RequestError } from "./source.ts";

const openapiPath = fileURLToPath(
  new URL("../../../packages/api/openapi.yaml", import.meta.url),
);

interface ApiContext {
  source: DiffSource;
  sessionId: string;
  token: string;
  store: ReviewStore;
  snapshot(mode: DiffMode, fresh?: boolean): Promise<RepositoryDiff>;
}

type CommentSelection = "all" | "open" | "resolved" | "stale";

async function commentRepositories(
  context: ApiContext,
  comments: readonly ReviewComment[],
) {
  const scopes = new Set(
    comments
      .map((comment) => comment.scope)
      .filter((commentScope) => context.source.scopes.includes(commentScope)),
  );
  return Promise.all(
    [...scopes].map((commentScope) => context.snapshot(commentScope)),
  );
}

function selectedComment(
  comment: ReviewComment,
  selection: CommentSelection,
  repositories: readonly RepositoryDiff[],
) {
  if (selection === "all") return true;
  const repository = repositories.find(
    (candidate) => candidate.mode === comment.scope,
  );
  const stale = commentApplicability(comment, repository ?? null) === "stale";
  return selection === "stale" ? stale : !stale && comment.status === selection;
}

function record(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function problem(response: ServerResponse, error: unknown) {
  const status = error instanceof RequestError ? error.status : 500;
  const detail =
    error instanceof Error ? error.message : "Unable to process request";
  response.writeHead(status, {
    "Content-Type": "application/problem+json; charset=utf-8",
    ...(status === 401 ? { "WWW-Authenticate": "Bearer" } : {}),
  });
  response.end(
    JSON.stringify({
      type: "about:blank",
      title: status >= 500 ? "Internal Server Error" : "Request Failed",
      status,
      detail,
    }),
  );
}

function json(response: ServerResponse, status: number, body: unknown) {
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
  });
  response.end(JSON.stringify(body));
}

async function requestBody(request: IncomingMessage): Promise<unknown> {
  if (!request.headers["content-type"]?.startsWith("application/json"))
    throw new RequestError(415, "Expected application/json request body");
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of request) {
    if (!Buffer.isBuffer(chunk))
      throw new RequestError(400, "Invalid request body");
    size += chunk.length;
    if (size > 1024 * 1024)
      throw new RequestError(413, "Request body exceeds 1 MiB");
    chunks.push(chunk);
  }
  try {
    const value: unknown = JSON.parse(Buffer.concat(chunks).toString("utf8"));
    return value;
  } catch {
    throw new RequestError(400, "Invalid JSON request body");
  }
}

function scope(url: URL) {
  const value = url.searchParams.get("scope");
  if (!isDiffMode(value)) throw new RequestError(400, "Invalid diff scope");
  return value;
}

async function currentFile(
  context: ApiContext,
  mode: DiffMode,
  diffId: string,
  fileId: string,
  fileVersion: string | null,
  fresh = false,
) {
  const snapshot = await context.snapshot(mode, fresh);
  if (snapshot.revision !== diffId)
    throw new RequestError(
      409,
      "Diff changed. Refresh to load the latest version.",
    );
  const file = snapshot.files.find((entry) => entry.id === fileId);
  if (!file) throw new RequestError(404, "File is not in the current diff");
  if (fileVersion !== file.fingerprint)
    throw new RequestError(
      409,
      "File changed. Refresh to load the latest version.",
    );
  return { snapshot, file };
}

function createCommentBody(value: unknown): value is {
  diffId: string;
  fileId: string;
  scope: DiffMode;
  fileVersion: string;
  side: "additions" | "deletions";
  start: number;
  end: number;
  body: string;
} {
  const keys = record(value) ? Object.keys(value) : [];
  return (
    record(value) &&
    keys.length === 8 &&
    keys.every((key) =>
      [
        "diffId",
        "fileId",
        "scope",
        "fileVersion",
        "side",
        "start",
        "end",
        "body",
      ].includes(key),
    ) &&
    typeof value.diffId === "string" &&
    typeof value.fileId === "string" &&
    isDiffMode(value.scope) &&
    typeof value.fileVersion === "string" &&
    (value.side === "additions" || value.side === "deletions") &&
    typeof value.start === "number" &&
    Number.isInteger(value.start) &&
    typeof value.end === "number" &&
    Number.isInteger(value.end) &&
    typeof value.body === "string"
  );
}

function updateCommentBody(value: unknown): value is {
  body?: string;
  status?: "open" | "resolved";
} {
  if (!record(value)) return false;
  const keys = Object.keys(value);
  return (
    keys.length > 0 &&
    keys.every((key) => key === "body" || key === "status") &&
    (value.body === undefined || typeof value.body === "string") &&
    (value.status === undefined ||
      value.status === "open" ||
      value.status === "resolved")
  );
}

async function commentCode(
  context: ApiContext,
  snapshot: RepositoryDiff,
  file: RepositoryDiff["files"][number],
  side: "additions" | "deletions",
  start: number,
  end: number,
) {
  const preview = await context.source.patch(
    snapshot.mode,
    file,
    snapshot.head,
  );
  const parsed =
    preview.contents && preview.message === null
      ? parseDiffFromFile(
          {
            name: file.oldPath ?? file.path,
            contents: preview.contents.before,
          },
          { name: file.path, contents: preview.contents.after },
        )
      : preview.patch
        ? parsePatchFiles(preview.patch, file.fingerprint, true).flatMap(
            (patch) => patch.files,
          )[0]
        : undefined;
  const contextResult = parsed
    ? commentContext(parsed, { side, start, end })
    : null;
  if (!contextResult)
    throw new RequestError(400, "Select up to 200 visible lines on one side");
  return contextResult.code;
}

function commentRoute(pathname: string) {
  const match = /^\/api\/v1\/comments\/([^/]+)$/.exec(pathname);
  return match?.[1] ?? null;
}

function fileRoute(pathname: string) {
  const match =
    /^\/api\/v1\/diffs\/([^/]+)\/files\/([^/]+)\/(patch|contents)$/.exec(
      pathname,
    );
  if (!match?.[1] || !match[2] || !match[3]) return null;
  return { diffId: match[1], fileId: match[2], resource: match[3] };
}

function reviewMarkRoute(pathname: string) {
  const match = /^\/api\/v1\/review-marks\/([^/]+)$/.exec(pathname);
  return match?.[1] ?? null;
}

export async function handleApi(
  request: IncomingMessage,
  response: ServerResponse,
  url: URL,
  context: ApiContext,
) {
  if (url.pathname === "/openapi.yaml") {
    if (request.method !== "GET" && request.method !== "HEAD")
      throw new RequestError(405, "Method not allowed");
    response.setHeader("Content-Type", "application/yaml; charset=utf-8");
    response.end(
      request.method === "HEAD" ? undefined : await readFile(openapiPath),
    );
    return true;
  }
  if (!url.pathname.startsWith("/api/v1/")) return false;
  if (
    context.token &&
    request.headers.authorization !== `Bearer ${context.token}`
  )
    throw new RequestError(401, "Missing or invalid API token");

  if (url.pathname === "/api/v1/metrics" && request.method === "GET") {
    json(response, 200, getProcessMetrics());
    return true;
  }
  if (url.pathname === "/api/v1/session" && request.method === "GET") {
    const snapshot = await context.snapshot(context.source.scopes[0] ?? "all");
    json(response, 200, {
      id: context.sessionId,
      source: context.source.kind,
      name: snapshot.name,
      root: snapshot.root,
      capabilities: {
        scopes: context.source.scopes,
        live: context.source.live,
        fullFileContents: context.source.kind === "local",
      },
    });
    return true;
  }
  if (url.pathname === "/api/v1/diffs/current" && request.method === "GET") {
    json(response, 200, await context.snapshot(scope(url)));
    return true;
  }
  const file = fileRoute(url.pathname);
  if (file && request.method === "GET") {
    const mode = scope(url);
    const current = await currentFile(
      context,
      mode,
      file.diffId,
      file.fileId,
      url.searchParams.get("fileVersion"),
    );
    json(
      response,
      200,
      file.resource === "patch"
        ? await context.source.patch(mode, current.file, current.snapshot.head)
        : await context.source.contents(
            mode,
            current.file,
            current.snapshot.head,
          ),
    );
    return true;
  }
  if (url.pathname === "/api/v1/comments" && request.method === "GET") {
    json(response, 200, {
      comments: await context.store.comments(context.sessionId),
    });
    return true;
  }
  if (url.pathname === "/api/v1/comments" && request.method === "DELETE") {
    const status = url.searchParams.get("status");
    if (
      status !== "all" &&
      status !== "open" &&
      status !== "resolved" &&
      status !== "stale"
    )
      throw new RequestError(400, "Invalid comment status");
    const comments = await context.store.comments(context.sessionId);
    const repositories = await commentRepositories(context, comments);
    const ids = new Set(
      comments
        .filter((comment) => selectedComment(comment, status, repositories))
        .map((comment) => comment.id),
    );
    const deleted = await context.store.deleteComments(context.sessionId, ids);
    json(response, 200, {
      comments: await context.store.comments(context.sessionId),
      deleted,
    });
    return true;
  }
  if (url.pathname === "/api/v1/comments" && request.method === "POST") {
    const body = await requestBody(request);
    if (!createCommentBody(body) || !body.body.trim())
      throw new RequestError(400, "Invalid comment");
    const current = await currentFile(
      context,
      body.scope,
      body.diffId,
      body.fileId,
      body.fileVersion,
      true,
    );
    const comment: ReviewComment = {
      id: randomBytes(16).toString("hex"),
      path: current.file.path,
      scope: body.scope,
      fingerprint: current.file.fingerprint,
      side: body.side,
      start: Math.min(body.start, body.end),
      end: Math.max(body.start, body.end),
      code: await commentCode(
        context,
        current.snapshot,
        current.file,
        body.side,
        body.start,
        body.end,
      ),
      body: body.body.trim(),
      status: "open",
      createdAt: Date.now(),
      origin: {
        source: current.snapshot.source,
        repository: current.snapshot.name,
        branch: current.snapshot.branch,
        head: current.snapshot.head,
        revision: current.snapshot.revision,
        file: { status: current.file.status, oldPath: current.file.oldPath },
      },
    };
    await context.store.putComment(context.sessionId, comment);
    json(response, 201, comment);
    return true;
  }
  if (url.pathname === "/api/v1/comments/import" && request.method === "POST") {
    const body = await requestBody(request);
    if (
      !record(body) ||
      Object.keys(body).length !== 1 ||
      !Array.isArray(body.comments) ||
      !body.comments.every(validComment)
    )
      throw new RequestError(400, "Invalid comment import");
    json(response, 200, {
      comments: await context.store.importComments(
        context.sessionId,
        body.comments,
      ),
    });
    return true;
  }
  if (url.pathname === "/api/v1/comments/export" && request.method === "GET") {
    const resolvedParameter = url.searchParams.get("includeResolved");
    if (
      resolvedParameter !== null &&
      resolvedParameter !== "true" &&
      resolvedParameter !== "false"
    )
      throw new RequestError(400, "Invalid includeResolved value");
    const includeResolved = resolvedParameter === "true";
    const requestedCommentId = url.searchParams.get("commentId");
    const revision = url.searchParams.get("revision");
    const requestedScope = url.searchParams.get("scope");
    if (requestedScope !== null && !isDiffMode(requestedScope))
      throw new RequestError(400, "Invalid diff scope");
    if ((revision === null) !== (requestedScope === null))
      throw new RequestError(
        400,
        "revision and scope must be provided together",
      );
    const comments = await context.store.comments(context.sessionId);
    const current =
      revision && requestedScope
        ? await context.snapshot(requestedScope)
        : null;
    const requestedComments = requestedCommentId
      ? comments.filter((comment) => comment.id === requestedCommentId)
      : comments;
    const selected = revision
      ? requestedComments.filter(
          (comment) =>
            comment.scope === requestedScope &&
            (comment.origin?.revision === revision ||
              (comment.origin === undefined &&
                current?.revision === revision &&
                current.files.some(
                  (file) =>
                    file.path === comment.path &&
                    file.fingerprint === comment.fingerprint,
                ))),
        )
      : requestedComments;
    const repositories = await commentRepositories(context, selected);
    response.setHeader("Content-Type", "application/xml; charset=utf-8");
    response.end(formatComments(selected, includeResolved, repositories));
    return true;
  }
  const commentId = commentRoute(url.pathname);
  if (commentId && request.method === "PATCH") {
    const body = await requestBody(request);
    if (!updateCommentBody(body) || body.body?.trim() === "")
      throw new RequestError(400, "Invalid comment update");
    const comments = await context.store.comments(context.sessionId);
    const current = comments.find((comment) => comment.id === commentId);
    if (!current) throw new RequestError(404, "Comment not found");
    const updated: ReviewComment = {
      ...current,
      ...(body.body === undefined ? {} : { body: body.body.trim() }),
      ...(body.status === undefined ? {} : { status: body.status }),
    };
    await context.store.putComment(context.sessionId, updated);
    json(response, 200, updated);
    return true;
  }
  if (commentId && request.method === "DELETE") {
    if (!(await context.store.deleteComment(context.sessionId, commentId)))
      throw new RequestError(404, "Comment not found");
    response.writeHead(204).end();
    return true;
  }
  if (url.pathname === "/api/v1/review-marks" && request.method === "GET") {
    json(response, 200, {
      marks: await context.store.marks(context.sessionId, scope(url)),
    });
    return true;
  }
  if (url.pathname === "/api/v1/review-marks" && request.method === "DELETE") {
    await context.store.clearMarks(context.sessionId, scope(url));
    response.writeHead(204).end();
    return true;
  }
  const fileId = reviewMarkRoute(url.pathname);
  if (fileId && request.method === "PUT") {
    const mode = scope(url);
    const body = await requestBody(request);
    if (
      !record(body) ||
      Object.keys(body).length !== 1 ||
      typeof body.fileVersion !== "string"
    )
      throw new RequestError(400, "Invalid review mark");
    const snapshot = await context.snapshot(mode, true);
    const changedFile = snapshot.files.find((entry) => entry.id === fileId);
    if (!changedFile)
      throw new RequestError(404, "File is not in the current diff");
    if (changedFile.fingerprint !== body.fileVersion)
      throw new RequestError(409, "File changed. Refresh to try again.");
    const mark = { fileId, fileVersion: body.fileVersion, scope: mode };
    await context.store.putMark(context.sessionId, mark);
    json(response, 200, mark);
    return true;
  }
  if (fileId && request.method === "DELETE") {
    await context.store.deleteMark(context.sessionId, scope(url), fileId);
    response.writeHead(204).end();
    return true;
  }
  if (
    url.pathname === "/api/v1/session" ||
    url.pathname === "/api/v1/diffs/current" ||
    url.pathname === "/api/v1/comments" ||
    url.pathname === "/api/v1/comments/import" ||
    url.pathname === "/api/v1/comments/export" ||
    url.pathname === "/api/v1/review-marks" ||
    file ||
    commentId ||
    fileId
  )
    throw new RequestError(405, "Method not allowed");
  throw new RequestError(404, "Unknown API route");
}

export async function respondWithProblem(
  response: ServerResponse,
  error: unknown,
) {
  problem(response, error);
}
