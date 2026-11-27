import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  FOLD_DISPLAY_MODE_FULL,
  FOLD_STATUS_FOLDED_WITH_SECOND_HALF_FOLDED,
  FOLD_STATUS_HALF_FOLDED,
  classifyFoldPosture,
  resolveFoldKeyboardShortcut,
  resolveFoldLayout
} from '../entry/src/main/ets/core/FoldPosture.ts';
import { buildQuickCommandGroups } from '../entry/src/main/ets/core/QuickCommandPalette.ts';

test('QuickCommandPalette sorts agents urgent-first, caps empty query at 6, and uses herdrTabName when enabled', () => {
  const sessions = [
    { id: 's-idle', title: 'Idle task', machineName: 'Mac Mini', state: 'idle' },
    { id: 's-unread', title: 'Organize notes', machineName: 'Mac Studio', state: 'unread' },
    { id: 's-work-1', title: 'Prepare changelog', machineName: 'Mac Studio', state: 'working' },
    {
      id: 's-needs-1',
      title: 'cli-release-audit',
      herdrTabName: 'Release Gate',
      machineName: 'Mac Studio',
      state: 'needs_you',
      keywords: ['ReleaseDesk', 'claude']
    },
    { id: 's-work-2', title: 'Build ArkTS', machineName: 'Build Box', state: 'working' },
    { id: 's-unread-2', title: 'Check diff', machineName: 'Mac Mini', state: 'unread' },
    { id: 's-idle-2', title: 'Extra idle', machineName: 'Mac Mini', state: 'idle' }
  ];

  const groups = buildQuickCommandGroups('', sessions, {
    useHerdrTabName: true,
    showWorkbench: true,
    showLiveView: true
  });

  assert.deepEqual(
    groups.map((g) => g.category),
    ['agents', 'workspaces', 'actions']
  );

  const agents = groups[0].entries;
  assert.equal(agents.length, 6);
  assert.equal(agents[0].id, 'agent:s-needs-1');
  assert.equal(agents[0].title, 'Release Gate');
  assert.equal(agents[1].tone, 'working');
  assert.equal(agents[2].tone, 'working');
  assert.equal(agents[3].tone, 'unread');
  assert.equal(agents[4].tone, 'unread');
  assert.equal(agents[5].tone, 'idle');

  // Filtering returns all matching entries case-insensitively across title, subtitle, machineName, keywords.
  const filtered = buildQuickCommandGroups('MAC MINI', sessions, { useHerdrTabName: false });
  assert.equal(filtered[0].category, 'agents');
  assert.equal(filtered[0].entries.length, 3);
  assert.ok(filtered[0].entries.some((e) => e.id === 'agent:s-idle-2'));

  // Filtering by original CLI title still finds herdr-renamed session.
  const byCli = buildQuickCommandGroups('cli-release-audit', sessions, { useHerdrTabName: true });
  assert.equal(byCli[0].entries[0].title, 'Release Gate');
});

test('QuickCommandPalette respects workspace visibility and workbench/LiveView flags', () => {
  const full = buildQuickCommandGroups('', [], {
    inboxVisible: true,
    todosVisible: true,
    showWorkbench: true,
    showLiveView: true
  });
  const wsIdsFull = full.find((g) => g.category === 'workspaces').entries.map((e) => e.id);
  assert.deepEqual(wsIdsFull, [
    'workspace:persona',
    'workspace:agents',
    'workspace:inbox',
    'workspace:todos',
    'workspace:knowledge',
    'workspace:health',
    'workspace:machines',
    'workspace:settings'
  ]);
  const actionIdsFull = full.find((g) => g.category === 'actions').entries.map((e) => e.id);
  assert.deepEqual(actionIdsFull, [
    'action:toggle_workbench',
    'action:workbench_code',
    'action:workbench_machines',
    'action:open_xiaoyi',
    'action:toggle_theme',
    'action:liveview_preview'
  ]);

  const hidden = buildQuickCommandGroups('', [], {
    inboxVisible: false,
    todosVisible: false,
    showWorkbench: false,
    showLiveView: false
  });
  const wsIdsHidden = hidden.find((g) => g.category === 'workspaces').entries.map((e) => e.id);
  assert.equal(wsIdsHidden.includes('workspace:inbox'), false);
  assert.equal(wsIdsHidden.includes('workspace:todos'), false);
  const actionIdsHidden = hidden.find((g) => g.category === 'actions').entries.map((e) => e.id);
  assert.deepEqual(actionIdsHidden, ['action:open_xiaoyi', 'action:toggle_theme']);
});

test('Smart book_half_open posture and Ctrl/Meta+K open_palette shortcut work cleanly alongside hover_tabletop', () => {
  assert.equal(
    classifyFoldPosture({
      widthClass: 'md',
      windowWidthVp: 707,
      isFoldable: true,
      foldStatus: FOLD_STATUS_HALF_FOLDED,
      foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
      nativeHoverAvailable: false
    }),
    'book_half_open'
  );

  assert.equal(
    classifyFoldPosture({
      widthClass: 'md',
      windowWidthVp: 707,
      windowHeightVp: 744,
      isFoldable: true,
      foldStatus: FOLD_STATUS_FOLDED_WITH_SECOND_HALF_FOLDED,
      foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
      nativeHoverAvailable: false
    }),
    'book_half_open'
  );

  // Horizontal hover crease still classifies as hover_tabletop.
  assert.equal(
    classifyFoldPosture({
      widthClass: 'md',
      windowWidthVp: 707,
      windowHeightVp: 773,
      isFoldable: true,
      foldStatus: FOLD_STATUS_HALF_FOLDED,
      foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
      nativeHoverAvailable: true
    }),
    'hover_tabletop'
  );

  const bookLayout = resolveFoldLayout({
    widthClass: 'md',
    windowWidthVp: 707,
    windowHeightVp: 773,
    isFoldable: true,
    foldStatus: FOLD_STATUS_HALF_FOLDED,
    foldDisplayMode: FOLD_DISPLAY_MODE_FULL,
    nativeHoverAvailable: false
  });
  assert.equal(bookLayout.posture, 'book_half_open');
  assert.equal(bookLayout.navigationMode, 'split');
  assert.equal(bookLayout.isFoldableSplit, true);
  assert.equal(bookLayout.supportsInspector, true);
  assert.equal(bookLayout.hoverMode, false);
  assert.equal(bookLayout.cardColumns, 2);

  assert.equal(resolveFoldKeyboardShortcut('k', true, false), 'open_palette');
  assert.equal(resolveFoldKeyboardShortcut('K', true, true), 'open_palette');
  assert.equal(resolveFoldKeyboardShortcut('i', true, true), 'toggle_inspector');
  assert.equal(resolveFoldKeyboardShortcut('k', false, true), 'none');
});

test('QuickCommandSheet, FoldInspectorPanel, SessionTools, and Index wire stage-2 craft and polish cleanly', () => {
  const sheet = readFileSync(new URL('../entry/src/main/ets/components/QuickCommandSheet.ets', import.meta.url), 'utf8');
  const inspector = readFileSync(new URL('../entry/src/main/ets/components/FoldInspectorPanel.ets', import.meta.url), 'utf8');
  const tools = readFileSync(new URL('../entry/src/main/ets/components/SessionTools.ets', import.meta.url), 'utf8');
  const index = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
  const clientModel = readFileSync(new URL('../entry/src/main/ets/models/ClientModel.ets', import.meta.url), 'utf8');

  // QuickCommandSheet Liquid Glass sheet & item IDs
  assert.match(sheet, /id\('quick\.palette\.search'\)/);
  assert.match(sheet, /id\('quick\.palette\.item\.' \+ entry\.id\)/);
  assert.match(sheet, /PhoneStyle\.cardRadius/);
  assert.match(sheet, /PhoneStyle\.cardBorder/);
  assert.match(sheet, /PhoneStyle\.cardElevation/);
  assert.match(sheet, /Haptics\.selection\(\)/);

  // FoldInspectorPanel: 2x2 workspace chip grid < 316vp, Demo interrupt enabled, Machines CPU/Load gauges
  assert.match(inspector, /useCompactChipGrid\(\)[\s\S]*< 316/);
  assert.match(inspector, /onAreaChange\(/);
  assert.match(inspector, /if \(this\.useCompactChipGrid\(\)\)[\s\S]*Column\(\{ space: 8 \}\)[\s\S]*Row\(\{ space: 8 \}\)[\s\S]*Row\(\{ space: 8 \}\)/);
  assert.doesNotMatch(inspector, /normalizedRowState\(row\) === 'working' && !this\.model\.demo/);
  assert.match(inspector, /normalizedRowState\(row\) === 'working'/);
  assert.match(inspector, /machine_cpu_load/);
  assert.match(inspector, /fold\.workbench\.machine\.cpu\./);
  assert.match(inspector, /fold\.workbench\.machine\.load\./);

  // SessionTools: two-tier non-clipping header in isCompactPane
  assert.match(tools, /if \(this\.isCompactPane\)/);
  assert.match(tools, /compactFolderBreadcrumbBar\(/);
  assert.match(tools, /TextOverflow\.Ellipsis/);
  assert.match(tools, /TextAlign\.Start/);
  assert.match(tools, /file_crumb_session/);

  // Index: shell.quick.palette on both phone & wide, Ctrl/Meta+K, instant theme toggle, Demo interrupt, book_half_open
  assert.match(index, /@Local quickPaletteOpen: boolean = false;/);
  assert.match(index, /resolveFoldKeyboardShortcut\(/);
  assert.match(index, /action === 'open_palette'/);
  assert.match(index, /id\('shell\.quick\.palette'\)/);
  assert.match(index, /SheetType\.CENTER/);
  assert.match(index, /SheetSize\.LARGE/);
  assert.match(index, /action:toggle_theme/);
  assert.match(index, /setAppearance\(/);
  assert.match(index, /this\.environment\.dark =/);
  assert.match(index, /book_half_open[\s\S]*fold_posture_book/);
  assert.doesNotMatch(index, /interruptSessionFromWorkbench[\s\S]*this\.model\.demo \|\|/);
  assert.match(clientModel, /canInterruptSession\(row: ResourceRow\)/);
});

test('All 4 locales include stage-2 strings with zero duplicate keys', () => {
  const requiredKeys = [
    'quick_palette_title',
    'quick_palette_placeholder',
    'quick_palette_agents',
    'quick_palette_workspaces',
    'quick_palette_actions',
    'quick_palette_toggle_theme',
    'quick_palette_toggle_workbench',
    'quick_palette_liveview_preview',
    'quick_palette_code_tab',
    'quick_palette_machines_tab',
    'quick_palette_empty',
    'fold_posture_book',
    'machine_cpu_load'
  ];

  for (const locale of ['base', 'en_US', 'zh_CN', 'ja_JP']) {
    const raw = readFileSync(
      new URL(`../entry/src/main/resources/${locale}/element/string.json`, import.meta.url),
      'utf8'
    );
    const parsed = JSON.parse(raw);
    const seen = new Set();
    for (const entry of parsed.string) {
      assert.equal(seen.has(entry.name), false, `${locale} has duplicate string key: ${entry.name}`);
      seen.add(entry.name);
    }
    for (const key of requiredKeys) {
      assert.equal(seen.has(key), true, `${locale} missing required key: ${key}`);
      const item = parsed.string.find((s) => s.name === key);
      assert.ok(item && item.value.trim().length > 0, `${locale} key ${key} has empty value`);
    }
  }
});
