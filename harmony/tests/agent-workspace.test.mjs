import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';

const coreHelper = process.env.PERSONA_CORE_HOST_REPLAY;
globalThis.agentWorkspaceCoreSort = (input) => {
  if (!coreHelper) throw new Error('PERSONA_CORE_HOST_REPLAY is required for the real-core AgentWorkspace tests');
  if (!coreHelper) throw new Error('PERSONA_CORE_HOST_REPLAY is required for the real-core AgentWorkspace tests');
  if (!coreHelper) throw new Error('PERSONA_CORE_HOST_REPLAY is required for the real-core AgentWorkspace tests');
  const result = spawnSync(coreHelper, [], {input: (() => {
    const payload = Buffer.from(input);
    const frame = Buffer.alloc(5);
    frame.writeUInt32LE(payload.length + 1, 0); frame[4] = 10;
    return Buffer.concat([frame, payload]);
  })(), encoding: 'buffer', timeout: 10000, maxBuffer: 32 * 1024 * 1024});
  if (result.error || result.status !== 0) throw new Error('real core helper failed');
  const size = result.stdout.readUInt32LE(0);
  if (size < 1 || result.stdout[4] !== 1) throw new Error('real core rejected the input');
  return result.stdout.subarray(5, 4 + size).toString('utf8');
};


registerHooks({
  resolve(specifier, context, next) {
    if (specifier === 'libpersona_core_ohos.so') return {url: 'agent-workspace-core:', shortCircuit: true};
    if (context.parentURL?.endsWith('/AgentWorkspace.ets') && specifier === './Wire')
      return { url: new URL('./Wire.ets', context.parentURL).href, shortCircuit: true };
    if (context.parentURL?.includes('/entry/src/main/ets/')) {
      const resolved = new URL(specifier, context.parentURL);
      if (resolved.pathname.endsWith('/Wire') || resolved.pathname.endsWith('/SessionRelations')) resolved.pathname += '.ets';
      else if (!/\.[a-z]+$/i.test(resolved.pathname)) resolved.pathname += '.ts';
      return { url: resolved.href, shortCircuit: true };
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'agent-workspace-core:') return {format: 'module', shortCircuit: true,
      source: 'export default {coreProjectSort(input) { return globalThis.agentWorkspaceCoreSort(input); }};'};
    if (url.endsWith('.ets') || url.endsWith('/AgentListAdapter.ts') || url.endsWith('/AgentListProjection.ts')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { SessionArchive } = await import('../entry/src/main/ets/models/SessionArchive.ets');
const { AgentWorkspace } = await import('../entry/src/main/ets/models/AgentWorkspace.ets');
const { ResourceRow } = await import('../entry/src/main/ets/models/Wire.ets');

function agent(id, fields = {}) {
  const data = {
    session_id: id, machine_id: 'm1', manager: 'work', title: 'Agent ' + id, cwd: '/repo', repo_root: '/repo',
    pane_id: 'herdr:pane-' + id, worktree: '/repo', branch: 'main', status: 'working', state: 'working', updated_at: '2026-11-11T10:00:00Z',
    ...fields
  };
  return ResourceRow.from(data, 'session');
}

function machine(id = 'm1', trust = 'personal', manager = 'work') {
  return ResourceRow.from({ id, manager, name: 'Computer ' + id, online: true, trust }, 'machine');
}

function clone(value) { return JSON.parse(JSON.stringify(value)); }

function clientFixture(options = {}) {
  const state = {
    scope: 'hub:work', generation: 3, manager: 'work', calls: [], blocked: new Set(),
    prefs: {
      pins: { spaces: ['m1/keep-space'], sessions: ['m1/keep-session'] },
      mutes: { spaces: ['m1/space'], sessions: ['m1/keep-muted'], allowed: ['m1/allowed', 'keep/allowed'] }
    }, history: (options.history || []).map((row) => row instanceof ResourceRow ? JSON.parse(row.raw) : row), views: new Map(), request: undefined
  };
  const repo = {
    async call(path, method = 'GET', body = {}) {
      const call = { path, method, body: clone(body) };
      state.calls.push(call);
      if (state.request) {
        const result = await state.request(call, state);
        if (result !== undefined) return clone(result);
      }
      if (path === '/api/v1/pins' && method === 'GET') return clone(state.prefs.pins);
      if (path === '/api/v1/mutes' && method === 'GET') return clone(state.prefs.mutes);
      if (path === '/api/v1/pins' && method === 'PUT') { state.prefs.pins = clone(body); return clone(body); }
      if (path === '/api/v1/mutes' && method === 'PUT') { state.prefs.mutes = clone(body); return clone(body); }
      if (path.startsWith('/api/v1/sessions/archived')) return { sessions: state.history };
      return { ok: true };
    }
  };
  const client = {
    connected: true, demo: false, sessionsReady: true, archives: new SessionArchive(), profile: { manager: state.manager }, features: [],
    sessions: options.sessions || [agent('s1', {manager: state.manager})], machines: options.machines || [machine('m1', 'personal', state.manager)],
    selected: new ResourceRow(),
    repo() { return repo; },
    scope() { return state.scope; },
    connectionGeneration() { return state.generation; },
    owns(generation, scope) { return generation === state.generation && scope === state.scope; },
    target(row) { return JSON.stringify([this.scope(), row.kind, row.machine, row.id]); },
    machineName(id) { return this.machines.find((row) => row.id === id)?.title || id; },
    shortPath(path) { return path.replace(/\\/g, '/').split('/').filter(Boolean).slice(-2).join('/'); },
    operationBlocked(path) { return state.blocked.has(path); },
    async loadView(key) { return state.views.get(state.scope + ':' + key) || ''; },
    async saveView(key, value) { state.views.set(state.scope + ':' + key, value); },
    directoryOwners(){return[this];}, groupName(){return 'Hub';}, directoryOwnerFor(row){return row.manager===this.profile.manager ? this : undefined;}, async refreshDirectory(){},
    async refresh() {}
  };
  return { state, client, repo };
}

async function opened(options = {}) {
  const fixture = clientFixture(options), workspace = new AgentWorkspace();
  await workspace.open(fixture.client);
  return { ...fixture, workspace };
}

test('pins fetch the current document and preserve unrelated space and session keys', async () => {
  const { workspace, state, client } = await opened();
  const row = client.sessions[0];
  state.prefs.pins = { spaces: ['m1/space-a', 'm1/space-b'], sessions: ['m1/s1', 'm2/other'] };
  state.calls.length = 0;
  assert.equal(await workspace.togglePin(row), true);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]), [
    ['GET', '/api/v1/pins'], ['PUT', '/api/v1/pins']
  ]);
  assert.deepEqual(state.calls[1].body, { spaces: ['m1/space-a', 'm1/space-b'], sessions: ['m2/other'] });
});

test('unmuting an agent inherited from a muted space writes an allowed exception', async () => {
  const { workspace, state, client } = await opened();
  const row = client.sessions[0]; row.raw = JSON.stringify({ ...row.data(), space_name: 'space' });
  state.prefs.mutes = { spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session'], allowed: ['m1/existing', 'keep/allowed'] };
  state.calls.length = 0;
  assert.equal(workspace.isMuted(row), true);
  assert.equal(await workspace.toggleMute(row), true);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]), [
    ['GET', '/api/v1/mutes'], ['PUT', '/api/v1/mutes']
  ]);
  assert.deepEqual(state.calls[1].body, {
    spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session'], allowed: ['m1/existing', 'keep/allowed', 'm1/s1']
  });
  assert.equal(workspace.isMuted(row), false);
});

test('muting an agent opts it back into a mute beneath an allowed exception', async () => {
  const { workspace, state, client } = await opened();
  const row = client.sessions[0]; row.raw = JSON.stringify({ ...row.data(), space_name: 'space' });
  state.prefs.mutes = { spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session'], allowed: ['m1/s1', 'keep/allowed'] };
  await workspace.refresh(client);
  state.calls.length = 0;
  assert.equal(workspace.isMuted(row), false);
  assert.equal(await workspace.toggleMute(row), true);
  assert.deepEqual(state.calls[1].body, {
    spaces: ['m1/space', 'm2/keep'], sessions: ['m2/session', 'm1/s1'], allowed: ['keep/allowed']
  });
  assert.equal(workspace.isMuted(row), true);
});

test('rename, close, delete, and seen use the existing routes and exact request bodies', async () => {
  const s1 = agent('s1');
  const done = agent('done', { state: 'unread', status: 'done' });
  const { workspace, state } = await opened({ sessions: [s1, done] });
  state.calls.length = 0;
  assert.equal(await workspace.rename(s1, 'Renamed', 'review'), true);
  assert.equal(await workspace.close(s1), true);
  assert.equal(await workspace.delete(done), true);
  assert.equal(await workspace.markSeen(done), true);
  assert.deepEqual(state.calls.filter((call) => call.path.includes('/sessions/')).map((call) => [call.method, call.path, call.body]), [
    ['POST', '/api/v1/sessions/m1/s1/rename', { title: 'Renamed', tab_name: 'review' }],
    ['POST', '/api/v1/sessions/m1/s1/close', {}],
    ['DELETE', '/api/v1/sessions/m1/done', {}],
    ['POST', '/api/v1/sessions/m1/done/seen', {}]
  ]);
});

test('a Just finished row uses the existing seen route and leaves its section immediately', async () => {
  const finished = agent('finished', {
    state: 'idle', status: 'done', work: { phase: 'done', just_finished: true }
  });
  const { workspace, state } = await opened({ sessions: [finished] });
  assert.deepEqual(workspace.groups.map((group) => group.id), ['state/just_finished']);
  assert.equal(workspace.canMarkSeen(finished), true);
  state.calls.length = 0;

  assert.equal(await workspace.markSeen(finished), true);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]), [
    ['POST', '/api/v1/sessions/m1/finished/seen']
  ]);
  assert.equal(finished.data().work.just_finished, true, 'the caller keeps its immutable row');
  const current = workspace.latestSourceRows.find((row) => row.id === 'finished');
  assert.equal(current.data().work.just_finished, false);
  assert.equal(workspace.canMarkSeen(current), false);
  assert.equal(workspace.groups.some((group) => group.id === 'state/just_finished'), false);
  assert.equal(workspace.visibleRows().some((row) => row.id === 'finished'), false);
});

test('the Just finished group has a native label in every resource set', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  assert.ok(source.includes("'state/just_finished': 'agent_group_just_finished'"));
  const expected = { base: 'Just finished', en_US: 'Just finished', zh_CN: '刚做完待看', ja_JP: '完了したばかり' };
  for (const [locale, value] of Object.entries(expected)) {
    const resources = JSON.parse(readFileSync(new URL(`../entry/src/main/resources/${locale}/element/string.json`, import.meta.url), 'utf8'));
    assert.equal(resources.string.find((entry) => entry.name === 'agent_group_just_finished')?.value, value, locale);
  }
});

test('history loads only when the hub advertises the archive capability and honors machine selection', async () => {
  const archived = agent('archive-1', { archived: true, state: 'offline', status: 'offline' });
  const { workspace, state, client } = await opened({ history: [archived] });
  client.features = ['agent_session_archives'];
  state.calls.length = 0;
  await workspace.setMachineFilter('m1');
  await workspace.setHistory(true);
  assert.ok(state.calls.some((call) => call.method === 'GET' && call.path === '/api/v1/sessions/archived?machine_id=m1&manager=work'));
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.groups.flatMap((group) => group.rows).map((row) => row.id), ['archive-1'], 'archived offline snapshots remain readable history');
});

test('the iOS state list shows friendly identity and state while retaining complete inspection metadata', async () => {
  const row = agent('s1', { cwd: '/work/persona', repo_root: '/work/persona', worktree: '/work/persona', last_text: 'Checking the build' });
  const { workspace } = await opened({ sessions: [row], machines: [machine('m1')] });
  assert.equal(workspace.groupingMode, 'state');
  assert.deepEqual(workspace.groups.map((group) => group.id), ['state/working']);
  assert.equal(workspace.groups[0].showMachine, false);
  assert.equal(workspace.groups[0].showRepository, false);
  assert.equal(workspace.groups[0].showWorktree, false);
  assert.deepEqual(workspace.rowMetadata(row), ['Hub · work', 'Computer m1', 'work/persona', 'working', 'Checking the build']);
  assert.deepEqual(workspace.listMetadata(row), ['Hub', 'Computer m1', 'working', 'Checking the build']);
});

test('iOS state, machine and space groupings share core visibility and release reading freeze', async () => {
  const first = agent('s1', { repo_root: '/work/persona', worktree: '/work/persona' });
  const second = agent('s2', { repo_root: '/work/persona', worktree: '/work/persona/feature', branch: 'feature' });
  const third = agent('s3', { machine_id: 'm2', repo_root: '/other/repo', cwd: '/other/repo', worktree: '/other/repo' });
  const { workspace, client } = await opened({ sessions: [first, second, third], machines: [machine('m1'), machine('m2')] });
  assert.equal(workspace.visibleRows().length, 3);
  assert.deepEqual(workspace.groups.map((group) => group.id), ['state/working']);
  workspace.setGroupingMode('machine');
  assert.deepEqual(workspace.groups.map((group) => group.id), ['machine/work|m1', 'machine/work|m2']);
  assert.ok(workspace.groups.every((group) => group.showMachine && !group.showRepository && !group.showWorktree));
  workspace.setGroupingMode('space');
  assert.equal(workspace.groups.length, 2);
  assert.ok(workspace.groups.every((group) => !group.showMachine && !group.showRepository && !group.showWorktree));
  workspace.setGroupingMode('state');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1', 's2', 's3']);
  workspace.setReadingDetail(true);
  client.sessions = [first, second, third];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1', 's2', 's3']);
  client.sessions = [first, second, third, agent('s4')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1', 's2', 's3']);
  workspace.setGroupingMode('machine');
  const groupedOrder = workspace.visibleRows().map((row) => row.id);
  assert.deepEqual(groupedOrder, ['s1', 's2', 's4', 's3']);
  client.sessions = [first, second, third, agent('s4'), agent('s5', { state: 'needs_you' })];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), groupedOrder);
});

test('external session selection updates and clears the left row selection', async () => {
  const first = agent('s1'), second = agent('s2');
  const { workspace, client } = await opened({ sessions: [first, second] });
  client.selected = agent('s2');
  workspace.toggleFold('state/idle');
  workspace.update(client);
  assert.equal(workspace.isSelected(second), true);
  client.selected = ResourceRow.from({ id: 'conversation-1', manager: 'work' }, 'conversation');
  workspace.update(client);
  assert.equal(workspace.selectedKey, '');
  assert.equal(workspace.isSelected(second), false);
  client.selected = agent('s1');
  await workspace.open(client);
  assert.equal(workspace.isSelected(first), true);
});

test('a detail reading freezes membership and refreezes after explicit filter changes', async () => {
  const first = agent('s1', { state: 'working', updated_at: '2026-11-11T10:00:00Z' });
  const second = agent('s2', { state: 'idle', status: 'idle', updated_at: '2026-11-11T09:00:00Z' });
  const { workspace, client } = await opened({ sessions: [first, second] });
  workspace.setReadingDetail(true);
  const latest = agent('s1', { state: 'idle', status: 'idle', updated_at: '2026-11-11T11:00:00Z' });
  workspace.toggleFold('state/idle');
  const added = agent('s3', { state: 'working', updated_at: '2026-11-11T12:00:00Z' });
  client.sessions = [latest, added];
  workspace.update(client, true);
  const frozen = workspace.groups.flatMap((group) => group.rows);
  assert.deepEqual(frozen.map((row) => row.id), ['s1'], 'the core folds idle state by default');
  assert.equal(frozen[0].state, 'idle');
  workspace.setQuickFilter('working');
  assert.deepEqual(workspace.groups.flatMap((group) => group.rows).map((row) => row.id), ['s3']);
  const updated = agent('s3', { state: 'idle', status: 'idle', updated_at: '2026-11-11T13:00:00Z' });
  const next = agent('s4', { state: 'working', updated_at: '2026-11-11T14:00:00Z' });
  client.sessions = [updated, next];
  workspace.update(client, true);
  assert.deepEqual(workspace.groups.flatMap((group) => group.rows).map((row) => row.id), ['s3']);
  assert.equal(workspace.visibleRows()[0].state, 'idle');

  workspace.setQuickFilter('all');
  const refrozen = workspace.visibleRows().map((row) => row.id);
  assert.deepEqual(refrozen, ['s4', 's3']);
  client.sessions = [updated, next, agent('s5', { state: 'needs_you', updated_at: '2026-11-11T15:00:00Z' })];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), refrozen);
});

test('an empty list also stays frozen until an explicit filter recomputes it', async () => {
  const { workspace, client } = await opened({ sessions: [] });
  workspace.setReadingDetail(true);
  client.sessions = [agent('arrived')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows(), []);
  workspace.setQuickFilter('all');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['arrived']);
});

test('session actions refuse read-only, untrusted, and already unresolved paths', async () => {
  const readOnly = agent('readonly', { source_kind: 'codex_history', read_only_reason: 'History' });
  const pending = agent('pending');
  const { workspace, state, client } = await opened({ sessions: [readOnly, pending], machines: [machine('m1', 'pending')] });
  assert.equal(workspace.canRename(readOnly), false);
  assert.equal(workspace.canClose(readOnly), false);
  assert.equal(await workspace.rename(readOnly, 'No', ''), false);
  client.machines = [machine()]; workspace.update(client);
  const path = '/api/v1/sessions/m1/pending/rename';
  state.blocked.add(path);
  assert.equal(await workspace.rename(pending, 'No', ''), false);
  assert.equal(workspace.error, 'pending_action');
  assert.equal(state.calls.some((call) => call.path === path), false);
  client.profile.manager = 'personal'; state.manager = 'personal'; state.scope = 'hub:personal'; state.generation++;
  assert.equal(await workspace.close(pending), false);
});

test('a scope change while reading pins prevents the old pairing from writing', async () => {
  const { workspace, state, client } = await opened();
  const entered = new Promise((resolve) => { state.entered = resolve; });
  let release;
  const waiting = new Promise((resolve) => { release = resolve; });
  state.request = async (call) => {
    if (call.path === '/api/v1/pins' && call.method === 'GET') {
      state.entered();
      await waiting;
      return { spaces: ['m1/space'], sessions: ['m1/s1'] };
    }
    return undefined;
  };
  const pending = workspace.togglePin(client.sessions[0]);
  await entered;
  client.profile.manager = 'personal'; state.manager = 'personal'; state.scope = 'hub:personal'; state.generation++;
  release();
  assert.equal(await pending, false);
  assert.equal(state.calls.some((call) => call.method === 'PUT' && call.path === '/api/v1/pins'), false);
});

test('list filters and selection restore within their manager scope', async () => {
  const { workspace, state, client } = await opened();
  workspace.setQuickFilter('working');
  workspace.setGroupingMode('space');
  await new Promise((resolve) => setTimeout(resolve, 220));
  assert.equal(JSON.parse(state.views.get('hub:work:agent-workspace')).quick, 'working');
  assert.equal(JSON.parse(state.views.get('hub:work:agent-workspace')).grouping, 'space');
  state.scope = 'hub:personal'; state.manager = 'personal'; client.profile.manager = 'personal'; state.generation++;
  state.views.set('hub:personal:agent-workspace', JSON.stringify({ quick: 'needsYou', grouping: 'space', machine: 'p1', search: 'review', selected: '["session","personal","p1","p1-s1"]' }));
  await workspace.open(client);
  assert.equal(workspace.quickFilter, 'needsYou');
  assert.equal(workspace.groupingMode, 'space');
  assert.equal(workspace.machineFilter, 'p1');
  assert.equal(workspace.search, 'review');
  assert.equal(workspace.selectedKey, '["session","personal","p1","p1-s1"]');
});

test('the first loaded live catalog populates a cold Agents list before membership is frozen',async()=>{
 const {client}=clientFixture({sessions:[]});client.sessionsReady=false;const workspace=new AgentWorkspace();await workspace.open(client,true);assert.equal(workspace.groups.length,0);
 client.sessions=[agent('first')];client.sessionsReady=true;workspace.update(client,true);assert.deepEqual(workspace.groups.flatMap(g=>g.rows.map(r=>r.id)),['first']);
 client.sessions=[agent('later'),agent('first',{state:'needs_you'})];workspace.update(client,true);assert.deepEqual(workspace.groups.flatMap(g=>g.rows.map(r=>r.id)),['first']);
});

test('manager-qualified machine filters keep same-Hub same-ID owners independent', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub-a'; a.client.profile.manager = 'alpha'; a.state.manager = 'alpha';
  b.client.profile.id = 'hub-a'; b.client.profile.manager = 'beta'; b.state.manager = 'beta'; b.state.scope = 'hub-a:beta';
  a.client.directoryReady = true; a.client.directoryOwners = () => [a.client, b.client];
  a.client.machines[0].manager = 'alpha'; b.client.machines[0].manager = 'beta';
  a.client.directoryOwnerFor = (row) => row.manager === 'beta' ? b.client : a.client;
  const first = agent('shared-id', {manager: 'alpha'}), second = agent('shared-id', {manager: 'beta'});
  first.group = 'hub-a'; second.group = 'hub-a';
  a.client.directorySessions = [first, second];
  const w = new AgentWorkspace(); await w.open(a.client);
  w.setGroupingMode('machine');
  assert.deepEqual(w.groups.map((group) => group.id).sort(), ['machine/hub-a|alpha|m1', 'machine/hub-a|beta|m1']);
  assert.equal(w.groups.every((group) => group.rows.length === 1), true);
  await w.setMachineFilter(w.machineIdentity(b.client.machines[0]));
  assert.deepEqual(w.visibleRows().map((row) => row.manager), ['beta']);
});

test('per-Hub pins and allowed exceptions cannot affect another Hub with the same short key', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub-a'; b.client.profile.id = 'hub-b'; b.state.scope = 'hub-b:work';
  a.state.prefs.pins.sessions = ['m1/shared']; b.state.prefs.pins.sessions = [];
  a.state.prefs.mutes.spaces = ['m1/project']; b.state.prefs.mutes.allowed = ['m1/shared'];
  const first = agent('shared', {space_name: 'project'}), second = agent('shared', {space_name: 'project'});
  first.group = 'hub-a'; second.group = 'hub-b';
  a.client.directoryReady = true; a.client.directorySessions = [first, second];
  a.client.directoryOwners = () => [a.client, b.client]; a.client.directoryOwnerFor = (row) => row.group === 'hub-b' ? b.client : a.client;
  const w = new AgentWorkspace(); await w.open(a.client);
  assert.equal(w.isPinned(first), true); assert.equal(w.isPinned(second), false);
  assert.equal(w.isMuted(first), true); assert.equal(w.isMuted(second), false);
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows[0].group, 'hub-a');
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows.some((row) => row.group === 'hub-b'), false);
});

test('same-Hub manager preferences and held state remain isolated', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub'; a.client.profile.manager = 'alpha'; a.state.manager = 'alpha';
  b.client.profile.id = 'hub'; b.client.profile.manager = 'beta'; b.state.manager = 'beta'; b.state.scope = 'hub:beta';
  a.state.prefs.pins.sessions = ['m1/shared']; b.state.prefs.pins.sessions = [];
  a.state.prefs.mutes.allowed = ['m1/shared']; b.state.prefs.mutes.spaces = ['m1/project'];
  a.client.machines[0].manager = 'alpha'; b.client.machines[0].manager = 'beta';
  const first = agent('shared', {manager: 'alpha', state: 'idle', space_name: 'project'});
  const second = agent('shared', {manager: 'beta', state: 'idle', space_name: 'project'});
  first.group = 'hub'; second.group = 'hub';
  a.client.directoryReady = true; a.client.directorySessions = [first, second];
  a.client.directoryOwners = () => [a.client, b.client]; a.client.directoryOwnerFor = (row) => row.manager === 'beta' ? b.client : a.client;
  const w = new AgentWorkspace(); await w.open(a.client);
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows[0].manager, 'alpha');
  assert.equal(w.groups.find((group) => group.id === 'pinned').rows.length, 1);
  w.selectRow(first);
  assert.equal(w.isPinned(first), true); assert.equal(w.isPinned(second), false);
  assert.equal(w.isMuted(second), true); assert.equal(w.isMuted(first), false);
});

test('legacy empty manager machine filters keep the Hub group', async () => {
  const {workspace, client} = await opened();
  client.profile.id = 'hub';
  client.sessions[0].group = 'hub';
  await workspace.setMachineFilter(JSON.stringify(['hub', '', 'm1']));
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['s1']);
});

test('time-bounded searches use caller time and group titles remain visible', async () => {
  const {workspace} = await opened({sessions: [agent('s1', {updated_at: new Date().toISOString()})]});
  workspace.setSearch('since:24h');
  assert.equal(workspace.visibleRows().length, 1);
  workspace.setGroupingMode('machine');
  assert.equal(workspace.groups[0].machineName, 'Computer m1');
  workspace.setGroupingMode('space');
  assert.equal(workspace.groups[0].machineName, 'repo');
});

test('native failure retains the same displayed rows and action authority', async () => {
  const {workspace, client} = await opened();
  const original = client.sessions[0], ticket = workspace.actionTicket(original);
  workspace.adapter = {project() {throw new Error('native unavailable');}};
  client.sessions = [agent('s1', {title: 'Replacement target'})];
  workspace.update(client);
  assert.equal(workspace.visibleRows()[0], original);
  assert.equal(ticket(), true);
  assert.equal(workspace.error, 'failed');
});

test('a pinned offline row survives frozen reading and current fold visibility', async () => {
  const pinned = agent('pinned', {state: 'offline', status: 'offline'});
  const {workspace, client} = await opened({sessions: [pinned]});
  client.sessions[0].state = 'offline';
  assert.equal(await workspace.togglePin(pinned), true);
  workspace.setReadingDetail(true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['pinned']);
  assert.equal(workspace.groups.some((group) => group.id === 'pinned'), true);
});

test('frozen reconstruction applies core visible rows, not every frozen idle row', async () => {
  const idle = agent('idle', {state: 'idle', status: 'idle'});
  const {workspace} = await opened({sessions: [idle]});
  workspace.setReadingDetail(true);
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['idle']);
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows(), []);
});

test('archived history respects folded groups before and after expansion', async () => {
  const archived = agent('archive-folded', {archived: true, state: 'offline', status: 'offline'});
  const {workspace, client} = await opened({history: [archived]});
  client.features = ['agent_session_archives'];
  await workspace.setHistory(true);
  assert.deepEqual(workspace.visibleRows(), []);
  assert.ok(workspace.groups.some((group) => group.totalRowCount > 0 && group.folded));
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['archive-folded']);
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows(), []);
});

test('frozen reading drops deleted and offline rows without admitting new members', async () => {
  const first = agent('keep'), second = agent('offline');
  const {workspace, client} = await opened({sessions: [first, second]});
  workspace.setReadingDetail(true);
  client.sessions = [agent('keep', {state: 'idle'}), agent('new-member')];
  workspace.toggleFold('state/idle');
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['keep']);
  client.sessions = [agent('keep', {state: 'offline'}), agent('new-member')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows(), []);
});

test('forgetting a Hub refolds frozen groups without resurrecting rows', async () => {
  const a = clientFixture(), b = clientFixture();
  a.client.profile.id = 'hub-a'; b.client.profile.id = 'hub-b'; b.state.scope = 'hub-b:work';
  const first = agent('first'), second = agent('second');
  first.group = 'hub-a'; second.group = 'hub-b';
  a.client.directoryReady = true; a.client.directorySessions = [first, second];
  a.client.directoryOwners = () => [a.client, b.client]; a.client.directoryOwnerFor = (row) => row.group === 'hub-b' ? b.client : a.client;
  const w = new AgentWorkspace(); await w.open(a.client, true);
  w.toggleFold('state/working');
  w.toggleFold('state/working');
  w.forgetHub('hub-b');
  assert.deepEqual(w.visibleRows().map((row) => row.group), ['hub-a']);
  w.toggleFold('state/working');
  assert.ok(w.visibleRows().every((row) => row.group !== 'hub-b'));
});

test('archived sessions get a registry entry for machine grouping', async () => {
  const a = clientFixture({history: [agent('archived', {archived: true, state: 'working', machine_id: 'retired'})]});
  a.client.features = ['agent_session_archives'];
  const w = new AgentWorkspace(); await w.open(a.client);
  await w.setHistory(true);
  w.setGroupingMode('machine');
  assert.ok(w.groups.some((group) => group.id.includes('retired')));
  assert.equal(w.groups[0].machineName, 'retired');
});

test('group folds use named native icons and idle expansion retains its count', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  const header=source.slice(source.indexOf('  @Builder groupHeader('),source.indexOf('  private groupCounts('));
  assert.match(header,/Button\(\{ type: ButtonType.Circle \}\)/);
  assert.match(header,/binding\.value\.folded \? Symbols\.down : Symbols\.up/);
  assert.match(header,/attributeModifier\(ButtonStyles\.icon\)/);
  assert.ok(header.includes("tr(binding.value.folded ? 'agent_expand' : 'agent_collapse')"));
  assert.ok(header.includes('this.toggleGroup(binding.value.id)'));
  assert.ok(source.includes("String(binding.value.totalRowCount - binding.value.rows.length) + ' ' + tr('agent_group_more_idle')"));
  assert.ok(source.includes('this.toggleIdle(binding.value.id)'));
  assert.ok(source.includes("private toggleIdle(id: string): void { Haptics.tap(); if (id === 'state/idle') this.workspace.toggleFold(id); else this.workspace.toggleIdleRows(id); }"));
});

test('merged rows with identical runtime ids isolate pin, mute and mutation ownership',async()=>{
 const a=clientFixture(),b=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';
 const first=agent('same'),second=agent('same');first.group='hub-a';second.group='hub-b';a.client.sessions=[first];b.client.sessions=[second];
 a.client.directoryReady=true;a.client.directorySessions=[first,second];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:a.client;
 a.client.groupName=row=>row.group==='hub-b'?'Second Hub':'First Hub';
 const w=new AgentWorkspace();await w.open(a.client);assert.equal(w.visibleRows().length,2);
 b.state.calls=[];a.state.calls=[];assert.equal(await w.togglePin(second),true);
 assert.equal(b.state.calls.some(c=>c.method==='PUT'&&c.path==='/api/v1/pins'),true);assert.equal(a.state.calls.length,0);
 assert.equal(w.isPinned(second),true);assert.equal(w.isPinned(first),false);
 b.state.calls=[];assert.equal(await w.rename(second,'Renamed',''),true);assert.equal(b.state.calls[0].path,'/api/v1/sessions/m1/same/rename');assert.equal(a.state.calls.length,0);
 w.selectExternal(second);w.update(a.client);assert.equal(w.isSelected(second),true);assert.equal(w.isSelected(first),false);
 assert.ok(w.rowMetadata(second)[0].includes('Second Hub'));
});
test('a mutation journal admission is revoked when its captured Hub goes away',async()=>{
 const f=clientFixture(),w=new AgentWorkspace();await w.open(f.client);let dispatched=0;
 f.repo.call=async(path,method,body,op,admit)=>{f.state.generation++;if(admit&&!admit())throw Error('request_updated');dispatched++;return{};};
 assert.equal(await w.rename(f.client.sessions[0],'changed',''),false);assert.equal(dispatched,0);
});

test('a background directory load cannot bypass established reading membership',async()=>{const first=agent('frozen-a'),second=agent('frozen-b'),{workspace,client}=await opened({sessions:[first,second]});workspace.setReadingDetail(true);client.directoryReady=true;client.directoryLoading=true;workspace.toggleFold('state/idle');client.directorySessions=[agent('new-member'),agent('frozen-a',{state:'idle'}),second];workspace.update(client,true);assert.deepEqual(workspace.visibleRows().map(row=>row.id),['frozen-a','frozen-b']);assert.equal(workspace.visibleRows()[0].state,'idle');});
test('forgetting a foreign Hub removes frozen rows, history, selection and action authority',async()=>{const a=clientFixture(),b=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';const first=agent('same'),second=agent('same');first.group='hub-a';second.group='hub-b';a.client.directoryReady=true;a.client.directorySessions=[first,second];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:a.client;const w=new AgentWorkspace();await w.open(a.client,true);w.selectExternal(second);w.historyRows=[second];const ticket=w.actionTicket(second);assert.equal(ticket(),true);w.forgetHub('hub-b');assert.deepEqual(w.visibleRows().map(row=>row.group),['hub-a']);assert.equal(w.selectedKey,'');assert.equal(w.historyRows.length,0);assert.equal(ticket(),false);});
test('an archive-only foreign owner is included and its open confirmation cannot survive reconnect',async()=>{const a=clientFixture(),b=clientFixture({history:[agent('archived',{archived:true,state:'offline'})]});a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';b.client.features=['agent_session_archives'];b.client.sessions=[];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:a.client;const w=new AgentWorkspace();await w.open(a.client);assert.equal(w.historySupported(),true);await w.setHistory(true);assert.ok(b.state.calls.some(call=>call.path.startsWith('/api/v1/sessions/archived')));w.toggleFold('state/idle');assert.equal(w.visibleRows().length,1,'the archived offline Agent remains readable history');await w.setHistory(false);const row=a.client.sessions[0],ticket=w.actionTicket(row);assert.equal(ticket(),true);a.state.generation++;assert.equal(ticket(),false);});

const nativePageSource=readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets',import.meta.url),'utf8');
function nativePageMethod(name){const start=nativePageSource.search(new RegExp('^  private (?:async )?'+name+'\\(','m'));assert.ok(start>=0);const body=nativePageSource.indexOf('{',start);let depth=1,quote='',escape=false,at=body+1;for(;at<nativePageSource.length&&depth;at++){const c=nativePageSource[at];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}return nativePageSource.slice(start,at);}
const AgentNativeActions=new Function('tr','Wire','return '+stripTypeScriptTypes('class AgentNativeActions{'+['openRename','saveRename','confirmClose','confirmDelete'].map(nativePageMethod).join('\n')+'}'))(key=>key,(await import('../entry/src/main/ets/models/Wire.ets')).Wire);
test('native rename, close and delete retain the owner generation captured when the editor or confirmation opened',async()=>{
 for(const action of ['rename','close','delete']){
  const a=clientFixture(),b=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';b.state.scope='hub-b:work';const row=agent('foreign',{state:'offline',status:'offline'});row.group='hub-b';a.client.directoryReady=true;a.client.directorySessions=[row];a.client.directoryOwners=()=>[a.client,b.client];a.client.directoryOwnerFor=()=>b.client;
  const workspace=new AgentWorkspace();await workspace.open(a.client);b.state.calls=[];const page=new AgentNativeActions();page.workspace=workspace;page.model=a.client;page.active=true;let dialog;page.getUIContext=()=>({showAlertDialog:value=>dialog=value});
  if(action==='rename'){page.openRename(row);page.renameTitle='Changed';b.state.generation++;await page.saveRename();assert.equal(page.editorOpen,false);}
  else{page[action==='close'?'confirmClose':'confirmDelete'](row);b.state.generation++;dialog.secondaryButton.action();await new Promise(resolve=>setImmediate(resolve));}
  assert.equal(b.state.calls.some(call=>call.method!=='GET'),false);
 }
});

test('forget cancels an aggregate history pass even after that Hub rows were collected',async()=>{const a=clientFixture(),b=clientFixture({history:[agent('b-history')]}),c=clientFixture();a.client.profile.id='hub-a';b.client.profile.id='hub-b';c.client.profile.id='hub-c';b.state.scope='hub-b:work';c.state.scope='hub-c:work';b.client.features=c.client.features=['agent_session_archives'];a.client.directoryOwners=()=>[a.client,b.client,c.client];a.client.directoryOwnerFor=row=>row.group==='hub-b'?b.client:row.group==='hub-c'?c.client:a.client;let release,started;const gate=new Promise(r=>release=r),entered=new Promise(r=>started=r);c.state.request=async call=>{if(call.path.startsWith('/api/v1/sessions/archived')){started();return gate;}};const w=new AgentWorkspace();await w.open(a.client);const history=w.setHistory(true);await entered;w.forgetHub('hub-b');release({sessions:[agent('c-history').data()]});await history;assert.equal(w.loading,false);assert.equal(w.historyRows.some(row=>row.group==='hub-b'),false);assert.equal(w.visibleRows().some(row=>row.group==='hub-b'),false);});
test('turning history off invalidates its pending response before restoring the live list',async()=>{const f=clientFixture();f.client.features=['agent_session_archives'];let release,started;const gate=new Promise(r=>release=r),entered=new Promise(r=>started=r);f.state.request=async call=>{if(call.path.startsWith('/api/v1/sessions/archived')){started();return gate;}};const w=new AgentWorkspace();await w.open(f.client);const old=w.setHistory(true);await entered;await w.setHistory(false);release({sessions:[agent('stale-archive').data()]});await old;assert.equal(w.history,false);assert.deepEqual(w.visibleRows().map(row=>row.id),['s1']);assert.equal(w.loading,false);});

test('detail activation while opening cannot freeze an empty list before its saved view loads', async () => {
  const { client } = clientFixture(), workspace = new AgentWorkspace();
  const opening = workspace.open(client, true);
  workspace.setReadingDetail(true);
  await opening;
  workspace.toggleFold('state/idle');
  assert.deepEqual(workspace.visibleRows().map(row => row.id), ['s1']);
  client.sessions = [agent('s1', { state: 'idle' }), agent('new-later')];
  workspace.update(client, true);
  assert.deepEqual(workspace.visibleRows().map(row => row.id), ['s1']);
  assert.equal(workspace.visibleRows()[0].state, 'idle');
  workspace.hide();
});


test('leaving a retained list revokes an in-flight pin while keeping grouping preferences', async () => {
 const {workspace,client,state}=await opened();workspace.setGroupingMode('machine');
 let release,entered;const ready=new Promise(r=>entered=r),gate=new Promise(r=>release=r);
 state.request=async call=>{if(call.path==='/api/v1/pins' && call.method==='GET'){entered();await gate;return {spaces:[],sessions:[]};}};
 const pending=workspace.togglePin(client.sessions[0]);await ready;workspace.revokeRowActions();release();
 assert.equal(await pending,false);assert.equal(state.calls.some(c=>c.method==='PUT'),false);assert.equal(workspace.groupingMode,'machine');assert.equal(workspace.isBoundTo(client),true);
});


test('a retired pin completion cannot release the busy lock of a new list operation', async () => {
 const {workspace,client,state}=await opened({sessions:[agent('s1'),agent('s2')]});
 const releases=[],arrivals=[];state.request=async call=>{if(call.path==='/api/v1/pins'&&call.method==='GET'){const n=arrivals.length;let release;const gate=new Promise(r=>release=r);releases.push(release);arrivals.push(n);await gate;return {spaces:[],sessions:[]};}};
 const old=workspace.togglePin(client.sessions[0]);while(arrivals.length<1)await new Promise(r=>setImmediate(r));
 workspace.revokeRowActions();const fresh=workspace.togglePin(client.sessions[1]);while(arrivals.length<2)await new Promise(r=>setImmediate(r));
 releases[0]();assert.equal(await old,false);assert.equal(workspace.busy,true,'old finally must not release new operation');
 releases[1]();assert.equal(await fresh,true);assert.equal(workspace.busy,false);assert.equal(state.calls.filter(c=>c.method==='PUT').length,1);
});

test('machine idle rows expand, survive a directory update, and collapse again', async () => {
  const {workspace,client} = await opened({sessions:[agent('busy'),agent('quiet',{state:'idle',status:'idle'})]});
  workspace.setGroupingMode('machine');
  const group = workspace.groups.find(item => item.id.startsWith('machine/'));
  assert.deepEqual(group.rows.map(item => item.id), ['busy']);
  workspace.toggleIdleRows(group.id);
  workspace.update(client);
  assert.equal(workspace.idleRowsExpanded(group.id),true);
  assert.deepEqual(workspace.visibleRows().map(item=>item.id).sort(),['busy','quiet']);
  workspace.toggleIdleRows(group.id);
  assert.deepEqual(workspace.visibleRows().map(item=>item.id),['busy']);
});

test('Needs you sits above computers and spaces, even when their folds hide the row, and counts match', async () => {
  const asking = agent('asking', { state: 'needs_you', status: 'needs_you', machine_id: 'm2' });
  const { workspace } = await opened({ sessions: [agent('busy'), asking, agent('quiet', { state: 'idle', status: 'idle' })],
    machines: [machine('m1'), machine('m2')] });
  assert.ok(!workspace.groups.some((group) => group.id === 'needs'), 'By Status has its own Needs you section');
  assert.equal(workspace.counts.needsYou, 1);
  assert.equal(workspace.counts.working, 1);
  workspace.setGroupingMode('machine');
  const folded = workspace.groups.find((group) => group.id === 'machine/work|m2');
  workspace.toggleFold(folded.id);
  const needs = workspace.groups[0];
  assert.equal(needs.id, 'needs');
  assert.deepEqual(needs.rows.map((row) => row.id), ['asking']);
  const machineGroup = workspace.groups.find((group) => group.id === 'machine/work|m2');
  assert.equal(machineGroup.folded, true);
  assert.equal(machineGroup.rows.length, 0);
  assert.equal(machineGroup.stateCounts.needs_you, 1, 'the folded computer still says what it hides');
  assert.deepEqual(workspace.groups.find((group) => group.id === 'machine/work|m1').stateCounts, { working: 1, idle: 1 });
  assert.equal(workspace.counts.needsYou, needs.rows.length, 'the chip and the section show one number');
  assert.deepEqual(workspace.visibleRows().map((row) => row.id), ['busy', 'asking'], 'each row once');
  workspace.setGroupingMode('space');
  assert.equal(workspace.groups[0].id, 'needs');
  workspace.setQuickFilter('working');
  assert.ok(!workspace.groups.some((group) => group.id === 'needs'), 'another quick filter shows only its own rows');
  assert.equal(workspace.counts.needsYou, 1, 'chip counts are taken before the filter narrows the list');
  workspace.setQuickFilter('all');
  workspace.setSearch('busy');
  assert.ok(!workspace.groups.some((group) => group.id === 'needs'), 'search results stand alone, as iOS');
});

test('related chats fold by actual parent and open directly during local search or Needs You',async()=>{
  const main=agent('main',{state:'idle'}), side=agent('side',{title:'Branch discussion',parent_agent_id:'main',state:'needs_you'}),
    codex=agent('delegate',{title:'Review snapshots',parent_session_id:'side',root_session_id:'main',is_subagent:true}),
    orphan=agent('orphan',{title:'Branch discussion',parent_agent_id:'elsewhere'}),
    embedded=agent('review',{subagents:[{id:'local',session_id:'review@local',description:'Inspect byte length',running:true}]});
  const {workspace}=await opened({sessions:[main,side,codex,orphan,embedded]});
  const ids=()=>workspace.visibleRows().map(row=>row.id);
  assert.ok(ids().includes('main') && ids().includes('orphan'));
  assert.ok(!ids().includes('side') && !ids().includes('delegate'));
  assert.equal(workspace.relations.root(codex).id,'main');
  workspace.toggleFamily(main);
  assert.deepEqual(new Set(ids().slice(ids().indexOf('main')+1,ids().indexOf('main')+3)),new Set(['side','delegate']));
  workspace.toggleFamily(main); assert.ok(!ids().includes('side'));
  workspace.setQuickFilter('needsYou');assert.deepEqual(ids(),['side']);
  workspace.setQuickFilter('all');workspace.setSearch('snapshots');assert.deepEqual(ids(),['delegate']);
  workspace.setSearch('byte length');assert.deepEqual(ids(),['review@local']);
  const child=workspace.visibleRows()[0]; assert.equal(child.data().read_only,true); assert.equal(workspace.relations.root(child).id,'review');
});
test('relations isolate machines, Hubs and managers and keep cycles and unrelated forks independent',async()=>{
  const {SessionRelations}=await import('../entry/src/main/ets/models/SessionRelations.ets');
  const rows=[agent('root'),agent('foreign',{parent_session_id:'root',machine_id:'other'}),agent('otherOwner',{parent_session_id:'root',manager:'different'}),
    agent('side',{parent_agent_id:'root'}),agent('a',{parent_session_id:'b'}),agent('b',{parent_session_id:'a'}),agent('fork',{forked_from_id:'root'}),
    agent('waiting',{asking_parent_session_id:'root',state:'needs_you'})];
  rows[3].group='hub-two';
  const graph=new SessionRelations(rows);
  assert.deepEqual(graph.children(rows[0]).map(r=>r.id),['waiting']);
  for(const r of rows.slice(1,7)) assert.equal(graph.root(r),undefined);
});

test('Claude workflow transcripts are searchable, read-only and deduplicated against ordinary agents and full sessions', async () => {
  const parent = agent('main', {
    subagents: [{id:'regular', session_id:'main@regular', description:'Regular child', running:false}],
    workflows: [{run_id:'wf_one', agents:[
      {id:'review', session_id:'main@review', label:'Review schema', status:'running'},
      {id:'regular', session_id:'main@regular', label:'Duplicate child'},
      {id:'full', session_id:'main@full', label:'Duplicate full row'},
      {id:'other', session_id:'elsewhere@other', label:'Unrelated record'}
    ]}]
  });
  const full = agent('main@full', {parent_session_id:'main', title:'Registered child'});
  const {workspace} = await opened({sessions:[parent, full]});
  const children = workspace.relations.children(parent);
  assert.deepEqual(children.map(row => row.id).sort(), ['main@full','main@regular','main@review']);
  assert.equal(children.find(row => row.id === full.id), full);
  workspace.setSearch('Review schema');
  const [review] = workspace.visibleRows();
  assert.equal(review.id, 'main@review');
  assert.equal(review.state, 'working');
  assert.equal(review.data().read_only, true);
  assert.deepEqual(review.data().capabilities, {read:true});
  assert.equal(workspace.relations.root(review).id, 'main');
});

test('offline search results retain their real state and respect machine, quick and readability filters', async () => {
  const main = agent('main');
  const old = {parent_agent_id:'main', title:'Find offline transcript', state:'offline', status:'offline'};
  const side = agent('side', {...old, capabilities:{read:true}});
  const {workspace} = await opened({sessions:[main, side, agent('other', {...old, machine_id:'m2'}),
    agent('denied', {...old, capabilities:{read:false}}), agent('hidden', {...old, hidden_by_directory:true})],
    machines:[machine('m1'),machine('m2')]});
  workspace.toggleFamily(main);
  assert.ok(workspace.visibleRows().includes(side));
  await workspace.setMachineFilter('m1');
  workspace.setSearch('Find offline transcript');
  for (const grouping of ['state','machine','space']) {
    workspace.setGroupingMode(grouping);
    assert.deepEqual(workspace.visibleRows().map(row => [row.id,row.state]), [['side','offline']]);
    workspace.selectRow(side);
    assert.equal(workspace.isSelected(side), true);
  }
  workspace.setQuickFilter('needsYou');
  assert.equal(workspace.visibleRows().length, 0);
  workspace.setQuickFilter('all');
  assert.equal(await workspace.togglePin(side), true);
  assert.deepEqual(workspace.visibleRows().map(row => row.id), ['side']);
});

test('pinned children have one direct entry, and unpinning puts them back under their main chat', async () => {
  const main = agent('main'), side = agent('side',{parent_agent_id:'main'}), task = agent('task',{parent_session_id:'main'});
  const {workspace,client} = await opened({sessions:[main,side,task]});
  workspace.toggleFamily(main);
  workspace.update(client, true);
  assert.equal(await workspace.togglePin(side), true);
  const pinned = () => workspace.groups.find(group => group.id === 'pinned')?.rows.map(row => row.id);
  assert.deepEqual(pinned(), ['side']);
  assert.deepEqual(workspace.foldedChildren(main).map(row => row.id), ['task']);
  assert.equal(workspace.visibleRows().filter(row => row.id === 'side').length, 1);
  workspace.toggleFamily(main);
  assert.ok(workspace.visibleRows().includes(side));
  assert.equal(await workspace.togglePin(side), true);
  assert.equal(pinned(), undefined);
  assert.ok(!workspace.visibleRows().includes(side));
  workspace.toggleFamily(main);
  assert.ok(workspace.visibleRows().includes(side));
});

test('cached child activity follows the connection of its own Hub and machine', async () => {
  const makeParent = (group,manager) => {
    const row = agent('main',{manager, state:'idle', status:'idle', workflows:[
      {agents:[{id:'worker',session_id:'main@worker',label:'Cached worker',running:true}]}
    ]});
    row.group = group;
    return row;
  };
  const disconnected = makeParent('hub-a','one'), online = makeParent('hub-b','two');
  const {client,workspace} = await opened({sessions:[disconnected,online]});
  const a = clientFixture().client, b = clientFixture().client;
  a.profile.id = 'hub-a'; a.profile.manager = 'one'; a.connected = false;
  b.profile.id = 'hub-b'; b.profile.manager = 'two';
  client.directoryOwners = () => [a,b];
  client.directoryOwnerFor = row => row.group === 'hub-a' ? a : b;
  workspace.setQuickFilter('working');
  assert.deepEqual(workspace.visibleRows().map(row => row.group), ['hub-b']);
  assert.equal(workspace.relations.find(disconnected).state, 'offline');
  assert.equal(workspace.relations.children(disconnected)[0].state, 'offline');
  a.connected = true;
  a.machines = [ResourceRow.from({id:'m1',manager:'one',online:false},'machine')];
  workspace.update(client);
  assert.deepEqual(workspace.visibleRows().map(row => row.group), ['hub-b']);
  a.machines = [machine('m1','personal','one')];
  workspace.update(client);
  assert.deepEqual(workspace.visibleRows().map(row => row.group).sort(), ['hub-a','hub-b']);
});

test('every path into a parent cycle stays standalone in any directory order', async () => {
  const {SessionRelations} = await import('../entry/src/main/ets/models/SessionRelations.ets');
  const a=agent('a',{parent_session_id:'b'}), b=agent('b',{parent_session_id:'a'});
  const viaA=agent('via-a',{parent_agent_id:'a'}), viaB=agent('via-b',{parent_session_id:'b'});
  const leaf=agent('leaf',{parent_session_id:'via-a'});
  for (const rows of [[a,b,viaA,viaB,leaf],[leaf,viaB,a,viaA,b],[viaA,a,b,leaf,viaB]]) {
    const graph=new SessionRelations(rows);
    for(const row of rows) { assert.equal(graph.root(row),undefined); assert.deepEqual(graph.children(row),[]); }
  }
});

test('history list publishes the exact owner archive used to navigate an archived family', async () => {
  const main=agent('arch-main',{archived:true,state:'offline',status:'offline'});
  const task=agent('arch-task',{archived:true,state:'offline',status:'offline',parent_session_id:'arch-main'});
  const {client,workspace}=await opened({sessions:[],history:[main,task]});
  client.features=['agent_session_archives'];
  await workspace.setHistory(true);
  workspace.toggleFold('state/idle');
  const listed = workspace.visibleRows()[0];
  assert.equal(listed.id, main.id);
  const {sessionRelations}=await import('../entry/src/main/ets/models/SessionRelations.ets');
  const detail=sessionRelations(client.archives.rows);
  const [child]=detail.children(listed);
  assert.equal(child.id, task.id);
  assert.equal(detail.root(child).key(), listed.key());
  assert.equal(client.sessions.length, 0);
});

test('archive refresh merges machines without reviving stale or foreign rows', async () => {
  const archive=new SessionArchive();
  const entry=(session_id,machine_id) => ({session_id,machine_id,archived:true,manager:'work'});
  let finishAll;
  const pending=archive.read(() => new Promise(resolve => {finishAll=resolve;}),'hub','work','',()=>true);
  await archive.read(async()=>({sessions:[entry('new','m1'),entry('wrong-machine','m2'),{...entry('foreign','m1'),manager:'other'}]}),
    'hub','work','m1',()=>true);
  finishAll({sessions:[entry('stale','m1'),entry('second','m2')]});
  await pending;
  assert.deepEqual(archive.rows.map(row => row.id).sort(), ['new','second']);
  await archive.read(async()=>({sessions:[]}),'hub','work','m1',()=>false);
  assert.deepEqual(archive.rows.map(row => row.id).sort(), ['new','second']);
  await archive.read(async()=>({sessions:[]}),'hub','work','m1',()=>true);
  assert.deepEqual(archive.rows.map(row => row.id), ['second']);
});

test('a folded side chat that needs you puts its parent in Needs you, the header counts and the chip, once', async () => {
  const main = agent('main', { state: 'idle', status: 'idle', machine_id: 'm2' });
  const side = agent('side', { title: 'Branch discussion', parent_agent_id: 'main', state: 'needs_you', status: 'needs_you', machine_id: 'm2' });
  const { workspace } = await opened({ sessions: [agent('busy'), main, side], machines: [machine('m1'), machine('m2')] });
  assert.equal(workspace.familyState(main), 'needs_you');
  assert.equal(workspace.counts.needsYou, 1, 'one family, one number');
  assert.deepEqual(workspace.groups.find((group) => group.id === 'state/needs').rows.map((row) => row.id), ['main']);
  workspace.setGroupingMode('machine');
  const computer = workspace.groups.find((group) => group.id === 'machine/work|m2');
  workspace.toggleFold(computer.id);
  const needs = workspace.groups[0];
  assert.equal(needs.id, 'needs');
  assert.deepEqual(needs.rows.map((row) => row.id), ['main'], 'the parent row, never the folded child as well');
  assert.equal(workspace.counts.needsYou, needs.rows.length);
  assert.equal(workspace.groups.find((group) => group.id === 'machine/work|m2').stateCounts.needs_you, 1);
  workspace.toggleFamily(main);
  assert.deepEqual(workspace.displayRows(needs).map((row) => row.id), ['main'], 'an opened family does not open inside Needs you');
  workspace.setQuickFilter('needsYou');
  assert.equal(workspace.familyState(main), 'idle', 'without folding the child is its own row');
  assert.deepEqual(workspace.groups[0].rows.map((row) => row.id), ['side']);
});

test('Interrupt is offered on a running or waiting turn its source can stop, and posts the existing route', async () => {
  const running = agent('run');
  const waiting = agent('wait', { state: 'needs_you', status: 'needs_you' });
  const idle = agent('rest', { state: 'idle', status: 'idle' });
  const refused = agent('refused', { source_kind: 'claude_desktop', capabilities: { read: true, send: true, interrupt: false } });
  const allowed = agent('allowed', { source_kind: 'claude_desktop', capabilities: { read: true, send: true, interrupt: true } });
  const locked = agent('locked', { read_only_reason: 'history' });
  const { workspace, state } = await opened({ sessions: [running, waiting, idle, refused, allowed, locked] });
  assert.deepEqual([running, waiting, idle, refused, allowed, locked].map((row) => workspace.canInterrupt(row)),
    [true, true, false, false, true, false]);
  state.calls.length = 0;
  assert.equal(await workspace.interrupt(idle), false);
  assert.equal(await workspace.interrupt(running), true);
  assert.deepEqual(state.calls.filter((call) => call.path.includes('/sessions/')).map((call) => [call.method, call.path, call.body]), [
    ['POST', '/api/v1/sessions/m1/run/interrupt', {}]
  ]);
  state.blocked.add('/api/v1/sessions/m1/wait/interrupt');
  assert.equal(await workspace.interrupt(waiting), false, 'a request already in flight is not sent twice');
});

test('Mark all as read under Unread marks every listed row at once and puts back a row the hub refuses', async () => {
  const first = agent('first', { state: 'unread', status: 'done' });
  const second = agent('second', { state: 'unread', status: 'done', machine_id: 'm2' });
  const quiet = agent('keep-muted', { state: 'unread', status: 'done' });
  const { workspace, state } = await opened({ sessions: [first, second, quiet, agent('busy')], machines: [machine('m1'), machine('m2')] });
  assert.deepEqual(workspace.unreadToMark(), [], 'only under the Unread filter');
  workspace.setGroupingMode('machine');
  workspace.setQuickFilter('unread');
  workspace.toggleFold('machine/work|m2');
  assert.equal(workspace.visibleRows().some((row) => row.id === 'second'), false);
  assert.deepEqual(workspace.unreadToMark().map((row) => row.id).sort(), ['first', 'second'], 'a folded group counts; a muted row does not');
  assert.equal(workspace.counts.unread, 2, 'the chip counts the same rows');

  let release; const held = new Promise((resolve) => { release = resolve; });
  state.request = async (call) => {
    if (!call.path.endsWith('/seen')) return undefined;
    await held;
    if (call.path.includes('/second/')) throw new Error('refused');
    return { ok: true };
  };
  state.calls.length = 0;
  const done = workspace.markAllSeen();
  assert.equal(workspace.counts.unread, 0, 'the rows leave Unread before the hub answers');
  assert.deepEqual(workspace.unreadToMark(), []);
  assert.equal(workspace.busy, true);
  release();
  assert.equal(await done, false);
  assert.deepEqual(state.calls.map((call) => [call.method, call.path]).sort(), [
    ['POST', '/api/v1/sessions/m1/first/seen'], ['POST', '/api/v1/sessions/m2/second/seen']
  ]);
  assert.equal(workspace.latestSourceRows.find((row) => row.id === 'first').state, 'idle');
  assert.equal(workspace.latestSourceRows.find((row) => row.id === 'second').state, 'unread', 'the refused row comes back');
  assert.equal(workspace.counts.unread, 1);
  assert.equal(workspace.error, 'failed');
  assert.equal(workspace.busy, false);
});

test('Collapse all folds every group but Needs you, Expand all opens them with Idle, and neither acts during search', async () => {
  const sessions = [agent('w1'), agent('n1', { state: 'needs_you', status: 'needs_you', machine_id: 'm2' }),
    agent('i1', { state: 'idle', status: 'idle' }), agent('u1', { state: 'unread', status: 'done', machine_id: 'm2' })];
  const { workspace } = await opened({ sessions, machines: [machine('m1'), machine('m2')] });
  workspace.setGroupingMode('machine');
  assert.equal(workspace.groups[0].id, 'needs');
  assert.equal(workspace.canFoldAll(false), false, 'nothing is folded yet');
  workspace.foldAll(true);
  assert.deepEqual(workspace.groups.filter((group) => group.id !== 'needs').map((group) => group.folded), [true, true]);
  assert.deepEqual(workspace.groups[0].rows.map((row) => row.id), ['n1'], 'Needs you stays open');
  assert.equal(workspace.canFoldAll(true), false);
  assert.equal(workspace.canFoldAll(false), true);

  workspace.setGroupingMode('state');
  const byStatus = (id) => workspace.groups.find((group) => group.id === id);
  assert.equal(byStatus('state/idle').folded, true, 'Idle folds by default');
  workspace.foldAll(false);
  assert.equal(workspace.groups.every((group) => !group.folded), true, 'Expand all opens Idle too');
  workspace.foldAll(true);
  assert.equal(byStatus('state/needs').folded, false, 'By Status keeps its Needs you group open');
  assert.equal(byStatus('state/idle').folded, true);
  assert.equal(byStatus('state/working').folded, true);

  workspace.setSearch('w1');
  assert.equal(workspace.canFoldAll(true), false);
  assert.equal(workspace.canFoldAll(false), false);
});

test('the quick filter chips leave out a muted row that is unread or working, as the filters list it (iOS filterCounts)', async () => {
  const sessions = [agent('first', { state: 'unread', status: 'done' }), agent('keep-muted', { state: 'unread', status: 'done' }),
    agent('busy'), agent('muted-busy', { space_name: 'space' }), agent('ask', { state: 'needs_you', status: 'needs_you', space_name: 'space' })];
  const { workspace } = await opened({ sessions });
  assert.equal(workspace.isMuted(sessions[3]), true, 'muted through its space');
  assert.deepEqual([workspace.counts.unread, workspace.counts.working, workspace.counts.needsYou], [1, 1, 1], 'a muted row that needs you still counts');
  for (const quick of ['unread', 'working']) {
    workspace.setQuickFilter(quick);
    assert.equal(workspace.visibleRows().length, workspace.counts[quick], quick + ': the chip says what the filter lists');
  }
  workspace.setSearch('Agent first');
  assert.equal(workspace.counts.unread, 1);
});
