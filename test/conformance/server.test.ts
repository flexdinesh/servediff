import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { createApiClient } from "../../packages/api/src/client.ts";

const fixture = fileURLToPath(
  new URL("../fixtures/sample.diff", import.meta.url),
);
const binary = fileURLToPath(new URL("../../dist/servediff", import.meta.url));
const apiValues = { allScope: "all" } satisfies { allScope: "all" };

async function freePort() {
  const server = createServer();
  await new Promise<void>((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", resolve);
  });
  const address = server.address();
  if (!address || typeof address === "string") {
    server.close();
    throw new Error("Unable to reserve port");
  }
  await new Promise<void>((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve())),
  );
  return address.port;
}

async function waitForServer(url: string) {
  let lastError: unknown;
  for (let attempt = 0; attempt < 80; attempt++) {
    try {
      const response = await fetch(`${url}/openapi.yaml`);
      if (response.ok) return;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("Go server did not start", { cause: lastError });
}

test("Go distribution implements the API contract", async (t) => {
  const port = await freePort();
  const server = spawn(
    binary,
    [
      "--fixture",
      fixture,
      "--state",
      "memory",
      "--no-browser",
      "--port",
      String(port),
    ],
    { stdio: "ignore" },
  );
  t.after(() => server.kill("SIGTERM"));
  const url = `http://127.0.0.1:${port}`;
  await waitForServer(url);
  const client = createApiClient({ baseUrl: url });

  const comments = await client.GET("/api/v1/comments");
  assert.deepEqual(comments.data, { comments: [] });

  const session = await client.GET("/api/v1/session");
  assert.equal(session.data?.source, "stdin");
  assert.equal(session.data?.name, "Piped diff");
  assert.deepEqual(session.data?.capabilities, {
    diff: {
      scopes: { state: "enabled", values: [apiValues.allScope] },
      refresh: { state: "unavailable" },
      stagingMetadata: { state: "unavailable" },
    },
    files: { contents: { state: "unavailable" } },
    review: { comments: { state: "enabled" } },
  });

  const diff = await client.GET("/api/v1/diffs/current", {
    params: { query: { scope: apiValues.allScope } },
  });
  assert.ok(diff.data);
  assert.equal(diff.data.files.length, 12);
  const file = diff.data.files.find(
    (candidate) => candidate.path === "src/value.ts",
  );
  assert.ok(file);

  const preview = await client.GET(
    "/api/v1/diffs/{diffId}/files/{fileId}/patch",
    {
      params: {
        path: { diffId: diff.data.revision, fileId: file.id },
        query: { scope: apiValues.allScope, fileVersion: file.fingerprint },
      },
    },
  );
  assert.match(preview.data?.patch ?? "", /export const value = 2/);

  const created = await client.POST("/api/v1/comments", {
    body: {
      diffId: diff.data.revision,
      fileId: file.id,
      scope: apiValues.allScope,
      fileVersion: file.fingerprint,
      side: "additions",
      start: 1,
      end: 1,
      body: "Keep the new value.",
    },
  });
  assert.equal(created.data?.path, file.path);
  assert.equal(created.data?.code, "+ export const value = 2;");
  assert.equal(created.data?.body, "Keep the new value.");
});
