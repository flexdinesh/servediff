import { installChangeEvents, notifyChange } from "./change-events.ts";
import { expect, test, type Page, type Route } from "@playwright/test";
import type { ApiContext, ApiRepositoryDiff } from "@servediff/api";
import {
  isDiffMode,
  type DiffMode,
  type ReviewComment,
} from "@servediff/shared";

test.beforeEach(async ({ page }) => installChangeEvents(page));

async function catalog(page: Page, contexts: ApiContext[]) {
  const repositories = [
    ...new Map(
      contexts
        .filter((item) => item.repositoryId && item.kind === "worktree")
        .map((item) => [
          item.repositoryId,
          {
            id: item.repositoryId,
            name: item.name,
            root: item.root,
            lastSubmittedAt: item.lastSubmittedAt,
          },
        ]),
    ).values(),
  ];
  await page.route("**/api/v2/repositories", (route) =>
    route.fulfill({ json: { repositories } }),
  );
  await page.route("**/api/v2/repositories/*/worktrees", (route) => {
    const id = new URL(route.request().url()).pathname.split("/")[4];
    return route.fulfill({
      json: { worktrees: contexts.filter((item) => item.repositoryId === id) },
    });
  });
}

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
    expiresAt: null,
    submittedFrom: null,
    availability: "available",
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
  await catalog(page, [session]);
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

test("draft defers hook changes until composition ends", async ({ page }) => {
  await page.clock.install();
  const state = await localWorkspace(page);
  await page.goto("/");
  await ready(page);
  await page.locator('[data-gutter] [data-column-number="50"]').last().hover();
  await page.locator("[data-utility-button]").click();
  const inline = page.locator("#viewer .comment-editor textarea");
  await inline.fill("Preserve this draft");
  const requestsBefore = state.scopes.length;
  await notifyChange(page, "change", "local-review");
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
  await notifyChange(page, "change", "local-review");
  await expect.poll(() => state.scopes.length).toBeGreaterThan(requestsAfter);
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
      repositoryId: "second",
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
  await catalog(page, contexts);
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
    .getByRole("button", { name: "Switch repository", exact: true })
    .click();
  await page
    .getByRole("combobox", { name: "Search repositories" })
    .fill("Second repo");
  await page.keyboard.press("Enter");
  await page
    .getByRole("dialog", { name: "Select worktree" })
    .getByRole("option")
    .click();
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
    .getByRole("button", { name: "Switch repository", exact: true })
    .click();
  await page.getByRole("option", { name: /Captured patch/ }).click();
  await expect(page.locator("#changes-title")).toHaveText("Piped diff");
  await expect(
    page.getByRole("button", { name: "Refresh changes" }),
  ).toBeHidden();
  await expect(page.locator("#connection")).toHaveText("Fixed snapshot");
  await expect(page).toHaveURL(/\/contexts\/piped-patch$/);
  await page.reload();
  await expect(page.locator("#changes-title")).toHaveText("Piped diff");
});

test("empty service explains how to register a repository or pipe", async ({
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
      "Run servediff . in a repository, or pipe a diff into servediff.",
    ),
  ).toBeVisible();
});

test("unavailable context keeps the switcher usable", async ({ page }) => {
  const state = await localWorkspace(page);
  const unavailable: ApiContext = {
    ...state.context,
    id: "unavailable",
    repositoryId: "unavailable-repo",
    name: "Unavailable repo",
    availability: "unavailable",
  };
  await page.route("**/api/v2/contexts?*", (route) =>
    route.fulfill({
      json: { contexts: [state.context, unavailable], nextCursor: null },
    }),
  );
  await catalog(page, [state.context, unavailable]);
  await page.route("**/api/v2/contexts/unavailable", (route) =>
    route.fulfill({ status: 503, json: { detail: "Repository moved" } }),
  );
  await page.goto("/");
  await ready(page);
  await page
    .getByRole("button", { name: "Switch repository", exact: true })
    .click();
  await page
    .getByRole("combobox", { name: "Search repositories" })
    .fill("Unavailable repo");
  await page.keyboard.press("Enter");
  await page
    .getByRole("dialog", { name: "Select worktree" })
    .getByRole("option")
    .click();
  await expect(page.getByText("Repository moved")).toBeVisible();
  await page
    .getByRole("button", { name: "Switch repository", exact: true })
    .click();
  await page
    .getByRole("combobox", { name: "Search repositories" })
    .fill("Local review");
  await page.keyboard.press("Enter");
  await page
    .getByRole("dialog", { name: "Select worktree" })
    .getByRole("option")
    .click();
  await ready(page);
});

test("repository popup orders checkouts by changes and shares click and keyboard search", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  const contexts: ApiContext[] = [
    { ...state.context, name: "servediff", branch: "main" },
    {
      ...state.context,
      id: "external-tree",
      name: "servediff",
      branch: "feature/picker",
      worktreeName: "picker-folder",
      root: "/elsewhere/picker-folder",
      lastChangedAt: 3,
    },
    {
      ...state.context,
      id: "other-repo",
      name: "dotfiles",
      repositoryId: "dotfiles",
      root: "/dotfiles",
      lastChangedAt: 2,
    },
  ];
  await catalog(page, contexts);
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
    name: "Switch repository",
    exact: true,
  });
  await expect(trigger).toHaveAttribute("aria-haspopup", "dialog");
  await expect(trigger).toContainText("servediff");
  await expect(trigger).toContainText("main");
  await trigger.click();
  let dialog = page.getByRole("dialog", { name: "Switch repository" });
  const search = dialog.getByRole("combobox", { name: "Search repositories" });
  await expect(search).toBeFocused();
  await expect(dialog.getByRole("option")).toHaveCount(2);
  await expect(dialog.locator(".project-picker-name")).toHaveText([
    "servediff",
    "dotfiles",
  ]);
  await search.fill("servediff");
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await page.keyboard.press("Enter");
  dialog = page.getByRole("dialog", { name: "Select worktree" });
  await expect(dialog.getByRole("option")).toHaveCount(2);
  await expect(dialog.locator(".project-picker-branch")).toHaveText([
    "feature/picker",
    "main",
  ]);
  await dialog
    .getByRole("combobox", { name: "Search worktrees" })
    .fill("picker-folder");
  await expect(dialog.getByRole("option")).toHaveCount(1);
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/contexts\/external-tree$/);
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
  await expect(trigger).toContainText("feature/picker");
  for (const shortcut of ["Control+k", "Meta+k"]) {
    await page.keyboard.press(shortcut);
    const popup = page.getByRole("dialog", { name: "Switch repository" });
    await expect(popup).toBeVisible();
    await expect(popup.getByRole("combobox")).toBeFocused();
    await popup.getByRole("option", { name: /servediff/ }).click();
    await page.getByRole("button", { name: "Back to repositories" }).click();
    await expect(popup.getByRole("combobox")).toBeFocused();
    await page.keyboard.press("Escape");
    await expect(trigger).toBeFocused();
  }
  await page.setViewportSize({ width: 360, height: 640 });
  await trigger.click();
  const popup = page.getByRole("dialog", { name: "Switch repository" });
  const bounds = await popup.boundingBox();
  if (!bounds) throw new Error("Missing popup bounds");
  expect(bounds.x).toBeGreaterThanOrEqual(0);
  expect(bounds.x + bounds.width).toBeLessThanOrEqual(360);
  expect(bounds.y + bounds.height).toBeLessThanOrEqual(640);
  await popup.getByRole("option", { name: /servediff/ }).click();
  await expect(
    page.getByRole("option", { name: /feature\/picker/ }),
  ).toBeVisible();
});

test("worktree discovery starts after repository selection and retries failures", async ({
  page,
}) => {
  const state = await localWorkspace(page);
  let requests = 0;
  let pending: Route | undefined;
  await page.route("**/api/v2/repositories/*/worktrees", (route) => {
    requests++;
    if (requests === 1) {
      pending = route;
      return;
    }
    return route.fulfill({ json: { worktrees: [state.context] } });
  });
  await page.goto("/");
  await ready(page);
  expect(requests).toBe(0);
  await page
    .getByRole("button", { name: "Switch repository", exact: true })
    .click();
  expect(requests).toBe(0);
  await page.getByRole("option", { name: /Local review/ }).click();
  await expect(
    page.getByText("Loading worktrees…", { exact: true }),
  ).toBeVisible();
  await expect.poll(() => requests).toBe(1);
  if (!pending) throw new Error("Missing discovery request");
  await pending.fulfill({ status: 503, json: { detail: "Git unavailable" } });
  await expect(page.getByRole("alert")).toContainText("Git unavailable");
  await page.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "Select worktree" }).getByRole("option"),
  ).toHaveCount(1);
  expect(requests).toBe(2);
  await page.getByRole("button", { name: "Back to repositories" }).click();
  await expect(
    page.getByRole("combobox", { name: "Search repositories" }),
  ).toBeFocused();
});
