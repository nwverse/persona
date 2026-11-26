import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
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
  deduplicateAgentPlace,
  isHalfFoldedStatus,
  isTriFoldExpandedStatus,
  resolveFoldLayout,
  resolveFoldShortcut
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

test('hardware keyboard shortcuts resolve Ctrl/Meta+K/N/I and Escape across fold postures', () => {
  assert.equal(resolveFoldShortcut('k', true, true), 'search');
  assert.equal(resolveFoldShortcut('K', true, false), 'search');
  assert.equal(resolveFoldShortcut('n', true, true), 'new_chat');
  assert.equal(resolveFoldShortcut('N', true, false), 'new_chat');
  assert.equal(resolveFoldShortcut('i', true, true), 'toggle_inspector');
  assert.equal(resolveFoldShortcut('I', true, false), 'none');
  assert.equal(resolveFoldShortcut('escape', false, false), 'dismiss');
  assert.equal(resolveFoldShortcut('Esc', true, true), 'dismiss');
  // Bare letters without Ctrl/Meta never steal text input.
  assert.equal(resolveFoldShortcut('k', false, true), 'none');
  assert.equal(resolveFoldShortcut('n', false, true), 'none');
  assert.equal(resolveFoldShortcut('i', false, true), 'none');
  assert.equal(resolveFoldShortcut('', true, true), 'none');
  assert.equal(resolveFoldShortcut('x', true, true), 'none');
});

test('deduplicateAgentPlace suppresses redundant title echoes while keeping distinct place metadata', () => {
  assert.equal(deduplicateAgentPlace('Review release checks', 'Review release checks'), '');
  assert.equal(deduplicateAgentPlace('Review release checks', 'review release checks'), '');
  assert.equal(deduplicateAgentPlace('Review release checks', '  Review release checks  '), '');
  assert.equal(deduplicateAgentPlace('Review release checks', 'Review release checks · Mac Studio'), 'Mac Studio');
  assert.equal(deduplicateAgentPlace('Review release checks', 'Review release checks › Claude 1'), 'Claude 1');
  assert.equal(deduplicateAgentPlace('Fix login flow', 'main'), 'main');
  assert.equal(deduplicateAgentPlace('Fix login flow', 'Claude 1'), 'Claude 1');
  assert.equal(deduplicateAgentPlace('', 'main'), 'main');
  assert.equal(deduplicateAgentPlace('Fix login flow', ''), '');
});

test('64vp icon rail, 3-dot header, 3-chip inspector, and elevated repository cards maintain structural invariants', () => {
  const index = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
  const agents = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  const rowContent = readFileSync(new URL('../entry/src/main/ets/components/AgentDirectoryRowContent.ets', import.meta.url), 'utf8');
  const projects = readFileSync(new URL('../entry/src/main/ets/components/ProjectsPage.ets', import.meta.url), 'utf8');
  const inspector = readFileSync(new URL('../entry/src/main/ets/components/FoldInspectorPanel.ets', import.meta.url), 'utf8');

  // Rebuilt 64vp rail in Index.ets: no truncated toolbarConfiguration expand text; clean railShell + expand circle.
  assert.match(index, /\.toolbarConfiguration\(\[\]\)/);
  assert.doesNotMatch(index, /ohos_ic_public_arrow_right/);
  assert.match(index, /@Builder railShell\(\)[\s\S]*id\('shell\.rail\.persona'\)[\s\S]*id\('shell\.rail\.agents'\)[\s\S]*id\('shell\.rail\.expand'\)/);
  // Clean 3-dot menu button replaces ohos_ic_public_more in destinationMenus.
  const destMenus = index.slice(index.indexOf('  @Builder destinationMenus('), index.indexOf('  private openSettingsRoot('));
  assert.match(destMenus, /Row\(\{ space: 3 \}\)[\s\S]*Circle\(\)\.width\(3\.5\)\.height\(3\.5\)/);
  // Hardware keyboard shortcuts wired into root Stack.
  assert.match(index, /\.onKeyEvent\(\(event: KeyEvent\) => this\.handleFoldKeyEvent\(event\)\)/);
  assert.match(index, /resolveFoldShortcut\(/);
  // Inspector onOpenToolsTab wired to open or switch tabs live.
  assert.match(index, /onOpenToolsTab:\s*\(tab: number\) =>/);

  // Rebuilt AgentListPage rail mode: 40x40 avatars with status marks & bindTips, zero repo outline in rail mode.
  assert.match(agents, /@Builder railAvatar\(binding: BoundAgentRow\)[\s\S]*\.width\(40\)[\s\S]*\.height\(40\)[\s\S]*\.bindTips\(/);
  const resultList = agents.slice(agents.indexOf('  @Builder resultList()'), agents.indexOf('  build()'));
  assert.match(resultList, /if \(this\.rail\) \{[\s\S]*this\.railAvatar\(rowBinding\)[\s\S]*id\('agents\.rail\.list'\)[\s\S]*\} else \{[\s\S]*AgentRepositoryRows\(/);
  // Wide split agentRow uses live motion marks instead of static dot.
  const wideRow = agents.slice(agents.indexOf('  @Builder agentRow('), agents.indexOf('  @Builder rowContext('));
  assert.match(wideRow, /NeedsYouPulseDot\(/);
  assert.match(wideRow, /WorkingArcSpinner\(/);

  // Duplicate place suppression in AgentDirectoryRowContent & ProjectsPage + worktree dividers & card elevation.
  assert.match(rowContent, /private displayPlace\(\): string \{\s*return deduplicateAgentPlace\(this\.agent\.row\.title, this\.place\);/);
  assert.match(projects, /deduplicateAgentPlace\(agent\.row\.title, paneName \|\| agent\.tab\)/);
  assert.match(projects, /outlineItemNeedsDivider\(entry\)[\s\S]*Divider\(\)\.strokeWidth\(0\.5\)/);
  assert.match(projects, /PhoneStyle\.cardElevation\(nativeEnvironment\.dark\)/);

  // Supercharged FoldInspectorPanel: posture pill + 3-chip Workspace launcher + Fleet Pulse counters.
  assert.match(inspector, /id\('fold\.inspector\.posture'\)/);
  assert.match(inspector, /this\.toolChip\('files', 0, 'fold\.inspector\.tools\.files'/);
  assert.match(inspector, /this\.toolChip\('git', 1, 'fold\.inspector\.tools\.git'/);
  assert.match(inspector, /this\.toolChip\('terminal', 2, 'fold\.inspector\.tools\.terminal'\)/);
  assert.match(inspector, /onOpenToolsTab\(tab\)/);
  assert.match(inspector, /id\('fold\.inspector\.count\.needs'\)|'fold\.inspector\.count\.needs'/);
});
