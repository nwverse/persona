// @vitest-environment jsdom
// A menu taller than the room it has scrolls inside, and says so: a fade at the edge where more rows are. The session
// header's ⋯ was held to 520px (or 70% of the window) however tall the window, and its cut fell between two rows, so the
// menu looked complete with Archive and Close out of sight.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Menu } from "./atoms";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let root: Root;
let host: HTMLDivElement;
let anchor: HTMLButtonElement;
// jsdom lays nothing out: the menu's scroll box is given its sizes
let sizes = { scroll: 610, client: 506 };

beforeEach(() => {
  window.matchMedia = ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  vi.spyOn(HTMLElement.prototype, "scrollHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.dataset.menu !== undefined ? sizes.scroll : 0;
  });
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(function (this: HTMLElement) {
    return this.dataset.menu !== undefined ? sizes.client : 0;
  });
  anchor = document.createElement("button");
  host = document.createElement("div");
  document.body.append(anchor, host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  anchor.remove();
  vi.restoreAllMocks();
});

const items = Array.from({ length: 19 }, (_, i) => ({ key: `r${i}`, label: `Row ${i}`, onSelect: () => {} }));
const panel = () => document.querySelector<HTMLElement>("[data-menu]")!;
const fades = () => [...panel().querySelectorAll<HTMLElement>("[data-menu-more]")].map((el) => el.dataset.menuMore);

describe("a menu taller than its room", () => {
  it("has no cap of its own short of the window", () => {
    act(() => root.render(<Menu anchor={anchor} onClose={() => {}} items={items} />));
    expect(panel().className).not.toMatch(/520px|70vh/);
    expect(panel().className).toContain("max-h-[calc(100dvh-(--spacing(4)))]");
  });

  it("fades its bottom edge while rows are below, its top edge once scrolled past the first", () => {
    sizes = { scroll: 610, client: 506 };
    act(() => root.render(<Menu anchor={anchor} onClose={() => {}} items={items} />));
    expect(fades()).toEqual(["down"]);
    act(() => {
      panel().scrollTop = 40;
      panel().dispatchEvent(new Event("scroll"));
    });
    expect(fades()).toEqual(["up", "down"]);
    act(() => {
      panel().scrollTop = 104;
      panel().dispatchEvent(new Event("scroll"));
    });
    expect(fades()).toEqual(["up"]);
  });

  it("draws no fade when every row shows", () => {
    sizes = { scroll: 300, client: 300 };
    act(() => root.render(<Menu anchor={anchor} onClose={() => {}} items={items.slice(0, 8)} />));
    expect(fades()).toEqual([]);
  });
});
