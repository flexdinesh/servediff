import { expect, test, type Page } from "@playwright/test";
import { resetFixtureState } from "./fixture-state.ts";

test.beforeEach(async ({ request }) => resetFixtureState(request));

async function seedReviewComments(page: Page) {
  await page.evaluate(async () => {
    const response = await fetch("/api/v1/diffs/current?scope=all");
    const data: unknown = await response.json();
    if (typeof data !== "object" || data === null)
      throw new Error("Bad fixture");
    const root = Reflect.get(data, "root");
    const files = Reflect.get(data, "files");
    if (typeof root !== "string" || !Array.isArray(files))
      throw new Error("Missing fixture metadata");
    const file = files.find(
      (entry) =>
        typeof entry === "object" &&
        entry !== null &&
        Reflect.get(entry, "path") === "src/value.ts",
    );
    const fingerprint =
      typeof file === "object" && file !== null
        ? Reflect.get(file, "fingerprint")
        : undefined;
    if (typeof fingerprint !== "string")
      throw new Error("Missing fixture fingerprint");
    localStorage.setItem(
      `servediff:comments:${root}`,
      JSON.stringify([
        {
          id: "open-review-comment",
          path: "src/value.ts",
          scope: "all",
          fingerprint,
          side: "additions",
          start: 3,
          end: 3,
          code: '+ export const label = "servediff";',
          body: "Open feedback",
          status: "open",
          createdAt: 1,
        },
        {
          id: "resolved-review-comment",
          path: "src/value.ts",
          scope: "all",
          fingerprint,
          side: "additions",
          start: 3,
          end: 3,
          code: '+ export const label = "servediff";',
          body: "Resolved feedback",
          status: "resolved",
          createdAt: 2,
        },
      ]),
    );
  });
  await page.reload();
}

test("narrow screens use unified layout without replacing the desktop preference", async ({
  page,
}) => {
  await page.setViewportSize({ width: 767, height: 844 });
  await page.addInitScript(() => localStorage.setItem("layout", "split"));
  await page.goto("/");

  await page.getByRole("button", { name: "View options" }).click();
  await expect(
    page.getByRole("menuitemradio", { name: "Unified", exact: true }),
  ).toHaveAttribute("aria-checked", "true");
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("layout")))
    .toBe("split");
  await page.keyboard.press("Escape");

  await page.setViewportSize({ width: 1280, height: 844 });
  await expect(
    page.getByRole("button", { name: "Split", exact: true }),
  ).toHaveAttribute("aria-pressed", "true");
});

test("view options expose secondary display settings and persist them", async ({
  page,
}) => {
  await page.goto("/");

  await page.getByRole("button", { name: "View options" }).click();
  const menu = page.getByRole("menu", { name: "View options" });
  const wrap = menu.getByRole("menuitemcheckbox", { name: "Wrap lines" });
  await expect(wrap).toHaveAttribute("aria-checked", "false");
  await expect(
    menu.getByRole("menuitemradio", { name: "Words", exact: true }),
  ).toHaveAttribute("aria-checked", "true");
  await expect(
    menu.getByRole("menuitemradio", { name: "Pierre" }),
  ).toHaveAttribute("aria-checked", "true");

  await wrap.click();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem("wrap")))
    .toBe("true");
});

test("mobile sidebar behaves as a modal drawer and restores focus", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");

  const trigger = page.getByRole("button", { name: "Show file sidebar" });
  await trigger.click();

  const drawer = page.getByRole("dialog", { name: "Review navigation" });
  await expect(drawer).toBeVisible();
  await expect(drawer).toHaveAttribute("aria-modal", "true");
  await expect(page.locator("main")).toHaveAttribute("inert", "");
  await expect(page.locator(".topbar")).toHaveAttribute("inert", "");
  await expect(drawer.locator(":focus")).toHaveCount(1);
  await expect(page.locator(".sidebar-backdrop")).toBeVisible();

  await page
    .locator(".sidebar-backdrop")
    .click({ position: { x: 380, y: 800 } });
  await expect(drawer).toBeHidden();
  await expect(trigger).toBeFocused();
  await expect(page.locator("main")).not.toHaveAttribute("inert", "");
  await expect(page.locator(".topbar")).not.toHaveAttribute("inert", "");
});

test("review actions use progressive disclosure, status filters, and confirmed deletion", async ({
  page,
}) => {
  await page.goto("/");
  await seedReviewComments(page);

  await expect(page.getByRole("button", { name: "Copy review" })).toBeVisible();
  await page.getByRole("button", { name: "Copy options" }).click();
  const options = page.getByRole("menu", { name: "Copy options" });
  await expect(options.getByRole("menuitem", { name: "Open" })).toBeVisible();
  await expect(options.getByRole("menuitem", { name: "All" })).toBeVisible();
  await expect(options.getByRole("menuitem")).toHaveCount(2);
  await page.keyboard.press("Escape");

  await page.getByRole("tab", { name: /Review/ }).click();
  const panel = page.locator("#comments-panel");
  await expect(panel.locator(".comment-summary")).toHaveCount(0);
  const filterTrigger = panel.locator(".comment-filter-trigger");
  await expect(filterTrigger).toHaveAccessibleName("Filter comments: Open");
  await expect(panel.locator(".comment-panel-header")).toHaveCSS(
    "justify-content",
    "flex-start",
  );
  await expect(filterTrigger.locator(".comment-filter-count")).toHaveText("1");
  await expect(filterTrigger.locator(".comment-filter-count")).toHaveCSS(
    "height",
    "16px",
  );
  await filterTrigger.click();
  const filters = page.getByRole("menu", { name: "Filter comments" });
  const openFilter = filters.getByRole("menuitemradio", { name: "Open 1" });
  const resolvedFilter = filters.getByRole("menuitemradio", {
    name: "Resolved 1",
  });
  const staleFilter = filters.getByRole("menuitemradio", { name: "Stale 0" });
  await expect(openFilter.locator(".comment-filter-count")).toHaveText("1");
  await expect(resolvedFilter.locator(".comment-filter-count")).toHaveText("1");
  await expect(staleFilter.locator(".comment-filter-count")).toHaveText("0");
  await expect(
    filters.getByRole("menuitemradio", { name: "All 2" }),
  ).toBeVisible();
  const filterMenuBox = await filters.boundingBox();
  expect(filterMenuBox?.width ?? Number.POSITIVE_INFINITY).toBeLessThanOrEqual(
    120,
  );
  await expect(openFilter).toHaveAttribute("aria-checked", "true");
  const selectedOptionAlignment = await openFilter.evaluate((option) => {
    const indicator = option.querySelector(
      '[data-slot="dropdown-menu-radio-item-indicator"]',
    );
    const label = Array.from(option.children).find(
      (child) => child.textContent === "Open",
    );
    if (!indicator || !label) throw new Error("Missing filter option content");
    return {
      indicatorRight: indicator.getBoundingClientRect().right,
      labelLeft: label.getBoundingClientRect().left,
    };
  });
  expect(
    selectedOptionAlignment.labelLeft - selectedOptionAlignment.indicatorRight,
  ).toBeGreaterThanOrEqual(4);
  await page.keyboard.press("Escape");
  await expect(panel.getByText("Open feedback")).toBeVisible();
  await expect(panel.getByText("Resolved feedback")).toBeHidden();

  await filterTrigger.click();
  await resolvedFilter.click();
  await expect(filterTrigger).toHaveAccessibleName("Filter comments: Resolved");
  await expect(panel.getByText("Open feedback")).toBeHidden();
  const resolvedCard = panel.locator(
    '[data-comment-id="resolved-review-comment"]',
  );
  await resolvedCard
    .getByRole("button", { name: /Expand resolved comment/ })
    .click();
  await resolvedCard
    .getByRole("button", { name: "Delete", exact: true })
    .click();

  const dialog = page.getByRole("dialog", { name: "Delete comment?" });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  await expect(resolvedCard).toBeVisible();
  await resolvedCard
    .getByRole("button", { name: "Delete", exact: true })
    .click();
  await page
    .getByRole("dialog", { name: "Delete comment?" })
    .getByRole("button", { name: "Delete comment" })
    .click();
  await expect(resolvedCard).toHaveCount(0);
  await expect(filterTrigger.locator(".comment-filter-count")).toHaveText("0");

  await filterTrigger.click();
  await openFilter.click();
  const openCard = panel.locator('[data-comment-id="open-review-comment"]');
  await openCard.getByRole("button", { name: "Edit", exact: true }).click();
  const editor = page.getByRole("textbox", { name: "Review comment" });
  await editor.fill("");
  const save = page.getByRole("button", { name: "Save comment" });
  await expect(save).toBeEnabled();
  await save.click();
  await expect(editor).toBeFocused();
  await expect(editor).toHaveAttribute("aria-invalid", "true");
  await expect(
    page.getByRole("alert").getByText("Enter a comment before saving."),
  ).toBeVisible();
});

test("bulk comment deletion selects a status and defaults to all", async ({
  page,
}) => {
  await page.goto("/");
  await seedReviewComments(page);
  await page.getByRole("tab", { name: /Review/ }).click();

  const panel = page.locator("#comments-panel");
  const filter = panel.locator(".comment-filter-trigger");
  const remove = panel.locator(".bulk-delete-primary");
  const removeOptions = panel.getByRole("button", { name: "Delete options" });
  await expect(remove).toHaveAttribute("data-variant", "destructive");
  await expect(remove).toHaveCSS("height", "24px");
  const controls = await panel.locator(".comment-panel-header").evaluate(() => {
    const filter = document.querySelector<HTMLElement>(
      "#comments-panel .comment-filter-trigger",
    );
    const remove = document.querySelector<HTMLElement>(
      "#comments-panel .bulk-delete-actions",
    );
    if (!filter || !remove) throw new Error("Missing review controls");
    const header = filter.parentElement;
    if (!header) throw new Error("Missing review controls header");
    return {
      marginLeft: getComputedStyle(remove).marginLeft,
      rightInset:
        header.getBoundingClientRect().right -
        remove.getBoundingClientRect().right,
    };
  });
  expect(controls.marginLeft).not.toBe("0px");
  expect(Math.abs(controls.rightInset)).toBeLessThanOrEqual(0.5);

  await removeOptions.click();
  const menu = page.getByRole("menu", { name: "Delete options" });
  await expect(menu.getByRole("menuitem", { name: "All 2" })).toBeVisible();
  await expect(menu.getByRole("menuitem", { name: "Open 1" })).toBeVisible();
  await menu.getByRole("menuitem", { name: "Resolved 1" }).click();
  const resolvedDialog = page.getByRole("dialog", {
    name: "Delete resolved comments?",
  });
  await expect(resolvedDialog).toContainText(
    "This permanently removes 1 resolved comment.",
  );
  await resolvedDialog.getByRole("button", { name: "Delete comments" }).click();
  await expect(filter.locator(".comment-filter-count")).toHaveText("1");

  await removeOptions.click();
  await expect(
    menu.getByRole("menuitem", { name: "Resolved 0" }),
  ).toHaveAttribute("data-disabled", "");
  await expect(menu.getByRole("menuitem", { name: "Stale 0" })).toHaveAttribute(
    "data-disabled",
    "",
  );
  await page.keyboard.press("Escape");

  await remove.click();
  const allDialog = page.getByRole("dialog", {
    name: "Delete all comments?",
  });
  await expect(allDialog).toContainText("This permanently removes 1 comment.");
  await allDialog.getByRole("button", { name: "Delete comments" }).click();
  await expect(page.locator("#comment-count")).toHaveText("0");
  await expect(panel.locator(".comment-panel-header")).toBeEmpty();
});

test("light and dark themes retain readable text and visible control boundaries", async ({
  page,
}) => {
  await page.goto("/");

  for (const theme of ["light", "dark"]) {
    await page.locator("html").evaluate((element, value) => {
      element.dataset.theme = value;
    }, theme);
    const contrast = await page.evaluate(() => {
      const rgb = (value: string) => {
        const channels = value
          .match(/[\d.]+/g)
          ?.slice(0, 3)
          .map(Number);
        if (!channels || channels.length !== 3)
          throw new Error(`Unsupported color: ${value}`);
        return channels;
      };
      const luminance = (channels: number[]) => {
        const values = channels.map((channel) => {
          const normalized = channel / 255;
          return normalized <= 0.04045
            ? normalized / 12.92
            : ((normalized + 0.055) / 1.055) ** 2.4;
        });
        const red = values[0];
        const green = values[1];
        const blue = values[2];
        if (red === undefined || green === undefined || blue === undefined)
          throw new Error("Missing RGB channel");
        return red * 0.2126 + green * 0.7152 + blue * 0.0722;
      };
      const ratio = (first: string, second: string) => {
        const values = [luminance(rgb(first)), luminance(rgb(second))].sort(
          (left, right) => right - left,
        );
        const lighter = values[0];
        const darker = values[1];
        if (lighter === undefined || darker === undefined)
          throw new Error("Missing luminance");
        return (lighter + 0.05) / (darker + 0.05);
      };
      const root = getComputedStyle(document.documentElement);
      const probe = document.createElement("span");
      probe.style.color = "var(--border-control)";
      document.body.append(probe);
      const controlBorder = getComputedStyle(probe).color;
      probe.remove();
      return {
        text: ratio(root.color, root.backgroundColor),
        control: ratio(controlBorder, root.backgroundColor),
      };
    });
    expect(contrast.text).toBeGreaterThanOrEqual(4.5);
    expect(contrast.control).toBeGreaterThanOrEqual(3);
  }
});
