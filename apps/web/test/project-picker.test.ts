import assert from "node:assert/strict";
import { test } from "node:test";
import type { ApiContext } from "@servediff/api";
import {
  contextChangeLabel,
  pickerResults,
  pickerEntries,
  defaultPickerFilters,
  pickerEntryAvailable,
  pickerFilterOptions,
} from "../src/project-picker.ts";

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
  changedFileCount: 0,
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
    changedFileCount: 1,
  };
  const results = pickerResults([capture, main], "");
  assert.equal(results[0]?.id, "capture");
  assert.equal(
    results.find((context) => context.id === "capture")?.name,
    "Captured patch",
  );
  assert.equal(pickerResults([main, capture], "captured")[0]?.id, "capture");
});

test("changed checkouts precede newer empty projects, including search results", () => {
  const changed = { ...worktree, changedFileCount: 2 };
  const newerEmpty = { ...main, lastChangedAt: 10 };
  const newerChanged = { ...clone, changedFileCount: 1, lastChangedAt: 4 };
  for (const query of ["", "servediff"]) {
    assert.deepEqual(
      pickerResults([newerEmpty, changed, newerChanged], query).map(
        (context) => context.id,
      ),
      ["clone", "picker", "main"],
    );
  }
});

test("unknown and unavailable metadata is not treated as reviewable changes", () => {
  const unknown = { ...main, changedFileCount: null };
  const unavailable: ApiContext = {
    ...clone,
    availability: "unavailable",
    changedFileCount: 8,
    lastChangedAt: 20,
  };
  const changed = { ...worktree, changedFileCount: 1 };
  assert.equal(
    pickerResults([unknown, unavailable, changed], "")[0]?.id,
    changed.id,
  );
  assert.equal(contextChangeLabel(unknown), "Status unknown");
  assert.equal(contextChangeLabel(unavailable), "Unavailable");
  assert.equal(contextChangeLabel(main), "No changes");
  assert.equal(contextChangeLabel(changed), "1 changed file");
  assert.equal(
    contextChangeLabel({ ...changed, changedFileCount: 2 }),
    "2 changed files",
  );
});

function observation(id: string, hostname: string, runId: string): ApiContext {
  return {
    ...main,
    id,
    kind: "observation",
    observation: {
      sourceId: `source-${hostname}`,
      hostname,
      runId,
      agent: "agent",
      trigger: "hook",
      repositoryKey: "repository",
      repositoryName: "servediff",
      remoteUrl: "https://example.test/servediff.git",
      checkoutKey: "checkout",
      root: main.root ?? "",
      worktreeName: "main-worktree",
      branch: "main",
      head: "head",
      collectedAt: 1000,
      collectorVersion: "test",
    },
  };
}

test("observation search distinguishes containers on the same branch and retains submissions", () => {
  const contexts = [
    observation("first", "container-a", "run-a"),
    observation("second", "container-b", "run-b"),
    observation("third", "container-a", "run-a"),
  ];
  assert.equal(pickerResults(contexts, "servediff main").length, 3);
  assert.deepEqual(
    pickerResults(contexts, "container-b run-b").map((context) => context.id),
    ["second"],
  );
  assert.equal(pickerResults(contexts, "source-container-a").length, 2);
});

test("repositories group observations without merging sources or repeated submissions", () => {
  const contexts = [
    observation("first", "container-a", "run-a"),
    observation("second", "container-b", "run-b"),
  ];
  const repositories = pickerEntries(contexts, "", "");
  assert.equal(repositories.length, 1);
  const repository = repositories[0];
  assert.equal(repository?.kind, "repository");
  if (repository?.kind !== "repository") throw new Error("Missing repository");
  assert.equal(repository.contexts.length, 2);
  assert.equal(pickerEntries(contexts, "", "repo").length, 2);
  assert.deepEqual(
    pickerEntries(contexts, "container-b", "repo").map((entry) => entry.id),
    ["second"],
  );
});

test("availability defaults to available and unavailable entries cannot be selected", () => {
  const unavailable: ApiContext = { ...clone, availability: "unavailable" };
  assert.deepEqual(pickerResults([main, unavailable], ""), [main]);
  const all = pickerEntries([main, unavailable], "", "", {
    ...defaultPickerFilters,
    availability: "all",
  });
  assert.equal(all.length, 2);
  assert.equal(all.filter(pickerEntryAvailable).length, 1);
  assert.deepEqual(
    pickerResults([main, unavailable], "", {
      ...defaultPickerFilters,
      availability: "unavailable",
    }),
    [unavailable],
  );
});

test("multi-value filters use OR within a filter and AND across filters before grouping", () => {
  const first = {
    ...observation("first", "host-a", "run"),
    worktreeName: "tree-a",
    changedFileCount: 1,
  };
  const second = {
    ...observation("second", "host-b", "run"),
    worktreeName: "tree-b",
    branch: "feature",
    changedFileCount: 2,
  };
  const third = {
    ...observation("third", "host-c", "run"),
    worktreeName: "tree-c",
    changedFileCount: 0,
  };
  const contexts = [first, second, third];
  const filters = {
    ...defaultPickerFilters,
    hosts: ["host-a", "host-b"],
    branches: ["main", "feature"],
    worktrees: ["tree-b"],
  };
  const entries = pickerEntries(contexts, "", "", filters);
  const grouped = entries[0];
  if (grouped?.kind !== "repository") throw new Error("Missing repository");
  assert.deepEqual(
    grouped.contexts.map((context) => context.id),
    ["second"],
  );
  assert.deepEqual(
    pickerEntries(contexts, "", "repo", filters).map((entry) => entry.id),
    ["second"],
  );
  assert.deepEqual(pickerResults(contexts, "host-a", filters), []);
  assert.deepEqual(pickerFilterOptions(contexts), {
    hosts: ["host-a", "host-b", "host-c"],
    branches: ["feature", "main"],
    worktrees: ["tree-a", "tree-b", "tree-c"],
  });
});

test("changes filters exclude unknown and unavailable counts", () => {
  const changed = { ...main, id: "changed", changedFileCount: 2 };
  const unknown = { ...main, id: "unknown", changedFileCount: null };
  const unavailable: ApiContext = {
    ...main,
    id: "unavailable",
    availability: "unavailable",
    changedFileCount: 2,
  };
  const contexts = [main, changed, unknown, unavailable];
  assert.deepEqual(
    pickerResults(contexts, "", {
      ...defaultPickerFilters,
      availability: "all",
      changes: "changed",
    }).map((context) => context.id),
    ["changed"],
  );
  assert.deepEqual(
    pickerResults(contexts, "", {
      ...defaultPickerFilters,
      availability: "all",
      changes: "unchanged",
    }).map((context) => context.id),
    ["main"],
  );
});

test("freshly reviewed observations sort by latest review without changing collection metadata", () => {
  const recentCollection = {
    ...observation("recent", "host", "run"),
    lastChangedAt: 20,
    lastSubmittedAt: 20,
  };
  const repeatedReview = {
    ...observation("repeated", "host", "run"),
    lastChangedAt: 1,
    lastSubmittedAt: 30,
  };
  assert.deepEqual(
    pickerResults([recentCollection, repeatedReview], "").map(
      (context) => context.id,
    ),
    ["repeated", "recent"],
  );
  assert.equal(repeatedReview.observation?.collectedAt, 1000);
});
