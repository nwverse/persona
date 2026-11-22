import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { setImmediate } from 'node:timers/promises';

registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.'))
      return { url: new URL(specifier + '.ets', context.parentURL).href, shortCircuit: true };
    return next(specifier, context);
  },
  load(url, context, next) {
    if (!url.endsWith('.ets')) return next(url, context);
    const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*|@Trace\s+|@Computed\s+/g, '');
    return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
  }
});
const { SignedInDevices } = await import('../entry/src/main/ets/models/SignedInDevices.ets');

function hub() {
  const state = {
    profile: { id: 'hub-a' }, connected: true, demo: false, epoch: 3, deleted: [], forgotten: [],
    tokens: [
      { id: 'ph', kind: 'phone', name: 'Phone' },
      { id: 'web-1', kind: 'web', name: 'Chrome' },
      { id: 'web-2', kind: 'web', name: 'Chrome' }
    ],
    connectionGeneration: () => 1, scope: () => 'hub-a/manager', pairingRevision() { return this.epoch; },
    owns(generation, scope) { return this.connectionGeneration() === generation && this.scope() === scope; },
    async forgetProfile(id) { this.forgotten.push(id); },
    repo() { return { call: async (path, method = 'GET', _body, _op, admit, _completed, acknowledged) => {
      if (method === 'GET') {
        assert.equal(path, '/api/v1/auth/tokens');
        return this.read ? this.read() : { current: 'ph', tokens: structuredClone(this.tokens) };
      }
      assert.equal(method, 'DELETE');
      assert.ok(admit());
      const id = path.slice('/api/v1/auth/tokens/'.length);
      this.deleted.push(id);
      this.tokens.find(t => t.id === id).revoked_at = '2026-01-01T00:00:00Z';
      await this.afterDelete?.();
      assert.ok(acknowledged({ ok: true }));
      return { ok: true };
    } }; }
  };
  return state;
}

async function open(owner) {
  const directory = new SignedInDevices();
  directory.bind(owner);
  await setImmediate();
  return directory;
}

test('identically named browsers revoke by credential ID, and repeated taps issue one delete', async () => {
  const owner = hub(), directory = await open(owner);
  assert.equal(directory.currentId, 'ph');
  const target = directory.rows[2].value;
  const confirm = directory.prepareRevoke(target);
  assert.deepEqual(owner.deleted, []);
  await Promise.all([confirm(), confirm()]);
  assert.deepEqual(owner.deleted, ['web-2']);
  assert.deepEqual(directory.rows.filter(r => !r.value.revoked).map(r => r.value.id), ['ph', 'web-1']);
  assert.equal(directory.prepareRevoke(target), undefined);
  assert.deepEqual(owner.forgotten, []);
});

test('a confirmation from another binding or an earlier pairing cannot revoke anything', async () => {
  const first = hub(), second = hub(), directory = await open(first);
  const stale = directory.prepareRevoke(directory.rows[1].value);
  directory.bind(second); await setImmediate();
  await stale();
  const fromOldPairing = directory.prepareRevoke(directory.rows[1].value);
  second.epoch++;
  await fromOldPairing();
  assert.deepEqual(first.deleted, []);
  assert.deepEqual(second.deleted, []);
});

test('late list response cannot resurrect a revoked login or replace a new Hub list', async () => {
  const owner = hub(), directory = await open(owner);
  const delayed = Promise.withResolvers();
  owner.read = () => delayed.promise;
  const pending = directory.refresh();
  delete owner.read;
  owner.tokens[1].revoked_at = '2026-01-01T00:00:00Z';
  await directory.refresh();
  delayed.resolve({ current: 'wrong', tokens: [] }); await pending;
  assert.equal(directory.currentId, 'ph');
  assert.ok(directory.rows[1].value.revoked);
  const oldHub = Promise.withResolvers();
  owner.read = () => oldHub.promise;
  const pendingHub = directory.refresh();
  directory.bind(hub()); await setImmediate();
  oldHub.resolve({ current: 'old', tokens: [] }); await pendingHub;
  assert.equal(directory.currentId, 'ph');
  assert.equal(directory.rows.length, 3);
});

test('lost delete response is reconciled with a fresh list without sending the delete again', async () => {
  const owner = hub(), directory = await open(owner);
  owner.afterDelete = () => { throw new Error('lost response'); };
  await directory.prepareRevoke(directory.rows[1].value)();
  assert.deepEqual(owner.deleted, ['web-1']);
  assert.ok(directory.rows[1].value.revoked);
  assert.equal(directory.error, '');
});

test('confirmed self-revocation unpairs only its own pairing, including when an acknowledgement is late', async () => {
  for (const replaced of [false, true]) {
    const owner = hub(), directory = await open(owner);
    if (replaced) owner.afterDelete = () => { owner.epoch++; };
    await directory.prepareRevoke(directory.rows[0].value)();
    assert.deepEqual(owner.deleted, ['ph']);
    assert.deepEqual(owner.forgotten, replaced ? [] : ['hub-a']);
  }
});

test('large device lists keep the current phone first and search the entire selected group', async () => {
  const owner = hub();
  const stamp = n => new Date(Date.UTC(2026, 0, 1) + n * 60_000).toISOString();
  const live = Array.from({ length: 600 }, (_, i) => ({
    id: 'live-' + i, name: 'Browser ' + i, kind: 'web', created_at: stamp(i),
    ...(i === 5 ? { last_seen: stamp(900) } : {})
  }));
  const history = Array.from({ length: 600 }, (_, i) => ({
    id: 'past-' + i, name: 'Old browser ' + i, kind: 'web', created_at: stamp(1), revoked_at: stamp(i)
  }));
  owner.tokens = [...history, ...live];
  Object.assign(owner.tokens.find(t => t.id === 'live-0'), { id: 'ph', name: 'Current phone', kind: 'phone' });
  const directory = await open(owner);
  assert.equal(directory.signedInRows.length, 600);
  assert.equal(directory.revokedRows.length, 600);
  assert.deepEqual(directory.signedInRows.slice(0, 3).map(r => r.value.id), ['ph', 'live-5', 'live-599']);
  assert.equal(directory.revokedRows[0].value.id, 'past-599');
  const describe = d => d.kind === 'phone' ? '手机 · 本机' : '浏览器';
  assert.deepEqual(directory.matching(false, ' browser 598 ', describe).map(r => r.value.id), ['live-598']);
  assert.deepEqual(directory.matching(true, 'BROWSER 598', describe).map(r => r.value.id), ['past-598']);
  assert.deepEqual(directory.matching(false, '本机', describe).map(r => r.value.id), ['ph']);
  assert.equal(directory.matching(true, '浏览器', describe).length, 600);
  assert.equal(directory.matching(true, '手机', describe).length, 0);
  assert.equal(directory.matching(false, '  ', describe).length, 600);
  const chosen = directory.matching(false, 'Browser 598', describe)[0];
  await directory.prepareRevoke(chosen.value)();
  assert.deepEqual(owner.deleted, ['live-598']);
  assert.equal(directory.signedInRows.length, 599);
  assert.equal(directory.revokedRows.length, 601);
  assert.equal(directory.matching(false, 'Browser 598', describe).length, 0);
});

const { browserIdentity, machinePlatform } = await import('../entry/src/main/ets/models/DeviceIdentity.ets');

test('browser and OS marks are independent and preserve the enrollment date in supported web labels', () => {
  const examples = [
    ['网页（Chrome · Mac · 11月22日）', 'chrome', 'macos', 'Chrome · 11月22日'],
    ['Web (Firefox · Linux · Nov 22)', 'firefox', 'linux', 'Firefox · Nov 22'],
    ['Web（Edge · Windows · 11月22日）', 'edge', 'windows', 'Edge · 11月22日'],
    ['Web (Safari · iPad · Nov 22)', 'safari', 'ipados', 'Safari · Nov 22'],
    ['Google Chrome', 'chrome', '', 'Chrome'],
    ['Web (Safari · Nov 22)', 'safari', '', 'Safari · Nov 22'],
    ['Web (Browser · Linux · Nov 22)', '', 'linux', 'Web (Browser · Linux · Nov 22)']
  ];
  for (const [name, family, system, title] of examples)
    assert.deepEqual(browserIdentity(name), { family, system, title });
  for (const name of ['Chrome research workstation', 'My Windows PC', 'Chrome · team laptop', 'Chromebook', 'constructor'])
    assert.deepEqual(browserIdentity(name), { family: '', system: '', title: name });
});

test('machine marks use the reported OS, distribution and ID_LIKE with a generic fallback', () => {
  assert.deepEqual(machinePlatform({ os: 'darwin', system: { name: 'macOS 26.0', version: '26.0', arch: 'arm64' } }),
    { family: 'macos', detail: 'macOS 26.0 · arm64' });
  assert.equal(machinePlatform({ os: 'windows', system: { distro: 'ubuntu' } }).family, 'windows');
  assert.equal(machinePlatform({ system: { os: 'linux', distro: 'nixos', like: 'debian' } }).family, 'nixos');
  assert.equal(machinePlatform({ system: { os: 'linux', distro: 'custom', like: 'rhel fedora' } }).family, 'redhat');
  assert.equal(machinePlatform({ system: { os: 'linux', distro: 'custom' } }).family, 'linux');
  assert.equal(machinePlatform({ platform: 'Windows 11' }).family, 'windows');
  assert.equal(machinePlatform({ name: 'Mac ops', hostname: 'windows-laptop' }).family, '');
  assert.deepEqual(machinePlatform({ os: 'freebsd', engine: 'example' }), { family: '', detail: 'freebsd · example' });
});

const { BrowserLoginResult } = await import('../entry/src/main/ets/models/BrowserLoginResult.ets');
const scanCode = 'persona://browser-login?hub=sample&request=' + 'a'.repeat(43);

function scanResult(replies) {
  const owner = hub(), sent = [];
  let signedIn = 0;
  owner.profile.hubId = 'sample';
  owner.repo = () => ({ transport: { async request(path, method, body) {
    assert.equal(path, '/api/v1/auth/browser-login/approve');
    assert.equal(method, 'POST');
    sent.push(JSON.parse(body));
    const next = await replies.shift();
    if (next instanceof Error) throw next;
    return JSON.stringify(next);
  } } });
  const result = new BrowserLoginResult(owner, () => signedIn++);
  return { owner, result, sent, signedIn: () => signedIn };
}

test('scanning inspects the current Hub; only Allow requests a number and polling never repeats Allow', async t => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: Date.now() });
  const known = { name: 'Web (Chrome · Mac)', network: 'lan', age_seconds: 12 };
  const { result, sent, signedIn } = scanResult([
    { ...known, state: 'scanned' }, { ...known, state: 'confirm', number: '47' },
    new Error('connection interrupted'), { ...known, state: 'approved' }
  ]);
  await result.load(scanCode);
  assert.equal(result.phase, 'ask');
  assert.equal(result.name, known.name);
  assert.equal(result.requestedAt, Date.now() - 12000);
  assert.equal(result.number, '');
  assert.equal(sent[0].allow, undefined);
  await Promise.all([result.decide(true), result.decide(true)]);
  assert.equal(result.phase, 'number');
  assert.equal(result.number, '47');
  t.mock.timers.tick(1500); await setImmediate();
  assert.equal(result.number, '47');
  t.mock.timers.tick(1500); await setImmediate();
  assert.equal(result.phase, 'success');
  assert.equal(result.number, '');
  assert.equal(signedIn(), 1);
  result.dismiss();
  assert.deepEqual(sent.map(v => [v.allow, v.show_number]), [[undefined, undefined], [true, true], [undefined, undefined], [undefined, undefined]]);
});

test('wrong Hub and invalid QR never contact any Hub; ended requests provide distinct outcomes', async () => {
  for (const [qr, expected] of [[scanCode.replace('hub=sample', 'hub=other'), 'other_hub'],
    [scanCode.replace('sample', '%ZZ'), 'invalid'], [scanCode + '&extra=1', 'invalid'], ['ordinary text', 'invalid']]) {
    const { result, sent } = scanResult([]);
    await result.load(qr); assert.equal(result.phase, expected); assert.equal(sent.length, 0);
  }
  for (const [reply, expected] of [[{ state: 'voided' }, 'voided'], [{ state: 'denied' }, 'denied'],
    [{ state: 'confirm' }, 'ended'], [Object.assign(new Error(), { status: 410 }), 'expired'],
    [Object.assign(new Error(), { status: 409 }), 'ended'], [{ state: 'other' }, 'failed']]) {
    const { result, sent } = scanResult([reply]);
    await result.load(scanCode); assert.equal(result.phase, expected); assert.equal(result.number, '');
    result.dismiss(); assert.equal(sent.length, 1);
  }
});

test('a swipe dismissing an in-flight Allow declines once and its late response cannot display a number', async () => {
  const delayed = Promise.withResolvers();
  const { result, sent, signedIn } = scanResult([{ state: 'scanned' }, delayed.promise, { state: 'denied' }]);
  await result.load(scanCode);
  const allow = result.decide(true);
  assert.equal(result.phase, 'ask');
  result.dismiss(); result.dismiss();
  delayed.resolve({ state: 'confirm', number: '47' }); await allow;
  assert.deepEqual(sent.map(v => v.allow), [undefined, true, false]);
  assert.equal(result.number, '');
  assert.equal(signedIn(), 0);
});

test('cancelling or replacing a pairing stops outstanding polls and never decides on a new pairing', async t => {
  t.mock.timers.enable({ apis: ['setTimeout', 'Date'], now: Date.now() });
  for (const replaced of [false, true]) {
    const { owner, result, sent } = scanResult([{ state: 'scanned' }, { state: 'confirm', number: '47' }, { state: 'denied' }]);
    await result.load(scanCode); await result.decide(true);
    if (replaced) owner.epoch++;
    await result.decide(false); result.dismiss();
    t.mock.timers.tick(6000); await setImmediate();
    assert.deepEqual(sent.map(v => v.allow), replaced ? [undefined, true] : [undefined, true, false]);
    assert.equal(result.phase, replaced ? 'failed' : 'denied');
    assert.equal(result.number, '');
  }
});
