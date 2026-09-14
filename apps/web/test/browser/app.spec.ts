import { expect, test } from "@playwright/test";
import { resetFixtureState } from "./fixture-state.ts";

test.beforeEach(async ({ request }) => resetFixtureState(request));

test("shows Node server process metrics in the compact status bar", async ({
  page,
}) => {
  await page.route("**/api/v1/metrics", (route) =>
    route.fulfill({
      contentType: "application/json",
      body: JSON.stringify({ cpuUsage: 12.5, rssBytes: 52_428_800 }),
    }),
  );
  await page.goto("/");

  await expect(page.locator("#server-cpu")).toHaveText("12.5%");
  await expect(page.locator("#server-ram")).toHaveText("50.0 MB");
  await expect(page.locator("#server-metrics")).toHaveAttribute(
    "title",
    /ServeDiff server process/,
  );
  await expect(page.locator(".main-footer")).toHaveCSS("min-height", "28px");
});

test("renders and filters a piped diff", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 4_000 });
  await page.goto("/");

  await expect(page.getByRole("heading", { name: "Piped diff" })).toBeVisible();
  await expect(page.locator("#connection")).toHaveText("Fixed snapshot");
  await expect(page.locator("#file-count")).toHaveText("12");

  const files = page.locator("#file-tree [data-path]");
  await expect(files).toHaveCount(12);
  await expect(files.first()).toHaveCSS("height", "28px");
  await expect(files.first()).toHaveCSS("font-size", "13px");
  const treeAlignment = await page.evaluate(() => {
    const center = (element: Element | null) => {
      if (!element) throw new Error("Missing tree element");
      const box = element.getBoundingClientRect();
      return box.top + box.height / 2;
    };
    return {
      folder:
        center(document.querySelector(".tree-folder .folder-icon")) -
        center(document.querySelector(".tree-folder .file-name")),
      chevron:
        center(document.querySelector(".tree-folder .tree-chevron svg")) -
        center(document.querySelector(".tree-folder .file-name")),
      file:
        center(document.querySelector(".file-row .file-type-icon")) -
        center(document.querySelector(".file-row .file-name")),
    };
  });
  expect(Math.abs(treeAlignment.folder)).toBeLessThanOrEqual(0.5);
  expect(Math.abs(treeAlignment.chevron)).toBeLessThanOrEqual(0.5);
  expect(Math.abs(treeAlignment.file)).toBeLessThanOrEqual(0.5);
  await expect(
    page.getByRole("button", { name: "Hide review tools" }),
  ).toHaveCSS("min-height", "28px");
  await expect(page.locator(".summary-row").first()).toHaveCSS(
    "font-size",
    "13px",
  );
  await expect(page.locator(".sidebar-tabs")).toHaveCSS("height", "36px");
  await expect(page.locator(".toolbar")).toHaveCSS("min-height", "36px");
  await expect(page.locator(".toolbar")).toHaveCSS("height", "36px");
  await expect(page.getByRole("button", { name: "View options" })).toHaveCSS(
    "height",
    "28px",
  );
  await expect(page.locator(".search-box")).toHaveCSS("height", "28px");
  await expect(page.getByRole("searchbox", { name: "Filter files" })).toHaveCSS(
    "font-size",
    "13px",
  );
  await expect(files.filter({ hasText: "value.ts" })).toHaveAttribute(
    "data-path",
    "src/value.ts",
  );
  await expect(files.filter({ hasText: "Badge.tsx" })).toHaveAttribute(
    "data-path",
    "src/components/Badge.tsx",
  );
  await expect(files.filter({ hasText: "legacy.md" })).toHaveAttribute(
    "data-path",
    "docs/legacy.md",
  );
  const treePaths = await files.evaluateAll((elements) =>
    elements.map((element) => element.getAttribute("data-path") ?? ""),
  );
  const headers = page.locator("#viewer [data-diffs-header]");
  await expect(headers).toHaveCount(treePaths.length);
  const diffHeaders = await headers.evaluateAll((elements) =>
    elements.map((element) => element.textContent ?? ""),
  );
  expect(diffHeaders).toHaveLength(treePaths.length);
  for (const [index, path] of treePaths.entries())
    expect(diffHeaders[index]).toContain(path);
  await expect(
    page.getByText("export const value = 2;", { exact: true }),
  ).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
  const treeFonts = await page.evaluate(() => {
    const folder = document.querySelector<HTMLElement>(".tree-folder");
    const file = document.querySelector<HTMLElement>(".file-row");
    const footer = document.querySelector<HTMLElement>(".main-footer");
    const container = document.querySelector("diffs-container");
    const code =
      container?.shadowRoot?.querySelector<HTMLElement>("[data-code]");
    if (!folder || !file || !footer || !code)
      throw new Error("Missing monospace surface");
    return {
      folder: getComputedStyle(folder).fontFamily,
      file: getComputedStyle(file).fontFamily,
      footer: getComputedStyle(footer).fontFamily,
      code: getComputedStyle(code).fontFamily,
      loaded: document.fonts.check('14px "JetBrains Mono Variable"'),
    };
  });
  expect(treeFonts.folder).toContain("JetBrains Mono Variable");
  expect(treeFonts.file).toContain("JetBrains Mono Variable");
  expect(treeFonts.footer).toContain("JetBrains Mono Variable");
  expect(treeFonts.code).toContain("JetBrains Mono Variable");
  expect(treeFonts.loaded).toBe(true);

  const openFolder = page.getByRole("button", {
    name: "Collapse folder docs",
  });
  await expect(openFolder).toHaveCSS("background-color", "rgba(0, 0, 0, 0)");
  await openFolder.hover();
  await expect(openFolder).not.toHaveCSS(
    "background-color",
    "rgba(0, 0, 0, 0)",
  );

  await page.getByRole("searchbox", { name: "Filter files" }).fill("Badge");
  await expect(files).toHaveCount(1);
  await expect(files).toHaveAttribute("data-path", "src/components/Badge.tsx");
});

test("file navigation scrolls to and retriggers a destination cue", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 420 });
  await page.goto("/");

  const file = page.locator(
    '#file-tree [data-path="src/components/Badge.tsx"]',
  );
  const target = page
    .locator("#viewer diffs-container")
    .filter({ hasText: "src/components/Badge.tsx" });
  const header = target.locator("[data-diffs-header]");
  await expect(header).toContainText("src/components/Badge.tsx");

  await file.click();
  await expect(file).toHaveAttribute("aria-current", "true");
  await expect(
    target.locator("[data-diffs-header][data-navigation-target]"),
  ).toHaveCount(1);
  await expect
    .poll(async () => {
      const [viewerBox, headerBox] = await Promise.all([
        page.locator("#viewer").boundingBox(),
        header.boundingBox(),
      ]);
      if (!viewerBox || !headerBox) return Number.POSITIVE_INFINITY;
      return Math.abs(headerBox.y - viewerBox.y);
    })
    .toBeLessThanOrEqual(1);

  await page.waitForTimeout(700);
  await file.click();
  await page.waitForTimeout(400);
  const cue = target.locator("[data-diffs-header][data-navigation-target]");
  await expect(cue).toHaveCount(1);
  await expect(cue).toHaveCount(0, { timeout: 1_000 });
});

test("file diffs have measured gaps and end dividers", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 4_000 });
  await page.goto("/");

  const containers = page.locator("#viewer diffs-container");
  await expect(containers).toHaveCount(12);
  await expect(containers.first().locator("[data-diffs-header]")).toHaveCSS(
    "height",
    "36px",
  );
  await expect(containers.first().locator(".review-button")).toHaveCSS(
    "height",
    "28px",
  );
  const gaps = await containers.evaluateAll((elements) =>
    elements.slice(0, -1).map((element, index) => {
      const next = elements[index + 1];
      if (!next) throw new Error("Missing adjacent diff");
      const currentBox = element.getBoundingClientRect();
      return next.getBoundingClientRect().top - currentBox.bottom;
    }),
  );
  expect(gaps).toHaveLength(11);
  expect(gaps.every((gap) => gap === 8)).toBe(true);

  const divider = await containers
    .first()
    .evaluate((element) => getComputedStyle(element).boxShadow);
  expect(divider).toContain("inset");
  expect(divider).toContain("-1px");
  const headerColors = await containers.first().evaluate((element) => {
    const root = element.shadowRoot;
    const header = root?.querySelector<HTMLElement>("[data-diffs-header]");
    if (!root || !header) throw new Error("Missing diff header");
    const probe = document.createElement("span");
    probe.style.background = "var(--diff-file-header)";
    root.append(probe);
    const panelProbe = document.createElement("span");
    panelProbe.style.background = "var(--panel)";
    document.body.append(panelProbe);
    const sidebar = document.querySelector("#sidebar");
    if (!sidebar) throw new Error("Missing sidebar");
    const colors = {
      header: getComputedStyle(header).backgroundColor,
      expected: getComputedStyle(probe).backgroundColor,
      gap: getComputedStyle(document.querySelector("#viewer") ?? element)
        .backgroundColor,
      sidebar: getComputedStyle(sidebar).backgroundColor,
      panel: getComputedStyle(panelProbe).backgroundColor,
    };
    probe.remove();
    panelProbe.remove();
    return colors;
  });
  expect(headerColors.header).toBe(headerColors.expected);
  expect(headerColors.header).not.toBe(headerColors.gap);
  expect(headerColors.sidebar).toBe(headerColors.panel);
});

test("reset viewed clears file review marks and disables when empty", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 4_000 });
  await page.goto("/");

  const reset = page.getByRole("button", { name: "Reset viewed" });
  await expect(reset).toBeDisabled();
  await expect(reset).toHaveAttribute("data-variant", "outline");
  await expect(reset).toHaveAttribute("data-size", "xs");
  await expect(reset).toHaveCSS("height", "24px");

  const viewed = page.locator("#viewer .review-button").last();
  const file = page.locator("#viewer diffs-container").last();
  const collapse = file.locator(".diff-collapse");
  await viewed.click();
  await expect(viewed).toHaveAttribute("aria-pressed", "true");
  await expect(collapse).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#review-count")).toHaveText("1 of 12 reviewed");
  await expect(reset).toBeEnabled();

  await viewed.click();
  await expect(viewed).toHaveAttribute("aria-pressed", "false");
  await expect(collapse).toHaveAttribute("aria-expanded", "true");
  await viewed.click();
  await expect(viewed).toHaveAttribute("aria-pressed", "true");
  const manuallyCollapsed = page.locator("#viewer .diff-collapse").first();
  await manuallyCollapsed.click();
  await expect(manuallyCollapsed).toHaveAttribute("aria-expanded", "false");

  await reset.click();
  await expect(viewed).toHaveAttribute("aria-pressed", "false");
  await expect(collapse).toHaveAttribute("aria-expanded", "true");
  await expect(manuallyCollapsed).toHaveAttribute("aria-expanded", "true");
  await expect(page.locator("#review-count")).toHaveText("0 of 12 reviewed");
  await expect(reset).toBeDisabled();
});

test("inline comments use a distinct structured surface while sidebar comments remain cards", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1280, height: 4_000 });
  await page.goto("/");
  await page.evaluate(async () => {
    const response = await fetch("/api/v1/diffs/current?scope=all");
    const data: unknown = await response.json();
    if (typeof data !== "object" || data === null)
      throw new Error("Bad fixture");
    const root = Reflect.get(data, "root");
    const files = Reflect.get(data, "files");
    if (typeof root !== "string" || !Array.isArray(files))
      throw new Error("Missing fixture metadata");
    const fingerprintFor = (path: string) => {
      const file = files.find(
        (entry) =>
          typeof entry === "object" &&
          entry !== null &&
          Reflect.get(entry, "path") === path,
      );
      const fingerprint =
        typeof file === "object" && file !== null
          ? Reflect.get(file, "fingerprint")
          : undefined;
      if (typeof fingerprint !== "string")
        throw new Error(`Missing fixture fingerprint for ${path}`);
      return fingerprint;
    };
    localStorage.setItem(
      `servediff:comments:${root}`,
      JSON.stringify([
        {
          id: "styled-comment",
          path: "src/value.ts",
          scope: "all",
          fingerprint: fingerprintFor("src/value.ts"),
          side: "additions",
          start: 3,
          end: 3,
          code: '  export const label = "servediff";',
          body: "Keep the exported value stable.",
          status: "open",
          createdAt: 1,
        },
        {
          id: "range-comment",
          path: "src/components/Badge.tsx",
          scope: "all",
          fingerprint: fingerprintFor("src/components/Badge.tsx"),
          side: "additions",
          start: 2,
          end: 4,
          code: "+ lines 2–4",
          body: "Review the full component body.",
          status: "open",
          createdAt: 2,
        },
        {
          id: "deletion-comment",
          path: "docs/legacy.md",
          scope: "all",
          fingerprint: fingerprintFor("docs/legacy.md"),
          side: "deletions",
          start: 3,
          end: 3,
          code: "- This documentation is no longer current.",
          body: "Confirm this removal.",
          status: "resolved",
          createdAt: 3,
        },
      ]),
    );
  });
  await page.reload();

  const inline = page.locator(
    '#viewer [data-comment-id="range-comment"] .comment-card',
  );
  await expect(inline).toBeVisible();
  await expect(inline).toHaveAttribute("data-expanded", "true");
  const openCommentToggle = inline.locator(".comment-collapse");
  await expect(openCommentToggle).toHaveAccessibleName(/Collapse comment/);
  await expect(openCommentToggle).toHaveAttribute("aria-expanded", "true");
  await expect(openCommentToggle.locator("svg")).toHaveClass(
    /lucide-chevron-down/,
  );
  await expect(openCommentToggle.locator("svg")).not.toHaveCSS(
    "transform",
    "none",
  );
  await openCommentToggle.click();
  await expect(inline).toHaveAttribute("data-expanded", "false");
  await expect(inline.locator(".comment-collapse-panel")).toBeHidden();
  await expect(openCommentToggle.locator("svg")).toHaveCSS("transform", "none");
  await inline.getByRole("button", { name: /Expand comment/ }).click();
  await expect(inline).toHaveAttribute("data-expanded", "true");
  await expect(inline.locator(".comment-collapse-panel")).toBeVisible();
  const highlights = await page
    .locator("diffs-container")
    .evaluateAll((containers) => {
      const inspect = (
        path: string,
        side: "additions" | "deletions",
        adjacentLine: number,
      ) => {
        const root = containers.find((container) =>
          container.shadowRoot
            ?.querySelector("[data-diffs-header]")
            ?.textContent?.includes(path),
        )?.shadowRoot;
        if (!root) throw new Error(`Missing rendered diff for ${path}`);
        const cells = Array.from(
          root.querySelectorAll<HTMLElement>(
            `[data-${side}] [data-commented-line]`,
          ),
        );
        return {
          lines: cells.map(
            (cell) =>
              cell.getAttribute("data-line") ??
              cell.getAttribute("data-column-number"),
          ),
          tones: cells.map((cell) => cell.getAttribute("data-commented-line")),
          highlighted: getComputedStyle(
            root.querySelector<HTMLElement>(
              `[data-${side}] [data-line][data-commented-line]`,
            ) ?? root.host,
          ).backgroundColor,
          adjacent: getComputedStyle(
            root.querySelector<HTMLElement>(
              `[data-${side}] [data-line="${adjacentLine}"]`,
            ) ?? root.host,
          ).backgroundColor,
        };
      };
      return {
        context: inspect("src/value.ts", "additions", 2),
        addition: inspect("src/components/Badge.tsx", "additions", 5),
        deletion: inspect("docs/legacy.md", "deletions", 1),
      };
    });
  expect(highlights.context.lines).toEqual(["3", "3"]);
  expect(highlights.context.tones).toEqual(["context", "context"]);
  expect(highlights.addition.lines).toEqual(["2", "3", "4", "2", "3", "4"]);
  expect(new Set(highlights.addition.tones)).toEqual(new Set(["addition"]));
  expect(highlights.deletion.lines).toEqual(["3", "3"]);
  expect(highlights.deletion.tones).toEqual(["deletion", "deletion"]);
  for (const highlight of Object.values(highlights))
    expect(highlight.highlighted).not.toBe(highlight.adjacent);
  const resolvedInline = page.locator(
    '#viewer [data-comment-id="deletion-comment"] .comment-card',
  );
  await expect(resolvedInline).toHaveAttribute("data-expanded", "false");
  await expect(resolvedInline.locator(".comment-collapse-panel")).toBeHidden();
  await expect(
    resolvedInline.getByRole("button", {
      name: /Expand resolved comment/,
    }),
  ).toHaveAttribute("aria-expanded", "false");
  await expect(inline).toHaveAttribute("data-slot", "card");
  await expect(inline).toHaveCSS("border-radius", "6px");
  await expect(inline).toHaveCSS("border-left-width", "1px");
  await expect(inline).toHaveCSS("border-right-width", "1px");
  await expect(inline).toHaveCSS("border-top-width", "1px");
  await expect(inline).toHaveCSS("border-bottom-width", "1px");
  await expect(inline.locator('[data-slot="card-header"]')).toHaveCount(1);
  await expect(inline.locator('[data-slot="card-content"]')).toHaveCount(1);
  await expect(inline.locator('[data-slot="card-footer"]')).toHaveCount(1);
  const headerAlignment = await inline.evaluate((element) => {
    const header = element.querySelector<HTMLElement>(".comment-card-header");
    const action = element.querySelector<HTMLElement>(
      '[data-slot="card-action"]',
    );
    if (!header || !action) throw new Error("Missing comment header regions");
    const headerBox = header.getBoundingClientRect();
    const actionBox = action.getBoundingClientRect();
    return {
      rightInset: headerBox.right - actionBox.right,
      actionMarginLeft: getComputedStyle(action).marginLeft,
      centerOffset:
        actionBox.top +
        actionBox.height / 2 -
        (headerBox.top + headerBox.height / 2),
    };
  });
  expect(headerAlignment.rightInset).toBeGreaterThanOrEqual(7);
  expect(headerAlignment.rightInset).toBeLessThanOrEqual(9);
  expect(headerAlignment.actionMarginLeft).not.toBe("0px");
  expect(Math.abs(headerAlignment.centerOffset)).toBeLessThanOrEqual(0.5);
  await expect(inline.locator(".comment-card-header")).toHaveCSS(
    "height",
    "36px",
  );
  await expect(inline.locator(".comment-actions")).toHaveCSS("height", "36px");
  await expect(inline.locator(".comment-state")).toHaveCSS("height", "28px");
  await expect(inline.locator(".comment-state")).toHaveCSS(
    "border-radius",
    "6px",
  );
  const surfaces = await inline.evaluate((element) => {
    const header = element.querySelector<HTMLElement>(".comment-card-header");
    const actions = element.querySelector<HTMLElement>(".comment-actions");
    if (!header || !actions) throw new Error("Missing comment regions");
    return {
      body: getComputedStyle(element).backgroundColor,
      header: getComputedStyle(header).backgroundColor,
      actions: getComputedStyle(actions).backgroundColor,
    };
  });
  expect(surfaces.header).not.toBe(surfaces.body);
  expect(surfaces.actions).not.toBe(surfaces.body);
  await expect(inline.locator(".comment-card-header svg")).toHaveCount(2);
  await expect(inline.locator(".comment-card-title span")).toHaveText(
    "· line 2–4",
  );
  await expect(inline.locator(".comment-actions svg")).toHaveCount(4);
  const commentActions = inline.locator(
    '.comment-actions [data-slot="button"]',
  );
  await expect(commentActions).toHaveCount(4);
  await expect(
    inline.getByRole("button", {
      name: "Copy comment at src/components/Badge.tsx:2–4",
    }),
  ).toBeVisible();
  expect(
    await commentActions.evaluateAll((buttons) =>
      buttons.map((button) => ({
        height: button.getBoundingClientRect().height,
        variant: button.getAttribute("data-variant"),
      })),
    ),
  ).toEqual([
    { height: 28, variant: "ghost" },
    { height: 28, variant: "ghost" },
    { height: 28, variant: "outline" },
    { height: 28, variant: "destructive" },
  ]);
  const resolve = inline.getByRole("button", {
    name: "Resolve",
    exact: true,
  });
  const resolveRestingBackground = await resolve.evaluate(
    (button) => getComputedStyle(button).backgroundColor,
  );
  await resolve.hover();
  const outlineHoverBackground = await resolve.evaluate(
    (button) => getComputedStyle(button).backgroundColor,
  );
  expect(outlineHoverBackground).not.toBe(resolveRestingBackground);

  await expect(inline).toHaveAttribute("data-one-sided", "");
  const oneSidedGeometry = await inline.evaluate((element) => {
    const parent = element.parentElement;
    if (!parent) throw new Error("Missing inline comment container");
    const card = element.getBoundingClientRect();
    const container = parent.getBoundingClientRect();
    return {
      width: card.width,
      left: card.left - container.left,
      right: container.right - card.right,
    };
  });
  expect(oneSidedGeometry.width).toBeLessThanOrEqual(680);
  expect(oneSidedGeometry.left).toBeGreaterThanOrEqual(23);
  expect(oneSidedGeometry.left).toBeLessThanOrEqual(25);
  expect(oneSidedGeometry.right).toBeGreaterThan(oneSidedGeometry.left);
  const pairedInline = page.locator(
    '#viewer [data-comment-id="styled-comment"] .comment-card',
  );
  await expect(pairedInline).toHaveAttribute("data-diff-layout", "split");
  await expect(pairedInline).not.toHaveAttribute("data-one-sided", "");
  const splitWidth = await pairedInline.evaluate(
    (element) => element.getBoundingClientRect().width,
  );
  expect(splitWidth).toBeLessThan(oneSidedGeometry.width);
  const unified = page.getByRole("button", { name: "Unified", exact: true });
  await unified.click();
  await expect(unified).toHaveAttribute("aria-pressed", "true");
  await expect(pairedInline).toHaveAttribute("data-diff-layout", "unified");
  const unifiedGeometry = await pairedInline.evaluate((element) => {
    const parent = element.parentElement;
    if (!parent) throw new Error("Missing inline comment container");
    const card = element.getBoundingClientRect();
    const container = parent.getBoundingClientRect();
    return {
      width: card.width,
      left: card.left - container.left,
      right: container.right - card.right,
    };
  });
  expect(unifiedGeometry.width).toBeLessThanOrEqual(680);
  expect(
    Math.abs(unifiedGeometry.width - oneSidedGeometry.width),
  ).toBeLessThanOrEqual(1);
  expect(unifiedGeometry.left).toBeGreaterThanOrEqual(23);
  expect(unifiedGeometry.left).toBeLessThanOrEqual(25);
  expect(unifiedGeometry.right).toBeGreaterThan(unifiedGeometry.left);
  const split = page.getByRole("button", { name: "Split", exact: true });
  await split.click();
  await expect(pairedInline).toHaveAttribute("data-diff-layout", "split");

  const viewed = page.locator("#viewer .review-button").first();
  await expect(viewed).toHaveCSS("height", "28px");
  await expect(viewed).toHaveAttribute("data-size", "sm");
  await expect(page.locator(".branch-badge")).toHaveCSS("height", "32px");
  await page.getByRole("tab", { name: /Review/ }).click();
  const sidebar = page.locator(
    '#comments-panel [data-comment-id="styled-comment"] .comment-card',
  );
  await expect(sidebar).toBeVisible();
  await expect(sidebar.locator(".comment-location")).toHaveText(
    "src/value.ts:3",
  );
  await expect(sidebar.locator(".comment-card-header")).toHaveCSS(
    "height",
    "32px",
  );
  await expect(sidebar.locator(".comment-actions")).toHaveCSS("height", "32px");
  await expect(sidebar.locator(".comment-state")).toHaveCSS("height", "24px");
  await page
    .locator(
      '#comments-panel [data-comment-id="range-comment"] .comment-location',
    )
    .click({ position: { x: 12, y: 12 } });
  const rangeTarget = page
    .locator("#viewer diffs-container")
    .filter({ hasText: "src/components/Badge.tsx" });
  const navigatedLines = rangeTarget.locator(
    "[data-additions] [data-line][data-comment-navigation-target]",
  );
  await expect(navigatedLines).toHaveCount(3);
  await expect(rangeTarget.locator("[data-diffs-header]")).not.toHaveAttribute(
    "data-navigation-target",
    "",
  );
  expect(
    await navigatedLines.evaluateAll((lines) =>
      lines.map((line) => line.getAttribute("data-line")),
    ),
  ).toEqual(["2", "3", "4"]);
  await expect(sidebar).toHaveAttribute("data-slot", "card");
  await expect(sidebar).toHaveCSS("border-radius", "12px");
  await expect(sidebar).toHaveCSS("border-left-width", "1px");
  await expect(sidebar).toHaveCSS("border-right-width", "1px");
  const [inlineBorder, sidebarBorder] = await Promise.all([
    inline.evaluate((element) => getComputedStyle(element).borderColor),
    sidebar.evaluate((element) => getComputedStyle(element).borderColor),
  ]);
  expect(inlineBorder).toBe(sidebarBorder);
  const sidebarActions = sidebar.locator(
    '.comment-actions [data-slot="button"]',
  );
  await expect(sidebarActions).toHaveCount(4);
  await expect(
    sidebar.getByRole("button", {
      name: "Copy comment at src/value.ts:3",
    }),
  ).toBeVisible();
  const sidebarActionMetrics = await sidebarActions.evaluateAll((buttons) =>
    buttons.map((button) => ({
      size: button.getAttribute("data-size"),
      height: button.getBoundingClientRect().height,
      top: button.getBoundingClientRect().top,
    })),
  );
  expect(sidebarActionMetrics.map(({ size }) => size)).toEqual([
    "icon-xs",
    "xs",
    "xs",
    "xs",
  ]);
  expect(sidebarActionMetrics.map(({ height }) => height)).toEqual([
    24, 24, 24, 24,
  ]);
  expect(
    Math.max(...sidebarActionMetrics.map(({ top }) => top)) -
      Math.min(...sidebarActionMetrics.map(({ top }) => top)),
  ).toBeLessThanOrEqual(1);

  const copyReview = page.locator("#copy-review");
  await expect(copyReview).toHaveAttribute("data-size", "sm");
  await expect(copyReview).toHaveAttribute("data-variant", "default");
  await expect(copyReview).toHaveCSS("height", "28px");
  const [copyBackground, sidebarBackground] = await Promise.all([
    copyReview.evaluate((button) => getComputedStyle(button).backgroundColor),
    page
      .locator("#sidebar")
      .evaluate((sidebar) => getComputedStyle(sidebar).backgroundColor),
  ]);
  expect(copyBackground).not.toBe(sidebarBackground);
  const copyRestingBackground = await copyReview.evaluate(
    (button) => getComputedStyle(button).backgroundColor,
  );
  await copyReview.hover();
  const copyHoverBackground = await copyReview.evaluate(
    (button) => getComputedStyle(button).backgroundColor,
  );
  expect(copyHoverBackground).not.toBe(copyRestingBackground);
});

test("theme picker follows system and persists explicit choices", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.addInitScript(() => localStorage.setItem("theme", "light"));
  await page.goto("/");

  const theme = page.getByRole("button", { name: "Theme: System" });
  await expect(theme).toBeVisible();
  await expect(theme.locator("svg")).toHaveClass(/lucide-monitor/);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("theme-v2")))
    .toBe("system");

  await theme.click();
  await page.getByRole("menuitemradio", { name: "Light" }).click();
  await expect(
    page.getByRole("button", { name: "Theme: Light" }).locator("svg"),
  ).toHaveClass(/lucide-sun/);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("theme-v2")))
    .toBe("light");

  await page.getByRole("button", { name: "Theme: Light" }).click();
  await page.getByRole("menuitemradio", { name: "System" }).click();
  await expect(
    page.getByRole("button", { name: "Theme: System" }).locator("svg"),
  ).toHaveClass(/lucide-monitor/);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.emulateMedia({ colorScheme: "light" });
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");

  await page.getByRole("button", { name: "Theme: System" }).click();
  await page.getByRole("menuitemradio", { name: "Dark" }).click();
  await page.reload();
  const darkTheme = page.getByRole("button", { name: "Theme: Dark" });
  await expect(darkTheme).toBeVisible();
  await expect(darkTheme.locator("svg")).toHaveClass(/lucide-moon/);
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});

test("display controls expose state and persist preferences", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Piped diff" })).toBeVisible();

  await page.getByRole("tab", { name: /Review/ }).click();
  await expect(page.getByRole("tab", { name: /Review/ })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.locator("#comments-panel")).toBeVisible();
  await expect(page.locator("#file-panel")).toBeHidden();
  await page.getByRole("tab", { name: /Changes/ }).click();

  const viewOptions = page.getByRole("button", { name: "View options" });
  await viewOptions.click();
  const wrap = page.getByRole("menuitemcheckbox", { name: "Wrap lines" });
  await expect(wrap).toHaveAttribute("aria-checked", "false");
  await wrap.click();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("wrap")))
    .toBe("true");
  await page.keyboard.press("Escape");

  const unified = page.getByRole("button", {
    name: "Unified",
    exact: true,
  });
  await unified.click();
  await expect(unified).toHaveAttribute("aria-pressed", "true");
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("layout")))
    .toBe("unified");

  await viewOptions.click();
  await page.getByRole("menuitemradio", { name: "Characters" }).click();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("line-diff-type")))
    .toBe("char");
  await page.keyboard.press("Escape");

  await viewOptions.click();
  await page.getByRole("menuitemradio", { name: "GitHub" }).click();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("diff-theme")))
    .toBe("github");
});

test("header icons and sidebar tab indicator use design-system sizes", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page.locator(".brand")).toHaveText("servediff");
  await expect(page.locator('img[src="/logo.png"]')).toHaveCount(0);

  const styles = await page.evaluate(() => {
    const size = (selector: string) => {
      const element = document.querySelector<SVGElement>(selector);
      if (!element) throw new Error(`Missing ${selector}`);
      return getComputedStyle(element).width;
    };
    const tab = document.querySelector<HTMLElement>("#files-tab");
    if (!tab) throw new Error("Missing Changes tab");
    const topbar = document.querySelector<HTMLElement>(".topbar");
    const divider = document.querySelector<HTMLElement>(".header-divider");
    if (!topbar || !divider) throw new Error("Missing header divider");
    const topbarBox = topbar.getBoundingClientRect();
    const dividerBox = divider.getBoundingClientRect();
    const indicator = getComputedStyle(tab, "::after");
    const colorProbe = document.createElement("span");
    colorProbe.style.color = "var(--accent)";
    document.body.append(colorProbe);
    const accent = getComputedStyle(colorProbe).color;
    colorProbe.remove();
    return {
      sidebar: size("#sidebar-toggle svg"),
      branch: size(".branch-badge svg"),
      refresh: size("#refresh svg"),
      theme: size("#theme svg"),
      tabBorderWidth: getComputedStyle(tab).borderBottomWidth,
      indicatorColor: indicator.backgroundColor,
      indicatorOpacity: indicator.opacity,
      dividerCenterOffset: Math.abs(
        topbarBox.top +
          topbarBox.height / 2 -
          (dividerBox.top + dividerBox.height / 2),
      ),
      dividerHeight: dividerBox.height,
      accent,
    };
  });

  expect(styles).toMatchObject({
    sidebar: "20px",
    branch: "16px",
    refresh: "16px",
    theme: "20px",
    tabBorderWidth: "1px",
    indicatorOpacity: "1",
    dividerHeight: 24,
  });
  expect(styles.dividerCenterOffset).toBeLessThanOrEqual(0.5);
  expect(styles.indicatorColor).toBe(styles.accent);
});

test("sidebar resizer supports pointer dragging", async ({ page }) => {
  await page.goto("/");
  const resizer = page.getByRole("separator", {
    name: "Resize file sidebar",
  });
  await expect(resizer).toBeVisible();
  const box = await resizer.boundingBox();
  expect(box?.height ?? 0).toBeGreaterThan(100);
  const before = Number(await resizer.getAttribute("aria-valuenow"));
  if (!box) throw new Error("Missing sidebar resizer bounds");
  await page.mouse.move(box.x + box.width / 2, box.y + 20);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 40, box.y + 20);
  await page.mouse.up();
  await expect
    .poll(async () => Number(await resizer.getAttribute("aria-valuenow")))
    .toBeGreaterThan(before);
});

test("fallback copy dialog traps and restores focus", async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(Navigator.prototype, "clipboard", {
      configurable: true,
      get: () => ({
        writeText: () => Promise.reject(new Error("Clipboard unavailable")),
      }),
    });
  });
  await page.goto("/");
  await expect(page.locator("#connection")).toHaveText("Fixed snapshot");
  await page.evaluate(async () => {
    const response = await fetch("/api/v1/diffs/current?scope=all");
    const data: unknown = await response.json();
    if (typeof data !== "object" || data === null)
      throw new Error("Bad fixture");
    const root = Reflect.get(data, "root");
    const files = Reflect.get(data, "files");
    if (typeof root !== "string" || !Array.isArray(files))
      throw new Error("Missing fixture metadata");
    const value = files.find(
      (entry) =>
        typeof entry === "object" &&
        entry !== null &&
        Reflect.get(entry, "path") === "src/value.ts",
    );
    const fingerprint =
      typeof value === "object" && value !== null
        ? Reflect.get(value, "fingerprint")
        : undefined;
    if (typeof fingerprint !== "string")
      throw new Error("Missing fixture fingerprint");
    localStorage.setItem(
      `servediff:comments:${root}`,
      JSON.stringify([
        {
          id: "browser-current",
          path: "src/value.ts",
          scope: "all",
          fingerprint,
          side: "additions",
          start: 1,
          end: 1,
          code: "+ export const value = 2;",
          body: "Keep the current value stable.",
          status: "open",
          createdAt: 2,
        },
        {
          id: "browser-test",
          path: "src/value.ts",
          scope: "all",
          fingerprint: "earlier",
          side: "additions",
          start: 1,
          end: 1,
          code: "+ export const value = 2;",
          body: "Keep the exported value stable.",
          status: "open",
          createdAt: 1,
        },
      ]),
    );
  });
  await page.reload();

  await page.getByRole("tab", { name: /Review/ }).click();
  await page.getByRole("button", { name: "Filter comments: Open" }).click();
  await page.getByRole("menuitemradio", { name: "Stale 1" }).click();
  await page.getByText(/Earlier review 1/).click();
  const card = page.locator(
    '#comments-panel [data-comment-id="browser-test"] .comment-card',
  );
  await expect(card).toHaveAttribute("data-expanded", "false");
  await expect(card.getByText("Stale", { exact: true })).toBeVisible();
  await expect(card.locator(".comment-state")).toHaveAttribute(
    "title",
    "Lifecycle: open; applicability: stale",
  );
  const expand = card.locator(".comment-collapse");
  await expect(expand).toHaveAccessibleName(/Expand stale comment/);
  await expand.click();
  const resolve = card.getByRole("button", { name: "Resolve", exact: true });
  await resolve.click();
  await expect(card).toHaveAttribute("data-expanded", "false");
  await expect(card.locator(".comment-collapse-panel")).toBeHidden();
  await expect(card.getByText("Stale", { exact: true })).toBeVisible();
  await expect(card.locator(".comment-state")).toHaveAttribute(
    "title",
    "Lifecycle: resolved; applicability: stale",
  );
  await expect(expand).toHaveAttribute("aria-expanded", "false");
  await expand.click();
  await expect(expand).toHaveAttribute("aria-expanded", "true");
  const reopen = card.getByRole("button", { name: "Reopen", exact: true });
  await expect(reopen).toBeVisible();
  await reopen.click();
  await expect(card.locator(".comment-state")).toHaveAttribute(
    "title",
    "Lifecycle: open; applicability: stale",
  );

  const exportQueries: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.pathname === "/api/v1/comments/export")
      exportQueries.push(url.search);
  });
  await expect(card).toHaveAttribute("data-expanded", "false");
  await expand.click();
  const copyComment = card.getByRole("button", {
    name: "Copy comment at src/value.ts:1",
  });
  await copyComment.click();
  const dialog = page.getByRole("dialog", { name: "Copy review comments" });
  const output = page.getByRole("textbox", { name: "Comments XML" });
  await expect(dialog).toBeVisible();
  let query = new URLSearchParams(exportQueries.at(-1) ?? "");
  expect(query.get("includeResolved")).toBe("true");
  expect(query.get("commentId")).toBe("browser-test");
  await expect(output).toHaveValue(/status="open" applicability="stale"/);
  await expect(output).not.toHaveValue(/Keep the current value stable/);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(copyComment).toBeFocused();

  const copyReview = page.getByRole("button", { name: "Copy review" });
  await copyReview.click();
  await expect(dialog).toBeVisible();
  query = new URLSearchParams(exportQueries.at(-1) ?? "");
  expect(query.get("includeResolved")).toBe("false");
  expect(query.get("commentId")).toBeNull();
  expect(query.get("revision")).toBeNull();
  expect(query.get("scope")).toBeNull();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(copyReview).toBeFocused();

  const copyOptions = page.getByRole("button", { name: "Copy options" });
  await copyOptions.click();
  await page.getByRole("menuitem", { name: "All" }).click();
  await expect(dialog).toBeVisible();
  query = new URLSearchParams(exportQueries.at(-1) ?? "");
  expect(query.get("includeResolved")).toBe("true");
  expect(query.get("commentId")).toBeNull();
  expect(query.get("revision")).toBeNull();
  expect(query.get("scope")).toBeNull();
  await expect(output).toBeFocused();
  await expect(output).toHaveValue(/status="open" applicability="stale"/);
  await expect(output).toHaveValue(/status="open" applicability="anchored"/);
  await expect
    .poll(() =>
      output.evaluate((element) => {
        if (!(element instanceof HTMLTextAreaElement)) return false;
        return (
          element.selectionStart === 0 &&
          element.selectionEnd === element.value.length
        );
      }),
    )
    .toBe(true);
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(copyOptions).toBeFocused();
});

test("mobile sidebar preserves accessible touch targets", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  const toggle = page.getByRole("button", { name: "Show file sidebar" });
  await expect(toggle).toBeVisible();
  const box = await toggle.boundingBox();
  await toggle.click();
  await expect(page.locator("#sidebar")).toBeVisible();
  expect(box?.width ?? 0).toBeGreaterThanOrEqual(44);
  expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
  const fileBox = await page
    .locator("#file-tree .file-row")
    .first()
    .boundingBox();
  const folderBox = await page
    .locator("#file-tree .tree-folder")
    .first()
    .boundingBox();
  const reviewToolsBox = await page
    .getByRole("button", { name: "Hide review tools" })
    .boundingBox();
  const resetBox = await page
    .getByRole("button", { name: "Reset viewed" })
    .boundingBox();
  const searchBox = await page.locator(".search-box").boundingBox();
  const filesTabBox = await page
    .getByRole("tab", { name: /Changes/ })
    .boundingBox();
  const commentsTabBox = await page
    .getByRole("tab", { name: /Review/ })
    .boundingBox();
  const toolbarBox = await page.locator(".toolbar").boundingBox();
  const viewOptionsBox = await page
    .getByRole("button", { name: "View options" })
    .boundingBox();
  expect(fileBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(folderBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(reviewToolsBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(resetBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(searchBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(filesTabBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(commentsTabBox?.height ?? 0).toBeGreaterThanOrEqual(44);
  expect(toolbarBox?.height ?? 0).toBeGreaterThanOrEqual(52);
  expect(viewOptionsBox?.width ?? 0).toBeGreaterThanOrEqual(44);
  expect(viewOptionsBox?.height ?? 0).toBeGreaterThanOrEqual(44);
});
