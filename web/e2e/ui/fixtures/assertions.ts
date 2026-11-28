import { expect, type Locator, type Page } from "@playwright/test";

export async function expectNoHorizontalOverflow(page: Page, selector?: string): Promise<void> {
  const result = await page.evaluate((sel) => {
    const doc = document.documentElement;
    const issues: string[] = [];
    if (doc.scrollWidth > doc.clientWidth + 1) {
      issues.push(`documentElement scrollWidth=${doc.scrollWidth} > clientWidth=${doc.clientWidth}`);
    }
    if (sel) {
      const target = document.querySelector<HTMLElement>(sel);
      if (!target) issues.push(`Missing container selector: ${sel}`);
      else if (target.scrollWidth > target.clientWidth + 1) {
        issues.push(`${sel} scrollWidth=${target.scrollWidth} > clientWidth=${target.clientWidth}`);
      }
    }
    return issues;
  }, selector);
  expect(result, "Horizontal overflow detected").toEqual([]);
}

export async function expectInsideViewport(locator: Locator, tolerance = 2): Promise<void> {
  await expect(locator).toBeVisible();
  const viewport = locator.page().viewportSize();
  expect(viewport, "Page should define a viewport size").not.toBeNull();
  await expect
    .poll(async () => {
      const box = await locator.boundingBox();
      if (!box) return "missing-bounding-box";
      if (box.width <= 0 || box.height <= 0) return `empty-box:${box.width}x${box.height}`;
      const right = box.x + box.width;
      const bottom = box.y + box.height;
      if (
        box.x >= -tolerance &&
        box.y >= -tolerance &&
        right <= viewport!.width + tolerance &&
        bottom <= viewport!.height + tolerance
      ) {
        return "inside";
      }
      return `x=${box.x},y=${box.y},right=${right},bottom=${bottom},vw=${viewport!.width},vh=${viewport!.height}`;
    })
    .toBe("inside");
}

export async function expectSingleLineButtons(container: Locator): Promise<void> {
  const wrapped = await container.evaluate((root) => {
    const failures: string[] = [];
    const buttons = root.querySelectorAll<HTMLButtonElement>("button");
    for (const btn of buttons) {
      const box = btn.getBoundingClientRect();
      if (box.width === 0 || box.height === 0) continue;
      if (getComputedStyle(btn).visibility === "hidden") continue;
      const walker = document.createTreeWalker(btn, NodeFilter.SHOW_TEXT);
      const tops = new Set<number>();
      let label = "";
      let node: Node | null;
      while ((node = walker.nextNode())) {
        const text = node.textContent?.trim() ?? "";
        if (!text) continue;
        const parentEl = node.parentElement;
        if (parentEl && (parentEl.closest(".sr-only") || parentEl.offsetWidth === 0 || parentEl.offsetHeight === 0)) {
          continue;
        }
        label += (label ? " " : "") + text;
        const range = document.createRange();
        range.selectNodeContents(node);
        for (const rect of range.getClientRects()) {
          if (rect.width > 0 && rect.height > 0) tops.add(Math.round(rect.top));
        }
      }
      if (tops.size > 1) {
        failures.push(`button "${label || btn.getAttribute("aria-label") || "(unnamed)"}" wrapped across ${tops.size} lines`);
      }
    }
    return failures;
  });
  expect(wrapped, "Toolbar or header button text must not wrap").toEqual([]);
}

export async function expectNoActiveBackdropFilter(page: Page): Promise<void> {
  const offenders = await page.evaluate(() => {
    const active: { classes: string; backdropFilter: string; webkitBackdropFilter: string }[] = [];
    for (const el of document.querySelectorAll<HTMLElement>('[class*="backdrop-"]')) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const cs = getComputedStyle(el);
      const webkit = cs.getPropertyValue("-webkit-backdrop-filter");
      if (cs.backdropFilter !== "none" || (webkit !== "" && webkit !== "none")) {
        active.push({
          classes: typeof el.className === "string" ? el.className : "",
          backdropFilter: cs.backdropFilter,
          webkitBackdropFilter: webkit,
        });
      }
    }
    return active;
  });
  expect(offenders, "Desktop Frosted Glass must keep CSS backdrop-filter disabled").toEqual([]);
}
