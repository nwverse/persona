// @vitest-environment jsdom
// The screen-share module (#14404; docs/web/web.md "Modules"): the live screen, /screen/<machine>, is its page of its
// own (`standalonePages`), drawn by main.tsx with no app shell. On (the default, and before any hello), the address is
// its page: its strings, then its code, load, and the page takes the app's token out of the address bar. Off, the
// address is absent (an unknown one in the app) and neither its chunk nor its strings are requested; the machine page's
// link to it follows the hub's flag.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isValidElement } from "react";
import { forgetModuleSwitches, setModuleSwitchedOff } from "../../core/module-switch";
import { connectModuleHub, loadModule, MODULES_FEATURE, registeredModules, registerModule, resetModulesForTest, standaloneAt, standaloneOn, type ModuleDescriptor } from "../../core/module-host";
import { parse } from "../../lib/router";
import { getState, reset, subscribe } from "../../lib/store";
import { SCREEN_PAGE } from "../../lib/computer";
import { screenShare } from "./module";
import { STANDALONE_MODULES, standalonePage } from "../standalone";
import { ScreenView } from "./ScreenView";

vi.mock("../../views/theme", () => ({})); // follows the system theme through matchMedia, which jsdom has none of

const chunk = vi.fn();
const catalog = vi.fn();
function counted(over: Partial<ModuleDescriptor> = {}): ModuleDescriptor {
  const locales = Object.fromEntries(Object.entries(screenShare.locales!).map(([lang, load]) => [lang, () => (catalog(lang), load())]));
  return { ...screenShare, locales, load: () => (chunk(), screenShare.load()), ...over };
}
const setState = (patch: Partial<ReturnType<typeof getState>>) => Object.assign(getState(), patch);

beforeEach(() => {
  connectModuleHub(getState, subscribe);
  localStorage.clear();
  forgetModuleSwitches();
  reset();
  resetModulesForTest();
  chunk.mockClear();
  catalog.mockClear();
});

afterEach(() => {
  resetModulesForTest();
  reset();
  localStorage.clear();
  forgetModuleSwitches();
  window.history.replaceState(null, "", "/");
});

describe("the screen-share module", () => {
  it("draws /screen/<machine> on its own, its strings before its code, and takes the token out of the address bar", async () => {
    registerModule(counted());
    expect(SCREEN_PAGE).toBe("screen");
    expect(standaloneAt(["screen", "mac"])).toMatchObject({ id: "screen-share" });
    expect(standaloneOn(SCREEN_PAGE)).toBe(true);
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
    window.history.replaceState(null, "", "/screen/mac#token=t1&embed=1&name=Studio&keep=phone");
    const code = await loadModule("screen-share");
    expect(catalog).toHaveBeenCalledTimes(1);
    expect(chunk).toHaveBeenCalledTimes(1);
    const page = code.Standalone!(window.location.href);
    expect(isValidElement(page) && page.type).toBe(ScreenView);
    expect((page as { props: { target: unknown } }).props.target).toMatchObject({ mid: "mac", token: "t1", embedded: true, name: "Studio", keep: "phone" });
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/screen/mac");
  });

  it("leaves an address with no machine to the app", async () => {
    registerModule(counted());
    window.history.replaceState(null, "", "/screen/#token=t1");
    const code = await loadModule("screen-share");
    expect(code.Standalone!(window.location.href)).toBeNull();
    expect(window.location.hash).toBe("#token=t1");
  });

  it("switched off, is absent and never loads its chunk or strings", async () => {
    setModuleSwitchedOff("screen-share", true);
    registerModule(counted());
    expect(standaloneAt(["screen", "mac"])).toBe("absent");
    expect(standaloneOn(SCREEN_PAGE)).toBe(false);
    expect(parse("/screen/mac").name).toBe("not-found");
    await expect(loadModule("screen-share")).rejects.toThrow(/not enabled/);
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
  });

  it("is what main.tsx draws in place of the app on its addresses, before anything is registered", async () => {
    expect(STANDALONE_MODULES).toContain(screenShare);
    const list = [counted()];
    // another address is the app's: nothing registered, loaded or taken
    window.history.replaceState(null, "", "/kb/notes#token=t1");
    expect(await standalonePage(window.location.href, list)).toBeNull();
    expect(window.location.hash).toBe("#token=t1");
    expect(chunk).not.toHaveBeenCalled();
    window.history.replaceState(null, "", "/screen/mac#token=t1");
    const page = await standalonePage(window.location.href, list);
    expect(isValidElement(page) && page.type).toBe(ScreenView);
    expect(window.location.hash).toBe("");
    expect(catalog).toHaveBeenCalledTimes(1);
    expect(chunk).toHaveBeenCalledTimes(1);
    // the host's registry is App's: the page needed none of it
    expect(registeredModules()).toEqual([]);
  });

  it("switched off, leaves its address to the app with the fragment dropped, loading nothing", async () => {
    setModuleSwitchedOff("screen-share", true);
    window.history.replaceState(null, "", "/screen/mac#token=t1&embed=1");
    expect(await standalonePage(window.location.href, [counted()])).toBeNull();
    expect(window.location.hash).toBe("");
    expect(window.location.pathname).toBe("/screen/mac");
    expect(chunk).not.toHaveBeenCalled();
    expect(catalog).not.toHaveBeenCalled();
  });

  it("follows the hub flag module.screen-share for the core's link to it", () => {
    registerModule(counted());
    expect(screenShare.feature).toBe("module.screen-share");
    setState({ features: [MODULES_FEATURE], loaded: true });
    expect(standaloneOn(SCREEN_PAGE)).toBe(false);
    setState({ features: [MODULES_FEATURE, "module.screen-share"], loaded: true });
    expect(standaloneOn(SCREEN_PAGE)).toBe(true);
    // an older hub, which publishes no module flags, keeps it
    setState({ features: [], loaded: true });
    expect(standaloneOn(SCREEN_PAGE)).toBe(true);
  });
});
