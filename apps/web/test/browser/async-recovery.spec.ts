import { expect, test, type Page, type Route } from "@playwright/test";
import { createApiClient, type ApiRepositoryDiff } from "@servediff/api";
import type { ReviewComment } from "@servediff/shared";

declare global {
  interface Window {
    reviewTestTools: {
      execute(name: string, input: Record<string, unknown>): Promise<unknown>;
    };
  }
}

async function fixture(baseURL: string | undefined) {
  if (!baseURL) throw new Error("Missing test URL");
  const { data } = await createApiClient({ baseUrl: baseURL }).GET(
    "/api/v1/diffs/current",
    { params: { query: { scope: "all" } } },
  );
  if (!data) throw new Error("Missing fixture diff");
  return data;
}

function commentFor(
  repository: ApiRepositoryDiff,
  body: string,
): ReviewComment {
  const file = repository.files.find(
    (file) => file.path === "src/components/Badge.tsx",
  );
  if (!file) throw new Error("Missing fixture file");
  return {
    id: "saved-comment",
    diffId: repository.id,
    versionId: repository.versionId,
    path: file.path,
    scope: "all",
    fingerprint: file.fingerprint,
    side: "additions",
    start: 3,
    end: 3,
    code: "+   return <span>{label}</span>;",
    body,
    status: "open",
    createdAt: 1,
    origin: {
      diffId: repository.id,
      versionId: repository.versionId,
      source: repository.source,
      repository: repository.name,
      branch: repository.branch,
      head: repository.head,
      revision: repository.revision,
      file: { status: file.status, oldPath: file.oldPath },
    },
  };
}

async function openWorkspace(page: Page) {
  await page.route("**/api/v2/contexts/*/review-marks?*", (route) =>
    route.fulfill({ json: { marks: [] } }),
  );
  await page.setViewportSize({ width: 1280, height: 844 });
  await page.goto("/");
  await expect(page.locator("#additions")).toHaveText("+43");
  await page.evaluate(() => document.fonts.ready);
}

async function openDraft(page: Page, line = 3) {
  await page
    .locator('#file-tree [data-path="src/components/Badge.tsx"]')
    .click();
  const file = page
    .locator("#viewer diffs-container")
    .filter({ hasText: "src/components/Badge.tsx" });
  const input = page.locator("#viewer .comment-editor textarea");
  await expect(async () => {
    if (await input.isVisible()) return;
    await file
      .locator(`[data-gutter] [data-column-number="${line}"]`)
      .hover({ timeout: 1_000 });
    await file
      .locator("[data-utility-button]")
      .click({ force: true, timeout: 1_000 });
    await expect(input).toBeVisible({ timeout: 1_000 });
  }).toPass({ timeout: 10_000 });
  return input;
}

function takeRequest(requests: Route[]) {
  const route = requests.shift();
  if (!route) throw new Error("Missing pending request");
  return route;
}

test("comment submission guards keyboard and button duplicates", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  const requests: Route[] = [];
  let comments: ReviewComment[] = [];
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (route.request().method() === "POST") {
      requests.push(route);
      return;
    }
    return route.fulfill({ json: { comments } });
  });
  await openWorkspace(page);
  const input = await openDraft(page);
  await input.fill("Submitted snapshot");
  await input.press("Control+Enter");
  await expect.poll(() => requests.length).toBe(1);
  await input.press("Control+Enter");
  const save = page.getByRole("button", { name: "Save comment" });
  await expect(save).toBeDisabled();
  expect(requests).toHaveLength(1);
  comments = [commentFor(repository, "Submitted snapshot")];
  await takeRequest(requests).fulfill({ status: 201, json: comments[0] });
  await expect(page.locator("#viewer .comment-editor")).toHaveCount(0);
});

test("late save preserves newer text and retries it against the saved ID", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  const requests: Route[] = [];
  let comments: ReviewComment[] = [];
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (route.request().method() === "POST") {
      requests.push(route);
      return;
    }
    return route.fulfill({ json: { comments } });
  });
  await page.route("**/api/v2/contexts/*/comments/saved-comment", (route) => {
    requests.push(route);
  });
  await openWorkspace(page);
  const input = await openDraft(page);
  await input.fill("Submitted snapshot");
  await input.press("Control+Enter");
  await expect.poll(() => requests.length).toBe(1);
  await input.fill("Newer unsaved edit");
  comments = [commentFor(repository, "Submitted snapshot")];
  await takeRequest(requests).fulfill({ status: 201, json: comments[0] });
  await expect(input).toHaveValue("Newer unsaved edit");
  await expect(input).toBeFocused();
  await input.press("Control+Enter");
  await expect.poll(() => requests.length).toBe(1);
  const update = takeRequest(requests);
  expect(update.request().method()).toBe("PATCH");
  expect(update.request().postDataJSON()).toEqual({
    body: "Newer unsaved edit",
  });
  comments = [commentFor(repository, "Newer unsaved edit")];
  await update.fulfill({ json: comments[0] });
  await expect(page.locator("#viewer .comment-editor")).toHaveCount(0);
});

test("late save cannot clear a replacement draft", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  const requests: Route[] = [];
  let comments: ReviewComment[] = [];
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (route.request().method() === "POST") {
      requests.push(route);
      return;
    }
    return route.fulfill({ json: { comments } });
  });
  await openWorkspace(page);
  const input = await openDraft(page);
  await input.fill("First draft");
  await input.press("Control+Enter");
  await expect.poll(() => requests.length).toBe(1);
  await input.press("Escape");
  const replacement = await openDraft(page, 4);
  await replacement.fill("Replacement draft");
  comments = [commentFor(repository, "First draft")];
  await takeRequest(requests).fulfill({ status: 201, json: comments[0] });
  await expect(page.locator("#comment-count")).toHaveText("1");
  await expect(replacement).toHaveValue("Replacement draft");
});

test("save failure stays visible on Changes and retains the draft for retry", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  let fail = true;
  let comments: ReviewComment[] = [];
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (route.request().method() !== "POST")
      return route.fulfill({ json: { comments } });
    if (fail)
      return route.fulfill({ status: 500, json: { detail: "Save rejected" } });
    comments = [commentFor(repository, "Retain me")];
    return route.fulfill({ status: 201, json: comments[0] });
  });
  await openWorkspace(page);
  const input = await openDraft(page);
  await input.fill("Retain me");
  await input.press("Control+Enter");
  await expect(page.locator("#viewer .comment-editor [role=alert]")).toHaveText(
    "Save rejected",
  );
  await expect(page.locator("#comment-feedback")).toBeVisible();
  await expect(input).toHaveValue("Retain me");
  fail = false;
  await input.press("Control+Enter");
  await expect(page.locator("#viewer .comment-editor")).toHaveCount(0);
});

test("concurrent reviewed files merge successful responses in either order", async ({
  page,
}) => {
  const requests: Route[] = [];
  const marks = new Map<string, string>();
  await page.route("**/api/v2/contexts/*/comments", (route) =>
    route.fulfill({ json: { comments: [] } }),
  );
  await openWorkspace(page);
  await page.route("**/api/v2/contexts/*/review-marks?*", (route) =>
    route.fulfill({
      json: {
        marks: [...marks].map(([fileId, fileVersion]) => ({
          fileId,
          fileVersion,
          scope: "all",
        })),
      },
    }),
  );
  await page.route("**/api/v2/contexts/*/review-marks/*?*", (route) => {
    requests.push(route);
  });
  const buttons = page.locator("#viewer .review-button");
  await buttons.nth(0).click();
  await buttons.nth(1).click();
  await expect.poll(() => requests.length).toBe(2);
  for (const route of requests.reverse()) {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    const payload: unknown = route.request().postDataJSON();
    if (
      !id ||
      typeof payload !== "object" ||
      payload === null ||
      !("fileVersion" in payload) ||
      typeof payload.fileVersion !== "string"
    )
      throw new Error("Invalid mark request");
    marks.set(id, payload.fileVersion);
    await route.fulfill({ json: {} });
  }
  await expect(page.locator("#review-count")).toHaveText("2 of 12 reviewed");
  await expect(
    page.locator("#viewer .review-button[aria-pressed=true]"),
  ).toHaveCount(2);
  await expect(
    page.locator("#viewer .diff-collapse[aria-expanded=false]"),
  ).toHaveCount(2);
});

test("a stale comment poll cannot undo successful resolution", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  let comments = [commentFor(repository, "Review this")];
  let holdReads = false;
  const requests: Route[] = [];
  await page.clock.install();
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (holdReads) {
      requests.push(route);
      return;
    }
    return route.fulfill({ json: { comments } });
  });
  await page.route("**/api/v2/contexts/*/comments/saved-comment", (route) => {
    comments = comments.map((comment) => ({ ...comment, status: "resolved" }));
    return route.fulfill({ json: comments[0] });
  });
  await openWorkspace(page);
  await page.getByRole("tab", { name: /Review/ }).click();
  const card = page.locator(
    '#comments-panel [data-comment-id="saved-comment"]',
  );
  await expect(card).toBeVisible();
  const stale = [...comments];
  holdReads = true;
  await page.clock.fastForward(3_000);
  await expect.poll(() => requests.length).toBe(1);
  await card.getByRole("button", { name: "Resolve", exact: true }).click();
  await expect(card).toHaveCount(0);
  holdReads = false;
  await takeRequest(requests).fulfill({ json: { comments: stale } });
  await page.getByRole("button", { name: "Filter comments: Open" }).click();
  await page.getByRole("menuitemradio", { name: "Resolved 1" }).click();
  await expect(card.locator(".comment-state")).toHaveText("Resolved");
});

test("slow comment polling completes instead of restarting every interval", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  let holdReads = false;
  const requests: Route[] = [];
  await page.clock.install();
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (holdReads) {
      requests.push(route);
      return;
    }
    return route.fulfill({ json: { comments: [] } });
  });
  await openWorkspace(page);
  holdReads = true;
  await page.clock.fastForward(3_000);
  await expect.poll(() => requests.length).toBe(1);
  await page.clock.fastForward(3_000);
  expect(requests).toHaveLength(1);
  await takeRequest(requests).fulfill({
    json: { comments: [commentFor(repository, "External comment")] },
  });
  await expect(page.locator("#comment-count")).toHaveText("1");
});

test("WebMCP resolution shares comment ownership and preserves an active draft", async ({
  page,
  baseURL,
}) => {
  await page.addInitScript(() => {
    class ModelContextFake extends EventTarget implements WebMCP.ModelContext {
      ontoolchange:
        | ((this: WebMCP.ModelContext, event: Event) => unknown)
        | null = null;
      tools = new Map<string, WebMCP.ModelContextTool>();
      async registerTool(
        tool: WebMCP.ModelContextTool,
        options?: WebMCP.ModelContextRegisterToolOptions,
      ) {
        this.tools.set(tool.name, tool);
        options?.signal?.addEventListener("abort", () => {
          if (this.tools.get(tool.name) === tool) this.tools.delete(tool.name);
        });
      }
      async getTools() {
        return [];
      }
    }
    const modelContext = new ModelContextFake();
    Object.defineProperty(document, "modelContext", { value: modelContext });
    window.reviewTestTools = {
      async execute(name, input) {
        const tool = modelContext.tools.get(name);
        if (!tool) throw new Error("Tool not registered");
        return tool.execute(input, { signal: new AbortController().signal });
      },
    };
  });
  const repository = await fixture(baseURL);
  let comments = [commentFor(repository, "Resolve through WebMCP")];
  let fail = true;
  let holdReads = false;
  const requests: Route[] = [];
  await page.clock.install();
  await page.route("**/api/v2/contexts/*/comments", (route) => {
    if (holdReads) {
      requests.push(route);
      return;
    }
    return route.fulfill({ json: { comments } });
  });
  await page.route(
    "**/api/v2/contexts/*/review/comments/saved-comment/resolve",
    (route) => {
      if (fail)
        return route.fulfill({
          status: 500,
          json: { detail: "Resolve rejected" },
        });
      comments = comments.map((comment) => ({
        ...comment,
        status: "resolved",
      }));
      return route.fulfill({
        json: { comment_id: "saved-comment", status: "resolved" },
      });
    },
  );
  await openWorkspace(page);
  await expect(page.locator("#comment-count")).toHaveText("1");
  const input = await openDraft(page, 4);
  await input.fill("Keep this active draft");
  const failure = await page.evaluate(async () => {
    try {
      await window.reviewTestTools.execute("resolve_review_comment", {
        comment_id: "saved-comment",
      });
      return "Unexpected success";
    } catch (error) {
      return error instanceof Error ? error.message : "Unknown failure";
    }
  });
  expect(failure).toBe("Resolve rejected");
  await expect(page.locator("#comment-count")).toHaveText("1");
  const stale = [...comments];
  holdReads = true;
  await page.clock.fastForward(3_000);
  await expect.poll(() => requests.length).toBe(1);
  fail = false;
  await page.evaluate(() =>
    window.reviewTestTools.execute("resolve_review_comment", {
      comment_id: "saved-comment",
    }),
  );
  holdReads = false;
  await takeRequest(requests).fulfill({ json: { comments: stale } });
  await expect(input).toHaveValue("Keep this active draft");
  await input.press("Escape");
  await page.getByRole("tab", { name: /Review/ }).click();
  await page.getByRole("button", { name: "Filter comments: Open" }).click();
  await page.getByRole("menuitemradio", { name: "Resolved 1" }).click();
  await expect(
    page.locator(
      '#comments-panel [data-comment-id="saved-comment"] .comment-state',
    ),
  ).toHaveText("Resolved");
});

test("session failure retries without reloading the page", async ({ page }) => {
  let fail = true;
  await page.route("**/api/v2/contexts/*", (route) =>
    fail
      ? route.fulfill({ status: 503, json: { detail: "Session unavailable" } })
      : route.continue(),
  );
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Cannot load servediff" }),
  ).toBeVisible();
  fail = false;
  await page.getByRole("button", { name: "Retry" }).click();
  await expect(page.getByRole("heading", { name: "Piped" })).toBeVisible();
});

test("reset waits for pending marks and clears their collapse state", async ({
  page,
  baseURL,
}) => {
  const repository = await fixture(baseURL);
  const first = repository.files[0];
  if (!first) throw new Error("Missing fixture file");
  let marks = [
    { fileId: first.id, fileVersion: first.fingerprint, scope: "all" },
  ];
  const updates: Route[] = [];
  let resets = 0;
  await page.route("**/api/v2/contexts/*/comments", (route) =>
    route.fulfill({ json: { comments: [] } }),
  );
  await openWorkspace(page);
  await page.route("**/api/v2/contexts/*/review-marks?*", (route) => {
    if (route.request().method() === "DELETE") {
      resets++;
      marks = [];
    }
    return route.fulfill({ json: { marks } });
  });
  // Reload initializes the owner from the server mark, not browser storage.
  await page.reload();
  await expect(page.locator("#review-count")).toHaveText("1 of 12 reviewed");
  await page.route("**/api/v2/contexts/*/review-marks/*?*", (route) => {
    updates.push(route);
  });
  const mark = page
    .locator("#viewer .review-button[aria-pressed=false]")
    .first();
  await mark.click();
  await expect.poll(() => updates.length).toBe(1);
  await page.getByRole("button", { name: "Reset reviewed" }).click();
  await expect(
    page.getByRole("button", { name: "Reset reviewed" }),
  ).toHaveAttribute("aria-busy", "true");
  expect(resets).toBe(0);
  await takeRequest(updates).fulfill({ json: {} });
  await expect.poll(() => resets).toBe(1);
  await expect(page.locator("#review-count")).toHaveText("0 of 12 reviewed");
  await expect(
    page.locator("#viewer .diff-collapse[aria-expanded=false]"),
  ).toHaveCount(0);
});

test("reviewed-file failure remains visible with comments disabled and retries", async ({
  page,
  baseURL,
}) => {
  if (!baseURL) throw new Error("Missing test URL");
  const { data: session } = await createApiClient({ baseUrl: baseURL }).GET(
    "/api/v1/session",
  );
  if (!session) throw new Error("Missing fixture session");
  await page.route("**/api/v2/contexts/*", (route) =>
    route.fulfill({
      json: {
        ...session,
        id: new URL(route.request().url()).pathname.split("/").at(-1),
        capabilities: {
          ...session.capabilities,
          review: { comments: { state: "disabled" } },
        },
      },
    }),
  );
  await openWorkspace(page);
  let marks: { fileId: string; fileVersion: string; scope: string }[] = [];
  await page.route("**/api/v2/contexts/*/review-marks?*", (route) =>
    route.fulfill({ json: { marks } }),
  );
  await page.route("**/api/v2/contexts/*/review-marks/*?*", (route) =>
    route.fulfill({ status: 500, json: { detail: "Mark rejected" } }),
  );
  await page.locator("#viewer .review-button").first().click();
  await expect(page.locator(".reviewed-error")).toContainText("Mark rejected");
  await expect(page.locator("#comments-tab")).toHaveCount(0);
  await expect(page.locator("#review-count")).toHaveText("0 of 12 reviewed");
  await page.getByRole("button", { name: "Retry reviewed files" }).click();
  await expect(page.locator(".reviewed-error")).toHaveCount(0);
  await page.route("**/api/v2/contexts/*/review-marks/*?*", async (route) => {
    const id = new URL(route.request().url()).pathname.split("/").at(-1);
    const payload: unknown = route.request().postDataJSON();
    if (
      !id ||
      typeof payload !== "object" ||
      payload === null ||
      !("fileVersion" in payload) ||
      typeof payload.fileVersion !== "string"
    )
      throw new Error("Invalid mark request");
    marks = [{ fileId: id, fileVersion: payload.fileVersion, scope: "all" }];
    await route.fulfill({ json: {} });
  });
  await page.locator("#viewer .review-button").first().click();
  await expect(page.locator("#review-count")).toHaveText("1 of 12 reviewed");
});
