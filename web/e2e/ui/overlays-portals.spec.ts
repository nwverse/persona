import { expect, openSplitPanelAgent, PNG_1X1, test } from "./fixtures/mock-hub";
import { expectInsideViewport, expectNoActiveBackdropFilter } from "./fixtures/assertions";

test.describe("Overlay & portal containment under Frosted Glass", () => {
  test("portals Palette, New Agent dialog, ImageViewer, and ComposerImageStage cleanly on desktop split view", async ({ openApp }) => {
    const page = await openApp({ theme: "dark", surfaceStyle: "liquid-glass", desktopBridge: true });
    await openSplitPanelAgent(page);

    // 1. Command Palette
    await page.locator("[data-mac-bay-tools] button[aria-label='Search chats']").click();
    const paletteDialog = page.locator('[role="dialog"]').first();
    await expect(paletteDialog).toBeVisible();
    expect(await paletteDialog.evaluate((el) => el.closest("[data-app-shell]") === null)).toBe(true);
    await expectInsideViewport(paletteDialog);
    await expectNoActiveBackdropFilter(page);
    await page.keyboard.press("Escape");
    await expect(paletteDialog).toBeHidden();

    // 2. New Agent Dialog
    await page.getByRole("button", { name: "New agent" }).first().click();
    await page.getByRole("menuitem", { name: "New agent" }).click();
    const modalDialog = page.locator('[role="dialog"]').first();
    await expect(modalDialog).toBeVisible();
    expect(await modalDialog.evaluate((el) => el.closest("[data-app-shell]") === null)).toBe(true);
    await expectInsideViewport(modalDialog);
    await expectNoActiveBackdropFilter(page);
    await page.keyboard.press("Escape");
    await expect(modalDialog).toBeHidden();

    // 3. Message ImageViewer
    await page.locator("[data-attached-photos] button").first().click();
    const viewerDialog = page.locator('[role="dialog"]').first();
    await expect(viewerDialog).toBeVisible();
    expect(await viewerDialog.evaluate((el) => el.closest("[data-app-shell]") === null)).toBe(true);
    await expectInsideViewport(viewerDialog);
    await expectNoActiveBackdropFilter(page);
    await page.keyboard.press("Escape");
    await expect(viewerDialog).toBeHidden();

    // 4. ComposerImageStage clears shell filter/backdrop-filter/clip-path
    const fileInput = page.locator('[data-app-shell] > main input[type="file"]').first();
    await fileInput.setInputFiles({
      name: "stage-check.png",
      mimeType: "image/png",
      buffer: PNG_1X1,
    });
    const thumbTile = page.getByRole("button", { name: "Edit stage-check.png" });
    await expect(thumbTile).toBeVisible();
    await thumbTile.click();

    const stageArea = page.getByTestId("composer-image-stage-area");
    await expect(stageArea).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("data-composer-stage", "1");
    await expectInsideViewport(stageArea);

    const shellFilter = await page.evaluate(() => {
      const shell = document.querySelector<HTMLElement>("[data-app-shell]");
      const main = document.querySelector<HTMLElement>("[data-app-shell] > main");
      return {
        shellFilter: shell ? getComputedStyle(shell).filter : null,
        mainFilter: main ? getComputedStyle(main).filter : null,
        mainClip: main ? getComputedStyle(main).clipPath : null,
      };
    });
    expect(shellFilter.shellFilter).toBe("none");
    expect(shellFilter.mainFilter).toBe("none");
    expect(shellFilter.mainClip).toBe("none");

    await page.getByTestId("composer-image-close").click();
    await expect(stageArea).toBeHidden();
    await expect(page.locator("html")).not.toHaveAttribute("data-composer-stage", "1");
  });

  test("portals ExpandedComposer to document.body under compact Web Frosted Glass", async ({ openApp }) => {
    const page = await openApp({
      theme: "dark",
      surfaceStyle: "liquid-glass",
      desktopBridge: false,
      viewport: { width: 390, height: 844 },
    });

    const mainInput = page.locator("[data-app-shell] > main textarea").first();
    await mainInput.fill(Array.from({ length: 10 }, (_, i) => `Draft line ${i + 1}`).join("\n"));

    const expandBtn = page.locator('[data-app-shell] > main [data-testid="composer-expand"]').first();
    await expect(expandBtn).toBeVisible();
    await expandBtn.click();

    const expanded = page.getByTestId("composer-expanded");
    await expect(expanded).toBeVisible();
    expect(await expanded.evaluate((el) => el.parentElement === document.body)).toBe(true);
    await expectInsideViewport(expanded);

    await page.getByTestId("composer-collapse").click();
    await expect(expanded).toBeHidden();
  });
});
