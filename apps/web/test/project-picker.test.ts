import assert from "node:assert/strict";
import { test } from "node:test";
import type { ApiContext } from "@servediff/api";
import { pickerGroups } from "../src/project-picker.ts";

const main: ApiContext = {
  id: "main",
  kind: "worktree",
  name: "servediff",
  branch: "main",
  worktreeName: null,
  root: "/repos/servediff",
  repositoryId: "repo",
  locationId: "main",
  createdAt: 1,
  lastSubmittedAt: 1,
  expiresAt: null,
  submittedFrom: null,
  availability: "available",
  capabilities: {
    diff: {
      scopes: { state: "enabled", values: ["all"] },
      refresh: { state: "enabled" },
      stagingMetadata: { state: "enabled" },
    },
    files: { contents: { state: "enabled" } },
    review: { comments: { state: "enabled" } },
  },
};
const worktree: ApiContext = {
  ...main,
  id: "picker",
  branch: "feature/project-picker",
  worktreeName: "picker-folder",
  root: "/external/team/picker-folder",
  lastSubmittedAt: 2,
};
const clone: ApiContext = {
  ...main,
  id: "clone",
  repositoryId: "other-repo",
  root: "/other/servediff",
};

test("groups by repository identity, keeps sibling checkouts adjacent, and separates clones", () => {
  const groups = pickerGroups([worktree, clone, main], "");
  assert.deepEqual(
    groups.map((group) => group.id),
    ["repo", "other-repo"],
  );
  assert.deepEqual(
    groups[0]?.contexts.map((context) => context.id),
    ["main", "picker"],
  );
});

test("search matches repo, branch, worktree, and external paths with all query words", () => {
  for (const query of [
    "servediff picker",
    "PICKER-folder",
    "external team",
    "ftprpck",
  ]) {
    assert.deepEqual(
      pickerGroups([main, worktree], query).flatMap((group) =>
        group.contexts.map((context) => context.id),
      ),
      ["picker"],
    );
  }
  assert.deepEqual(pickerGroups([main, worktree], "nonexistent"), []);
});

test("exact matches precede fuzzy matches and captures stay selectable", () => {
  const exact = { ...clone, name: "picker", branch: "other" };
  assert.equal(
    pickerGroups([main, worktree, exact], "picker")[0]?.contexts[0]?.id,
    "clone",
  );
  const capture: ApiContext = {
    ...main,
    id: "capture",
    kind: "capture",
    name: "Captured patch",
    root: null,
    repositoryId: null,
    locationId: null,
    branch: null,
  };
  const groups = pickerGroups([capture, main], "");
  assert.equal(groups.at(-1)?.name, "Piped diffs");
  assert.equal(
    pickerGroups([main, capture], "captured")[0]?.contexts[0]?.id,
    "capture",
  );
});
