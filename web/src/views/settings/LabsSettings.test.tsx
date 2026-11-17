// @vitest-environment jsdom
// Settings › Labs (#9877): the terminal renderer's card says its default, scope, when it takes effect and how to go back;
// a choice is kept in this browser (Standard as no key), the old WebGL switch shows as WebGL, a value from another version
// is named and left until a choice replaces it, and a browser that doesn't keep the choice says so with nothing changed.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { LabsSettings } from "./LabsSettings";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const KEY = "persona.terminal.webgl";
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
  localStorage.clear();
});

const card = () => host.querySelector<HTMLElement>('[data-lab="terminal-renderer"]')!;
const radios = () => [...card().querySelectorAll<HTMLButtonElement>('[role="radio"]')];
const chosen = () => radios().find((r) => r.getAttribute("aria-checked") === "true")?.textContent;
const render = () => act(() => root.render(<LabsSettings />));

describe("Settings › Labs", () => {
  it("explains Labs and its browser-only scope, and lists the terminal renderer under Agents", () => {
    render();
    expect(host.textContent).toContain(t("settings-labs.intro"));
    expect(host.textContent).toContain(t("settings-labs.scope_note"));
    expect(host.querySelector('[data-settings-group="labs-agents"]')?.textContent).toContain(t("settings-labs.area_agents"));
    expect(card().textContent).toContain(t("settings-labs.terminal_renderer"));
    expect(card().textContent).toContain(t("settings-labs.trial"));
  });

  it("says the default, who it affects, when it takes effect and how to go back", () => {
    render();
    const terms = [...card().querySelectorAll("dt")].map((d) => d.textContent);
    const values = [...card().querySelectorAll("dd")].map((d) => d.textContent);
    expect(terms).toEqual([t("settings-labs.default"), t("settings-labs.scope"), t("settings-labs.activation"), t("settings-labs.rollback")]);
    expect(values).toEqual([
      t("settings-labs.terminal_renderer_standard"),
      t("settings-labs.scope_browser"),
      t("settings-labs.terminal_renderer_activation"),
      t("settings-labs.terminal_renderer_rollback"),
    ]);
  });

  it("offers Standard and WebGL as one named choice, Standard chosen with nothing saved", () => {
    render();
    expect(card().querySelector('[role="radiogroup"]')?.getAttribute("aria-label")).toBe(t("settings-labs.terminal_renderer"));
    expect(radios().map((r) => r.textContent)).toEqual([t("settings-labs.terminal_renderer_standard"), t("settings-labs.terminal_renderer_webgl")]);
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_standard"));
  });

  it("keeps WebGL as the old switch's value, says it is saved, and Standard removes it again", () => {
    render();
    act(() => radios()[1].click());
    expect(localStorage.getItem(KEY)).toBe("1");
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_webgl"));
    expect(card().textContent).toContain(t("settings-labs.saved"));
    act(() => radios()[0].click());
    expect(localStorage.getItem(KEY)).toBeNull();
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_standard"));
  });

  it("shows WebGL for the WebGL switch saved before Labs", () => {
    localStorage.setItem(KEY, "1");
    render();
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_webgl"));
    expect(card().textContent).not.toContain(t("settings-labs.invalid"));
  });

  it("names a saved value it doesn't recognize, leaves it, and replaces it once a choice is made", () => {
    localStorage.setItem(KEY, "webgl2");
    render();
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_standard"));
    expect(card().textContent).toContain(t("settings-labs.invalid"));
    expect(localStorage.getItem(KEY)).toBe("webgl2");
    act(() => radios()[0].click()); // Standard, already shown, still replaces it
    expect(localStorage.getItem(KEY)).toBeNull();
    expect(card().textContent).not.toContain(t("settings-labs.invalid"));
  });

  it("says when the browser won't keep the choice, and shows what is still in effect", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    render();
    act(() => radios()[1].click());
    expect(card().querySelector('[role="alert"]')?.textContent).toBe(t("settings-labs.save_failed"));
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_standard"));
    expect(card().textContent).not.toContain(t("settings-labs.saved"));
  });

  it("says when the browser won't let it read what is saved", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new DOMException("denied", "SecurityError");
    });
    render();
    expect(card().textContent).toContain(t("settings-labs.unreadable"));
    expect(chosen()).toBe(t("settings-labs.terminal_renderer_standard"));
  });
});
