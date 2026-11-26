import test from 'node:test';
import assert from 'node:assert/strict';
import {
  FOLD_DISPLAY_MODE_FULL,
  FOLD_DISPLAY_MODE_MAIN,
  FOLD_STATUS_EXPANDED,
  FOLD_STATUS_EXPANDED_WITH_SECOND_EXPANDED,
  FOLD_STATUS_FOLDED,
  FOLD_STATUS_HALF_FOLDED,
  FOLD_STATUS_UNKNOWN,
  classifyFoldPosture,
  compactTreeInset,
  isHalfFoldedStatus,
  isTriFoldExpandedStatus,
  resolveFoldLayout
} from '../entry/src/main/ets/core/FoldPosture.ts';

test('straight phone (Mate 90 Pro) stays compact stack with tight tree indentation', () => {
  const layout = resolveFoldLayout({
    widthClass: 'sm',
    windowWidthVp: 366,
    windowHeightVp: 809,
    isFoldable: false,
    foldStatus: FOLD_STATUS_UNKNOWN,
    foldDisplayMode: 0,
    nativeHoverAvailable: false
  });
  assert.equal(layout.posture, 'straight');
  assert.equal(layout.navigationMode, 'stack');
  assert.equal(layout.supportsInspector, false);
  assert.equal(layout.autoShowInspector, false);
  assert.equal(layout.cardColumns, 1);

  // Tight phone tree insets give agent rows ~36vp extra title space over legacy 58vp inset.
  assert.equal(compactTreeInset(0, 366, 1), 14);
  assert.equal(compactTreeInset(1, 366, 1), 18);
  assert.equal(compactTreeInset(2, 366, 1), 22);
  assert.ok(compactTreeInset(2, 366, 1) <= 24);
});

test('Mate X7 transitions seamlessly between cover stack, 707vp dual-pane split, and tabletop hover', () => {
  const cover = resolveFoldLayout({
    widthClass: 'sm',
    windowWidthVp: 346,
    windowHeightVp: 782,
    isFoldable: true,
    foldStatus: FOLD_STATUS_FOLDED,
    foldDisplayMode: FOLD_DISPLAY_MODE_MAIN,
    nativeHoverAvailable: false
  });
  assert.equal(cover.posture, 'cover');
  assert.equal(cover.navigationMode, 'stack');
  assert.equal(cover.supportsInspector, false);

  const unfolded = resolveFoldLayout({
    widthClass: 'md',
    windowWidthVp: 707,
    windowHeightVp: 773,
    isFoldable: true,
    foldStatus: FOLD_STATUS_EXPANDED,
    foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
    nativeHoverAvailable: false
  });
  assert.equal(unfolded.posture, 'dual_unfolded');
  assert.equal(unfolded.navigationMode, 'split');
  assert.equal(unfolded.isFoldableSplit, true);
  assert.ok(unfolded.masterWidth >= 256 && unfolded.masterWidth <= 300);
  assert.ok(unfolded.masterWidth + unfolded.detailMin <= 707);
  assert.equal(unfolded.supportsInspector, true);
  assert.equal(unfolded.autoShowInspector, false);
  assert.equal(unfolded.collapseRailForInspector, true);
  assert.equal(unfolded.cardColumns, 2);

  const hover = resolveFoldLayout({
    widthClass: 'md',
    windowWidthVp: 707,
    windowHeightVp: 773,
    isFoldable: true,
    foldStatus: FOLD_STATUS_HALF_FOLDED,
    foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
    nativeHoverAvailable: true
  });
  assert.equal(hover.posture, 'hover_tabletop');
  assert.equal(hover.hoverMode, true);
  assert.equal(isHalfFoldedStatus(FOLD_STATUS_HALF_FOLDED), true);
});

test('Mate XT tri-fold unlocks 3-pane command center (master + chat + auto inspector)', () => {
  assert.equal(isTriFoldExpandedStatus(FOLD_STATUS_EXPANDED_WITH_SECOND_EXPANDED), true);

  const trifold = resolveFoldLayout({
    widthClass: 'lg',
    windowWidthVp: 1061,
    windowHeightVp: 744,
    isFoldable: true,
    foldStatus: FOLD_STATUS_EXPANDED_WITH_SECOND_EXPANDED,
    foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
    nativeHoverAvailable: true
  });
  assert.equal(trifold.posture, 'trifold_unfolded');
  assert.equal(trifold.navigationMode, 'split');
  assert.equal(trifold.supportsInspector, true);
  assert.equal(trifold.autoShowInspector, true);
  assert.equal(trifold.collapseRailForInspector, false);
  assert.ok(trifold.inspectorWidth >= 276);
  assert.ok(trifold.masterWidth + trifold.detailMin + trifold.inspectorWidth <= 1061);

  // Dual-screen intermediate posture on Mate XT stays 2-pane without crowding the chat.
  const dualIntermediate = classifyFoldPosture({
    widthClass: 'md',
    windowWidthVp: 682,
    windowHeightVp: 744,
    isFoldable: true,
    foldStatus: FOLD_STATUS_EXPANDED,
    foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
    nativeHoverAvailable: false
  });
  assert.equal(dualIntermediate, 'dual_unfolded');
});
