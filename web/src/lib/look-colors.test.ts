// Automatic theme-color seed extraction tests (#15449).
import { describe, expect, it } from "vitest";
import { DEFAULT_AUTO_COLOR, extractCropColor, extractLookColor, normalizeHexColor } from "./look-colors";

function solidRGBA(r: number, g: number, b: number, a = 255, count = 100): Uint8ClampedArray {
  const out = new Uint8ClampedArray(count * 4);
  for (let i = 0; i < count; i++) {
    out[i * 4] = r;
    out[i * 4 + 1] = g;
    out[i * 4 + 2] = b;
    out[i * 4 + 3] = a;
  }
  return out;
}

describe("look-colors", () => {
  it("normalizes valid #RRGGBB hex strings and rejects everything else", () => {
    expect(normalizeHexColor("#3a7bd5")).toBe("#3A7BD5");
    expect(normalizeHexColor("  #ff00aa ")).toBe("#FF00AA");
    expect(normalizeHexColor("#123")).toBeNull();
    expect(normalizeHexColor("blue")).toBeNull();
    expect(normalizeHexColor(undefined)).toBeNull();
  });

  it("extracts the dominant vivid hue from RGBA pixels", () => {
    expect(extractLookColor(solidRGBA(58, 123, 213))).toBe("#3A7BD5");
    expect(extractLookColor(solidRGBA(242, 194, 48))).toBe("#F2C230");
  });

  it("keeps near-gray, dark, transparent, or sparse colorful images neutral gray", () => {
    expect(extractLookColor(solidRGBA(180, 180, 180))).toBe(DEFAULT_AUTO_COLOR);
    expect(extractLookColor(solidRGBA(40, 20, 20))).toBe(DEFAULT_AUTO_COLOR); // too dark (v <= 0.35)
    expect(extractLookColor(solidRGBA(255, 0, 0, 100))).toBe(DEFAULT_AUTO_COLOR); // transparent (a <= 200)
    expect(extractLookColor(solidRGBA(255, 0, 0, 255, 49))).toBe(DEFAULT_AUTO_COLOR); // < 50 colorful pixels
  });

  it("picks the dominant hue when mixed with background gray and a minority accent", () => {
    const gray = solidRGBA(240, 240, 240, 255, 300);
    const blue = solidRGBA(40, 120, 220, 255, 120);
    const orange = solidRGBA(240, 130, 30, 255, 60);
    const combined = new Uint8ClampedArray(gray.length + blue.length + orange.length);
    combined.set(gray, 0);
    combined.set(blue, gray.length);
    combined.set(orange, gray.length + blue.length);
    expect(extractLookColor(combined)).toBe("#2878DC");
  });

  it("wraps circular hue bins around 0 / 360 degrees for red pixels", () => {
    // Mix red-magenta (~355 deg, bin 35) and pure red (~5 deg, bin 0)
    const a = solidRGBA(230, 40, 56, 255, 40);
    const b = solidRGBA(230, 56, 40, 255, 60);
    const combined = new Uint8ClampedArray(a.length + b.length);
    combined.set(a, 0);
    combined.set(b, a.length);
    const hex = extractLookColor(combined);
    expect(hex).toMatch(/^#E6[0-9A-F]{4}$/);
  });

  it("samples a cropped image via canvas and returns null when canvas context is unavailable", () => {
    const pixels = solidRGBA(39, 116, 127, 255, 144 * 144);
    const fakeDoc = {
      createElement: () => ({
        width: 0,
        height: 0,
        getContext: () => ({
          imageSmoothingEnabled: false,
          imageSmoothingQuality: "low",
          clearRect() {},
          drawImage() {},
          getImageData: () => ({ data: pixels }),
        }),
      }),
    } as unknown as Document;
    expect(extractCropColor({} as CanvasImageSource, { w: 400, h: 400 }, { x: 200, y: 200, zoom: 1 }, fakeDoc)).toBe("#27747F");

    const noCtxDoc = { createElement: () => ({ getContext: () => null }) } as unknown as Document;
    expect(extractCropColor({} as CanvasImageSource, { w: 400, h: 400 }, { x: 200, y: 200, zoom: 1 }, noCtxDoc)).toBeNull();
  });
});
