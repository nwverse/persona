import type { DesktopSurfaceStyle } from "../../spec/desktop-bridge.js";

export type MacLiquidGlassVibrancy = "hud" | "popover";

/** Picks a crisp, scheme-appropriate macOS vibrancy material for Liquid Glass (dark hud vs light popover). */
export function liquidGlassVibrancy(shouldUseDarkColors: boolean): MacLiquidGlassVibrancy {
  return shouldUseDarkColors ? "hud" : "popover";
}

/** Applies or clears macOS Liquid Glass vibrancy on a BrowserWindow without touching destroyed windows. */
export function applyMacSurfaceStyle(
  window: {
    isDestroyed(): boolean;
    setVibrancy(type: MacLiquidGlassVibrancy | null): void;
    setBackgroundColor(color: string): void;
  },
  style: DesktopSurfaceStyle,
  shouldUseDarkColors: boolean,
  opaqueBackground: string,
): void {
  if (window.isDestroyed()) return;
  if (style === "liquid-glass") {
    window.setVibrancy(liquidGlassVibrancy(shouldUseDarkColors));
    window.setBackgroundColor("#00000000");
  } else {
    window.setVibrancy(null);
    window.setBackgroundColor(opaqueBackground);
  }
}
