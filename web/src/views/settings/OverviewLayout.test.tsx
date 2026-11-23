// @vitest-environment jsdom
// Settings › General › Overview (#13019): Classic layout is off by default and turns the classic Overview on and off.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it } from "vitest";
import { initI18n, t } from "../../i18n";
import { classicOverview } from "../../lib/overview-layout";
import { OverviewLayoutGroup } from "./OverviewLayout";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(async () => {
  await initI18n("en");
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

it("is off by default and switches the classic layout on and back off", async () => {
  await act(async () => root.render(<OverviewLayoutGroup />));
  const sw = host.querySelector('[role="switch"]') as HTMLButtonElement;
  expect(host.textContent).toContain(t("overview-projects.settings_classic"));
  expect(sw.getAttribute("aria-checked")).toBe("false");
  expect(classicOverview()).toBe(false);
  await act(async () => sw.click());
  expect(classicOverview()).toBe(true);
  expect(sw.getAttribute("aria-checked")).toBe("true");
  await act(async () => sw.click());
  expect(classicOverview()).toBe(false);
});
