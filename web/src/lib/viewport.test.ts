import { describe, expect, it } from "vitest";
import { enterSends, keyboardFrame, keyboardVars } from "./viewport";

describe("keyboardFrame", () => {
  it("follows the visible part while the keyboard covers the bottom", () => {
    expect(keyboardFrame(844, { height: 508.4, offsetTop: 120, scale: 1 })).toEqual({ top: 120, height: 508 });
  });

  it("is nothing without a keyboard, without visualViewport, or while pinch-zoomed", () => {
    expect(keyboardFrame(844, { height: 844, offsetTop: 0, scale: 1 })).toBeUndefined();
    expect(keyboardFrame(844, { height: 829, offsetTop: 0, scale: 1 })).toBeUndefined(); // a scrollbar
    expect(keyboardFrame(844, undefined)).toBeUndefined();
    expect(keyboardFrame(844, { height: 400, offsetTop: 200, scale: 2 })).toBeUndefined();
  });
});

describe("keyboardVars", () => {
  it("puts the visible part in --kb-top / --kb-h while the keyboard is up", () => {
    expect(keyboardVars({ top: 120, height: 508 })).toEqual({ "--kb-top": "120px", "--kb-h": "508px" });
  });

  it("leaves them to the stylesheet without a keyboard", () => {
    expect(keyboardVars(undefined)).toBeNull();
  });
});

describe("enterSends", () => {
  it("sends on Enter with a precise pointer, never with Shift; Mod+Alt+Enter is the card shortcut's, not a send", () => {
    expect(enterSends({ key: "Enter", shiftKey: false }, true)).toBe(true);
    expect(enterSends({ key: "Enter", shiftKey: true }, true)).toBe(false);
    // Alt alone keeps sending as it always did; the shortcut carries Mod with it
    expect(enterSends({ key: "Enter", shiftKey: false, altKey: true }, true)).toBe(true);
    expect(enterSends({ key: "Enter", shiftKey: false, altKey: true, ctrlKey: true }, true)).toBe(false);
    expect(enterSends({ key: "Enter", shiftKey: false, altKey: true, metaKey: true }, true)).toBe(false);
    expect(enterSends({ key: "a", shiftKey: false }, true)).toBe(false);
  });

  it("makes a new line on a touch screen alone", () => {
    expect(enterSends({ key: "Enter", shiftKey: false }, false)).toBe(false);
  });
});
