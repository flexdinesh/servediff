import assert from "node:assert/strict";
import { test } from "node:test";
import type { ChangedFile } from "@servediff/shared";
import { toggleReviewedFileState } from "../src/review-state.ts";

const file: ChangedFile = {
  id: "src-file",
  path: "src/file.ts",
  oldPath: null,
  status: "M",
  indexStatus: " ",
  worktreeStatus: "M",
  additions: 1,
  deletions: 1,
  binary: false,
  fingerprint: "revision",
};

test("marking a file viewed collapses it and unviewing expands it", () => {
  const viewed = toggleReviewedFileState(new Map(), new Set(), file);
  assert.equal(viewed.reviews.get(file.path), file.fingerprint);
  assert.deepEqual(viewed.collapsed, new Set([file.path]));

  const unviewed = toggleReviewedFileState(
    viewed.reviews,
    viewed.collapsed,
    file,
  );
  assert.equal(unviewed.reviews.has(file.path), false);
  assert.deepEqual(unviewed.collapsed, new Set());
});
