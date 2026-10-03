import assert from "node:assert/strict";
import { test } from "node:test";
import type { ApiContext } from "@servediff/api";
import { pickerResults } from "../src/project-picker.ts";

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
  lastChangedAt: 1,
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
  lastChangedAt: 2,
};
const clone: ApiContext = {
  ...main,
  id: "clone",
  repositoryId: "other-repo",
  root: "/other/servediff",
};

test("lists individual checkouts by recency without repository grouping", () => {
  const recentClone = { ...clone, lastSubmittedAt: 0, lastChangedAt: 3 };
  assert.deepEqual(
    pickerResults([worktree, recentClone, main], "").map(
      (context) => context.id,
    ),
    ["clone", "picker", "main"],
  );
});

test("repo name search includes all its checkouts and excludes other repos", () => {
  const other = { ...clone, name: "dotfiles", root: "/repos/dotfiles" };
  assert.deepEqual(
    pickerResults([main, other, worktree], "servediff").map(
      (context) => context.id,
    ),
    ["picker", "main"],
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
      pickerResults([main, worktree], query).map((context) => context.id),
      ["picker"],
    );
  }
  assert.deepEqual(pickerResults([main, worktree], "nonexistent"), []);
});

test("search lists newest changes before stronger matches", () => {
  const exact = { ...clone, name: "picker", branch: "other" };
  assert.deepEqual(
    pickerResults([main, exact, worktree], "picker").map(
      (context) => context.id,
    ),
    ["picker", "clone"],
  );
  const tied = { ...exact, lastChangedAt: worktree.lastChangedAt };
  assert.equal(pickerResults([worktree, tied], "picker")[0]?.id, tied.id);
});

test("captures stay selectable", () => {
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
  const results = pickerResults([capture, main], "");
  assert.equal(
    results.find((context) => context.id === "capture")?.name,
    "Captured patch",
  );
  assert.equal(pickerResults([main, capture], "captured")[0]?.id, "capture");
});
