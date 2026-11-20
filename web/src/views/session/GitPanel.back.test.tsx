// @vitest-environment jsdom
// Back inside the full-screen Git panel on a narrow screen (#11511): a diff, a commit and a commit's file each have an
// entry over the panel's, so Back steps out of them one at a time before it closes the panel, and × takes them all off.
// The real side panel and Git panel, in StrictMode (its effects run twice), on jsdom's history.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { GitCommit, GitFile, GitStatus } from "../../lib/files";

vi.hoisted(() => {
  (globalThis as Record<string, unknown>).matchMedia = (query: string) => ({
    matches: query === "(max-width: 1023px)",
    media: query,
    addEventListener() {},
    removeEventListener() {},
  });
});
vi.mock("../../lib/use-fs-live", () => ({ useFsLive: () => {} }));
import { setToken } from "../../lib/api";
import { onOverlayEntry, overlayDepth } from "../../lib/back-close";
import { closePanel, getPanel, openPanel } from "../../lib/panel";
import { t } from "../../i18n";
import { SidePanel } from "../SidePanel";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
Element.prototype.scrollIntoView ??= () => {};

const CHAT = "/s/m1/s1";
const CWD = "/home/me/app";
const GIT = { kind: "git", mid: "m1", sid: "s1", cwd: CWD } as const;
const changed: GitFile = { path: "src/main.ts", index: " ", worktree: "M", added: 1, deleted: 1 };
const commit: GitCommit = { hash: "c0ffee1234", short: "c0ffee12", subject: "Show the city name", author: "Author" };
const status: GitStatus = { is_repo: true, branch: "main", head: commit, files: [changed], commits: [commit] };
const DIFF = "diff --git a/src/main.ts b/src/main.ts\n--- a/src/main.ts\n+++ b/src/main.ts\n@@ -1 +1 @@\n-old\n+new\n";
let host: HTMLDivElement;
let root: Root;

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
/** The view shown over the list: its header's title, or null with the list showing. */
const view = () => host.querySelector(`button[aria-label="${t("session-files-parts.back")}"]`)?.closest("header")?.querySelector(".font-semibold")?.textContent ?? null;
const button = (text: string) => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes(text) && !b.closest(".hidden")) ?? null;
const click = (el: Element | null) => act(() => (el as HTMLElement).click());

async function openGit(panel: Parameters<typeof openPanel>[0] = GIT) {
  act(() => openPanel(panel));
  await until(() => Boolean(button("Show the city name")) || Boolean(view()), "the Git panel shows");
}

beforeEach(() => {
  setToken("synthetic-A");
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const u = new URL(String(input), "http://local.invalid");
    if (u.pathname.endsWith("/git/status")) return Response.json(status);
    if (u.pathname.endsWith("/git/diff")) return Response.json({ path: changed.path, diff: DIFF, truncated: false });
    if (u.pathname.endsWith("/git/show")) return Response.json({ commit, files: [changed], diff: DIFF, truncated: false });
    return Response.json({});
  }));
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
  setToken("");
  vi.unstubAllGlobals();
});

describe("Back in the Git panel on a narrow screen", () => {
  it("a diff, then Back: the list shows and the panel stays", async () => {
    await openGit();
    await click(button("main.ts"));
    await until(() => view() === "main.ts", "the diff opens");
    await settle();
    expect(overlayDepth()).toBe(2);
    await pressBack();
    expect(view()).toBeNull();
    expect(button("Show the city name")).not.toBeNull();
    expect(aside()).not.toBeNull();
    await pressBack();
    expect(aside()).toBeNull();
    expect(location.pathname).toBe(CHAT);
  });

  it("a commit, a file in it, then Back twice: the commit, then the list", async () => {
    await openGit();
    await click(button("Show the city name"));
    await until(() => view() === "c0ffee12", "the commit opens");
    await until(() => Boolean(button("main.ts")), "its files are listed");
    await click(button("main.ts"));
    await until(() => view() === "main.ts", "the commit's file opens");
    await settle();
    expect(overlayDepth()).toBe(3);
    await pressBack();
    expect(view()).toBe("c0ffee12");
    await pressBack();
    expect(view()).toBeNull();
    expect(aside()).not.toBeNull();
    expect(overlayDepth()).toBe(1);
  });

  it("× from a commit's file takes the three entries off: the history ends on the conversation's entry", async () => {
    await openGit();
    await click(button("Show the city name"));
    await until(() => Boolean(button("main.ts")), "the commit's files are listed");
    await click(button("main.ts"));
    await until(() => view() === "main.ts", "the commit's file opens");
    await settle();
    await click(host.querySelector(`button[aria-label="${t("session-files-parts.close_panel")}"]`));
    await settle();
    expect(aside()).toBeNull();
    expect(onOverlayEntry()).toBe(false);
    expect(location.pathname).toBe(CHAT);
    await pressBack();
    expect(location.pathname).toBe("/agents");
  });

  it("opened at a spot (a comment card's diff): Back shows the list", async () => {
    await openGit({ ...GIT, at: { path: changed.path, line: 1, diff: { kind: "unstaged" } } });
    await until(() => view() === "main.ts", "the diff opens at the spot");
    await settle();
    expect(overlayDepth()).toBe(2);
    await pressBack();
    expect(view()).toBeNull();
    expect(aside()).not.toBeNull();
  });
});
