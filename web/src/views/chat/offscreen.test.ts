import { expect, it } from "vitest";
import { shiftAbove } from "./offscreen";

// the view's top at 100 px; each change: its top now, its height before and now
it.each([
  { what: "one above the view grew", changes: [{ top: -500, was: 300, now: 450 }], shift: 150 },
  { what: "one the view's top reached was drawn smaller (its end keeps its place)", changes: [{ top: 40, was: 900, now: 500 }], shift: -400 },
  { what: "one that starts on screen", changes: [{ top: 100, was: 300, now: 600 }], shift: 0 },
  { what: "two above, in any order, and one on screen", changes: [{ top: 700, was: 300, now: 200 }, { top: -800, was: 300, now: 400 }, { top: -400, was: 200, now: 260 }], shift: 160 },
  // the second one is on screen now only because the first one grew: it started above the view's top
  { what: "one the growth above pushed onto the screen", changes: [{ top: -300, was: 100, now: 400 }, { top: 350, was: 100, now: 150 }], shift: 350 },
])("$what", ({ changes, shift }) => {
  expect(shiftAbove(changes, 100)).toBe(shift);
});
