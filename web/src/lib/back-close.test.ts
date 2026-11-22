import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { isOverlayEntry, onOverlayEntry, openOverlay, overlayDepth, overlayState, settleOverlayEntry } from "./back-close";
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

// A browser's history in miniature: back() pops asynchronously and fires popstate, like the real one.
function fakeBrowser() {
  const loc = { href: "/s/m/a", pathname: "/s/m/a", search: "" };
  const addresses = [loc.href];
  const stack: unknown[] = [null];
  let at = 0;
  const move = (address: string) => { loc.href = address; [loc.pathname, loc.search = ""] = address.split(/(?=\?)/); };
  const listeners = new Set<() => void>();
  const history = {
    get state() {
      return stack[at];
    },
    get length() {
      return at + 1;
    },
    pushState(s: unknown, _title = "", url = loc.href) {
      addresses.splice(at + 1, Infinity, url);
      move(url);
      stack.splice(at + 1);
      stack.push(s);
      at++;
    },
    replaceState(s: unknown, _title = "", url = loc.href) {
      addresses[at] = url;
      move(url);
      stack[at] = s;
    },
    back() {
      this.go(-1);
    },
    go(delta: number) {
      setTimeout(() => {
        if (!delta || at + delta < 0 || at + delta >= stack.length) return;
        at += delta;
        move(addresses[at]);
        listeners.forEach((l) => l());
      }, 5);
    },
  };
  vi.stubGlobal("history", history);
  vi.stubGlobal("location", loc);
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
    expect(b.history.length).toBe(1); // The real page was replaced beneath the sheet.
    expect(isOverlayEntry(b.history.state, "o1")).toBe(false);
  });
  it("navigates from a stack once and leaves the real origin and destination in Back and Forward", async () => {
    const b = fakeBrowser();
    navigate("/source", false, { scroll: 7 });
    const lower = vi.fn();
    const upper = vi.fn();
    openOverlay(lower);
    openOverlay(upper);
    navigate("/next", false, { picked: 3 });
    await vi.runAllTimersAsync();
    expect([location.href, history.state]).toEqual(["/next", { picked: 3 }]);
    expect(upper).toHaveBeenCalledOnce();
    expect(lower).toHaveBeenCalledOnce();
    b.pressBack();
    await vi.runAllTimersAsync();
    expect([location.href, history.state]).toEqual(["/source", { scroll: 7 }]);
    b.history.go(1);
    await vi.runAllTimersAsync();
    expect([location.href, history.state]).toEqual(["/next", { picked: 3 }]);
    b.history.go(-2);
    await vi.runAllTimersAsync();
    expect(location.href).toBe("/s/m/a");
  });

  it("can follow a link after Forward revisits an old, already-closed stack", async () => {
    const b = fakeBrowser();
    navigate("/source");
    const lower = openOverlay(vi.fn());
    const upper = openOverlay(vi.fn());
    upper();
    lower();
    await vi.runAllTimersAsync();
    b.history.go(2); // Forward can reach entries left in the browser even though the overlays are closed.
    await vi.runAllTimersAsync();
    expect(overlayDepth()).toBe(2);
    navigate("/next");
    await vi.runAllTimersAsync();
    expect(location.href).toBe("/next");
    b.pressBack();
    await vi.runAllTimersAsync();
    expect([location.href, overlayDepth()]).toEqual(["/source", 0]);
  });

  it("replaces the page under live layers and keeps its state when navigating from the last layer", async () => {
    const b = fakeBrowser();
    navigate("/source");
    const lower = vi.fn();
    const upper = vi.fn();
    openOverlay(lower);
    openOverlay(upper);
    navigate("/renamed", true, { scroll: 12 });
    expect(overlayDepth()).toBe(2);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(upper).toHaveBeenCalledOnce();
    expect(lower).not.toHaveBeenCalled();
    expect([location.href, history.state.scroll, overlayDepth()]).toEqual(["/renamed", 12, 1]);
    navigate("/next");
    await vi.runAllTimersAsync();
    b.pressBack();
    await vi.runAllTimersAsync();
    expect([location.href, history.state]).toEqual(["/renamed", { scroll: 12 }]);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(location.href).toBe("/s/m/a");
  });

  it("defers removing a lower layer beneath a live menu and preserves the other live layer", async () => {
    const b = fakeBrowser();
    const bottom = vi.fn();
    const dialog = vi.fn();
    const menu = vi.fn();
    openOverlay(bottom);
    const closeDialog = openOverlay(dialog);
    openOverlay(menu);
    closeDialog(); // a sheet switches to desktop on rotation
    await vi.runAllTimersAsync();
    expect(overlayDepth()).toBe(3);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(overlayDepth()).toBe(1);
    expect(menu).toHaveBeenCalledOnce();
    expect(dialog).not.toHaveBeenCalled();
    expect(bottom).not.toHaveBeenCalled();
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(bottom).toHaveBeenCalledOnce();
    expect(overlayDepth()).toBe(0);
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
    expect(b.history.length).toBe(1);
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

  it("settles eight stored entries in one traversal, before drawing the restored page", async () => {
    const b = fakeBrowser();
    navigate("/source", false, { selected: 3 });
    for (let level = 1; level <= 8; level++) {
      b.history.pushState({ selected: 3, personaOverlay: `saved-${level}`, personaOverlayDepth: level, personaOverlayAt: "/source" });
    }
    const go = vi.spyOn(b.history, "go");
    await settle();
    expect(go.mock.calls).toEqual([[-8]]);
    expect([location.href, history.state]).toEqual(["/source", { selected: 3 }]);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(location.href).toBe("/s/m/a");
  });

  it("recovers old entries without depth and preserves a replacement across reload", async () => {
    const b = fakeBrowser();
    navigate("/source");
    b.history.pushState({ personaOverlay: "old1", personaOverlayAt: "/source" });
    b.history.pushState({ personaOverlay: "old2", personaOverlayAt: "/source", selection: 4 }, "", "/renamed");
    await settle();
    expect([location.href, history.state]).toEqual(["/renamed", { selection: 4 }]);
    b.pressBack();
    await vi.runAllTimersAsync();
    expect(location.href).toBe("/s/m/a");
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
