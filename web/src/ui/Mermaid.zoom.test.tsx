// @vitest-environment jsdom
// The zoomed Mermaid diagram is a modal: the focus moves into it, and closing it (Esc or a click) gives it back to "Enlarge".
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// jsdom has no matchMedia (useMedia reads it); the diagram is drawn already, so Mermaid itself never loads
vi.hoisted(() => {
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
});
vi.mock("../lib/mermaid", () => ({
  cachedMermaid: () => '<svg data-drawn=""></svg>',
  renderMermaid: () => Promise.resolve('<svg data-drawn=""></svg>'),
  mermaidProblem: () => ({ summary: "", detail: "" }),
}));
import { t } from "../i18n";
import { MermaidBlock } from "./Mermaid";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<MermaidBlock code="graph TD; A-->B" />));
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const zoomButton = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("ui-mermaid.zoom")}"]`)!;
const dialog = () => document.querySelector<HTMLElement>('[role="dialog"]');

function open() {
  zoomButton().focus();
  act(() => zoomButton().click());
  expect(document.activeElement).toBe(dialog()); // it left "Enlarge", so getting back there is the close's doing
}

describe("Mermaid zoom", () => {
  it("opens as a modal dialog that has the focus", () => {
    open();
    const d = dialog()!;
    expect(d.getAttribute("aria-modal")).toBe("true");
    expect(d.getAttribute("aria-label")).toBe(t("ui-mermaid.diagram"));
    expect(document.activeElement).toBe(d);
  });

  it("gives the focus back to 放大 when Esc closes it", () => {
    open();
    act(() => void window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })));
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(zoomButton());
  });

  it("gives the focus back to 放大 when a click outside the diagram closes it", () => {
    open();
    act(() => dialog()!.click());
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(zoomButton());
  });

  it("wraps Tab around the viewer controls and returns focus after Close", () => {
    open();
    const buttons = [...dialog()!.querySelectorAll("button")];
    for (const button of buttons) vi.spyOn(button, "getClientRects").mockReturnValue({ length: 1 } as DOMRectList);
    const close = buttons.at(-1)!;
    const first = buttons[0];
    const tab = (target: HTMLElement, shiftKey: boolean) => {
      const event = new KeyboardEvent("keydown", { key: "Tab", shiftKey, bubbles: true, cancelable: true });
      act(() => target.dispatchEvent(event));
      expect(event.defaultPrevented).toBe(true);
    };
    close.focus();
    tab(close, false);
    expect(document.activeElement).toBe(first);
    tab(first, true);
    expect(document.activeElement).toBe(close);
    act(() => close.click());
    expect(dialog()).toBeNull();
    expect(document.activeElement).toBe(zoomButton());
  });
});
