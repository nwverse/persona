// @vitest-environment jsdom
// Two tabs of the same browser: the theme or the language picked in one shows in the other. The other tab stayed light
// until it was reloaded, and its Settings said 深色 over a light page (they read the stored choice); a language picked in
// one tab left the others in the old one.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.hoisted(() => {
  window.matchMedia = ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
import { reloadsForLanguage } from "../i18n";
import { useTheme } from "./theme";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  document.documentElement.classList.remove("dark");
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  localStorage.clear();
});

/** What another tab's choice looks like here: the stored value changed, then the storage event. */
const elsewhere = (key: string | null, value?: string) =>
  act(() => {
    if (key && value !== undefined) localStorage.setItem(key, value);
    window.dispatchEvent(new StorageEvent("storage", { key }));
  });

function Picked() {
  return <span data-theme={useTheme()} />;
}

describe("a theme picked in another tab", () => {
  it("shows here at once, and Settings here say what the page shows", () => {
    act(() => root.render(<Picked />));
    expect(host.querySelector("span")!.dataset.theme).toBe("system");
    elsewhere("persona.theme", "dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(host.querySelector("span")!.dataset.theme).toBe("dark");
    elsewhere("persona.theme", "light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(host.querySelector("span")!.dataset.theme).toBe("light");
  });

  it("follows a cleared storage (the system's again) and ignores other keys", () => {
    localStorage.setItem("persona.theme", "dark");
    elsewhere("persona.sidebarWidth", "300");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    localStorage.clear();
    elsewhere(null);
    expect(document.documentElement.classList.contains("dark")).toBe(false);
  });
});

describe("a language picked in another tab", () => {
  // the page runs in zh-Hans (the tests' catalog)
  it("reloads this page into it, as the tab that picked it does", () => {
    localStorage.setItem("persona.language", "en");
    expect(reloadsForLanguage("persona.language")).toBe(true);
  });
  it("does not when it is the page's language already, or another key changed", () => {
    localStorage.setItem("persona.language", "zh-Hans");
    expect(reloadsForLanguage("persona.language")).toBe(false);
    localStorage.setItem("persona.language", "en");
    expect(reloadsForLanguage("persona.theme")).toBe(false);
    expect(reloadsForLanguage(null)).toBe(false);
  });
});
