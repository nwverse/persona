import { expect, test } from "./fixtures/mock-hub";
import {
  expectInsideViewport,
  expectNoActiveBackdropFilter,
  expectNoHorizontalOverflow,
  expectSingleLineButtons,
} from "./fixtures/assertions";

test.describe("Inbox sticky group headers & toolbar wrapping", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`renders opaque bg-page/95 sticky group headers with no horizontal overflow (${theme})`, async ({ openApp }) => {
      const page = await openApp({
        theme,
        surfaceStyle: "liquid-glass",
        desktopBridge: true,
        path: "/inbox",
      });

      const bar = page.locator("[data-inbox-bar]");
      await expect(bar).toBeVisible();

      const stickyHeader = page.locator("[data-group] > .sticky").first();
      await expect(stickyHeader).toBeVisible();

      const classes = await stickyHeader.getAttribute("class");
      expect(classes).toContain("bg-page/95");
      expect(classes).not.toContain("bg-canvas/95");

      await expectInsideViewport(stickyHeader);
      await expectNoActiveBackdropFilter(page);
      await expectNoHorizontalOverflow(page, '[aria-label="Inbox list"], [data-app-shell] > main');
      await expectSingleLineButtons(page.locator("[data-app-shell] > main header").first());
    });
  }
});
