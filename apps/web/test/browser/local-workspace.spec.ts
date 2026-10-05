import { expect, test, type Page, type Route } from "@playwright/test";
import type { ApiContext, ApiRepositoryDiff } from "@servediff/api";
import {
  isDiffMode,
  type DiffMode,
  type ReviewComment,
} from "@servediff/shared";

async function localWorkspace(page: Page) {
  await page.route("**/api/v2/metrics", (route) =>
    route.fulfill({ json: { cpuUsage: 0, rssBytes: 0 } }),
  );
  const before = Array.from({ length: 100 }, (_, index) => `line ${index + 1}`);
  before[49] = 'export const value = "before";';
  const state = {
    version: 1,
    failPatch: false,
    holdScopes: new Set<DiffMode>(),
    scopes: new Array<DiffMode>(),
    pendingScopes: new Array<{ route: Route; repository: ApiRepositoryDiff }>(),
    holdMarks: false,
    pendingMarks: new Array<Route>(),
    contentsRequests: 0,
    comments: new Array<ReviewComment>(),
  };
  const session: ApiContext = {
    id: "local-review",
    locationId: "location",
    repositoryId: "repo",
    kind: "worktree",
    createdAt: 1,
    lastSubmittedAt: 1,
    lastChangedAt: 1,
    changedFileCount: 1,
    expiresAt: null,
    submittedFrom: null,
    availability: "available",
    stale: false,
    root: "/local-review",
    name: "Local review",
    branch: "main",
    worktreeName: null,
    capabilities: {
      diff: {
        scopes: { state: "enabled", values: ["all", "staged", "unstaged"] },
        refresh: { state: "enabled" },
        stagingMetadata: { state: "enabled" },
      },
      files: { contents: { state: "enabled" } },
      review: { comments: { state: "enabled" } },
    },
  };
  function repository(mode: DiffMode): ApiRepositoryDiff {
    return {
      id: `diff-${mode}`,
      versionId: `${mode}-v${state.version}`,
      locationId: "location",
      repositoryId: "repo",
      source: "local",
      root: session.root ?? "",
      name: session.name,
      branch: "main",
      head: "head",
      mode,
      revision: `${mode}-v${state.version}`,
      files: [
        {
          id: "value-file",
          path: "src/value.ts",
          oldPath: null,
          status: "M",
          indexStatus: "M",
          worktreeStatus: "M",
          additions: 1,
          deletions: 1,
          binary: false,
          fingerprint: `${mode}-v${state.version}`,
        },
      ],
    };
  }
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts: [session], nextCursor: null } }),
  );
  await page.route("**/api/v2/contexts/*", (route) =>
    route.fulfill({ json: session }),
  );
  await page.route("**/api/v2/contexts/*/diffs/current?*", (route) => {
    const scope = new URL(route.request().url()).searchParams.get("scope");
    if (!isDiffMode(scope)) throw new Error("Invalid scope");
    state.scopes.push(scope);
    const diff = repository(scope);
    if (state.holdScopes.has(scope)) {
      state.pendingScopes.push({ route, repository: diff });
      return;
    }
    return route.fulfill({ json: diff });
  });
  await page.route("**/api/v2/contexts/*/diffs/*/files/*/patch?*", (route) => {
    if (state.failPatch)
      return route.fulfill({ status: 500, json: { detail: "Patch rejected" } });
    const version = new URL(route.request().url()).searchParams.get(
      "fileVersion",
    );
    return route.fulfill({
      json: {
        message: null,
        patch: [
          "diff --git a/src/value.ts b/src/value.ts",
          "--- a/src/value.ts",
          "+++ b/src/value.ts",
          "@@ -47,7 +47,7 @@",
          " line 47",
          " line 48",
          " line 49",
          '-export const value = "before";',
          `+export const value = "${version}";`,
          " line 51",
          " line 52",
          " line 53",
          "",
        ].join("\n"),
      },
    });
  });
  await page.route(
    "**/api/v2/contexts/*/diffs/*/files/*/contents?*",
    (route) => {
      state.contentsRequests++;
      const version = new URL(route.request().url()).searchParams.get(
        "fileVersion",
      );
      const after = [...before];
      after[49] = `export const value = "${version}";`;
      return route.fulfill({
        json: {
          before: before.join("\n") + "\n",
          after: after.join("\n") + "\n",
        },
      });
    },
  );
  await page.route("**/api/v2/contexts/*/comments", (route) =>
    route.fulfill({ json: { comments: state.comments } }),
  );
  await page.route("**/api/v2/contexts/*/review-marks?*", (route) =>
    route.fulfill({ json: { marks: [] } }),
  );
  await page.route("**/api/v2/contexts/*/review-marks/*?*", (route) => {
    if (state.holdMarks) {
      state.pendingMarks.push(route);
      return;
    }
    return route.fulfill({ json: {} });
  });
  return Object.assign(state, { context: session, repository });
}

async function ready(page: Page, value = "all-v1") {
  await expect(
    page.getByText(`export const value = "${value}";`, { exact: true }),
  ).toBeVisible();
}

async function deletableWorkspace(
  page: Page,
  snapshot = false,
  single = false,
) {
  const state = await localWorkspace(page);
  if (snapshot) state.context.kind = "capture";
  const contexts = [
    state.context,
    { ...state.context, id: "other", name: "Other review" },
  ];
  if (single) contexts.splice(1);
  const deletion = {
    requests: new Array<string>(),
    fail: false,
    hold: false,
    pending: new Array<Route>(),
  };
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts, nextCursor: null } }),
  );
  await page.route("**/api/v2/contexts/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    if (route.request().method() === "DELETE") {
      if (!id) throw new Error("Missing deletion ID");
      deletion.requests.push(id);
      if (deletion.fail)
        return route.fulfill({
          status: 500,
          json: { detail: "Storage unavailable. Try again." },
        });
      const index = contexts.findIndex((context) => context.id === id);
      if (index >= 0) contexts.splice(index, 1);
      if (deletion.hold) {
        deletion.pending.push(route);
        return;
      }
      return route.fulfill({ status: 204 });
    }
    const context = contexts.find((context) => context.id === id);
    return context
      ? route.fulfill({ json: context })
      : route.fulfill({ status: 404, json: { detail: "Context not found" } });
  });
  await page.goto("/");
  await ready(page);
  return { ...deletion, contexts, state, deletion };
}

test("context deletion confirms its scope and cancel restores focus without deleting", async ({
  page,
}) => {
  const state = await deletableWorkspace(page);
  const trigger = page.getByRole("button", {
    name: "Delete context",
    exact: true,
  });
  await trigger.click();
  const dialog = page.getByRole("dialog", {
    name: "Delete context?",
    exact: true,
  });
  await expect(dialog).toContainText("all its stored diffs");
  await expect(dialog).toContainText("comments and reviewed-file marks");
  await expect(dialog).toContainText("Local review");
  await expect(
    dialog.getByRole("button", { name: "Cancel", exact: true }),
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
  expect(state.requests).toHaveLength(0);
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
});

test("snapshot deletion navigates to another review and removes the snapshot from the picker", async ({
  page,
}) => {
  const state = await deletableWorkspace(page, true);
  await page
    .getByRole("button", { name: "Delete snapshot", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Delete snapshot?",
    exact: true,
  });
  await dialog
    .getByRole("button", { name: "Delete snapshot", exact: true })
    .click();
  await expect(page).toHaveURL(/\/contexts\/other$/);
  await ready(page);
  await expect(dialog).toBeHidden();
  expect(state.requests).toEqual(["local-review"]);
  const picker = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await expect(picker).toBeFocused();
  await picker.click();
  await expect(page.getByRole("option")).toHaveCount(1);
  await expect(page.getByRole("option")).toContainText("Piped");
  await page.getByRole("option").click();
  await expect(page.getByRole("option")).toHaveCount(1);
  await expect(page.getByRole("option")).toHaveAttribute(
    "title",
    /Context: other/,
  );
});

test("deletion failure stays retryable and pending deletion blocks dismissal and duplicate writes", async ({
  page,
}) => {
  const { deletion } = await deletableWorkspace(page);
  deletion.fail = true;
  await page
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Delete context?",
    exact: true,
  });
  await dialog
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  await expect(dialog.getByRole("alert")).toHaveText(
    "Storage unavailable. Try again.",
  );
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
  deletion.fail = false;
  deletion.hold = true;
  await dialog
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  await expect(
    dialog.getByRole("button", { name: "Deleting…", exact: true }),
  ).toBeDisabled();
  await expect(
    dialog.getByRole("button", { name: "Cancel", exact: true }),
  ).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeVisible();
  expect(deletion.requests).toEqual(["local-review", "local-review"]);
  const pending = deletion.pending.shift();
  if (!pending) throw new Error("Missing deletion request");
  await pending.fulfill({ status: 204 });
  await expect(page).toHaveURL(/\/contexts\/other$/);
});

test("deleting the last context shows a focused empty state on mobile", async ({
  page,
}) => {
  await deletableWorkspace(page, false, true);
  await page.setViewportSize({ width: 360, height: 640 });
  await page
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  const dialog = page.getByRole("dialog", {
    name: "Delete context?",
    exact: true,
  });
  const bounds = await dialog.boundingBox();
  if (!bounds) throw new Error("Missing deletion dialog bounds");
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(360);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(640);
  await dialog
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  await expect(page).toHaveURL(/\/$/);
  await expect(
    page.getByRole("heading", { name: "No review contexts yet" }),
  ).toBeVisible();
  await expect(page.locator("main.session-status")).toBeFocused();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "No review contexts yet" }),
  ).toBeVisible();
});

test("rapid scopes ignore old diff responses and reviewed-file failures", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  await page.goto("/");
  await ready(page);
  state.holdScopes.add("staged");
  await page.getByRole("button", { name: "Staged", exact: true }).click();
  await expect.poll(() => state.pendingScopes.length).toBe(1);
  await page.getByRole("button", { name: "Unstaged", exact: true }).click();
  await ready(page, "unstaged-v1");
  const stale = state.pendingScopes.shift();
  if (!stale) throw new Error("Missing staged request");
  await stale.route.fulfill({ json: stale.repository });
  await expect(
    page.getByRole("button", { name: "Unstaged", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await ready(page, "unstaged-v1");
  state.holdMarks = true;
  await page
    .getByRole("button", { name: "Mark src/value.ts reviewed" })
    .click();
  await expect.poll(() => state.pendingMarks.length).toBe(1);
  await page.getByRole("button", { name: "All changes", exact: true }).click();
  await ready(page);
  const mark = state.pendingMarks.shift();
  if (!mark) throw new Error("Missing mark request");
  await mark.fulfill({ status: 500, json: { detail: "Old scope failure" } });
  await expect(page.locator("#review-count")).toHaveText("0 of 1 reviewed");
  await expect(page.locator(".reviewed-error")).toHaveCount(0);
  await expect(page.locator(".diff-collapse")).toHaveAttribute(
    "aria-expanded",
    "true",
  );
});

test("draft survives scopes and explicit reload without automatic diff polling", async ({
  page,
}) => {
  await page.clock.install();
  const state = await localWorkspace(page);
  await page.goto("/");
  await ready(page);
  const inline = page.locator("#viewer .comment-editor textarea");
  // Worker rendering can replace the hover utility during initial highlighting.
  await expect(async () => {
    if (await inline.isVisible()) return;
    await page
      .locator('[data-gutter] [data-column-number="50"]')
      .last()
      .hover({ timeout: 1_000 });
    await page.locator("[data-utility-button]").click({ timeout: 1_000 });
    await expect(inline).toBeVisible({ timeout: 1_000 });
  }).toPass({ timeout: 10_000 });
  await inline.fill("Preserve this draft");
  const requestsBefore = state.scopes.length;
  await page.clock.fastForward(3_000);
  expect(state.scopes).toHaveLength(requestsBefore);
  await page.getByRole("button", { name: "Staged", exact: true }).click();
  await ready(page, "staged-v1");
  await page.getByRole("tab", { name: /Review/ }).click();
  await expect(
    page.locator("#comments-panel .comment-editor textarea"),
  ).toHaveValue("Preserve this draft");
  await page.getByRole("button", { name: "All changes", exact: true }).click();
  await ready(page);
  await expect(inline).toHaveValue("Preserve this draft");
  state.version = 2;
  await page.getByRole("button", { name: "Refresh changes" }).click();
  await ready(page, "all-v2");
  const sidebar = page.locator("#comments-panel .comment-editor textarea");
  await expect(sidebar).toHaveValue("Preserve this draft");
  await sidebar.press("Escape");
  const requestsAfter = state.scopes.length;
  await page.clock.fastForward(3_000);
  expect(state.scopes).toHaveLength(requestsAfter);
  await page.keyboard.press("Alt+/");
  await expect(
    page.getByRole("searchbox", { name: "Filter files" }),
  ).toBeFocused();
});

test("full-file context and selection survive layout changes", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  await page.goto("/");
  await ready(page);
  await page.locator("[data-expand-button]").first().click();
  await expect.poll(() => state.contentsRequests).toBe(1);
  await expect(page.locator("#viewer")).toContainText("line 30");
  await page.getByRole("button", { name: "Unified", exact: true }).click();
  await expect(page.locator("#viewer")).toContainText("line 30");
  await ready(page);
  await page.getByRole("button", { name: "Split", exact: true }).click();
  const editor = page.locator("#viewer .comment-editor-title");
  // Worker rendering can replace the hover utility after switching layouts.
  await expect(async () => {
    if (await editor.isVisible()) return;
    await page
      .locator('[data-gutter] [data-column-number="50"]')
      .last()
      .hover({ timeout: 1_000 });
    await page.locator("[data-utility-button]").click({ timeout: 1_000 });
    await expect(editor).toBeVisible({ timeout: 1_000 });
  }).toPass({ timeout: 10_000 });
  await expect(page.locator("#viewer .comment-editor-title")).toContainText(
    "src/value.ts:50",
  );
  expect(state.contentsRequests).toBe(1);
});

test("failed preview retries without a revision change", async ({ page }) => {
  const state = await localWorkspace(page);
  state.failPatch = true;
  await page.goto("/");
  await expect(page.locator("#notice")).toContainText("Refresh to retry");
  await expect(page.locator("#viewer")).toContainText("Patch rejected");
  state.failPatch = false;
  await page.getByRole("button", { name: "Refresh changes" }).click();
  await ready(page);
  await expect(page.locator("#notice")).toBeHidden();
});

test("comment navigation changes scope and clears filters after previews load", async ({
  page,
}) => {
  const warnings: string[] = [];
  page.on("console", (message) => {
    if (message.text().includes("CodeView.scrollTo:"))
      warnings.push(message.text());
  });
  const state = await localWorkspace(page);
  state.comments = [
    {
      id: "staged-comment",
      diffId: "diff-staged",
      versionId: "staged-v1",
      path: "src/value.ts",
      scope: "staged",
      fingerprint: "staged-v1",
      side: "additions",
      start: 50,
      end: 50,
      code: '+export const value = "staged-v1";',
      body: "Navigate to staged change",
      status: "open",
      createdAt: 1,
    },
  ];
  await page.goto("/");
  await ready(page);
  await page.getByRole("searchbox", { name: "Filter files" }).fill("unmatched");
  await expect(page.locator("#viewer diffs-container")).toHaveCount(0);
  await page.getByRole("tab", { name: /Review/ }).click();
  await page.locator("#comments-panel .earlier-review > summary").click();
  await page
    .locator(
      '#comments-panel [data-comment-id="staged-comment"] .comment-location',
    )
    .click();
  await expect(
    page.locator(
      "#viewer [data-additions] [data-line][data-comment-navigation-target]",
    ),
  ).toHaveCount(1);
  await ready(page, "staged-v1");
  await expect(
    page.getByRole("button", { name: "Staged", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
  await expect(
    page.locator('#viewer [data-comment-id="staged-comment"]'),
  ).toBeVisible();
  expect(warnings).toEqual([]);
  await page.getByRole("tab", { name: /Changes/ }).click();
  await expect(
    page.getByRole("searchbox", { name: "Filter files" }),
  ).toHaveValue("");
});

test("context switching isolates delayed repository responses and fixed captures", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  const contexts: ApiContext[] = [
    state.context,
    {
      ...state.context,
      id: "second-repo",
      name: "Second repo",
      root: "/second-repo",
    },
    {
      ...state.context,
      id: "piped-patch",
      kind: "capture",
      name: "Captured patch",
      root: null,
      locationId: null,
      repositoryId: null,
      capabilities: {
        ...state.context.capabilities,
        diff: {
          scopes: { state: "enabled", values: ["all"] },
          refresh: { state: "unavailable" },
          stagingMetadata: { state: "unavailable" },
        },
        files: { contents: { state: "unavailable" } },
      },
    },
  ];
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts, nextCursor: null } }),
  );
  await page.route("**/api/v2/contexts/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    const context = contexts.find((entry) => entry.id === id);
    return route.fulfill({ json: context });
  });
  let delayed: Route | undefined;
  let delay = false;
  await page.route("**/api/v2/contexts/*/diffs/current?*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/")[4];
    const context = contexts.find((entry) => entry.id === id);
    if (!context) throw new Error("Unknown context");
    if (delay && id === state.context.id) {
      delayed = route;
      return;
    }
    return route.fulfill({
      json: {
        ...state.repository("all"),
        id: `diff-${id}`,
        name: context.name,
        root: context.root ?? "",
        source: context.kind === "capture" ? "stdin" : "local",
      },
    });
  });
  await page.goto("/");
  await ready(page);
  delay = true;
  await page.getByRole("button", { name: "Refresh changes" }).click();
  await expect.poll(() => delayed !== undefined).toBe(true);
  await page
    .getByRole("button", { name: "Switch review", exact: true })
    .click();
  await page
    .getByRole("combobox", { name: "Search reviews" })
    .fill("Second repo");
  await page.keyboard.press("Enter");
  await expect(page.locator(".context-switcher-name")).toHaveText(
    "Second repo",
  );
  await ready(page);
  if (!delayed) throw new Error("Missing delayed request");
  await delayed.fulfill({
    json: { ...state.repository("all"), name: "Stale repo" },
  });
  await expect(page.locator(".context-switcher-name")).toHaveText(
    "Second repo",
  );
  await page
    .getByRole("button", { name: "Switch review", exact: true })
    .click();
  await page.getByRole("option", { name: /^Piped/ }).click();
  await page.getByRole("option").click();
  await expect(page.locator("#changes-title")).toHaveText("Piped");
  await expect(
    page.getByRole("button", { name: "Refresh changes" }),
  ).toBeHidden();
  await expect(page.locator("#connection")).toHaveText("Fixed snapshot");
  await expect(page).toHaveURL(/\/contexts\/piped-patch$/);
  await page.reload();
  await expect(page.locator("#changes-title")).toHaveText("Piped");
});

test("empty service explains how to review a repository or pipe", async ({
  page,
}) => {
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts: [], nextCursor: null } }),
  );
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "No review contexts yet" }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "Run servediff review in a repository, or pipe a diff into servediff pipe.",
    ),
  ).toBeVisible();
});

test("Piped history ignores repository filters and switches timestamp rows by keyboard", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  const piped: ApiContext = {
    ...state.context,
    id: "piped-observation",
    kind: "observation",
    source: "stdin",
    name: "Invoking project",
    branch: "invoking-branch",
    worktreeName: "invoking-worktree",
    stale: true,
    createdAt: new Date(2026, 9, 5, 20, 17).getTime(),
  };
  const newer: ApiContext = {
    ...piped,
    id: "piped-empty",
    createdAt: new Date(2026, 9, 5, 20, 18).getTime(),
    changedFileCount: 0,
  };
  const contexts = [state.context, piped, newer];
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts, nextCursor: null } }),
  );
  await page.route("**/api/v2/contexts/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    return route.fulfill({
      json: contexts.find((context) => context.id === id),
    });
  });
  await page.route("**/api/v2/contexts/*/diffs/current?*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/")[4];
    return route.fulfill({
      json: {
        ...state.repository("all"),
        source: id === state.context.id ? "local" : "stdin",
      },
    });
  });
  await page.goto("/");
  await ready(page);
  const trigger = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  await dialog.getByRole("button", { name: "Stale", exact: true }).click();
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("Piped");
  await dialog.getByRole("combobox").fill("piped");
  await page.keyboard.press("Enter");
  await expect(
    dialog.getByRole("combobox", { name: "Search Piped" }),
  ).toHaveValue("");
  await expect(
    dialog.getByRole("button", { name: "Filters", exact: true }),
  ).toHaveCount(0);
  const rows = dialog.getByRole("option");
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText("No changes");
  await expect(dialog).not.toContainText("Invoking project");
  await expect(dialog).not.toContainText("invoking-branch");
  await expect(rows).not.toContainText(["Stale", "Stale"]);
  await expect(
    dialog.getByRole("button", { name: "Stale", exact: true }),
  ).toBeHidden();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/contexts\/piped-observation$/);
  await expect(trigger).toContainText("Piped");
  await expect(trigger).not.toContainText("Invoking project");
  await expect(trigger.locator(".context-switcher-worktree")).toHaveCount(0);
  await expect(page).toHaveTitle("servediff · Piped");
  await trigger.click();
  await page.keyboard.press("Enter");
  await expect(
    dialog.getByRole("option", { name: /Current review/ }),
  ).toHaveCount(1);
  await page.keyboard.press("ArrowLeft");
  await expect(
    dialog.getByRole("combobox", { name: "Search reviews" }),
  ).toBeVisible();
  await expect(
    dialog.getByText("Stale snapshots · With changes", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(trigger).toBeFocused();
});

for (const kind of [
  "worktree",
  "observation",
  "capture",
] satisfies ApiContext["kind"][]) {
  test(`landing stays unselected when ${kind} contexts have no usable diff`, async ({
    page,
  }) => {
    const state = await localWorkspace(page);
    const contexts: ApiContext[] = [
      { ...state.context, kind, changedFileCount: 0 },
      { ...state.context, kind, id: "unknown", changedFileCount: null },
      {
        ...state.context,
        kind,
        id: "unavailable",
        availability: "unavailable",
      },
    ];
    await page.route("**/api/v2/contexts?*", (route) =>
      route.fulfill({ json: { contexts, nextCursor: null } }),
    );
    await page.goto("/");
    await expect(
      page.getByRole("heading", { name: "No changes to review" }),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
    const trigger = page.getByRole("button", {
      name: "Switch review",
      exact: true,
    });
    await expect(trigger).toHaveText(/Switch review/);
    await expect(trigger).not.toContainText("Local review");
    expect(state.scopes).toEqual([]);
    await trigger.click();
    const dialog = page.getByRole("dialog", { name: "Switch review" });
    if (kind === "capture") {
      await expect(dialog.getByRole("option")).toHaveCount(1);
      await dialog.getByRole("option", { name: /^Piped/ }).click();
      await expect(
        dialog.getByRole("option", { name: /Unavailable/ }),
      ).toBeDisabled();
    } else {
      await expect(dialog.getByRole("option")).toHaveCount(0);
      await dialog
        .getByRole("button", { name: "Filters", exact: true })
        .click();
      await dialog.getByRole("checkbox", { name: "All", exact: true }).check();
    }
    await expect(dialog.getByRole("option")).toHaveCount(
      kind === "observation" ? 1 : 3,
    );
    await page.keyboard.press("Escape");
    await page.setViewportSize({ width: 360, height: 640 });
    await expect(
      page.getByRole("heading", { name: "No changes to review" }),
    ).toBeVisible();
    await page.reload();
    await expect(
      page.getByRole("heading", { name: "No changes to review" }),
    ).toBeVisible();
    await expect(page).toHaveURL(/\/$/);
  });
}

test("landing redirects to the first usable diff across catalog pages", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  await page.route("**/api/v2/contexts?*", (route) => {
    const cursor = new URL(route.request().url()).searchParams.get("cursor");
    return route.fulfill({
      json: cursor
        ? {
            contexts: [state.context, { ...state.context, id: "later" }],
            nextCursor: null,
          }
        : {
            contexts: [
              { ...state.context, id: "empty", changedFileCount: 0 },
              { ...state.context, id: "unknown", changedFileCount: null },
              {
                ...state.context,
                id: "unavailable",
                availability: "unavailable",
              },
            ],
            nextCursor: "next",
          },
    });
  });
  await page.goto("/");
  await ready(page);
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
});

test("landing selects a diff when a catalog refresh adds changes", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  state.context.changedFileCount = 0;
  await page.route("**/api/v2/events", (route) => route.abort());
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "No changes to review" }),
  ).toBeVisible();
  state.context.changedFileCount = 1;
  await page.evaluate(() =>
    document.dispatchEvent(new Event("visibilitychange")),
  );
  await ready(page);
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
});

test("empty contexts remain accessible through the picker and direct URLs", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  state.context.changedFileCount = 0;
  await page.route("**/api/v2/contexts/*/diffs/current?*", (route) =>
    route.fulfill({ json: { ...state.repository("all"), files: [] } }),
  );
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "No changes to review" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Switch review", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  await dialog.getByRole("checkbox", { name: "All", exact: true }).check();
  await dialog.getByRole("option", { name: /Local review/ }).click();
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
  await expect(
    page.getByRole("heading", { name: "No changes in this snapshot" }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "No changes in this snapshot" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
});

test("deleting the last diff returns to landing when empty contexts remain", async ({
  page,
}) => {
  const state = await deletableWorkspace(page, false, true);
  state.contexts.push({
    ...state.state.context,
    id: "empty",
    changedFileCount: 0,
  });
  await page.evaluate(() =>
    document.dispatchEvent(new Event("visibilitychange")),
  );
  const trigger = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await trigger.click();
  const picker = page.getByRole("dialog", { name: "Switch review" });
  await picker.getByRole("button", { name: "Filters", exact: true }).click();
  await picker.getByRole("checkbox", { name: "All", exact: true }).check();
  await expect(picker.getByRole("option")).toHaveCount(2);
  await page.keyboard.press("Escape");
  await page
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "Delete context?", exact: true })
    .getByRole("button", { name: "Delete context", exact: true })
    .click();
  await expect(page).toHaveURL(/\/$/);
  await expect(
    page.getByRole("heading", { name: "No changes to review" }),
  ).toBeVisible();
  await expect(page.locator("main.session-status")).toBeFocused();
});

test("unavailable context keeps the switcher usable", async ({ page }) => {
  const state = await localWorkspace(page);
  const unavailable: ApiContext = {
    ...state.context,
    id: "unavailable",
    name: "Unavailable repo",
    availability: "unavailable",
  };
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({
      json: { contexts: [state.context, unavailable], nextCursor: null },
    }),
  );
  await page.route("**/api/v2/contexts/unavailable", (route) =>
    route.fulfill({ status: 503, json: { detail: "Repository moved" } }),
  );
  await page.goto("/contexts/unavailable");
  await expect(page.getByText("Repository moved")).toBeVisible();
  await page
    .getByRole("button", { name: "Switch review", exact: true })
    .click();
  await page
    .getByRole("combobox", { name: "Search reviews" })
    .fill("Local review");
  await page.keyboard.press("Enter");
  await ready(page);
  await page
    .getByRole("button", { name: "Switch review", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  await dialog.getByRole("checkbox", { name: "All", exact: true }).check();
  const search = dialog.getByRole("combobox", { name: "Search reviews" });
  await search.fill("Unavailable repo");
  await expect(dialog.getByRole("option")).toBeDisabled();
  await search.press("Enter");
  await expect(dialog).toBeVisible();
  await expect(page).toHaveURL(/\/contexts\/local-review$/);
});

test("repository popup orders checkouts by changes and shares click and keyboard search", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  const contexts: ApiContext[] = [
    {
      ...state.context,
      name: "servediff",
      branch: "main",
      changedFileCount: 0,
      lastChangedAt: 10,
    },
    {
      ...state.context,
      id: "external-tree",
      name: "servediff",
      branch: "feature/picker",
      worktreeName: "picker-folder",
      root: "/elsewhere/picker-folder",
      lastChangedAt: 3,
      changedFileCount: 6,
    },
    {
      ...state.context,
      id: "other-repo",
      name: "dotfiles",
      repositoryId: "dotfiles",
      root: "/dotfiles",
      lastChangedAt: 2,
      changedFileCount: 0,
    },
  ];
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts, nextCursor: null } }),
  );
  await page.route("**/api/v2/contexts/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    return route.fulfill({
      json: contexts.find((context) => context.id === id),
    });
  });
  await page.goto("/");
  await ready(page);
  const trigger = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await expect(trigger).toHaveAttribute("aria-haspopup", "dialog");
  await expect(trigger).toContainText("servediff");
  await expect(trigger).toContainText("feature/picker");
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  const search = dialog.getByRole("combobox", { name: "Search reviews" });
  await expect(search).toBeFocused();
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  const includeAll = dialog.getByRole("checkbox", { name: "All", exact: true });
  await expect(includeAll).not.toBeChecked();
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("feature/picker");
  await includeAll.check();
  await expect(dialog.getByRole("option")).toHaveCount(3);
  await expect(dialog.locator(".project-picker-name")).toHaveText([
    "servediff",
    "servediff",
    "dotfiles",
  ]);
  await expect(dialog.locator(".project-picker-changes")).toHaveText([
    "6 changed files",
    "No changes",
    "No changes",
  ]);
  const desktopBounds = await dialog.boundingBox();
  if (!desktopBounds) throw new Error("Missing popup bounds");
  expect(desktopBounds.y).toBeLessThan(120);
  await search.fill("servediff");
  await expect(dialog.getByRole("option")).toHaveCount(2);
  await expect(dialog.locator(".project-picker-branch")).toHaveText([
    "feature/picker",
    "main",
  ]);
  await search.fill("");
  await expect(
    dialog.getByRole("option", { name: /feature\/picker/ }),
  ).toContainText("Worktree · /elsewhere/picker-folder");
  await search.fill("no matching repo");
  await expect(dialog.getByRole("status")).toHaveText("No matching reviews");
  await search.fill("");
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("ArrowUp");
  await expect(
    dialog.getByRole("option", { name: /feature\/picker/ }),
  ).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/contexts\/external-tree$/);
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
  await expect(trigger).toContainText("feature/picker");

  for (const shortcut of ["Control+k", "Meta+k"]) {
    await page.keyboard.press(shortcut);
    await expect(dialog).toBeVisible();
    await expect(search).toBeFocused();
    await expect(search).toHaveValue("");
    await search.fill("servediff main");
    await expect(dialog.getByRole("option")).toHaveCount(1);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
  }
  await page.setViewportSize({ width: 360, height: 640 });
  await trigger.click();
  const bounds = await dialog.boundingBox();
  if (!bounds) throw new Error("Missing popup bounds");
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(360);
  expect(bounds.y).toBeGreaterThanOrEqual(0);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(640);
  await expect(
    dialog.getByRole("option", { name: /feature\/picker/ }),
  ).toBeVisible();
  await dialog.getByRole("option", { name: /dotfiles/ }).click();
  await expect(page).toHaveURL(/\/contexts\/other-repo$/);
  await expect(dialog).toBeHidden();
});

test("picker distinguishes unknown and unavailable status and refreshes change counts", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  let changedFileCount = 0;
  const unknown: ApiContext = {
    ...state.context,
    id: "unknown",
    name: "Unknown repo",
    root: "/unknown",
    changedFileCount: null,
  };
  const unavailable: ApiContext = {
    ...state.context,
    id: "unavailable",
    name: "Missing repo",
    root: "/missing",
    availability: "unavailable",
    changedFileCount: null,
  };
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({
      json: {
        contexts: [
          { ...state.context, changedFileCount },
          unknown,
          unavailable,
        ],
        nextCursor: null,
      },
    }),
  );
  await page.goto("/contexts/local-review");
  await ready(page);
  await page
    .getByRole("button", { name: "Switch review", exact: true })
    .click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  const includeAll = dialog.getByRole("checkbox", { name: "All", exact: true });
  await expect(includeAll).not.toBeChecked();
  await expect(dialog.getByRole("option")).toHaveCount(0);
  await includeAll.check();
  await expect(
    dialog.getByRole("option", { name: /Local review/ }),
  ).toContainText("No changes");
  await expect(
    dialog.getByRole("option", { name: /Unknown repo/ }),
  ).toContainText("Status unknown");
  await expect(
    dialog.getByRole("option", { name: /Missing repo/ }),
  ).toContainText("Unavailable");
  await expect(
    dialog.getByRole("option", { name: /Missing repo/ }),
  ).toBeDisabled();
  const search = dialog.getByRole("combobox", { name: "Search reviews" });
  await search.fill("Local");
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await includeAll.uncheck();
  await expect(dialog.getByRole("option")).toHaveCount(0);
  await search.focus();
  changedFileCount = 2;
  await page.evaluate(() =>
    document.dispatchEvent(new Event("visibilitychange")),
  );
  await expect(dialog.getByRole("option")).toContainText("2 changed files");
  await expect(search).toBeFocused();
  await expect(search).toHaveValue("Local");
  await expect(search).toHaveAttribute(
    "aria-activedescendant",
    (await dialog.getByRole("option").getAttribute("id")) ?? "",
  );
});

test("picker combines freshness and All filters and preserves them on reopening", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  const contexts: ApiContext[] = [
    state.context,
    { ...state.context, id: "old", name: "Old changes", stale: true },
    {
      ...state.context,
      id: "empty",
      name: "Empty latest",
      changedFileCount: 0,
    },
    {
      ...state.context,
      id: "unknown",
      name: "Unknown latest",
      changedFileCount: null,
    },
    {
      ...state.context,
      id: "missing",
      name: "Missing latest",
      availability: "unavailable",
    },
    {
      ...state.context,
      id: "old-empty",
      name: "Empty stale",
      stale: true,
      changedFileCount: 0,
    },
    {
      ...state.context,
      id: "old-missing",
      name: "Missing stale",
      stale: true,
      availability: "unavailable",
    },
  ];
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts, nextCursor: null } }),
  );
  await page.goto("/");
  await ready(page);
  const trigger = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  const search = dialog.getByRole("combobox", { name: "Search reviews" });
  const latest = dialog.getByRole("button", { name: "Latest", exact: true });
  const stale = dialog.getByRole("button", { name: "Stale", exact: true });
  const allFreshness = dialog.getByRole("button", { name: "All", exact: true });
  const includeAll = dialog.getByRole("checkbox", { name: "All", exact: true });
  await expect(latest).toHaveAttribute("aria-pressed", "true");
  await expect(allFreshness).toHaveAttribute("aria-pressed", "false");
  await expect(stale).toHaveAttribute("aria-pressed", "false");
  await expect(includeAll).not.toBeChecked();
  await expect(
    dialog.getByTitle(/include unavailable.*no changes/i),
  ).toBeVisible();
  const checkboxBounds = await includeAll.boundingBox();
  const staleBounds = await stale.boundingBox();
  if (!checkboxBounds || !staleBounds) throw new Error("Missing filter bounds");
  expect(checkboxBounds.x).toBeGreaterThan(staleBounds.x + staleBounds.width);
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("Local review");
  await search.fill("Old changes");
  await expect(dialog.getByRole("option")).toHaveCount(0);
  await allFreshness.click();
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("Stale");
  await search.fill("");
  await expect(dialog.getByRole("option")).toHaveCount(2);
  await stale.click();
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("Old changes");
  await includeAll.check();
  await expect(dialog.getByRole("option")).toHaveCount(3);
  await expect(
    dialog.getByRole("option", { name: /Empty stale/ }),
  ).toContainText("No changes");
  await expect(
    dialog.getByRole("option", { name: /Missing stale/ }),
  ).toContainText("Unavailable");
  await latest.click();
  await expect(dialog.getByRole("option")).toHaveCount(4);
  await expect(dialog.getByRole("option", { name: /Old changes/ })).toHaveCount(
    0,
  );
  await includeAll.uncheck();
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await stale.click();
  await includeAll.check();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await trigger.click();
  await expect(stale).toHaveAttribute("aria-pressed", "true");
  await expect(includeAll).toBeChecked();
  await expect(dialog.getByRole("option")).toHaveCount(3);
});

test("observation picker drills into repositories and searches independent sources on the same branch", async ({
  page,
}) => {
  await page.addInitScript(() => {
    class IngestionEvents extends EventTarget {
      notify = () =>
        this.dispatchEvent(new MessageEvent("ingestion", { data: "{}" }));
      constructor() {
        super();
        window.addEventListener("test-ingestion", this.notify);
      }
      close() {
        window.removeEventListener("test-ingestion", this.notify);
      }
    }
    Object.defineProperty(window, "EventSource", { value: IngestionEvents });
  });
  const state = await localWorkspace(page);
  function observation(
    id: string,
    hostname: string,
    runId: string,
    collectedAt: number,
  ): ApiContext {
    return {
      ...state.context,
      id,
      kind: "observation",
      worktreeName: "main-checkout",
      lastChangedAt: collectedAt,
      observation: {
        sourceId: `source-${hostname}`,
        hostname,
        runId,
        agent: "test-agent",
        trigger: "hook",
        repositoryKey: "repo",
        repositoryName: "Local review",
        remoteUrl: "https://example.test/review.git",
        checkoutKey: "checkout",
        root: "/workspace/review",
        worktreeName: "main-checkout",
        ...(id === "second" ? { linkedWorktree: true } : {}),
        branch: "main",
        head: "head",
        collectedAt,
        collectorVersion: "test",
      },
      capabilities: {
        ...state.context.capabilities,
        diff: {
          ...state.context.capabilities.diff,
          refresh: { state: "unavailable" },
        },
      },
    };
  }
  const contexts = [
    { ...observation("first", "container-a", "run-a", 1000), stale: true },
    observation("second", "container-b", "run-b", 2000),
    observation("third", "container-a", "run-a", 3000),
  ];
  let catalogRequests = 0;
  await page.route("**/api/v2/contexts?*", (route) => {
    catalogRequests++;
    return route.fulfill({ json: { contexts, nextCursor: null } });
  });
  await page.route("**/api/v2/contexts/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    return route.fulfill({
      json: contexts.find((context) => context.id === id),
    });
  });
  await page.route("**/api/v2/events", (route) => route.abort());
  await page.clock.install();
  await page.goto("/");
  await ready(page);
  await expect(
    page.getByRole("button", { name: "Refresh changes" }),
  ).toBeHidden();
  await expect(page.locator("#changes-title")).toHaveClass("sr-only");
  await expect(page.locator("#repo-path")).toContainText("container-a");
  await expect(page.locator("#repo-path")).not.toContainText("run-a");
  await expect(page.locator("#repo-path")).not.toContainText(
    "source-container-a",
  );
  await expect(page.locator("#repo-path")).not.toContainText("Collected");
  await expect(page.locator("#repo-path")).toHaveAttribute(
    "title",
    /Context: first/,
  );
  await expect(page.locator(".context-switcher-worktree")).toHaveCount(0);
  const requests = state.scopes.length;
  const catalogReads = catalogRequests;
  await page.clock.fastForward(9000);
  expect(state.scopes).toHaveLength(requests);
  expect(catalogRequests).toBe(catalogReads);
  const trigger = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  const search = dialog.getByRole("combobox", { name: "Search reviews" });
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("2 snapshots");
  await search.press("Enter");
  await expect(
    dialog.getByRole("listbox", { name: "Collected snapshots" }),
  ).toBeVisible();
  await expect(dialog.getByRole("option")).toHaveCount(2);
  await expect(dialog.getByRole("option", { name: /container-a/ })).toHaveCount(
    1,
  );
  await search.fill("main container-b run-b");
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await dialog.getByRole("button", { name: "Back to reviews" }).click();
  await expect(search).toBeFocused();
  await expect(search).toHaveValue("main container-b run-b");
  await expect(dialog.getByRole("option")).toContainText("1 snapshot");
  await search.press("Enter");
  await search.press("Enter");
  await expect(page).toHaveURL(/\/contexts\/second$/);
  await expect(trigger).toBeFocused();
  await expect(page.locator(".context-switcher-worktree")).toHaveAccessibleName(
    "Linked worktree: main-checkout",
  );
  await expect(page.locator("#repo-path")).toContainText("container-b");
  await expect(page.locator("#repo-path")).not.toContainText("run-b");
  await expect(trigger).toHaveAttribute(
    "title",
    /Run: run-b[\s\S]*Context: second/,
  );
  await page.setViewportSize({ width: 360, height: 640 });
  await expect(page.locator(".context-switcher-worktree")).toBeVisible();
  await expect(page.locator(".context-switcher-worktree-name")).toBeHidden();
  await trigger.click();
  await search.press("Enter");
  const bounds = await dialog.boundingBox();
  if (!bounds) throw new Error("Missing picker bounds");
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(360);
  await search.fill("not-a-source");
  await expect(
    dialog.getByText("No snapshots found", { exact: true }),
  ).toBeVisible();
  await search.fill("");
  await search.press("ArrowLeft");
  await expect(dialog.getByRole("listbox", { name: "Reviews" })).toBeVisible();
  const selected = contexts.find((context) => context.id === "second");
  if (!selected) throw new Error("Missing selected observation");
  selected.stale = true;
  contexts.push(observation("fourth", "container-b", "run-new", 4000));
  const diffReads = state.scopes.length;
  await page.evaluate(() => window.dispatchEvent(new Event("test-ingestion")));
  await expect(dialog.getByRole("option")).toContainText("2 snapshots");
  await expect(page).toHaveURL(/\/contexts\/second$/);
  expect(state.scopes).toHaveLength(diffReads);
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  await dialog.getByRole("button", { name: "All", exact: true }).click();
  await expect(dialog.getByRole("option")).toContainText("4 snapshots");
  await search.press("Enter");
  await expect(dialog.getByRole("option")).toHaveCount(4);
  await expect(
    dialog.getByRole("option", { name: /container-b.*run-b/ }),
  ).toContainText("Stale");
  await expect(page).toHaveURL(/\/contexts\/second$/);
});

test("picker combines filters before grouping and preserves them across navigation and reopening", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  function snapshot(
    id: string,
    hostname: string,
    branch: string,
    worktreeName: string,
    changedFileCount: number,
  ): ApiContext {
    return {
      ...state.context,
      id,
      kind: "observation",
      branch,
      worktreeName,
      changedFileCount,
      observation: {
        sourceId: `source-${hostname}`,
        hostname,
        runId: "run",
        agent: "",
        trigger: "review",
        repositoryKey: "repo",
        repositoryName: state.context.name,
        remoteUrl: "https://example.test/review.git",
        checkoutKey: worktreeName,
        root: `/workspace/${worktreeName}`,
        worktreeName,
        branch,
        head: "head",
        collectedAt: 1000,
        collectorVersion: "test",
      },
    };
  }
  const contexts = [
    snapshot("first", "host-a", "main", "tree-a", 1),
    snapshot("second", "host-b", "feature", "tree-b", 2),
    snapshot("third", "host-c", "main", "tree-c", 0),
    {
      ...state.context,
      id: "missing",
      name: "Missing checkout",
      availability: "unavailable",
    },
  ];
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({ json: { contexts, nextCursor: null } }),
  );
  await page.route("**/api/v2/contexts/*", (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    return route.fulfill({
      json: contexts.find((context) => context.id === id),
    });
  });
  await page.goto("/");
  await ready(page);
  const trigger = page.getByRole("button", {
    name: "Switch review",
    exact: true,
  });
  await trigger.click();
  const dialog = page.getByRole("dialog", { name: "Switch review" });
  const search = dialog.getByRole("combobox", { name: "Search reviews" });
  await expect(dialog.getByRole("option")).toContainText("2 snapshots");
  await expect(
    dialog.getByRole("option", { name: /Missing checkout/ }),
  ).toHaveCount(0);
  await dialog.getByRole("button", { name: "Filters", exact: true }).click();
  await dialog.getByRole("button", { name: "Host", exact: true }).click();
  await page
    .getByRole("menuitemcheckbox", { name: "host-a", exact: true })
    .click();
  await page
    .getByRole("menuitemcheckbox", { name: "host-b", exact: true })
    .click();
  await page.keyboard.press("Escape");
  await expect(dialog.getByRole("option")).toContainText("2 snapshots");
  await dialog.getByRole("button", { name: "Branch", exact: true }).click();
  await page
    .getByRole("menuitemcheckbox", { name: "feature", exact: true })
    .click();
  await page.keyboard.press("Escape");
  await expect(dialog.getByRole("option")).toContainText("1 snapshot");
  await dialog.getByRole("button", { name: "Worktree", exact: true }).click();
  await page
    .getByRole("menuitemcheckbox", { name: "tree-b", exact: true })
    .click();
  await page.keyboard.press("Escape");
  await dialog.getByRole("button", { name: "Stale", exact: true }).click();
  await expect(
    dialog.getByText("No reviews found", { exact: true }),
  ).toBeVisible();
  await dialog.getByRole("button", { name: "Latest", exact: true }).click();
  await search.focus();
  await search.press("Enter");
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await expect(dialog.getByRole("option")).toContainText("tree-b");
  await dialog.getByRole("button", { name: "Back to reviews" }).click();
  await expect(dialog.getByRole("option")).toContainText("1 snapshot");
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await trigger.click();
  await expect(dialog.getByRole("option")).toContainText("1 snapshot");
  await expect(dialog.locator(".project-picker-filter-summary")).toContainText(
    "Host: host-a · Host: host-b · Branch: feature · Worktree: tree-b",
  );
  await page.setViewportSize({ width: 360, height: 640 });
  const bounds = await dialog.boundingBox();
  if (!bounds) throw new Error("Missing picker bounds");
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(360);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(640);
  await dialog
    .getByRole("button", { name: "Clear filters", exact: true })
    .click();
  await expect(dialog.getByRole("option")).toContainText("2 snapshots");
  await dialog.getByRole("checkbox", { name: "All", exact: true }).check();
  await expect(dialog.getByRole("option", { name: /3 snapshots/ })).toHaveCount(
    1,
  );
  const unavailable = dialog.getByRole("option", { name: /Missing checkout/ });
  await expect(unavailable).toBeDisabled();
  await expect(unavailable).toContainText(
    "No collected snapshot for this checkout",
  );
  await search.fill("Missing checkout");
  await search.press("ArrowDown");
  await search.press("Enter");
  await expect(dialog).toBeVisible();
  await expect(search).not.toHaveAttribute("aria-activedescendant");
  await expect(page).toHaveURL(/\/contexts\/first$/);
  await search.fill("");
  await search.press("ArrowDown");
  await expect(unavailable).toHaveAttribute("aria-selected", "false");
});
