import { expect, test, type Page, type Route } from "@playwright/test";
import type { ApiRepositoryDiff, ApiSession } from "@servediff/api";
import {
  isDiffMode,
  type DiffMode,
  type ReviewComment,
} from "@servediff/shared";

async function localWorkspace(page: Page) {
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
  const session: ApiSession = {
    id: "local-review",
    user: { id: "user", name: "tester" },
    locationId: "location",
    repositoryId: "repo",
    source: "local",
    root: "/local-review",
    name: "Local review",
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
      root: session.root,
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
  await page.route("**/api/v1/session", (route) =>
    route.fulfill({ json: session }),
  );
  await page.route("**/api/v1/diffs/current?*", (route) => {
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
  await page.route("**/api/v1/diffs/*/files/*/patch?*", (route) => {
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
  await page.route("**/api/v1/diffs/*/files/*/contents?*", (route) => {
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
  });
  await page.route("**/api/v1/comments", (route) =>
    route.fulfill({ json: { comments: state.comments } }),
  );
  await page.route("**/api/v1/review-marks?*", (route) =>
    route.fulfill({ json: { marks: [] } }),
  );
  await page.route("**/api/v1/review-marks/*?*", (route) => {
    if (state.holdMarks) {
      state.pendingMarks.push(route);
      return;
    }
    return route.fulfill({ json: {} });
  });
  return state;
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

test("draft survives scopes and forced refresh while polling pauses and resumes", async ({
  page,
}) => {
  await page.clock.install();
  const state = await localWorkspace(page);
  await page.goto("/");
  await ready(page);
  await page.locator('[data-gutter] [data-column-number="50"]').last().hover();
  await page.locator("[data-utility-button]").click();
  const inline = page.locator("#viewer .comment-editor textarea");
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
