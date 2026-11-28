#!/usr/bin/env node
// Synthetic, in-memory watch acceptance server. Never forwards to a real hub.
import http from 'node:http';
import { appendFileSync, mkdirSync, readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash, randomUUID, timingSafeEqual } from 'node:crypto';

export const TEST_TOKEN = 'persona-wear-fixture-only';
const args = process.argv.slice(2);
const option = (name, fallback) => {
  const index = args.indexOf(name);
  return index < 0 ? fallback : args[index + 1];
};
const host = option('--host', '127.0.0.1');
const port = Number(option('--port', '18765'));
const logPath = option('--events', '');
if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('Invalid port');
if (logPath) mkdirSync(dirname(resolve(logPath)), { recursive: true });
const now = () => new Date().toISOString();
const digest = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');
const events = [];
const instance = randomUUID();
const petDirectory = fileURLToPath(new URL('../../server/internal/hub/personas/nwverse/pet/', import.meta.url));
const petFiles = new Set(['pet.json', 'needs_you.png', 'working.png', 'done.png', 'idle.png', 'sleep.png', 'offline.png', 'tap.png',
  'face-needs_you.png', 'face-working.png', 'face-done.png', 'face-idle.png', 'face-sleep.png', 'face-offline.png']);
const widgetFamilies = ['large', 'medium', 'small', 'accessory_rectangular', 'accessory_circular', 'accessory_inline'];
const chartKinds = ['line', 'area', 'bar', 'pie', 'donut', 'candlestick'];
const widgetCatalog = [
  { extension: 'fixture-tools', extension_name: 'Sample extension', id: 'overview', title: 'Sample status widget', families: widgetFamilies, opens: 'status' },
  { extension: 'fixture-tools', extension_name: 'Sample extension', id: 'unsupported', title: 'Sample unsupported widget', families: ['large'] },
];
const messageReceipts = new Map();
const operations = new Map();
let state;

function reset(scenario = 'core') {
  const sessions = [
    ['assistant', 'Sample assistant', true, 'idle'],
    ['question', 'Sample trip planning', false, 'needs_you'],
    ['plan', 'Sample weekly plan', false, 'needs_you'],
    ['permission', 'Sample report review', false, 'needs_you'],
    ['screen', 'Sample computer choice', false, 'needs_you'],
    ['sensitive', 'Sample protected request', false, 'needs_you'],
    ['working', 'Sample task in progress', false, 'working'],
  ].map(([id, title, assistant, status]) => ({
    machine_id: 'fixture', session_id: id, title, assistant, state: status,
    source_kind: 'fixture', capabilities: { send: true, interrupt: true, approve: true },
    updated_at: now(), last_text: 'Synthetic test data. No real agent or account is connected.',
  }));
  const card = (id, kind, extra) => ({
    id: `fixture-${id}`, machine_id: 'fixture', session_id: id, kind,
    task_title: sessions.find(session => session.session_id === id).title,
    can_cancel: true, ...extra,
  });
  const interactions = [
    card('question', 'question', {
      summary: 'Choose a sample destination.',
      questions: [{ question: 'Where should the sample trip go?', multi_select: false,
        options: [{ label: 'Seaside', description: 'A quiet sample route.' }, { label: 'Mountains', description: 'A scenic sample route.' }] }],
    }),
    card('plan', 'plan', {
      summary: 'Review this synthetic plan.',
      plan: '1. Collect the sample notes.\n2. Write a three-point summary.\n3. Show the result for review.\n\nThis fixture does not execute commands.',
    }),
    card('permission', 'permission', {
      summary: 'Read a synthetic report once?', tool_name: 'read_file', tool_title: 'Read sample report',
    }),
    card('screen', 'screen', {
      summary: 'Choose the sample output style.', screen: 'Sample terminal\n1. Concise\n2. Detailed\n3. Type something',
      dialog: { multi: false, options: [{ key: '1', label: 'Concise' }, { key: '2', label: 'Detailed' },
        { key: '3', label: 'Type something', kind: 'input' }] },
    }),
    card('sensitive', 'permission', {
      summary: 'This sample request needs a protected value. Continue on your computer.',
      tool_name: 'secret_run', secret: true, fields: [{ name: 'sample_value', secret: true }],
    }),
  ];
  if (scenario === 'nonvoice') {
    for (const [id, title, status, writable] of [
      ['unread', 'Sample unread result', 'unread', true],
      ['screen-enter', 'Sample Enter confirmation', 'needs_you', true],
      ['screen-readonly', 'Sample read-only task', 'needs_you', false],
    ]) sessions.push({
      machine_id: 'fixture', session_id: id, title, assistant: false, state: status, source_kind: 'fixture',
      capabilities: { send: writable, interrupt: writable, approve: writable }, updated_at: now(),
      last_text: 'Synthetic non-voice acceptance data.',
    });
    for (const id of ['screen-enter', 'screen-readonly']) interactions.push(card(id, 'screen', {
      summary: 'Synthetic terminal input. No command will run.', screen: 'Sample terminal awaiting input.',
      dialog: { multi: false, options: [{ key: '1', label: 'Concise' }, { key: '2', label: 'Detailed' }] },
    }));
  }
  if (scenario === 'matrix') {
    sessions.push({ machine_id: 'fixture', session_id: 'sensitive-handoff', title: 'Sample protected handoff', assistant: false,
      state: 'needs_you', capabilities: { send: true, interrupt: true, approve: true }, updated_at: now() });
    interactions.push(card('sensitive-handoff', 'permission', {
      summary: 'This sample handoff needs a protected value. Continue on your computer.',
      tool_name: 'secret_handoff', secret: true, fields: [{ name: 'sample_value', secret: true }],
    }));
  }
  state = {
    scenario, mode: 'online', delayMs: 0, chartKind: 'line', failNext: null, interactions, resolved: [], sessions,
    messages: Object.fromEntries(sessions.map(session => [session.session_id, [
      { id: 'work', seq: 1, turn_id: 'fixture-intro', kind: 'tool_use', role: 'assistant', tool_name: 'sample_read', text: 'Synthetic tool activity. Nothing was executed.' },
      { id: 'intro', seq: 2, turn_id: 'fixture-intro', kind: 'text', role: 'assistant', text: 'Synthetic test conversation. You can safely send a sample reply here.' },
    ]])),
    inbox: [{ id: 'fixture-inbox', title: 'Sample invitation', summary: 'A synthetic invitation for watch testing.',
      source: 'mail', status: 'action', manager: 'fixture-manager', connector: { can_reply: true, reply_to: 'Sample sender' } }],
  };
  messageReceipts.clear();
  operations.clear();
}
reset();

function record(event) {
  // No headers, query parameters, submitted text, or credentials enter the audit.
  const row = { at: now(), ...event };
  events.push(row);
  if (events.length > 3000) events.shift();
  if (logPath) appendFileSync(logPath, `${JSON.stringify(row)}\n`, { mode: 0o600 });
}
function authorized(request) {
  const actual = Buffer.from(request.headers.authorization || '');
  const expected = Buffer.from(`Bearer ${TEST_TOKEN}`);
  return actual.length === expected.length && timingSafeEqual(actual, expected);
}
function loopback(request) {
  return ['127.0.0.1', '::1', '::ffff:127.0.0.1'].includes(request.socket.remoteAddress);
}
async function bodyOf(request) {
  let data = '';
  for await (const chunk of request) {
    data += chunk;
    if (Buffer.byteLength(data) > 64 * 1024) throw new Error('Body too large');
  }
  if (!data) return {};
  const parsed = JSON.parse(data);
  if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('Expected object');
  return parsed;
}
function summary() {
  return { fixture: true, instance, scenario: state.scenario, mode: state.mode, pending: state.interactions.map(item => item.id),
    resolved: state.resolved, inboxPending: state.inbox.filter(item => !item.done_at).length,
    messageCounts: Object.fromEntries(Object.entries(state.messages).map(([id, rows]) => [id, rows.length])),
    events };
}
function validResolution(card, body) {
  const owner = state.sessions.find(item => item.session_id === card.session_id);
  if (owner?.capabilities.approve !== true || card.muted || card.delegating) return false;
  if (body.dismiss === true && Object.keys(body).length === 1) return true;
  if (card.kind === 'permission') return typeof body.allow === 'boolean' && Object.keys(body).length === 1 && !(card.secret && body.allow);
  if (card.kind === 'plan') return typeof body.approve === 'boolean' && (!body.approve || body.mode === 'acceptEdits');
  if (card.kind === 'question') return card.questions.every(question =>
    question.options.some(option => option.label === body.answers?.[question.question]));
  if (card.kind === 'screen') return ['1', '2'].includes(body.option) ||
    (Array.isArray(body.keys) && body.keys.length === 1 && ['esc', 'enter'].includes(body.keys[0]));
  return false;
}

function widgetView(id, family) {
  const unsupported = [{ type: 'button', label: 'Never execute sample action', action: 'sample-delete' },
    { type: 'field', label: 'Never edit sample setting', name: 'sample-setting' },
    { type: 'link', label: 'Never open sample website', url: 'https://example.invalid/' }];
  const items = id === 'unsupported' ? unsupported : [
    { type: 'text', text: 'Sample read-only status.' },
    { type: 'metric', label: 'Sample count', value: '7', detail: 'Seven synthetic tasks' },
    { type: 'progress', label: 'Sample progress', ratio: 0.5, detail: 'Half of sample work' },
    { type: 'row', title: 'Sample next step', subtitle: 'Review on computer', value: 'Queued' },
    { type: 'stat_grid', stats: [{ label: 'Sample open', value: '2' }, { label: 'Sample done', value: '5' }] },
    { type: 'chart', title: 'Sample trend', kind: state.chartKind, unit: ' units', series: [{ name: 'Sample series', points:
      state.chartKind === 'candlestick' ? [
        { label: 'Mon', value: 2, open: 1, high: 3, low: 0, close: 2 }, { label: 'Tue', value: 3, open: 2, high: 4, low: 1, close: 3 },
        { label: 'Wed', value: 2, open: 3, high: 4, low: 1, close: 2 },
      ] : [{ label: 'Mon', value: 1, display: '1' }, { label: 'Tue', value: 2, display: '2' }, { label: 'Wed', value: 3, display: '3' }],
    }] }, ...unsupported,
  ];
  return { view: { title: id === 'unsupported' ? 'Sample unsupported widget' : 'Sample status widget', sections: [{ items }] },
    expires_at: new Date(Date.now() + 60000).toISOString(), family };
}

const server = http.createServer(async (request, response) => {
  const url = new URL(request.url, 'http://fixture.invalid');
  const path = url.pathname;
  const method = request.method;
  const reply = (status, payload, event = {}) => {
    record({ method, path, status, ...event });
    response.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', 'X-Persona-Fixture': 'true' });
    response.end(JSON.stringify(payload));
  };
  try {
    if (path === '/api/v1/health' && method === 'GET') return reply(200, { ok: true, fixture: true, instance, version: 'wear-test-fixture', protocol: 1 });
    if (!authorized(request)) return reply(401, { code: 'unauthorized' });
    if (path === '/__test') {
      if (!loopback(request)) return reply(403, { code: 'localhost_only' });
      if (method === 'GET') return reply(200, summary());
      if (method !== 'POST') return reply(405, { code: 'method_not_allowed' });
      const body = await bodyOf(request);
      if (body.scenario !== undefined && (!['core', 'nonvoice', 'surfaces', 'matrix'].includes(body.scenario) || body.reset !== true))
        return reply(400, { code: 'invalid_scenario' });
      if (body.reset === true) reset(body.scenario);
      if (body.chartKind !== undefined) {
        if (!chartKinds.includes(body.chartKind)) return reply(400, { code: 'invalid_chart_kind' });
        state.chartKind = body.chartKind;
      }
      if (body.clearEvents === true) events.length = 0;
      if (body.mode !== undefined) {
        if (!['online', 'offline', 'error', 'stale'].includes(body.mode)) return reply(400, { code: 'invalid_mode' });
        state.mode = body.mode;
      }
      if (body.delayMs !== undefined) {
        if (!Number.isInteger(body.delayMs) || body.delayMs < 0 || body.delayMs > 30000) return reply(400, { code: 'invalid_delay' });
        state.delayMs = body.delayMs;
      }
      if (body.failNext !== undefined) {
        const failure = body.failNext;
        if (failure !== null && (typeof failure.path !== 'string' || !failure.path.startsWith('/api/v1/') ||
          ![401, 403, 404, 409, 410, 429, 500, 503].includes(failure.status) ||
          (failure.family !== undefined && !widgetFamilies.includes(failure.family)))) return reply(400, { code: 'invalid_failure' });
        state.failNext = failure;
      }
      if (body.expire !== undefined) {
        if (!Array.isArray(body.expire) || body.expire.some(id => typeof id !== 'string')) return reply(400, { code: 'invalid_ids' });
        state.interactions = state.interactions.filter(card => !body.expire.includes(card.id));
      }
      return reply(200, { ok: true, fixture: true, mode: state.mode });
    }
    if (state.mode === 'offline') {
      record({ method, path, status: 0, action: 'connection_closed' });
      request.socket.destroy();
      return;
    }
    if (state.delayMs) await new Promise(resolveDelay => setTimeout(resolveDelay, state.delayMs));
    if (state.mode === 'error') return reply(503, { code: 'fixture_unavailable' });
    const body = ['POST', 'PUT'].includes(method) ? await bodyOf(request) : {};
    if (state.failNext?.path === path &&
        (state.failNext.family === undefined || state.failNext.family === body.context?.size)) {
      const failure = state.failNext;
      state.failNext = null;
      const event = widgetFamilies.includes(body.context?.size) ? { family: body.context.size } : {};
      return reply(failure.status, { code: 'fixture_injected_error' }, event);
    }
    if (method === 'GET') {
      if (state.scenario === 'surfaces' && path === '/api/v1/extensions/-/widgets') return reply(200, { widgets: widgetCatalog });
      if (state.scenario === 'surfaces' && path.startsWith('/api/v1/personas/nwverse/pet/')) {
        const name = path.slice('/api/v1/personas/nwverse/pet/'.length);
        if (!petFiles.has(name)) return reply(404, { code: 'not_found' });
        const content = readFileSync(resolve(petDirectory, name));
        record({ method, path, status: 200, action: 'pet_asset', bytes: content.length });
        response.writeHead(200, { 'Content-Type': name === 'pet.json' ? 'application/json; charset=utf-8' : 'image/png',
          'Content-Length': content.length, 'Cache-Control': 'no-store', 'X-Persona-Fixture': 'true' });
        response.end(content);
        return;
      }
      if (path === '/api/v1/sessions') return reply(200, { sessions: state.sessions });
      if (path === '/api/v1/interactions') return reply(200, { interactions: state.interactions });
      if (path === '/api/v1/machines') return reply(200, { machines: [{ id: 'fixture', name: 'Sample computer', online: true }] });
      if (path === '/api/v1/inbox') return reply(200, { items: state.inbox });
      const chat = /^\/api\/v1\/sessions\/fixture\/([^/]+)\/items$/.exec(path);
      if (chat && state.messages[chat[1]]) return reply(200, { items: state.messages[chat[1]] });
    }
    if (method === 'POST') {
      const widget = /^\/api\/v1\/extensions\/fixture-tools\/-\/widget\/(overview|unsupported)$/.exec(path);
      if (state.scenario === 'surfaces' && widget) {
        const context = body.context;
        if (Object.keys(body).length !== 1 || !context || typeof context !== 'object' || Array.isArray(context) ||
            !widgetFamilies.includes(context.size) || context.platform !== 'android' || context.ui_version !== 3 ||
            typeof context.locale !== 'string' || typeof context.tz !== 'string') return reply(400, { code: 'invalid_widget_context' });
        return reply(200, widgetView(widget[1], context.size), { action: 'widget_projection', id: widget[1], family: context.size });
      }
      if (path === '/api/v1/auth/tokens') {
        if (body.kind !== 'watch') return reply(403, { code: 'watch_only' });
        return reply(200, { id: 'fixture-watch', token: TEST_TOKEN, role: 'answer', name: 'Synthetic watch' });
      }
      const operation = request.headers['x-persona-op-id'];
      const operationKey = operation ? `${path}:${operation}` : null;
      if (operationKey && operations.has(operationKey)) {
        const prior = operations.get(operationKey);
        if (prior.digest !== digest(body)) return reply(409, { code: 'operation_conflict' });
        return reply(200, prior.payload, { action: 'operation_replay' });
      }
      const remember = payload => {
        if (operationKey) operations.set(operationKey, { digest: digest(body), payload });
        return payload;
      };
      const chat = /^\/api\/v1\/sessions\/fixture\/([^/]+)\/(messages|interrupt|seen)$/.exec(path);
      if (chat && state.messages[chat[1]]) {
        const session = state.sessions.find(item => item.session_id === chat[1]);
        if (chat[2] === 'messages') {
          if (!session.capabilities.send) return reply(403, { code: 'read_only' });
          if (typeof body.text !== 'string' || !body.text.trim() || body.text.length > 16000 ||
            typeof body.client_msg_id !== 'string' || !/^[A-Za-z0-9_-]{8,128}$/.test(body.client_msg_id)) return reply(400, { code: 'invalid_message' });
          const key = `${chat[1]}:${body.client_msg_id}`;
          if (messageReceipts.has(key)) {
            const prior = messageReceipts.get(key);
            if (prior.digest !== digest(body.text)) return reply(409, { code: 'message_id_conflict' });
            // A new operation alias keeps this message's original receipt, independently of earlier aliases.
            return reply(200, remember(prior.payload), { action: 'message_replay' });
          }
          const accepted = { ok: true, state: 'accepted', client_msg_id: body.client_msg_id };
          messageReceipts.set(key, { digest: digest(body.text), payload: accepted });
          state.messages[chat[1]].push({ id: body.client_msg_id, kind: 'text', role: 'user', text: body.text });
          state.messages[chat[1]].push({ id: `${body.client_msg_id}-reply`, kind: 'text', role: 'assistant', text: 'Sample reply received. No real agent was started.' });
          session.state = 'unread'; session.updated_at = now();
          return reply(200, remember(accepted),
            { action: 'message', session: chat[1], textLength: body.text.length, textDigest: digest(body.text) });
        }
        if (chat[2] === 'interrupt' && !session.capabilities.interrupt) return reply(403, { code: 'read_only' });
        if (chat[2] === 'interrupt' || session.state === 'unread') session.state = 'idle';
        return reply(200, remember({ ok: true }), { action: chat[2], session: chat[1] });
      }
      const interaction = /^\/api\/v1\/interactions\/([^/]+)\/resolve$/.exec(path);
      if (interaction) {
        const card = state.interactions.find(item => item.id === interaction[1]);
        if (!card || state.mode === 'stale') {
          state.interactions = state.interactions.filter(item => item.id !== interaction[1]);
          return reply(410, { code: 'gone' });
        }
        if (!validResolution(card, body)) return reply(403, { code: 'invalid_watch_resolution' });
        state.interactions = state.interactions.filter(item => item.id !== card.id);
        state.resolved.push(card.id);
        state.sessions.find(item => item.session_id === card.session_id).state = 'idle';
        return reply(200, remember({ ok: true, state: 'accepted' }),
          { action: 'resolve', id: card.id, kind: card.kind, fields: Object.keys(body), allow: body.allow,
            approve: body.approve, option: body.option, dismiss: body.dismiss, keys: body.keys });
      }
      const inbox = /^\/api\/v1\/inbox\/([^/]+)\/(done|reply|draft)$/.exec(path);
      if (inbox) {
        const item = state.inbox.find(row => row.id === inbox[1]);
        if (!item) return reply(404, { code: 'not_found' });
        if (url.searchParams.get('manager') !== item.manager) return reply(400, { code: 'wrong_manager' });
        if (item.done_at && inbox[2] !== 'done') return reply(409, { code: 'inbox_item_done' });
        if (inbox[2] === 'draft') {
          if (body.instruction !== undefined && typeof body.instruction !== 'string') return reply(400, { code: 'invalid_instruction' });
          return reply(200, remember({ text: 'Thank you for the sample invitation. I can join the synthetic review.' }),
            { action: 'inbox_draft', id: item.id, fields: Object.keys(body), instructionLength: body.instruction?.length ?? 0,
              managerMatched: url.searchParams.get('manager') === item.manager });
        }
        if (inbox[2] === 'done') {
          if (body.done !== true) return reply(400, { code: 'invalid_done' });
          item.done_at = now();
        } else if (typeof body.text !== 'string' || !body.text.trim()) return reply(400, { code: 'invalid_reply' });
        return reply(200, remember({ ok: true }), { action: `inbox_${inbox[2]}`, id: item.id,
          textLength: body.text?.length, textDigest: body.text === undefined ? undefined : digest(body.text) });
      }
    }
    return reply(404, { code: 'not_found' });
  } catch {
    reply(400, { code: 'invalid_request' });
  }
});
server.on('error', error => { console.error(`Fixture could not listen: ${error.code || 'unknown error'}`); process.exitCode = 1; });
server.listen(port, host, () => console.log(`Synthetic watch fixture listening at http://${host}:${port}. No real hub is connected.`));
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close(() => process.exit(0)));
