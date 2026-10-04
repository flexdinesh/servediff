import { expect, test } from "@playwright/test";
import { resetFixtureState } from "./fixture-state.ts";

test.use({ launchOptions: { ignoreDefaultArgs: ["--hide-scrollbars"] } });
test.beforeEach(async ({ request }) => resetFixtureState(request));

test("diff tracks fill frames and headers align with their gutters", async ({
  page,
}) => {
  const colorSchemes: ("light" | "dark")[] = ["light", "dark"];
  for (const colorScheme of colorSchemes) {
    await page.emulateMedia({ colorScheme });
    await page.setViewportSize({ width: 1440, height: 4_000 });
    await page.goto("/");
    const files = page.locator("#viewer diffs-container");
    await expect(files).toHaveCount(12);
    await expect
      .poll(() =>
        files.evaluateAll((elements) =>
          elements.slice(0, 4).every((element) => {
            const code =
              element.shadowRoot?.querySelector<HTMLElement>("[data-code]");
            const last =
              code?.querySelector("[data-content]")?.lastElementChild;
            if (!code || !last) return false;
            const trackHeight = code.offsetHeight - code.clientHeight;
            return (
              trackHeight > 0 &&
              getComputedStyle(code).backgroundColor ===
                getComputedStyle(last).backgroundColor &&
              Math.abs(
                element.getBoundingClientRect().bottom -
                  last.getBoundingClientRect().bottom -
                  trackHeight,
              ) <= 0.5
            );
          }),
        ),
      )
      .toBe(true);
    await expect
      .poll(() =>
        files.evaluateAll((elements) => {
          const first = elements[0];
          const next = elements[1];
          const scaffold = first?.parentElement?.parentElement;
          if (!scaffold || !first || !next)
            throw new Error("Missing virtual scroll scaffold");
          const gap =
            next.getBoundingClientRect().top -
            first.getBoundingClientRect().bottom;
          const measured = elements.reduce(
            (height, element) =>
              height + element.getBoundingClientRect().height,
            (elements.length - 1) * gap,
          );
          return Math.abs(scaffold.getBoundingClientRect().height - measured);
        }),
      )
      .toBeLessThanOrEqual(0.5);

    const longDiff = files.filter({ hasText: "src/components/Button.tsx" });
    const code = longDiff.locator("[data-code][data-additions]");
    await code.evaluate((element) => {
      element.scrollLeft = 50;
    });
    await expect
      .poll(() => code.evaluate((element) => element.scrollLeft))
      .toBeGreaterThan(0);

    for (const viewport of [
      { width: 1440, height: 1000 },
      { width: 390, height: 844 },
    ]) {
      await page.setViewportSize(viewport);
      await expect
        .poll(() =>
          files.first().evaluate((element) => {
            const theme = document.querySelector("#theme");
            const toggle = document.querySelector("#toggle-all-files");
            if (!theme || !toggle) throw new Error("Missing shell actions");
            const edge = element.getBoundingClientRect().right;
            return Math.max(
              Math.abs(theme.getBoundingClientRect().right - edge),
              Math.abs(toggle.getBoundingClientRect().right - edge),
            );
          }),
        )
        .toBeLessThanOrEqual(0.5);
    }
    const header = await page.locator(".topbar").boundingBox();
    const toolbar = await page.locator(".toolbar").boundingBox();
    const theme = await page.locator("#theme").boundingBox();
    expect(toolbar?.height).toBe(header?.height);
    for (const id of ["#view-options", "#toggle-all-files"]) {
      const button = await page.locator(id).boundingBox();
      expect(button?.width).toBe(theme?.width);
      expect(button?.height).toBe(theme?.height);
    }
  }
});
