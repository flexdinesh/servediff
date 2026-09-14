import assert from "node:assert/strict";
import { readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { createApiClient } from "@servediff/api";
import { ReviewStore } from "../src/review-store.ts";
import { startServer } from "../src/server.ts";

const input = await readFile(
  fileURLToPath(new URL("../../../test/fixtures/sample.diff", import.meta.url)),
  "utf8",
);

test("persists comments and review marks through the authenticated API", async (t) => {
  const path = join(
    tmpdir(),
    `servediff-review-${process.pid}-${Date.now()}.json`,
  );
  t.after(() => rm(path, { force: true }));
  const token = "test-token";
  const first = await startServer({
    directory: ".",
    input,
    port: 0,
    dev: true,
    token,
    store: new ReviewStore(path),
  });
  const api = `${first.url}/api/v1`;
  const client = createApiClient({ baseUrl: first.url, token });
  assert.equal((await fetch(`${first.url}/openapi.yaml`)).status, 200);
  assert.equal((await fetch(`${api}/comments`)).status, 401);
  assert.equal((await fetch(`${api}/metrics`)).status, 401);
  const { data: metrics } = await client.GET("/api/v1/metrics");
  assert.ok(metrics);
  assert.ok(metrics.rssBytes > 0);
  assert.ok(Number.isFinite(metrics.cpuUsage));
  assert.ok(metrics.cpuUsage >= 0);
  const { data: session } = await client.GET("/api/v1/session");
  assert.ok(session);
  assert.deepEqual(session.capabilities, {
    scopes: ["all"],
    live: false,
    fullFileContents: false,
  });
  const { data: snapshot } = await client.GET("/api/v1/diffs/current", {
    params: { query: { scope: "all" } },
  });
  assert.ok(snapshot);
  const file = snapshot.files.find((entry) => entry.path === "src/value.ts");
  assert.ok(file);
  const { data: created, response: createdResponse } = await client.POST(
    "/api/v1/comments",
    {
      body: {
        diffId: snapshot.revision,
        fileId: file.id,
        scope: "all",
        fileVersion: file.fingerprint,
        side: "additions",
        start: 1,
        end: 1,
        body: "Keep the new value.",
      },
    },
  );
  assert.equal(createdResponse.status, 201);
  assert.ok(created);
  assert.equal(created.code, "+ export const value = 2;");
  assert.ok(created.origin);
  assert.equal(created.origin.revision, snapshot.revision);
  const { data: resolved } = await client.PATCH(
    "/api/v1/comments/{commentId}",
    {
      params: { path: { commentId: created.id } },
      body: { status: "resolved" },
    },
  );
  assert.ok(resolved);
  assert.equal(resolved.status, "resolved");
  const { response: importResponse } = await client.POST(
    "/api/v1/comments/import",
    {
      body: {
        comments: [
          {
            ...created,
            id: "stale-comment",
            fingerprint: "earlier-file-version",
            status: "open",
          },
        ],
      },
    },
  );
  assert.equal(importResponse.status, 200);
  const { response: markResponse } = await client.PUT(
    "/api/v1/review-marks/{fileId}",
    {
      params: {
        path: { fileId: file.id },
        query: { scope: "all" },
      },
      body: { fileVersion: file.fingerprint },
    },
  );
  assert.equal(markResponse.status, 200);
  const unresolvedExport = await client.GET("/api/v1/comments/export", {
    params: { query: { includeResolved: false } },
    parseAs: "text",
  });
  assert.equal(unresolvedExport.data ?? "", "");
  const allExport =
    (
      await client.GET("/api/v1/comments/export", {
        params: { query: { includeResolved: true } },
        parseAs: "text",
      })
    ).data ?? "";
  assert.match(allExport, /Keep the new value/);
  assert.match(allExport, /status="open" applicability="stale"/);
  assert.match(allExport, /status="resolved" applicability="anchored"/);
  const singleExport =
    (
      await client.GET("/api/v1/comments/export", {
        params: {
          query: { includeResolved: true, commentId: "stale-comment" },
        },
        parseAs: "text",
      })
    ).data ?? "";
  assert.match(singleExport, /status="open" applicability="stale"/);
  assert.doesNotMatch(singleExport, /applicability="anchored"/);
  await first.close();

  const second = await startServer({
    directory: ".",
    input,
    port: 0,
    dev: true,
    token,
    store: new ReviewStore(path),
  });
  t.after(() => second.close());
  const secondClient = createApiClient({ baseUrl: second.url, token });
  const { data: comments } = await secondClient.GET("/api/v1/comments");
  assert.ok(comments);
  assert.equal(comments.comments[0]?.id, created.id);
  const { data: marks } = await secondClient.GET("/api/v1/review-marks", {
    params: { query: { scope: "all" } },
  });
  assert.ok(marks);
  assert.deepEqual(marks.marks, [
    { fileId: file.id, fileVersion: file.fingerprint, scope: "all" },
  ]);
  const { data: staleDeletion } = await secondClient.DELETE(
    "/api/v1/comments",
    { params: { query: { status: "stale" } } },
  );
  assert.ok(staleDeletion);
  assert.equal(staleDeletion.deleted, 1);
  assert.deepEqual(
    staleDeletion.comments.map((comment) => comment.status),
    ["resolved"],
  );
  const { data: resolvedDeletion } = await secondClient.DELETE(
    "/api/v1/comments",
    { params: { query: { status: "resolved" } } },
  );
  assert.ok(resolvedDeletion);
  assert.equal(resolvedDeletion.deleted, 1);
  assert.deepEqual(resolvedDeletion.comments, []);
  const { data: restored } = await secondClient.POST(
    "/api/v1/comments/import",
    {
      body: {
        comments: [
          { ...created, id: "bulk-open", status: "open" },
          { ...resolved, id: "bulk-resolved", status: "resolved" },
        ],
      },
    },
  );
  assert.equal(restored?.comments.length, 2);
  const { data: openDeletion } = await secondClient.DELETE("/api/v1/comments", {
    params: { query: { status: "open" } },
  });
  assert.ok(openDeletion);
  assert.equal(openDeletion.deleted, 1);
  assert.equal(openDeletion.comments[0]?.status, "resolved");
  const { data: allDeletion } = await secondClient.DELETE("/api/v1/comments", {
    params: { query: { status: "all" } },
  });
  assert.ok(allDeletion);
  assert.equal(allDeletion.deleted, 1);
  assert.deepEqual(allDeletion.comments, []);
});
