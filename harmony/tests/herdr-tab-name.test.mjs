import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { herdrTabName, sessionDisplayTitle, sessionDisplayTitleDetail } from '../entry/src/main/ets/core/SessionNaming.ts';
import { isHubStateKey } from '../entry/src/main/ets/core/StorageOwnership.ts';

test('herdrTabName follows iOS and Web rules (#13899)', () => {
  // Herdr session with slot_id and slot_name
  assert.equal(herdrTabName({
    slot_id: 'slot-1',
    space_kind: 'herdr',
    slot_name: 'my-task'
  }), 'my-task');

  // Supports herdr-core (Herdr App) and tab_id fallback
  assert.equal(herdrTabName({
    tab_id: 'tab-2',
    host: 'herdr-core',
    tab_name: '  fix-auth  '
  }), 'fix-auth');

  // Excludes assistant conversations
  assert.equal(herdrTabName({
    assistant: true,
    slot_id: 'slot-1',
    space_kind: 'herdr',
    slot_name: 'my-task'
  }), '');

  // Excludes non-herdr hosts (orca, tern, codex_app)
  assert.equal(herdrTabName({
    slot_id: 'slot-1',
    space_kind: 'orca',
    slot_name: 'my-task'
  }), '');

  // Requires slot_id or tab_id
  assert.equal(herdrTabName({
    space_kind: 'herdr',
    slot_name: 'my-task'
  }), '');

  // Blank label returns empty
  assert.equal(herdrTabName({
    slot_id: 'slot-1',
    space_kind: 'herdr',
    slot_name: '   '
  }), '');
});

test('sessionDisplayTitle keeps default-off rendering unchanged and promotes tab name when enabled', () => {
  const data = { slot_id: 'slot-1', space_kind: 'herdr', slot_name: 'my-task' };
  assert.equal(sessionDisplayTitle('Work on session 1a2b', data, false), 'Work on session 1a2b');
  assert.equal(sessionDisplayTitle('Work on session 1a2b', data, true), 'my-task');
  assert.equal(sessionDisplayTitle('Work on session 1a2b', { slot_id: 's1', space_kind: 'orca', slot_name: 'x' }, true), 'Work on session 1a2b');

  assert.equal(sessionDisplayTitleDetail('Work on session 1a2b', data, false), '');
  assert.equal(sessionDisplayTitleDetail('Work on session 1a2b', data, true), 'Work on session 1a2b');
  assert.equal(sessionDisplayTitleDetail('my-task', data, true), '');
});

test('Hub-scoped agents-display cache key is wiped cleanly on Forget Hub', () => {
  const key = 'view:' + JSON.stringify(['hub-a', '']) + ':agents-display';
  assert.equal(isHubStateKey(key, 'hub-a'), true);
  assert.equal(isHubStateKey(key, 'hub-b'), false);
});

test('ClientModel, UI components, and trilingual resources wire use_herdr_tab_name cleanly', () => {
  const clientModel = readFileSync(new URL('../entry/src/main/ets/models/ClientModel.ets', import.meta.url), 'utf8');
  const settings = readFileSync(new URL('../entry/src/main/ets/components/SettingsPage.ets', import.meta.url), 'utf8');
  const rowContent = readFileSync(new URL('../entry/src/main/ets/components/AgentDirectoryRowContent.ets', import.meta.url), 'utf8');
  const listPage = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  const index = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
  const inspector = readFileSync(new URL('../entry/src/main/ets/components/FoldInspectorPanel.ets', import.meta.url), 'utf8');

  assert.match(clientModel, /\/api\/v1\/agents\/display-settings/);
  assert.match(clientModel, /setUseHerdrTabName/);
  assert.match(clientModel, /use_herdr_tab_name/);
  assert.match(settings, /id\('appearance\.use-herdr-tab-name'\)/);
  assert.match(rowContent, /sessionDisplayTitle\(/);
  assert.match(listPage, /presentation\.herdrTabName\s*=\s*herdrTabName\(data\)/);
  assert.match(listPage, /this\.rowTitle\(binding\)/);
  const agentRowBlock = listPage.slice(listPage.indexOf('  @Builder agentRow('), listPage.indexOf('  @Builder searchControl()'));
  assert.doesNotMatch(agentRowBlock, /\.data\(\)/);
  assert.match(index, /sessionDisplayTitle\(/);
  assert.match(inspector, /sessionDisplayTitle\(/);

  for (const loc of ['base', 'en_US', 'zh_CN', 'ja_JP']) {
    const data = JSON.parse(readFileSync(new URL(`../entry/src/main/resources/${loc}/element/string.json`, import.meta.url), 'utf8'));
    const map = new Map(data.string.map((entry) => [entry.name, entry.value]));
    assert.ok(map.get('agents_use_herdr_tab_name'), `${loc} missing agents_use_herdr_tab_name`);
    assert.ok(map.get('agents_use_herdr_tab_name_hint'), `${loc} missing agents_use_herdr_tab_name_hint`);
  }
});
