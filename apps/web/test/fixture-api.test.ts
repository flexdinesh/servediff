import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { ReviewStore } from "../../server/src/review-store.ts";
import { openPatch } from "../../server/src/stdin.ts";
import { seedFixtureReview } from "./fixture-api.ts";

test("seeds comments and reviewed-file state for web development", async () => {
  const input = await readFile(
    fileURLToPath(
      new URL("../../../test/fixtures/sample.diff", import.meta.url),
    ),
    "utf8",
  );
  const repository = await openPatch(input).snapshot("all");
  const store = new ReviewStore(null);
  const sessionId = "fixture-test";

  await seedFixtureReview(store, sessionId, repository);

  const comments = await store.comments(sessionId);
  assert.deepEqual(
    comments.map(({ path, status }) => ({ path, status })),
    [
      { path: "src/value.ts", status: "open" },
      { path: "src/components/Badge.tsx", status: "resolved" },
      { path: "src/components/Button.tsx", status: "open" },
    ],
  );
  assert.equal(comments[0]?.origin?.revision, repository.revision);
  assert.equal(comments[1]?.origin?.revision, repository.revision);
  assert.equal(comments[2]?.origin?.revision, "earlier-fixture-review");
  assert.equal(comments[2]?.fingerprint, "earlier-button-version");

  const legacy = repository.files.find(
    (file) => file.path === "docs/legacy.md",
  );
  assert.ok(legacy);
  assert.deepEqual(await store.marks(sessionId, "all"), [
    {
      fileId: legacy.id,
      fileVersion: legacy.fingerprint,
      scope: "all",
    },
  ]);
});
