import { expect, test } from "./fixtures/mock-hub";
import {
  expectInsideViewport,
  expectNoHorizontalOverflow,
  expectSingleLineButtons,
} from "./fixtures/assertions";

test.describe("Mobile narrow viewport (390x844)", () => {
  for (const theme of ["light", "dark"] as const) {
    test(`keeps header, composer, and sidebar drawer within 390x844 viewport (${theme})`, async ({ openApp }) => {
      const page = await openApp({
        theme,
        surfaceStyle: "default",
        desktopBridge: false,
        viewport: { width: 390, height: 844 },
      });

      await expectNoHorizontalOverflow(page);

      const header = page.locator("[data-app-shell] > main header").first();
      await expectSingleLineButtons(header);

      const textarea = page.locator("[data-app-shell] > main textarea").first();
      await expectInsideViewport(textarea);

      const sidebarToggle = header.getByRole("button", { name: /^Sidebar/ }).first();
      await expect(sidebarToggle).toBeVisible();
      await sidebarToggle.click();

      const drawer = page.locator('[data-sidebar-column][role="dialog"][aria-modal="true"]');
      await expect(drawer).toBeVisible();
      await expectInsideViewport(drawer);

      // Click scrim on the right side outside the 284 px drawer to dismiss cleanly.
      await page.mouse.click(360, 200);
      await expect(page.locator('[data-sidebar-column][aria-modal="true"]')).toHaveCount(0);
      await expectNoHorizontalOverflow(page);
    });
  }
});
