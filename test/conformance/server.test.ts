import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile } from "node:fs/promises";
import { createServer } from "node:net";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import {
  createApiClient,
  type ApiChangedFile,
  type ApiFilePatch,
} from "../../packages/api/src/client.ts";
import { ReviewStore } from "../../apps/server/src/review-store.ts";
import { startServer } from "../../apps/server/src/server.ts";

const fixture = fileURLToPath(
  new URL("../fixtures/sample.diff", import.meta.url),
);
const binary = fileURLToPath(
  new URL("../../dist/servediff-go", import.meta.url),
);
const apiValues = {
  allScope: "all",
  additionsSide: "additions",
} satisfies { allScope: "all"; additionsSide: "additions" };

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

test("Node and Go implement the fixture API contract", async (t) => {
  const token = "conformance-token";
  const input = await readFile(fixture, "utf8");
  const node = await startServer({
    directory: ".",
    input,
    port: 0,
    token,
    store: new ReviewStore(null),
  });
  t.after(() => node.close());

  const port = await freePort();
  const go = spawn(
    binary,
    [
      "--fixture",
      fixture,
      "--state",
      "memory",
      "--token",
      token,
      "--no-browser",
      "--port",
      String(port),
    ],
    { stdio: "ignore" },
  );
  t.after(() => go.kill("SIGTERM"));
  const goUrl = `http://127.0.0.1:${port}`;
  await waitForServer(goUrl);

  const nodeClient = createApiClient({ baseUrl: node.url, token });
  const goClient = createApiClient({ baseUrl: goUrl, token });
  const [nodeSession, goSession] = await Promise.all([
    nodeClient.GET("/api/v1/session"),
    goClient.GET("/api/v1/session"),
  ]);
  assert.deepEqual(goSession.data, nodeSession.data);

  const [nodeDiff, goDiff] = await Promise.all([
    nodeClient.GET("/api/v1/diffs/current", {
      params: { query: { scope: apiValues.allScope } },
    }),
    goClient.GET("/api/v1/diffs/current", {
      params: { query: { scope: apiValues.allScope } },
    }),
  ]);
  assert.deepEqual(goDiff.data, nodeDiff.data);
  assert.ok(nodeDiff.data);
  assert.ok(goDiff.data);

  for (const file of nodeDiff.data.files) {
    const goFile: ApiChangedFile | undefined = goDiff.data.files.find(
      (candidate) => candidate.id === file.id,
    );
    assert.ok(goFile);
    const nodePatch: { data?: ApiFilePatch } = await nodeClient.GET(
      "/api/v1/diffs/{diffId}/files/{fileId}/patch",
      {
        params: {
          path: { diffId: nodeDiff.data.revision, fileId: file.id },
          query: {
            scope: apiValues.allScope,
            fileVersion: file.fingerprint,
          },
        },
      },
    );
    const goPatch: { data?: ApiFilePatch } = await goClient.GET(
      "/api/v1/diffs/{diffId}/files/{fileId}/patch",
      {
        params: {
          path: { diffId: nodeDiff.data.revision, fileId: file.id },
          query: {
            scope: apiValues.allScope,
            fileVersion: file.fingerprint,
          },
        },
      },
    );
    assert.deepEqual(goPatch.data, nodePatch.data);
  }

  const file = nodeDiff.data.files.find(
    (candidate) => candidate.path === "src/value.ts",
  );
  assert.ok(file);
  const body = {
    diffId: nodeDiff.data.revision,
    fileId: file.id,
    scope: apiValues.allScope,
    fileVersion: file.fingerprint,
    side: apiValues.additionsSide,
    start: 1,
    end: 1,
    body: "Keep the new value.",
  };
  const [nodeComment, goComment] = await Promise.all([
    nodeClient.POST("/api/v1/comments", { body }),
    goClient.POST("/api/v1/comments", { body }),
  ]);
  assert.ok(nodeComment.data);
  assert.ok(goComment.data);
  const {
    id: _nodeID,
    createdAt: _nodeCreated,
    ...nodeStable
  } = nodeComment.data;
  const { id: _goID, createdAt: _goCreated, ...goStable } = goComment.data;
  assert.deepEqual(goStable, nodeStable);
});
