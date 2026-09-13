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
  assert.match(
    (
      await client.GET("/api/v1/comments/export", {
        params: { query: { includeResolved: true } },
        parseAs: "text",
      })
    ).data ?? "",
    /Keep the new value/,
  );
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
});
