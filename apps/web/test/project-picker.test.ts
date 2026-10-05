import assert from "node:assert/strict";
import { test } from "node:test";
import type { ApiContext } from "@servediff/api";
import {
  contextChangeLabel,
  contextIsLinkedWorktree,
  contextCheckoutLabel,
  contextDiagnostics,
  observationSummary,
  observationDetail,
  pickerResults,
  pickerEntries,
  defaultPickerFilters,
  pickerEntryAvailable,
  pickerFilterOptions,
  contextIsPiped,
  contextTimestampLabel,
  pipedGroupId,
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
const allFilters = { ...defaultPickerFilters, includeAll: true };

test("freshness and inclusion filters combine before search and repository grouping", () => {
  const latest = {
    ...observation("latest", "host", "run-new"),
    changedFileCount: 2,
  };
  const stale = {
    ...observation("stale", "host", "run-old"),
    changedFileCount: 1,
    stale: true,
  };
  const empty = {
    ...observation("empty", "other", "run"),
    changedFileCount: 0,
  };
  const unknown = { ...empty, id: "unknown", changedFileCount: null };
  const unavailable: ApiContext = {
    ...latest,
    id: "unavailable",
    availability: "unavailable",
  };
  const contexts = [latest, stale, empty, unknown, unavailable];
  assert.deepEqual(pickerResults(contexts, ""), [latest]);
  assert.deepEqual(
    pickerResults(contexts, "", {
      ...defaultPickerFilters,
      freshness: "stale",
    }),
    [stale],
  );
  assert.deepEqual(
    pickerResults(contexts, "", { ...defaultPickerFilters, freshness: "all" })
      .map((context) => context.id)
      .sort(),
    ["latest", "stale"],
  );
  assert.deepEqual(
    pickerResults(contexts, "", allFilters)
      .map((context) => context.id)
      .sort(),
    ["empty", "latest", "unavailable", "unknown"],
  );
  assert.deepEqual(
    pickerResults(contexts, "", { ...allFilters, freshness: "stale" }),
    [stale],
  );
  assert.equal(
    pickerResults(contexts, "", { ...allFilters, freshness: "all" }).length,
    contexts.length,
  );
  assert.equal(pickerEntries(contexts, "run-old", "repo").length, 0);
  const groups = pickerEntries(contexts, "", "");
  const group = groups[0];
  if (group?.kind !== "repository") throw new Error("Missing repository group");
  assert.deepEqual(group.contexts, [latest]);
});

test("lists individual checkouts by recency without repository grouping", () => {
  const recentClone = { ...clone, lastSubmittedAt: 0, lastChangedAt: 3 };
  assert.deepEqual(
    pickerResults([worktree, recentClone, main], "", allFilters).map(
      (context) => context.id,
    ),
    ["clone", "picker", "main"],
  );
});

test("repo name search includes all its checkouts and excludes other repos", () => {
  const other = { ...clone, name: "dotfiles", root: "/repos/dotfiles" };
  assert.deepEqual(
    pickerResults([main, other, worktree], "servediff", allFilters).map(
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
      pickerResults([main, worktree], query, allFilters).map(
        (context) => context.id,
      ),
      ["picker"],
    );
  }
  assert.deepEqual(pickerResults([main, worktree], "nonexistent"), []);
});

test("search lists newest changes before stronger matches", () => {
  const exact = { ...clone, name: "picker", branch: "other" };
  assert.deepEqual(
    pickerResults([main, exact, worktree], "picker", allFilters).map(
      (context) => context.id,
    ),
    ["picker", "clone"],
  );
  const tied = { ...exact, lastChangedAt: worktree.lastChangedAt };
  assert.equal(
    pickerResults([worktree, tied], "picker", allFilters)[0]?.id,
    tied.id,
  );
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
  assert.equal(pickerResults([main, capture], "piped")[0]?.id, "capture");
  assert.deepEqual(pickerResults([main, capture], "captured"), []);
});

test("changed checkouts precede newer empty projects, including search results", () => {
  const changed = { ...worktree, changedFileCount: 2 };
  const newerEmpty = { ...main, lastChangedAt: 10 };
  const newerChanged = { ...clone, changedFileCount: 1, lastChangedAt: 4 };
  for (const query of ["", "servediff"]) {
    assert.deepEqual(
      pickerResults([newerEmpty, changed, newerChanged], query, allFilters).map(
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
    pickerResults([unknown, unavailable, changed], "", allFilters)[0]?.id,
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
    changedFileCount: 1,
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

test("snapshot header shows only hostname and time, with labeled IDs in diagnostics", () => {
  const context = observation("context-id", "host", "session-id");
  const summary = observationSummary(context);
  assert.ok(summary.startsWith("host · "));
  assert.ok(!summary.includes("session-id"));
  assert.ok(!summary.includes("source-host"));
  assert.ok(!summary.includes("Collected"));
  assert.ok(observationDetail(context).includes("Run: session-id"));
  const diagnostics = contextDiagnostics(context);
  assert.ok(diagnostics.includes("Source: source-host"));
  assert.ok(diagnostics.includes("Run: session-id"));
  assert.ok(diagnostics.includes("Context: context-id"));
  const anonymous = observation("context-id", "", "");
  assert.ok(!observationSummary(anonymous).includes("source-"));
  assert.ok(observationDetail(anonymous).includes("Source: source-"));
});

test("worktree indicator requires explicit checkout metadata for observations", () => {
  const context = observation("context-id", "host", "");
  assert.equal(contextIsLinkedWorktree(context), false);
  assert.equal(contextCheckoutLabel(context), "Checkout type unknown");
  assert.equal(contextIsLinkedWorktree(main), false);
  assert.equal(contextIsLinkedWorktree(worktree), true);
  if (!context.observation) throw new Error("Missing observation");
  context.observation.linkedWorktree = false;
  assert.equal(contextIsLinkedWorktree(context), false);
  assert.equal(contextCheckoutLabel(context), "Primary checkout");
  context.observation.linkedWorktree = true;
  assert.equal(contextIsLinkedWorktree(context), true);
  assert.equal(contextCheckoutLabel(context), "Linked worktree: main-worktree");
  context.branch = "detached at abc123";
  assert.equal(contextIsLinkedWorktree(context), true);
});

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

test("All includes unavailable entries without making them selectable", () => {
  const unavailable: ApiContext = { ...clone, availability: "unavailable" };
  assert.deepEqual(pickerResults([main, unavailable], ""), []);
  const all = pickerEntries([main, unavailable], "", "", allFilters);
  assert.equal(all.length, 2);
  assert.equal(all.filter(pickerEntryAvailable).length, 1);
  assert.deepEqual(
    pickerResults([main, unavailable], "servediff", allFilters)
      .map((context) => context.id)
      .sort(),
    ["clone", "main"],
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

test("default inclusion excludes no changes, unknown and unavailable counts", () => {
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
    pickerResults(contexts, "").map((context) => context.id),
    ["changed"],
  );
  assert.deepEqual(
    pickerResults(contexts, "", allFilters)
      .map((context) => context.id)
      .sort(),
    ["changed", "main", "unavailable", "unknown"],
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

test("Piped groups legacy captures and stdin observations independently of repositories and filters", () => {
  const piped: ApiContext = {
    ...observation("piped-old", "host", "run"),
    source: "stdin",
    stale: true,
  };
  if (!piped.observation) throw new Error("Missing observation");
  piped.observation.linkedWorktree = true;
  const capture: ApiContext = {
    ...main,
    id: "piped-new",
    kind: "capture",
    createdAt: 2000,
    changedFileCount: 0,
  };
  const repo = observation("repo-context", "host", "run");
  const contexts = [piped, repo, capture];
  const filters = {
    ...defaultPickerFilters,
    freshness: "stale",
    branches: ["unrelated"],
    worktrees: ["elsewhere"],
    hosts: ["absent"],
  } satisfies typeof defaultPickerFilters;
  const entries = pickerEntries(contexts, "", "", filters);
  assert.equal(entries.length, 1);
  const group = entries[0];
  if (group?.kind !== "piped") throw new Error("Missing Piped group");
  assert.deepEqual(
    group.contexts.map((context) => context.id),
    ["piped-new", "piped-old"],
  );
  assert.deepEqual(
    pickerEntries(contexts, "", pipedGroupId, filters).map((entry) => entry.id),
    ["piped-new", "piped-old"],
  );
  assert.deepEqual(
    pickerEntries(contexts, "", "repo").map((entry) => entry.id),
    ["repo-context"],
  );
  assert.equal(contextIsPiped(piped), true);
  assert.equal(contextIsLinkedWorktree(piped), false);
  assert.deepEqual(pickerResults([piped], "servediff"), []);
  assert.deepEqual(pickerResults([piped], "main"), []);
  assert.deepEqual(pickerFilterOptions([piped, capture]), {
    hosts: [],
    branches: [],
    worktrees: [],
  });
  assert.ok(!contextDiagnostics(piped).includes("servediff"));
  assert.ok(!contextDiagnostics(piped).includes("main"));
  assert.ok(contextDiagnostics(piped).includes("Context: piped-old"));
  assert.deepEqual(
    pickerResults(contexts, "").map((context) => context.id),
    ["repo-context", "piped-new", "piped-old"],
  );
});

test("Piped timestamp labels use capture time and distinguish today, yesterday, and older years", () => {
  const now = new Date(2026, 9, 5, 20, 30);
  const context: ApiContext = {
    ...main,
    kind: "capture",
    createdAt: new Date(2026, 9, 5, 20, 17).getTime(),
  };
  assert.ok(contextTimestampLabel(context, now).startsWith("Today, "));
  assert.ok(
    contextTimestampLabel(
      { ...context, createdAt: new Date(2026, 9, 4, 20, 17).getTime() },
      now,
    ).startsWith("Yesterday, "),
  );
  assert.ok(
    contextTimestampLabel(
      { ...context, createdAt: new Date(2025, 9, 5, 20, 17).getTime() },
      now,
    ).includes("2025"),
  );
  const observed = {
    ...observation("piped", "host", "run"),
    source: "stdin",
    createdAt: 1,
  } satisfies ApiContext;
  assert.equal(
    contextTimestampLabel(observed, now),
    contextTimestampLabel({ ...context, createdAt: 1000 }, now),
  );
});
