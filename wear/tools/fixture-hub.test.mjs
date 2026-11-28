import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:net';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

const token = 'persona-wear-fixture-only';
const messagePath = '/api/v1/sessions/fixture/assistant/messages';

async function fixture(t) {
  // Reserve a local ephemeral port; the child binds only loopback and never forwards requests.
  const reservation = createServer();
  reservation.listen(0, '127.0.0.1');
  await once(reservation, 'listening');
  const port = reservation.address().port;
  await new Promise(resolve => reservation.close(resolve));
  const child = spawn(process.execPath, [fileURLToPath(new URL('./fixture-hub.mjs', import.meta.url)), '--port', String(port)],
    { windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
  t.after(async () => {
    if (child.exitCode == null && child.signalCode == null) {
      const closed = once(child, 'close');
      child.kill('SIGTERM');
      await closed;
    }
  });
  await new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('Synthetic fixture startup timed out')), 5000);
    let output = '';
    child.stdout.on('data', data => {
      output += data.toString();
      if (output.includes('Synthetic watch fixture listening')) { clearTimeout(timeout); resolve(); }
    });
    child.once('error', error => { clearTimeout(timeout); reject(error); });
    child.once('exit', () => { clearTimeout(timeout); reject(new Error('Synthetic fixture exited during startup')); });
  });
  return async (path, body, operation) => {
    const response = await fetch(`http://127.0.0.1:${port}${path}`, {
      method: body == null ? 'GET' : 'POST',
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', ...(operation ? { 'X-Persona-Op-Id': operation } : {}) },
      body: body == null ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(5000),
    });
    return { status: response.status, body: await response.json() };
  };
}

for (const order of [['sample-op-b', 'sample-op-a'], ['sample-op-a', 'sample-op-b']]) {
  test(`message receipt stays original across unkeyed and operation replays: ${order.join(' then ')}`, async t => {
    const request = await fixture(t);
    const body = { text: 'Synthetic message', client_msg_id: 'sample-message' };
    const accepted = await request(messagePath, body, 'sample-op-a');
    assert.deepEqual(accepted, { status: 200, body: { ok: true, state: 'accepted', client_msg_id: body.client_msg_id } });
    assert.deepEqual(await request(messagePath, body), accepted);
    for (const operation of [...order, ...order]) assert.deepEqual(await request(messagePath, body, operation), accepted);
    const audit = (await request('/__test')).body;
    assert.equal(audit.messageCounts.assistant, 4);
    assert.equal(audit.events.filter(event => event.action === 'message').length, 1);
    assert.equal(audit.events.some(event => event.action === 'message_replay'), true);
    assert.equal(audit.events.some(event => event.action === 'operation_replay'), true);
  });
}

test('new operation aliases retain their own conflict checks without changing earlier receipts', async t => {
  const request = await fixture(t);
  const first = { text: 'First synthetic message', client_msg_id: 'sample-first' };
  const second = { text: 'Second synthetic message', client_msg_id: 'sample-second' };
  const acceptedFirst = await request(messagePath, first, 'sample-op-a');
  const acceptedSecond = await request(messagePath, second, 'sample-op-b');
  assert.deepEqual(await request(messagePath, first, 'sample-op-c'), acceptedFirst);
  for (const operation of ['sample-op-a', 'sample-op-c']) {
    assert.deepEqual(await request(messagePath, second, operation), { status: 409, body: { code: 'operation_conflict' } });
    assert.deepEqual(await request(messagePath, first, operation), acceptedFirst);
  }
  assert.deepEqual(await request(messagePath, second, 'sample-op-b'), acceptedSecond);
  assert.deepEqual(await request(messagePath, { ...first, text: 'Changed synthetic text' }), { status: 409, body: { code: 'message_id_conflict' } });
  assert.equal((await request('/__test')).body.messageCounts.assistant, 6);
});

test('a message first accepted without an operation returns its original receipt after keyed retries', async t => {
  const request = await fixture(t);
  const body = { text: 'Initially unkeyed synthetic message', client_msg_id: 'sample-unkeyed' };
  const accepted = await request(messagePath, body);
  for (const operation of ['sample-op-b', 'sample-op-a', 'sample-op-b', 'sample-op-a']) {
    assert.deepEqual(await request(messagePath, body, operation), accepted);
  }
  assert.equal((await request('/__test')).body.messageCounts.assistant, 4);
});

test('surface chart control supplies finite data for every production chart kind and resets to line', async t => {
  const request = await fixture(t);
  await request('/__test', { reset: true, scenario: 'surfaces' });
  const context = { context: { size: 'large', platform: 'android', ui_version: 3, locale: 'en', tz: 'UTC' } };
  const projection = () => request('/api/v1/extensions/fixture-tools/-/widget/overview', context);
  for (const kind of ['line', 'area', 'bar', 'pie', 'donut', 'candlestick']) {
    assert.equal((await request('/__test', { chartKind: kind })).status, 200);
    const response = await projection();
    assert.equal(response.status, 200);
    const chart = response.body.view.sections[0].items.find(item => item.type === 'chart');
    assert.equal(chart.kind, kind);
    assert.equal(chart.series[0].points.length, 3);
    for (const point of chart.series[0].points) {
      assert.ok(Number.isFinite(point.value));
      if (kind === 'candlestick') {
        for (const key of ['open', 'high', 'low', 'close']) assert.ok(Number.isFinite(point[key]));
        assert.ok(point.low <= Math.min(point.open, point.close));
        assert.ok(point.high >= Math.max(point.open, point.close));
      }
    }
  }
  assert.equal((await request('/__test', { chartKind: 'script' })).status, 400);
  await request('/__test', { reset: true, scenario: 'surfaces' });
  assert.equal((await projection()).body.view.sections[0].items.find(item => item.type === 'chart').kind, 'line');
});

test('accessory projections preserve a large-only fault until the app consumes it once', async t => {
  const request = await fixture(t);
  const path = '/api/v1/extensions/fixture-tools/-/widget/overview';
  const projection = size => request(path, { context: { size, platform: 'android', ui_version: 3, locale: 'en', tz: 'UTC' } });
  assert.equal((await request('/__test', { reset: true, scenario: 'surfaces', clearEvents: true,
    failNext: { path, status: 503, family: 'large' } })).status, 200);
  for (const family of ['accessory_circular', 'accessory_rectangular', 'accessory_inline']) {
    assert.equal((await projection(family)).status, 200);
  }
  assert.deepEqual(await projection('large'), { status: 503, body: { code: 'fixture_injected_error' } });
  assert.equal((await projection('large')).status, 200);
  const rows = (await request('/__test')).body.events.filter(event => event.path === path);
  assert.deepEqual(rows.map(({ status, family }) => ({ status, family })), [
    { status: 200, family: 'accessory_circular' }, { status: 200, family: 'accessory_rectangular' },
    { status: 200, family: 'accessory_inline' }, { status: 503, family: 'large' }, { status: 200, family: 'large' },
  ]);
});

test('fault families reject unknown values without replacing a pending valid fault', async t => {
  const request = await fixture(t);
  const path = '/api/v1/extensions/fixture-tools/-/widget/overview';
  assert.equal((await request('/__test', { reset: true, scenario: 'surfaces',
    failNext: { path, status: 503, family: 'large' } })).status, 200);
  for (const family of ['unknown', 'LARGE', '', null, 1, ['large'], { size: 'large' }]) {
    assert.deepEqual(await request('/__test', { failNext: { path, status: 503, family } }),
      { status: 400, body: { code: 'invalid_failure' } });
  }
  assert.equal((await request(path, { context: { size: 'large', platform: 'android', ui_version: 3, locale: 'en', tz: 'UTC' } })).status, 503);
  const catalog = '/api/v1/extensions/-/widgets';
  assert.equal((await request('/__test', { failNext: { path: catalog, status: 404 } })).status, 200);
  assert.equal((await request(catalog)).status, 404);
  assert.equal((await request(catalog)).status, 200);
});

test('matrix includes both protected tool forms and neither accepts an allow response', async t => {
  const request = await fixture(t);
  assert.equal((await request('/__test', { reset: true, scenario: 'matrix' })).status, 200);
  const cards = (await request('/api/v1/interactions')).body.interactions.filter(item => item.secret);
  assert.deepEqual(cards.map(item => item.tool_name).sort(), ['secret_handoff', 'secret_run']);
  for (const card of cards) {
    assert.notEqual((await request(`/api/v1/interactions/${card.id}/resolve`, { allow: true })).status, 200);
    assert.equal((await request(`/api/v1/interactions/${card.id}/resolve`, { allow: false })).status, 200);
  }
});
