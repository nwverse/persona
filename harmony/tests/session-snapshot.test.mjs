import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
registerHooks({ resolve(specifier, context, next) {
  if (context.parentURL?.endsWith('.ts') && specifier.startsWith('.') && !/\.\w+$/.test(specifier))
    return { url: new URL(specifier + '.ts', context.parentURL).href, shortCircuit: true };
  return next(specifier, context);
} });
const { encodeSessionSnapshot, decodeSessionSnapshot, compactRow, clipText, SNAPSHOT_TEXT_LIMIT } =
  await import('../entry/src/main/ets/core/SessionSnapshot.ts');
const { utf8Bytes } = await import('../entry/src/main/ets/core/TranscriptCache.ts');

const row = (id, raw = {}, title = 'Session ' + id) =>
  ({ id, title, detail: '', kind: 'session', state: 'idle', manager: 'personal', group: 'hub', machine: 'desk', session: id, path: '/p',
    raw: JSON.stringify({ session_id: id, title, ...raw }) });

test('rows keep short display text and every nested value, so a pasted-prompt title cannot fill the snapshot', () => {
  const prompt = '把这份文档整理成要点。'.repeat(5000), held = [{ id: 'm1', text: 'x'.repeat(2000) }];
  const compact = compactRow(row('a', { last_text: prompt, undelivered: held, efforts: ['low', 'high'] }, prompt));
  assert.equal(compact.title.length, SNAPSHOT_TEXT_LIMIT + 1);
  assert.ok(compact.title.endsWith('…'));
  const raw = JSON.parse(compact.raw);
  assert.equal(raw.title, compact.title);
  assert.equal(raw.last_text.length, SNAPSHOT_TEXT_LIMIT + 1);
  assert.deepEqual(raw.undelivered, held, 'held messages stay whole');
  assert.deepEqual(raw.efforts, ['low', 'high']);
  assert.equal(raw.session_id, 'a');
  assert.equal(clipText('😀'.repeat(200), 301).endsWith('\ud83d…'), false, 'never splits a surrogate pair');
});

test('past the budget the least recently active sessions go first; held ones and chats stay', () => {
  const sessions = [];
  for (let i = 0; i < 60; i++) sessions.push(row('s' + i, { updated_at: new Date(Date.UTC(2026, 10, 1, 0, i)).toISOString(), note: 'n'.repeat(250) }));
  sessions.push(row('held', { updated_at: '2020-01-01T00:00:00Z', undelivered: [{ id: 'm' }] }));
  const chats = [{ ...row('c1'), kind: 'conversation' }];
  const whole = encodeSessionSnapshot(sessions, chats, []);
  assert.equal(JSON.parse(whole).sessions.length, 61, 'under the budget nothing is dropped');
  const budget = Math.floor(utf8Bytes(whole) / 2);
  const text = encodeSessionSnapshot(sessions, chats, [{ target: 't', id: 'm' }], budget);
  assert.ok(text && utf8Bytes(text) <= budget);
  const kept = JSON.parse(text);
  const ids = kept.sessions.map(item => item.id);
  assert.ok(ids.includes('held'), 'a row with an undelivered message is kept however old');
  assert.ok(ids.includes('s59') && !ids.includes('s0'), 'the newest stay, the oldest go');
  assert.deepEqual(ids.filter(id => id !== 'held'), ids.filter(id => id !== 'held').slice().sort((a, b) => +a.slice(1) - +b.slice(1)), 'kept rows keep their order');
  assert.deepEqual(kept.conversations.map(item => item.id), ['c1']);
  assert.deepEqual(kept.heldActions, [{ target: 't', id: 'm' }]);
  assert.equal(encodeSessionSnapshot([row('held', { undelivered: [{ id: 'm', text: 'y'.repeat(400) }] })], [], [], 100), '',
    'nothing is stored when even the rows that must stay do not fit');
});

test('an absent, cut or malformed snapshot reads as none', () => {
  const text = encodeSessionSnapshot([row('a')], [], []);
  assert.deepEqual(decodeSessionSnapshot(text).sessions.map(item => item.id), ['a']);
  for (const bad of ['', text.slice(0, -5), '[]', 'null', '"x"']) assert.equal(decodeSessionSnapshot(bad), undefined, JSON.stringify(bad.slice(0, 10)));
});

test('the Agents list says it is loading, or why it cannot load, before any list has arrived', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/models/AgentWorkspace.ets', import.meta.url), 'utf8');
  const method = name => {
    const start = source.search(new RegExp('^  ' + name + '\\(', 'm')); assert.ok(start >= 0, name);
    let depth = 0, at = source.indexOf('{', start);
    for (; at < source.length; at++) { if (source[at] === '{') depth++; else if (source[at] === '}' && --depth === 0) break; }
    return source.slice(start, at + 1);
  };
  const Workspace = new Function('return ' + stripTypeScriptTypes('class W {' + method('awaitingSource') + method('sourceProblem') + '}'))();
  const w = new Workspace();
  w.history = false; w.client = { sessionsReady: false, directoryReady: false, connected: false, error: '' };
  assert.equal(w.awaitingSource(), true); assert.equal(w.sourceProblem(), '', 'still connecting: loading');
  w.client.error = 'offline'; assert.equal(w.sourceProblem(), 'hub_unreachable');
  w.client.error = 'workspace_pair_again'; assert.equal(w.sourceProblem(), 'workspace_pair_again');
  w.client.sessionsReady = true; assert.equal(w.awaitingSource(), false, 'a restored snapshot counts as a list');
  assert.equal(w.sourceProblem(), '');
  w.client.sessionsReady = false; w.history = true; assert.equal(w.awaitingSource(), false, 'history has its own loading');
  const page = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  assert.ok(page.includes("!this.workspace.hasRows() && !this.workspace.loading && !this.workspace.awaitingSource()) ListItem() {"),
    '"empty" is only said of a list that has arrived');
});
