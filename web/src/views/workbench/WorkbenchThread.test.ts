// The thread frame's sizes (#13439): the changes region keeps the conversation its room and folds away instead of
// squeezing it; the drawer stays between its least and its share of the column.
import { describe, expect, it } from "vitest";
import { CHAT_MIN, PANEL_MIN } from "../../lib/layout";
import { DIFF_DEFAULT, diffFits, diffWidth, DRAWER_DEFAULT, DRAWER_MIN, drawerHeight } from "./WorkbenchThread";

describe("the changes region's width", () => {
  it("is the default until asked, never under the panel's least, and leaves the conversation its room", () => {
    expect(diffWidth(0, 0)).toBe(DIFF_DEFAULT);
    expect(diffWidth(100, 2000)).toBe(PANEL_MIN);
    expect(diffWidth(900, 1000)).toBe(1000 - CHAT_MIN);
    expect(diffWidth(Infinity, 1400)).toBe(1400 - CHAT_MIN);
  });

  it("folds away when the conversation and the panel don't both fit, and fits when room is unknown", () => {
    expect(diffFits(0)).toBe(true);
    expect(diffFits(CHAT_MIN + PANEL_MIN)).toBe(true);
    expect(diffFits(CHAT_MIN + PANEL_MIN - 1)).toBe(false);
  });
});

describe("the terminal drawer's height", () => {
  it("is the default until asked, at least its least and at most 60% of the column", () => {
    expect(drawerHeight(0, 0)).toBe(DRAWER_DEFAULT);
    expect(drawerHeight(10, 900)).toBe(DRAWER_MIN);
    expect(drawerHeight(800, 900)).toBe(540);
    expect(drawerHeight(300, 200)).toBe(DRAWER_MIN);
  });
});
