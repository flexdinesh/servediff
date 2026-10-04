import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { createApiClient } from "../../packages/api/src/client.ts";
import type { components } from "../../packages/api/src/schema.d.ts";

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
      "serve",
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

  const catalog = await client.GET("/api/v2/contexts");
  assert.equal(catalog.data?.contexts.length, 1);
  const context = catalog.data?.contexts[0];
  assert.ok(context);
  assert.equal(context.kind, "observation");
  const contextId = context.id;

  const comments = await client.GET("/api/v2/contexts/{contextId}/comments", {
    params: { path: { contextId } },
  });
  assert.deepEqual(comments.data, { comments: [] });

  const session = await client.GET("/api/v1/session");
  assert.equal(session.data?.source, "stdin");
  assert.deepEqual(session.data?.capabilities, {
    diff: {
      scopes: { state: "enabled", values: [apiValues.allScope] },
      refresh: { state: "unavailable" },
      stagingMetadata: { state: "unavailable" },
    },
    files: { contents: { state: "unavailable" } },
    review: { comments: { state: "enabled" } },
  });

  const diff = await client.GET("/api/v2/contexts/{contextId}/diffs/current", {
    params: { path: { contextId }, query: { scope: apiValues.allScope } },
  });
  assert.ok(diff.data);
  assert.equal(session.data?.name, diff.data.name);
  assert.equal(diff.data.files.length, 12);
  const file = diff.data.files.find(
    (candidate) => candidate.path === "src/value.ts",
  );
  assert.ok(file);

  const preview = await client.GET(
    "/api/v2/contexts/{contextId}/diffs/{diffId}/files/{fileId}/patch",
    {
      params: {
        path: { contextId, diffId: diff.data.id, fileId: file.id },
        query: {
          scope: apiValues.allScope,
          versionId: diff.data.versionId,
          fileVersion: file.fingerprint,
        },
      },
    },
  );
  assert.match(preview.data?.patch ?? "", /export const value = 2/);

  const created = await client.POST("/api/v2/contexts/{contextId}/comments", {
    params: { path: { contextId } },
    body: {
      diffId: diff.data.id,
      versionId: diff.data.versionId,
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

  const legacy = await client.GET("/api/v1/diffs/current", {
    params: { query: { scope: apiValues.allScope } },
  });
  assert.equal(legacy.data?.id, diff.data.id);
  assert.equal(legacy.data?.versionId, diff.data.versionId);

  const patches: Record<string, components["schemas"]["FilePatch"]> = {};
  for (const observedFile of diff.data.files) {
    const result: { data?: components["schemas"]["FilePatch"] } =
      await client.GET(
        "/api/v2/contexts/{contextId}/diffs/{diffId}/files/{fileId}/patch",
        {
          params: {
            path: {
              contextId,
              diffId: diff.data.id,
              fileId: observedFile.id,
            },
            query: {
              scope: apiValues.allScope,
              versionId: diff.data.versionId,
              fileVersion: observedFile.fingerprint,
            },
          },
        },
      );
    assert.ok(result.data);
    patches[observedFile.id] = result.data;
  }
  const body = {
    protocolVersion: 1,
    submissionId: "contract-observation",
    metadata: {
      sourceId: "sandbox-contract-source",
      hostname: "sandbox-host",
      runId: "agent-run",
      agent: "contract-agent",
      trigger: "agent-hook",
      repositoryKey: "contract-repository",
      repositoryName: "contract-repo",
      remoteUrl: "",
      checkoutKey: "sandbox-checkout",
      root: "/workspace/repo",
      worktreeName: "sandbox-worktree",
      branch: "agent-branch",
      head: null,
      collectedAt: Date.now(),
      collectorVersion: "conformance",
    },
    scopes: [
      {
        snapshot: {
          ...diff.data,
          root: "/workspace/repo",
          name: "contract-repo",
          branch: "agent-branch",
          head: null,
        },
        patches,
      },
    ],
  } satisfies components["schemas"]["IngestionRequest"];
  const ingested = await client.POST("/api/v2/ingestions", { body });
  assert.equal(ingested.response.status, 200);
  assert.ok(ingested.data);
  assert.notEqual(ingested.data.contextId, contextId);
  const replay = await client.POST("/api/v2/ingestions", { body });
  assert.deepEqual(replay.data, ingested.data);

  const filtered = await client.GET("/api/v2/contexts", {
    params: {
      query: {
        repository: "contract-repo",
        branch: "agent-branch",
        worktree: "sandbox-worktree",
        hostname: "sandbox-host",
        sourceId: "sandbox-contract-source",
        runId: "agent-run",
      },
    },
  });
  assert.equal(filtered.data?.contexts.length, 1);
  assert.equal(filtered.data?.contexts[0]?.id, ingested.data.contextId);
  assert.deepEqual(filtered.data?.contexts[0]?.observation, body.metadata);

  const conflict = await client.POST("/api/v2/ingestions", {
    body: {
      ...body,
      metadata: { ...body.metadata, hostname: "different-host" },
    },
  });
  assert.equal(conflict.response.status, 409);
});
