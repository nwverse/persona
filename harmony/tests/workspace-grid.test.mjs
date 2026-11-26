import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  WORKSPACE_A11Y_SINGLE_COL_FONT_SCALE,
  WORKSPACE_ULTRA_WIDE_BREAKPOINT_VP,
  WORKSPACE_WIDE_BREAKPOINT_VP,
  workspaceGridColumns,
  workspaceGridGutter,
  workspaceHorizontalPadding
} from '../entry/src/main/ets/core/WorkspaceGrid.ts';

test('workspaceGridColumns adapts from straight phone to Mate X7 dual-fold and Mate XT tri-fold', () => {
  assert.equal(WORKSPACE_WIDE_BREAKPOINT_VP, 560);
  assert.equal(WORKSPACE_ULTRA_WIDE_BREAKPOINT_VP, 960);
  assert.equal(WORKSPACE_A11Y_SINGLE_COL_FONT_SCALE, 1.6);

  // Straight phones & cover screens (< 560vp)
  for (const width of [-10, 0, Number.NaN, 320, 346, 366, 402, 559.9]) {
    assert.equal(workspaceGridColumns(width, 1.0), 1, 'narrow width ' + width);
  }

  // Mate X7 unfolded & 2-screen Mate XT (560–959vp)
  for (const width of [560, 640, 680, 707, 840, 959.9]) {
    assert.equal(workspaceGridColumns(width, 1.0), 2, 'dual-fold width ' + width);
    assert.equal(workspaceGridColumns(width, 1.45), 2, 'medium text on dual-fold ' + width);
  }

  // Mate XT tri-fold & MatePad Pro 13 (>= 960vp)
  for (const width of [960, 1060, 1280, 1440]) {
    assert.equal(workspaceGridColumns(width, 1.0), 3, 'tri-fold width ' + width);
    assert.equal(workspaceGridColumns(width, 1.0, 2), 2, 'maxCols=2 cap at ' + width);
    assert.equal(workspaceGridColumns(width, 1.0, 1), 1, 'maxCols=1 cap at ' + width);
  }
});

test('workspaceGridColumns collapses to 1 column under accessibility fontScale >= 1.6', () => {
  assert.equal(workspaceGridColumns(707, 1.59), 2);
  assert.equal(workspaceGridColumns(1060, 1.59), 3);
  for (const scale of [1.6, 1.75, 2.0, 3.2]) {
    assert.equal(workspaceGridColumns(707, scale), 1, 'Mate X7 at scale ' + scale);
    assert.equal(workspaceGridColumns(1060, scale), 1, 'Mate XT at scale ' + scale);
  }
});

test('workspaceHorizontalPadding and workspaceGridGutter scale smoothly across display classes', () => {
  for (const width of [0, Number.NaN, 346, 366, 559]) {
    assert.equal(workspaceHorizontalPadding(width), 16, 'phone padding ' + width);
  }
  for (const width of [560, 707, 959]) {
    assert.equal(workspaceHorizontalPadding(width), 20, 'foldable padding ' + width);
  }
  for (const width of [960, 1060, 1280]) {
    assert.equal(workspaceHorizontalPadding(width), 24, 'wide padding ' + width);
  }

  assert.equal(workspaceGridGutter(0), 10);
  assert.equal(workspaceGridGutter(1), 10);
  assert.equal(workspaceGridGutter(2), 12);
  assert.equal(workspaceGridGutter(3), 14);
});

test('all five secondary workspaces wire flagship elevation, haptics, and foldable responsiveness', () => {
  const read = (file) => readFileSync(new URL('../entry/src/main/ets/components/' + file, import.meta.url), 'utf8');
  for (const file of [
    'InboxWorkspacePage.ets',
    'TodoWorkspacePage.ets',
    'KnowledgeWorkspacePage.ets',
    'HealthWorkspacePage.ets',
    'MachineDirectory.ets',
    'ResourcePage.ets'
  ]) {
    const source = read(file);
    assert.match(source, /WorkspaceGrid/, file + ' imports WorkspaceGrid');
    assert.match(source, /PhoneStyle\.cardElevation\(/, file + ' applies PhoneStyle.cardElevation');
    assert.match(source, /Haptics\./, file + ' wires Haptics');
  }
  assert.match(read('InboxWorkspacePage.ets'), /NeedsYouPulseDot/);
  assert.match(read('TodoWorkspacePage.ets'), /WorkingArcSpinner/);
  assert.match(read('TodoWorkspacePage.ets'), /NeedsYouPulseDot/);
  assert.match(read('KnowledgeRecordingView.ets'), /NeedsYouPulseDot/);
  assert.match(read('HealthToday.ets'), /PhoneStyle\.cardElevation\(/);
  assert.match(read('MachineSubscriptionCards.ets'), /PhoneStyle\.cardElevation\(/);
  assert.match(read('PhoneTodoDetailForm.ets'), /PhoneStyle\.cardElevation\(/);
});
