// @vitest-environment jsdom
// Back inside the full-screen Files panel on a narrow screen (#11511): a file opened in it has an entry over the
// panel's, so Back shows the list again before it closes the panel; × takes both entries off at once; a panel restored
// on a file is the same. The real side panel and Files panel, in StrictMode (its effects run twice), on jsdom's history.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const screen = vi.hoisted(() => ({ narrow: true }));
vi.hoisted(() => {
  (globalThis as Record<string, unknown>).matchMedia = (query: string) => ({
    get matches() {
      return query === "(max-width: 1023px)" && screen.narrow;
    },
    media: query,
    addEventListener() {},
    removeEventListener() {},
  });
});
vi.mock("../../lib/use-fs-live", () => ({ useFsLive: () => {} }));
import { setToken } from "../../lib/api";
import { onOverlayEntry, overlayDepth } from "../../lib/back-close";
import { closePanel, getPanel, openPanel } from "../../lib/panel";
import { getState } from "../../lib/store";
import { t } from "../../i18n";
import { SidePanel } from "../SidePanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
HTMLElement.prototype.scrollTo ??= () => {};
Element.prototype.scrollIntoView ??= () => {};

const CHAT = "/s/m1/s1";
const DIR = "/home/me/app";
const FILES = { kind: "files", mid: "m1", sid: "s1", cwd: DIR } as const;
let host: HTMLDivElement;
let root: Root;
const original = { features: getState().features, machines: getState().machines };

/** Waits for `ok`: a lazy chunk and the fake computer's answers come after an unknown number of ticks. */
const until = async (ok: () => boolean, what: string) => {
  for (let i = 0; i < 300 && !ok(); i++) await act(() => new Promise((r) => setTimeout(r, 10)));
  expect(ok(), what).toBe(true);
};
/** Lets the closes' Back land (jsdom moves on a later task) and what it sets off render. */
const settle = () => act(() => new Promise((r) => setTimeout(r, 80)));
const pressBack = async () => {
  history.back();
  await settle();
};
const aside = () => host.querySelector("aside");
const row = (name: string) => host.querySelector<HTMLButtonElement>(`button[title="${DIR}/${name}"]`);
const viewer = () => host.querySelector(`button[aria-label="${t("session-files-parts.back")}"]`);
const listShown = () => Boolean(row("notes.txt")) && !row("notes.txt")!.closest(".hidden");
const click = (el: Element | null) => act(() => (el as HTMLElement).click());

async function openFiles(panel: Parameters<typeof openPanel>[0] = FILES) {
  act(() => openPanel(panel));
  await until(() => Boolean(aside()), "the panel shows");
}

beforeEach(() => {
  screen.narrow = true;
  setToken("synthetic-A");
  Object.assign(getState(), { features: ["file_edit", "file_info"], machines: [{ id: "m1", name: "Work computer", online: true, home: "/home/me", os: "linux" }] });
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const u = new URL(String(input), "http://local.invalid");
    if (u.pathname.endsWith("/git/status")) return Response.json({ is_repo: false, files: [], commits: [] });
    if (u.pathname.endsWith("/fs")) return Response.json({ path: DIR, entries: [{ name: "src", dir: true, size: 0 }, { name: "notes.txt", dir: false, size: 5 }], truncated: false });
    if (u.pathname.endsWith("/file")) {
      const path = u.searchParams.get("path")!;
      return Response.json({ path, name: path.split("/").pop(), size: 5, text: "hello", truncated: false, is_dir: false });
    }
    return Response.json({});
  }));
  // the page the conversation was opened from, then the conversation: Back from it leaves to /agents
  history.pushState(null, "", "/agents");
  history.pushState(null, "", CHAT);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  act(() => root.render(<StrictMode><SidePanel /></StrictMode>));
});

afterEach(async () => {
  if (getPanel()) act(() => closePanel());
  await settle();
  act(() => root.unmount());
  host.remove();
  Object.assign(getState(), original);
  setToken("");
  vi.unstubAllGlobals();
});

describe("Back in the Files panel on a narrow screen", () => {
  it("a file, then Back: the list shows and the panel stays; the next Back closes it on the conversation", async () => {
    await openFiles();
    await until(() => Boolean(row("notes.txt")), "the folder is listed");
    expect(overlayDepth()).toBe(1);
    await click(row("notes.txt"));
    await until(() => Boolean(viewer()), "the file opens");
    await settle();
    expect(overlayDepth()).toBe(2);
    await pressBack();
    expect(viewer()).toBeNull();
    expect(listShown()).toBe(true);
    expect(aside()).not.toBeNull();
    expect(overlayDepth()).toBe(1);
    await pressBack();
    expect(aside()).toBeNull();
    expect(location.pathname).toBe(CHAT);
    expect(onOverlayEntry()).toBe(false);
  });

  it("× with a file open takes both entries off: the history ends on the conversation's entry", async () => {
    await openFiles();
    await until(() => Boolean(row("notes.txt")), "the folder is listed");
    await click(row("notes.txt"));
    await until(() => Boolean(viewer()), "the file opens");
    await settle();
    expect(overlayDepth()).toBe(2);
    await click(host.querySelector(`button[aria-label="${t("session-files-parts.close_panel")}"]`));
    await settle();
    expect(aside()).toBeNull();
    expect(location.pathname).toBe(CHAT);
    expect(onOverlayEntry()).toBe(false);
    await pressBack();
    expect(location.pathname).toBe("/agents");
  });

  it("the viewer's ‹ takes the file's entry off: the next Back closes the panel", async () => {
    await openFiles();
    await until(() => Boolean(row("notes.txt")), "the folder is listed");
    await click(row("notes.txt"));
    await until(() => Boolean(viewer()), "the file opens");
    await settle();
    await click(viewer());
    await settle();
    expect(listShown()).toBe(true);
    expect(overlayDepth()).toBe(1);
    await pressBack();
    expect(aside()).toBeNull();
    expect(location.pathname).toBe(CHAT);
  });

  it("a panel restored on a file: Back shows the list", async () => {
    await openFiles({ ...FILES, file: `${DIR}/notes.txt` });
    await until(() => Boolean(viewer()), "the file shows");
    await settle();
    expect(overlayDepth()).toBe(2);
    await pressBack();
    expect(viewer()).toBeNull();
    await until(listShown, "the list shows");
    expect(aside()).not.toBeNull();
    await pressBack();
    expect(aside()).toBeNull();
    expect(location.pathname).toBe(CHAT);
  });

  it("a wide screen writes no history for a file", async () => {
    screen.narrow = false;
    await openFiles();
    await until(() => Boolean(host.querySelector(`[title="${DIR}/notes.txt"]`)), "the tree lists the folder");
    await click(host.querySelector(`[title="${DIR}/notes.txt"]`));
    await until(() => Boolean(viewer()), "the file opens");
    await settle();
    expect(onOverlayEntry()).toBe(false);
  });
});
