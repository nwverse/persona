// @vitest-environment jsdom
import { act, createElement as h, Fragment, StrictMode, useState, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isOverlayEntry, onOverlayEntry, openOverlay, overlayDepth, overlayState, settleOverlayEntry, takeOverlayEntry, useBackStack, useCloseOnBack } from "./back-close";
import { navigate } from "./router";

describe("overlay history entries", () => {
  it("marks an entry as one overlay's, keeping the page's state", () => {
    const s = overlayState({ scroll: 3 }, "o1");
    expect(s).toEqual({ scroll: 3, personaOverlay: "o1" });
    expect(isOverlayEntry(s, "o1")).toBe(true);
    expect(isOverlayEntry(s, "o2")).toBe(false);
  });
  it("is not fooled by the router's null state or another overlay's entry", () => {
    expect(isOverlayEntry(null, "o1")).toBe(false);
    expect(isOverlayEntry(overlayState(null, "o2"), "o1")).toBe(false);
    expect(overlayState("x", "o3")).toEqual({ personaOverlay: "o3" });
  });
});

// A browser's history in miniature: back() and go() move asynchronously and fire popstate, like the real one.
function fakeBrowser() {
  const stack: unknown[] = [null];
  let at = 0;
  const listeners = new Set<() => void>();
  const traverse = (delta: number) =>
    setTimeout(() => {
      if (!delta || at + delta < 0 || at + delta >= stack.length) return;
      at += delta;
      listeners.forEach((l) => l());
    }, 5);
  const history = {
    get state() {
      return stack[at];
    },
    get length() {
      return at + 1;
    },
    pushState(s: unknown) {
      stack.splice(at + 1);
      stack.push(s);
      at++;
    },
    replaceState(s: unknown) {
      stack[at] = s;
    },
    back() {
      traverse(-1);
    },
    go(delta: number) {
      traverse(delta);
    },
  };
  vi.stubGlobal("history", history);
  vi.stubGlobal("location", { href: "/s/m/a", pathname: "/s/m/a" });
  vi.stubGlobal("addEventListener", (_: string, l: () => void) => listeners.add(l));
  vi.stubGlobal("removeEventListener", (_: string, l: () => void) => listeners.delete(l));
  return { history, pressBack: () => history.back() };
}

describe("openOverlay", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("Back closes it and leaves the page where it was", async () => {
    const b = fakeBrowser();
    const onClose = vi.fn();
    openOverlay(onClose);
    expect(b.history.length).toBe(2);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(onClose).toHaveBeenCalledOnce();
    expect(b.history.length).toBe(1);
  });

  it("closing it from the page takes its entry back off", async () => {
    const b = fakeBrowser();
    const onClose = vi.fn();
    openOverlay(onClose)();
    await vi.runAllTimersAsync();
    expect(b.history.length).toBe(1);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("opened again right after closing (StrictMode's double effect) stays open on one entry", async () => {
    const b = fakeBrowser();
    const first = vi.fn();
    const second = vi.fn();
    openOverlay(first)(); // mount, then StrictMode's cleanup…
    openOverlay(second); // …and the effect again
    await vi.runAllTimersAsync();
    expect(second).not.toHaveBeenCalled();
    expect(first).not.toHaveBeenCalled();
    expect(b.history.length).toBe(2);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(second).toHaveBeenCalledOnce();
    expect(b.history.length).toBe(1);
  });

  it("counts stacked entries, while reopening the same entry keeps its depth", async () => {
    const b = fakeBrowser();
    const closeFirst = openOverlay(vi.fn());
    expect(overlayDepth()).toBe(1);
    closeFirst();
    openOverlay(vi.fn()); // StrictMode's cleanup, then the same overlay's effect again: reuse, not another entry
    await vi.runAllTimersAsync();
    expect(overlayDepth()).toBe(1);
    expect(b.history.length).toBe(2);

    openOverlay(vi.fn());
    expect(overlayDepth()).toBe(2);
    expect(b.history.length).toBe(3);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(overlayDepth()).toBe(1);
    expect(onOverlayEntry()).toBe(true);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(overlayDepth()).toBe(0);
    expect(onOverlayEntry()).toBe(false);
  });

  it("an overlay entry written before the depth field still counts as one", () => {
    fakeBrowser();
    expect(overlayDepth(null)).toBe(0);
    expect(overlayDepth(overlayState(null, "old"))).toBe(1);
  });

  it("a navigation from inside the overlay takes its entry: one Back leaves the new page", async () => {
    const b = fakeBrowser();
    const onClose = vi.fn();
    const close = openOverlay(onClose); // the drawer opens over /s/m/a
    expect(b.history.length).toBe(2);
    // a drawer link: the router replaces the overlay's entry with the new page, then the drawer closes
    expect(takeOverlayEntry()).toBe(true);
    b.history.replaceState(null);
    close();
    await vi.runAllTimersAsync();
    expect(b.history.length).toBe(2); // the page under the drawer, then the new page: no dead copy between them
    expect(onClose).not.toHaveBeenCalled();
  });

  it("nothing to take when no overlay is open", () => {
    fakeBrowser();
    expect(takeOverlayEntry()).toBe(false);
  });

  it("the router: a drawer link replaces the drawer's entry, so one Back leaves the new page", async () => {
    const b = fakeBrowser();
    const close = openOverlay(vi.fn());
    navigate("/overview"); // the link, then the drawer closes
    close();
    await vi.runAllTimersAsync();
    expect(b.history.length).toBe(2);
    expect(isOverlayEntry(b.history.state, "o1")).toBe(false);
  });

  it("the router: a replace keeps an open overlay's marker (an old /s/ link turned into its /t/)", () => {
    const b = fakeBrowser();
    openOverlay(vi.fn());
    const marked = b.history.state;
    navigate("/t/m/w1/w1:t1/a", true);
    expect(b.history.state).toEqual(marked);
  });

  it("a sheet that sends the page elsewhere with a replace (deleting this chat goes home) closes without going back", async () => {
    const b = fakeBrowser();
    const close = openOverlay(vi.fn());
    const loc = location as unknown as { href: string; pathname: string };
    navigate("/overview", true);
    loc.href = loc.pathname = "/overview"; // the stub's location follows the replace
    close();
    await vi.runAllTimersAsync();
    expect(b.history.length).toBe(2); // stayed on the new page: no Back to the deleted chat
    expect(isOverlayEntry(b.history.state, "o1")).toBe(false);
  });
});

// A reload keeps the history and its states but none of the page's overlays (#5134): the first load settles them.
describe("settleOverlayEntry", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  const settle = async () => {
    const done = settleOverlayEntry();
    await vi.runAllTimersAsync();
    await done;
  };

  it("a reload with the side panel open goes back off its entry: the first Back then leaves the page", async () => {
    const b = fakeBrowser();
    openOverlay(vi.fn()); // the panel, then the reload
    expect(b.history.length).toBe(2);
    await settle();
    expect(b.history.length).toBe(1);
    expect(b.history.state).toBeNull();
  });

  it("goes back off overlays stacked on each other (the drawer and a sheet over it)", async () => {
    const b = fakeBrowser();
    openOverlay(vi.fn());
    openOverlay(vi.fn());
    expect(b.history.length).toBe(3);
    await settle();
    expect(b.history.length).toBe(1);
  });

  it("keeps the page's own state on the entry it lands on", async () => {
    const b = fakeBrowser();
    b.history.replaceState({ scroll: 3 });
    openOverlay(vi.fn());
    await settle();
    expect(b.history.state).toEqual({ scroll: 3 });
  });

  it("the page moved under it (a replace while it was open): takes the marker off and stays", async () => {
    const b = fakeBrowser();
    openOverlay(vi.fn());
    const loc = location as unknown as { href: string; pathname: string };
    navigate("/overview", true);
    loc.href = loc.pathname = "/overview";
    await settle();
    expect(b.history.length).toBe(2);
    expect(isOverlayEntry(b.history.state, "o1")).toBe(false);
    expect(Object.keys((b.history.state as object | null) ?? {}).some((k) => k.startsWith("personaOverlay"))).toBe(false);
  });

  it("an overlay's entry with nothing under it stays, without the marker", async () => {
    const b = fakeBrowser();
    b.history.replaceState({ personaOverlay: "o9", personaOverlayAt: "/s/m/a" });
    await settle();
    expect(b.history.length).toBe(1);
    expect(b.history.state).toBeNull();
  });

  it("leaves a page with no overlay's entry alone", async () => {
    const b = fakeBrowser();
    const back = vi.spyOn(b.history, "back");
    b.history.pushState({ scroll: 1 });
    await settle();
    expect(back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(2);
  });
});

// A full-screen side panel with a file open in it, a sheet over that (#11511): closing one from the page takes off its
// entry and every entry over it, in one Back, however many closes come in that tick and in whatever order.
describe("overlays stacked on each other", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  const traversals = (b: ReturnType<typeof fakeBrowser>) => ({ back: vi.spyOn(b.history, "back"), go: vi.spyOn(b.history, "go") });

  it("closing one with two over it goes back once by 3, and the next Back leaves the page", async () => {
    const b = fakeBrowser();
    b.history.pushState({ page: "chat" }); // the conversation, over the page it was opened from
    const [panel, file, sheet] = [vi.fn(), vi.fn(), vi.fn()];
    const closePanel = openOverlay(panel);
    openOverlay(file);
    openOverlay(sheet);
    expect(overlayDepth()).toBe(3);
    const t = traversals(b);
    closePanel();
    await vi.runAllTimersAsync();
    expect(t.go.mock.calls).toEqual([[-3]]);
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.state).toEqual({ page: "chat" });
    // what was open over it went with it; its own close came from the page
    expect([panel.mock.calls.length, file.mock.calls.length, sheet.mock.calls.length]).toEqual([0, 1, 1]);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(b.history.length).toBe(1);
  });

  it.each([["child first"], ["parent first"]])("closes in one tick (%s) go back once", async (order) => {
    const b = fakeBrowser();
    const closePanel = openOverlay(vi.fn());
    const closeFile = openOverlay(vi.fn());
    const t = traversals(b);
    if (order === "child first") {
      closeFile();
      closePanel();
    } else {
      closePanel();
      closeFile();
    }
    await vi.runAllTimersAsync();
    expect(t.go.mock.calls).toEqual([[-2]]);
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(1);
  });

  it("a close after the Back started, before it landed, does not go back again", async () => {
    const b = fakeBrowser();
    const closePanel = openOverlay(vi.fn());
    const closeFile = openOverlay(vi.fn());
    const t = traversals(b);
    closePanel();
    await vi.advanceTimersByTimeAsync(0); // the Back is under way: the file's entry is still the current one
    expect(t.go).toHaveBeenCalledOnce();
    expect(overlayDepth()).toBe(2);
    closeFile(); // a view's effect cleaned up late
    await vi.runAllTimersAsync();
    expect(t.go.mock.calls).toEqual([[-2]]);
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(1);
  });

  it("a close under where a started Back lands goes on from there", async () => {
    const b = fakeBrowser();
    const panel = vi.fn();
    const closePanel = openOverlay(panel);
    const closeFile = openOverlay(vi.fn());
    const t = traversals(b);
    closeFile(); // the viewer's ‹
    await vi.advanceTimersByTimeAsync(0);
    closePanel(); // × right after, before that Back landed
    await vi.runAllTimersAsync();
    expect(t.back).toHaveBeenCalledTimes(2);
    expect(b.history.length).toBe(1);
    expect(panel).not.toHaveBeenCalled();
  });

  it.each([["the lower first"], ["the upper first"]])(
    "StrictMode's second run of their effects (cleanups %s) keeps both entries, one per overlay",
    async (order) => {
      const b = fakeBrowser();
      const closeLower = openOverlay(vi.fn());
      const closeUpper = openOverlay(vi.fn());
      const t = traversals(b);
      if (order === "the lower first") {
        closeLower();
        closeUpper();
      } else {
        closeUpper();
        closeLower();
      }
      const [lower, upper] = [vi.fn(), vi.fn()];
      openOverlay(lower);
      openOverlay(upper);
      await vi.runAllTimersAsync();
      expect(t.back).not.toHaveBeenCalled();
      expect(t.go).not.toHaveBeenCalled();
      expect(b.history.length).toBe(3);
      b.pressBack();
      await vi.runAllTimersAsync();
      expect([lower.mock.calls.length, upper.mock.calls.length]).toEqual([0, 1]);
      b.pressBack();
      await vi.runAllTimersAsync();
      expect(lower).toHaveBeenCalledOnce();
      expect(b.history.length).toBe(1);
    },
  );

  it("one closed and another opened in its place over a stacked one: the entry is reused, the one under stays", async () => {
    const b = fakeBrowser();
    const panel = vi.fn();
    openOverlay(panel);
    const closeMenu = openOverlay(vi.fn());
    const t = traversals(b);
    closeMenu(); // a row menu's item opens a file
    const file = vi.fn();
    openOverlay(file);
    await vi.runAllTimersAsync();
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(3);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(file).toHaveBeenCalledOnce();
    expect(panel).not.toHaveBeenCalled();
  });

  it("a navigation from a stacked overlay in the tick it closes takes the top entry: nothing goes back", async () => {
    const b = fakeBrowser();
    openOverlay(vi.fn());
    const close = openOverlay(vi.fn());
    const t = traversals(b);
    close();
    navigate("/overview"); // the router replaces the top entry with the new page
    await vi.runAllTimersAsync();
    expect(t.back).not.toHaveBeenCalled();
    expect(t.go).not.toHaveBeenCalled();
    expect(onOverlayEntry()).toBe(false);
  });
});

// Views inside an overlay (Files' open files, the Git panel's diff and commit views): an entry each, over the overlay's.
describe("useBackStack", () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  let host: HTMLDivElement;
  let root: Root;
  let b: ReturnType<typeof fakeBrowser>;
  let popped: number[];
  /** Lets the effects' microtasks, the closes' timer and the fake browser's traversals (5 ms each) run. */
  const settle = () => act(() => new Promise((ok) => setTimeout(ok, 40)));
  const render = async (el: ReactNode) => {
    act(() => root.render(h(StrictMode, null, el)));
    await settle();
  };
  const back = async () => {
    b.pressBack();
    await settle();
  };
  beforeEach(() => {
    b = fakeBrowser();
    popped = [];
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    act(() => root.unmount());
    await settle();
    host.remove();
    vi.unstubAllGlobals();
  });

  let openView = () => {};
  function Views({ depth }: { depth: number }) {
    const [n, setN] = useState(depth);
    openView = () => setN((v) => v + 1);
    useBackStack(n, (i) => (popped.push(i), setN(i)));
    return h("span", { "data-views": n });
  }
  function Panel({ open, views, onClose = () => {} }: { open: boolean; views: number; onClose?: () => void }) {
    useCloseOnBack(open, onClose);
    return open ? h(Views, { depth: views }) : null;
  }
  const shown = () => Number(host.querySelector("[data-views]")?.getAttribute("data-views") ?? -1);

  it("0 → 2 → 0: two entries over the page, then one Back by 2", async () => {
    function Stack({ depth }: { depth: number }) {
      useBackStack(depth, (i) => popped.push(i));
      return null;
    }
    await render(h(Stack, { depth: 0 }));
    expect(b.history.length).toBe(1);
    await render(h(Stack, { depth: 2 }));
    expect(overlayDepth()).toBe(2);
    expect(b.history.length).toBe(3);
    const t = { back: vi.spyOn(b.history, "back"), go: vi.spyOn(b.history, "go") };
    await render(h(Stack, { depth: 0 }));
    expect(t.go.mock.calls).toEqual([[-2]]);
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(1);
    expect(popped).toEqual([]); // taken off from the page, not by Back
  });

  it("Back steps out of the top view first (popTo keeps the ones under it), then closes the panel", async () => {
    const close = vi.fn();
    await render(h(Panel, { open: true, views: 0, onClose: close }));
    expect(b.history.length).toBe(2);
    act(() => openView());
    await settle();
    act(() => openView());
    await settle();
    expect(shown()).toBe(2);
    expect(b.history.length).toBe(4);
    await back();
    expect(popped).toEqual([1]);
    expect(shown()).toBe(1);
    expect(b.history.length).toBe(3);
    await back();
    expect(popped).toEqual([1, 0]);
    expect(shown()).toBe(0);
    expect(close).not.toHaveBeenCalled();
    await back();
    expect(close).toHaveBeenCalledOnce();
    expect(b.history.length).toBe(1);
  });

  it("a panel that mounts with a view open (a restored file) has its entry under the view's", async () => {
    const close = vi.fn();
    await render(h(Panel, { open: true, views: 1, onClose: close }));
    expect(overlayDepth()).toBe(2);
    expect(b.history.length).toBe(3);
    await back();
    expect(popped).toEqual([0]);
    expect(shown()).toBe(0);
    expect(close).not.toHaveBeenCalled();
    await back();
    expect(close).toHaveBeenCalledOnce();
    expect(b.history.length).toBe(1);
  });

  it("closing the panel with two views open takes the three entries off in one Back", async () => {
    await render(h(Panel, { open: true, views: 2 }));
    expect(b.history.length).toBe(4);
    const t = { back: vi.spyOn(b.history, "back"), go: vi.spyOn(b.history, "go") };
    await render(h(Panel, { open: false, views: 2 }));
    expect(t.go.mock.calls).toEqual([[-3]]);
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(1);
    expect(popped).toEqual([]);
  });

  it("a menu row that opens the panel on a file hands the menu's entry to the panel (one commit)", async () => {
    let pick = () => {};
    const close = vi.fn();
    function Menu() {
      useCloseOnBack(true, () => {});
      return null;
    }
    function Shell() {
      const [menu, setMenu] = useState(true);
      const [panel, setPanel] = useState(false);
      pick = () => {
        setPanel(true);
        setMenu(false);
      };
      return h(Fragment, null, menu && h(Menu), h(Panel, { open: panel, views: 1, onClose: close }));
    }
    await render(h(Shell));
    expect(b.history.length).toBe(2);
    const t = { back: vi.spyOn(b.history, "back"), go: vi.spyOn(b.history, "go") };
    act(() => pick());
    await settle();
    expect(t.back).not.toHaveBeenCalled();
    expect(b.history.length).toBe(3); // the menu's entry became the panel's, the file's is over it
    await back();
    expect(popped).toEqual([0]);
    expect(close).not.toHaveBeenCalled();
    await back();
    expect(close).toHaveBeenCalledOnce();
    expect(b.history.length).toBe(1);
  });

  it("off (a wide screen): no entries", async () => {
    function Stack() {
      useBackStack(2, () => {}, false);
      return null;
    }
    await render(h(Stack));
    expect(b.history.length).toBe(1);
  });
});
