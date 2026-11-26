/**
 * HarmonyOS foldable & tri-fold posture engine (Mate X7 dual-fold, Mate XT tri-fold,
 * MatePad / MateBook wide displays, and Mate 90 Pro straight phones).
 *
 * Pure TypeScript so layout decisions are deterministically verified on the host
 * without depending on ArkUI runtime mocks.
 */

export type FoldPostureKind =
  | 'straight'
  | 'cover'
  | 'dual_unfolded'
  | 'trifold_unfolded'
  | 'hover_tabletop';

export type FoldWidthClass = 'xs' | 'sm' | 'md' | 'lg' | 'xl';

export interface FoldPostureInput {
  readonly widthClass: FoldWidthClass;
  readonly windowWidthVp: number;
  readonly windowHeightVp: number;
  readonly isFoldable: boolean;
  /** HarmonyOS `display.FoldStatus` numeric value (0=unknown, 1=expanded, 2=folded, 3=half, 11=dual-axis expanded, 21/13/23=tri-fold half variants). */
  readonly foldStatus: number;
  /** HarmonyOS `display.FoldDisplayMode` numeric value (1=full, 2=main, 3=sub, 4=coordination). */
  readonly foldDisplayMode: number;
  readonly nativeHoverAvailable: boolean;
}

export interface FoldAdaptiveLayout {
  readonly posture: FoldPostureKind;
  readonly navigationMode: 'stack' | 'split';
  readonly isFoldableSplit: boolean;
  readonly masterWidth: number;
  readonly masterMin: number;
  readonly masterMax: number;
  readonly detailMin: number;
  readonly supportsInspector: boolean;
  readonly autoShowInspector: boolean;
  readonly collapseRailForInspector: boolean;
  readonly inspectorWidth: number;
  readonly readingMaxWidth: number;
  readonly hoverMode: boolean;
  readonly cardColumns: number;
}

// HarmonyOS API 26 `display.FoldStatus` values
export const FOLD_STATUS_UNKNOWN = 0;
export const FOLD_STATUS_EXPANDED = 1;
export const FOLD_STATUS_FOLDED = 2;
export const FOLD_STATUS_HALF_FOLDED = 3;
export const FOLD_STATUS_EXPANDED_WITH_SECOND_EXPANDED = 11;
export const FOLD_STATUS_FOLDED_WITH_SECOND_EXPANDED = 12;
export const FOLD_STATUS_HALF_FOLDED_WITH_SECOND_EXPANDED = 13;
export const FOLD_STATUS_EXPANDED_WITH_SECOND_HALF_FOLDED = 21;
export const FOLD_STATUS_FOLDED_WITH_SECOND_HALF_FOLDED = 22;
export const FOLD_STATUS_HALF_FOLDED_WITH_SECOND_HALF_FOLDED = 23;

// HarmonyOS API 26 `display.FoldDisplayMode` values
export const FOLD_DISPLAY_MODE_UNKNOWN = 0;
export const FOLD_DISPLAY_MODE_FULL = 1;
export const FOLD_DISPLAY_MODE_MAIN = 2;
export const FOLD_DISPLAY_MODE_SUB = 3;
export const FOLD_DISPLAY_MODE_COORDINATION = 4;

export function isHalfFoldedStatus(foldStatus: number): boolean {
  return (
    foldStatus === FOLD_STATUS_HALF_FOLDED ||
    foldStatus === FOLD_STATUS_HALF_FOLDED_WITH_SECOND_EXPANDED ||
    foldStatus === FOLD_STATUS_EXPANDED_WITH_SECOND_HALF_FOLDED ||
    foldStatus === FOLD_STATUS_FOLDED_WITH_SECOND_HALF_FOLDED ||
    foldStatus === FOLD_STATUS_HALF_FOLDED_WITH_SECOND_HALF_FOLDED
  );
}

export function isTriFoldExpandedStatus(foldStatus: number): boolean {
  return foldStatus === FOLD_STATUS_EXPANDED_WITH_SECOND_EXPANDED;
}

export function widthClassFromBreakpoint(bp: number): FoldWidthClass {
  // ArkUI WidthBreakpoint: 0=XS, 1=SM, 2=MD, 3=LG, 4=XL
  if (bp <= 0) return 'xs';
  if (bp === 1) return 'sm';
  if (bp === 2) return 'md';
  if (bp === 3) return 'lg';
  return 'xl';
}

export function classifyFoldPosture(input: FoldPostureInput): FoldPostureKind {
  const width = Number.isFinite(input.windowWidthVp) ? Math.max(0, input.windowWidthVp) : 0;
  if (input.isFoldable && isHalfFoldedStatus(input.foldStatus) && input.nativeHoverAvailable) {
    return 'hover_tabletop';
  }
  if (
    isTriFoldExpandedStatus(input.foldStatus) ||
    (input.isFoldable && width >= 920) ||
    input.widthClass === 'xl'
  ) {
    return 'trifold_unfolded';
  }
  if (input.isFoldable) {
    if (
      input.foldStatus === FOLD_STATUS_FOLDED ||
      input.foldDisplayMode === FOLD_DISPLAY_MODE_MAIN ||
      input.foldDisplayMode === FOLD_DISPLAY_MODE_SUB ||
      width < 560
    ) {
      return 'cover';
    }
    return 'dual_unfolded';
  }
  if (input.widthClass === 'lg') return 'trifold_unfolded';
  if (input.widthClass === 'md' && width >= 640) return 'dual_unfolded';
  return 'straight';
}

/**
 * Resolves adaptive multi-pane layout metrics for Mate 90 Pro (straight),
 * Mate X7 (cover / 707vp dual-unfolded / half-folded hover), and
 * Mate XT (single / dual / 1060vp+ tri-fold 3-pane command center).
 */
export function resolveFoldLayout(input: FoldPostureInput): FoldAdaptiveLayout {
  const posture = classifyFoldPosture(input);
  const width = Number.isFinite(input.windowWidthVp) ? Math.max(0, input.windowWidthVp) : 0;
  const hoverMode = posture === 'hover_tabletop';

  if (posture === 'trifold_unfolded') {
    const canAutoInspector = width >= 960 || isTriFoldExpandedStatus(input.foldStatus);
    const masterWidth = width >= 1200 ? 320 : 296;
    const inspectorWidth = width >= 1200 ? 308 : 276;
    return {
      posture: posture,
      navigationMode: 'split',
      isFoldableSplit: input.isFoldable,
      masterWidth: masterWidth,
      masterMin: 264,
      masterMax: 420,
      detailMin: 340,
      supportsInspector: true,
      autoShowInspector: canAutoInspector,
      collapseRailForInspector: width < 920,
      inspectorWidth: inspectorWidth,
      readingMaxWidth: 740,
      hoverMode: false,
      cardColumns: width >= 1120 ? 3 : 2
    };
  }

  if (posture === 'dual_unfolded' || (hoverMode && width >= 600)) {
    // Mate X7 unfolded (~707vp) and Mate XT 2-screen (~680vp):
    // True HarmonyOS dual-pane split (master 268vp + live detail 439vp) with toggleable inspector.
    const masterWidth = width >= 760 ? 296 : Math.max(256, Math.round(width * 0.39));
    return {
      posture: posture,
      navigationMode: 'split',
      isFoldableSplit: input.isFoldable,
      masterWidth: masterWidth,
      masterMin: 244,
      masterMax: Math.min(360, Math.round(width * 0.48)),
      detailMin: 320,
      supportsInspector: width >= 660,
      autoShowInspector: false,
      collapseRailForInspector: true,
      inspectorWidth: 268,
      readingMaxWidth: 720,
      hoverMode: hoverMode,
      cardColumns: 2
    };
  }

  // Straight phones (Mate 90 Pro ~366vp), Mate X7 cover (~346vp), Mate XT 1-screen (~360vp)
  return {
    posture: posture,
    navigationMode: 'stack',
    isFoldableSplit: false,
    masterWidth: 320,
    masterMin: 280,
    masterMax: 440,
    detailMin: 320,
    supportsInspector: false,
    autoShowInspector: false,
    collapseRailForInspector: false,
    inspectorWidth: 0,
    readingMaxWidth: 680,
    hoverMode: hoverMode,
    cardColumns: 1
  };
}

/**
 * Compact hierarchical left inset for repository/worktree agent trees.
 * On narrow straight phones / foldable cover screens (< 560vp), keeps indentation tight
 * so agent titles retain ~120px more readable width without premature ellipsis.
 */
export function compactTreeInset(level: number, windowWidthVp: number, controlScale: number): number {
  const clamped = Math.max(0, Math.min(3, Math.floor(level)));
  const scale = Number.isFinite(controlScale) && controlScale > 0 ? controlScale : 1;
  if (windowWidthVp > 0 && windowWidthVp < 560) {
    // 14, 18, 22, 28 vp (vs legacy 14, 36, 58, 80 vp — saves 36vp on session rows!)
    const compactSteps: number[] = [14, 18, 22, 28];
    return Math.round(compactSteps[clamped] * Math.min(scale, 1.15));
  }
  return Math.round(14 + 20 * scale * clamped);
}
