// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearBuiltInShellCaches, readWebShellCookie, shellSwitchHref, WebShellPicker, writeWebShellCookie, type ExtensionWebShell } from "./WebShellPicker";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let mockShells: ExtensionWebShell[] = [];
let host: HTMLDivElement;
let root: Root;

vi.mock("../../lib/extension-declared", () => ({
  useDeclaredList: () => mockShells,
}));

describe("WebShellPicker", () => {
  beforeEach(() => {
    mockShells = [];
    document.cookie = "persona_web_shell=; max-age=0; path=/";
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(() => {
    act(() => root.unmount());
    host.remove();
  });

  it("renders nothing when no web_shells extension is installed", () => {
    act(() => root.render(<WebShellPicker />));
    expect(host.firstChild).toBeNull();
  });

  it("parses and writes same-origin cookies and builds switch URLs without query activation", () => {
    expect(readWebShellCookie("a=1; persona_web_shell=focus-ui%2Ffocus-4layer; b=2")).toBe("focus-ui/focus-4layer");
    expect(readWebShellCookie("other=1")).toBe("");
    expect(shellSwitchHref("", "")).toBe("/?shell=builtin");
    expect(shellSwitchHref("focus-ui/focus-4layer", "/persona")).toBe("/persona/");
    writeWebShellCookie("focus-ui/focus-4layer", "", false);
    expect(readWebShellCookie()).toBe("focus-ui/focus-4layer");
    writeWebShellCookie("", "", false);
    expect(readWebShellCookie()).toBe("");
  });

  it("sets the same-origin cookie, clears built-in SW caches, and reloads the root on selection", async () => {
    mockShells = [
      { extension: "focus-ui", extension_name: "Focus UI", version: "1.0.0", id: "focus-4layer", key: "focus-ui/focus-4layer", name: "Focus Workspace" },
    ];
    const nav = vi.fn();
    const clear = vi.fn(async () => {});
    act(() => root.render(<WebShellPicker onNavigate={nav} onClearCaches={clear} />));
    const select = host.querySelector("select") as HTMLSelectElement;
    expect(select).not.toBeNull();
    expect(select.value).toBe("");
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, "value")?.set;
      setter?.call(select, "focus-ui/focus-4layer");
      select.dispatchEvent(new Event("change", { bubbles: true }));
    });
    expect(readWebShellCookie()).toBe("focus-ui/focus-4layer");
    expect(clear).toHaveBeenCalledTimes(1);
    expect(nav).toHaveBeenCalledWith("/");
  });

  it("only unregisters Persona's own Service Worker scope and leaves sibling apps untouched", async () => {
    const removed: string[] = [];
    const origSW = navigator.serviceWorker;
    Object.defineProperty(navigator, "serviceWorker", {
      configurable: true,
      value: {
        getRegistrations: async () => [
          { scope: `${location.origin}/`, unregister: async () => { removed.push("/"); return true; } },
          { scope: `${location.origin}/other-app/`, unregister: async () => { removed.push("/other-app/"); return true; } },
        ],
      },
    });
    try {
      await clearBuiltInShellCaches("");
      expect(removed).toEqual(["/"]);
    } finally {
      Object.defineProperty(navigator, "serviceWorker", { configurable: true, value: origSW });
    }
  });
});
