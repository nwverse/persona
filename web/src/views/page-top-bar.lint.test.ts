/// <reference types="node" />
// Every page's top bar is one height with one side padding (#16462; docs/web/web.md "The top bar": 56 px like every
// page header), so moving between pages never moves the title or the line under it. Read from the source: a `<header>`
// with a fixed height (`h-N`) is a top bar, of a page or of a side panel, and it is `h-14` with `px-3` (and `sm:px-4`
// when it widens at all). A bar sized by its content (`min-h-14`, padding only) is not this rule's.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const SRC = fileURLToPath(new URL("../", import.meta.url));
const SOURCE_FILE = /\.tsx$/;
const TEST_FILE = /\.test\.tsx$/;
/** `<header className="…">` or `<header className={cx("…", …)}>`: the class list it always has. */
const HEADER = /<header\s+className=(?:"([^"]*)"|\{cx\(\s*"([^"]*)")/g;
const FIXED_HEIGHT = /^h-(\d+(?:\.\d+)?|\[[^\]]+\])$/;

/** Not an app page's bar: the screen-share viewer is a window of its own, its thin bar over a remote screen. */
const OWN_WINDOW = new Set(["modules/screen-share/ScreenView.tsx"]);

/** The fixed-height top bars in `source`, as their class lists. */
function topBars(source: string): string[][] {
  const out: string[][] = [];
  for (const m of source.matchAll(HEADER)) {
    const classes = (m[1] ?? m[2] ?? "").split(/\s+/).filter(Boolean);
    if (classes.some((c) => FIXED_HEIGHT.test(c))) out.push(classes);
  }
  return out;
}

/** What is off in one bar's class list: its height, its side padding. */
function offRule(classes: string[]): string[] {
  const off: string[] = [];
  const heights = classes.filter((c) => FIXED_HEIGHT.test(c));
  if (heights.join(" ") !== "h-14") off.push(heights.join(" "));
  const padding = classes.filter((c) => /^(?:[a-z0-9]+:)?px-/.test(c));
  if (!padding.includes("px-3") || padding.some((c) => c !== "px-3" && c !== "sm:px-4")) off.push(padding.join(" "));
  return off;
}

const files = (readdirSync(SRC, { recursive: true }) as string[])
  .map((file) => file.split("\\").join("/"))
  .filter((file) => SOURCE_FILE.test(file) && !TEST_FILE.test(file));
const bars = files.flatMap((file) => topBars(readFileSync(join(SRC, file), "utf8")).map((classes) => ({ file, classes })));

describe("page top bars (#16462)", () => {
  it("are all 56 px with the same side padding", () => {
    const off = bars.filter((bar) => !OWN_WINDOW.has(bar.file)).flatMap(({ file, classes }) => offRule(classes).map((what) => `${file}: ${what}`));
    expect(off).toEqual([]);
  });

  it("reads the pages' bars, so a pattern that finds none cannot pass for one that keeps the rule", () => {
    const read = new Set(bars.map((bar) => bar.file));
    for (const page of ["views/Todos.tsx", "views/inbox/Inbox.tsx", "views/assistant/Routines.tsx", "views/knowledge/KBList.tsx", "views/Home.tsx", "views/Machines.tsx"]) {
      expect(read).toContain(page);
    }
    expect(bars.length).toBeGreaterThan(30);
  });

  it("tells a bar off the rule from one on it", () => {
    expect(topBars('<header className="flex h-13 shrink-0 px-4 sm:px-6">')).toEqual([["flex", "h-13", "shrink-0", "px-4", "sm:px-6"]]);
    expect(topBars('<header className={cx("flex h-14 px-3 sm:px-4", wide && "x")}>')).toEqual([["flex", "h-14", "px-3", "sm:px-4"]]);
    expect(topBars('<header className="flex min-h-14 px-3 py-2 sm:px-4">')).toEqual([]);
    expect(offRule(["flex", "h-13", "px-4", "sm:px-6"])).toEqual(["h-13", "px-4 sm:px-6"]);
    expect(offRule(["flex", "h-14", "px-3", "sm:px-4"])).toEqual([]);
    expect(offRule(["flex", "h-14", "px-3"])).toEqual([]);
  });
});
