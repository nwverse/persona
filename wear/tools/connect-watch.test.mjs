import assert from 'node:assert/strict';
import test from 'node:test';
import { connectWatch, parseDevices, parseOptions, parseTlsServices } from './connect-watch.mjs';

const serial = 'TESTWATCH';
const service = `adb-${serial}-example`;
const named = `${service}._adb-tls-connect._tcp`;
const oldEndpoint = '192.0.2.10:30001';
const newEndpoint = '192.0.2.10:30002';
const otherEndpoint = '192.0.2.20:30003';
const listing = rows => `List of devices attached\n${rows}\n`;
const mdns = rows => `List of discovered mdns services\n${rows}\n`;

function mock(entries) {
  const responses = new Map(entries.map(([args, result]) => [JSON.stringify(args), result]));
  const calls = [];
  const run = args => {
    calls.push(args);
    const key = JSON.stringify(args);
    assert.ok(responses.has(key), `Unexpected ADB command: ${key}`);
    const response = responses.get(key);
    if (response instanceof Error) throw response;
    return response;
  };
  return { run, calls };
}

test('reuses only the matching online TLS transport and verifies its hardware identity', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing(`${otherEndpoint} device model:Other\n${named} device model:Watch`)],
    [['-s', named, 'shell', 'getprop', 'ro.serialno'], `${serial}\n`],
  ]);
  assert.equal(connectWatch({ serial, run }), named);
  assert.equal(calls.length, 2);
});

test('reuses an online endpoint after checking other online device identities, without mDNS', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing(`${otherEndpoint} device\n${newEndpoint} device`)],
    [['-s', otherEndpoint, 'shell', 'getprop', 'ro.serialno'], 'ANOTHERWATCH'],
    [['-s', newEndpoint, 'shell', 'getprop', 'ro.serialno'], serial],
  ]);
  assert.equal(connectWatch({ serial, run }), newEndpoint);
  assert.equal(calls.length, 3);
  assert.equal(calls.some(call => call[0] === 'connect'), false);
  assert.equal(calls.some(call => call[0] === 'mdns'), false);
});

test('tries another advertisement for the same watch when an old port is unreachable', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing(`${otherEndpoint} device`)],
    [['-s', otherEndpoint, 'shell', 'getprop', 'ro.serialno'], 'ANOTHERWATCH'],
    [['mdns', 'services'], mdns(`${service} _adb-tls-connect._tcp ${oldEndpoint}\n${service} (2) _adb-tls-connect._tcp ${newEndpoint}`)],
    [['connect', oldEndpoint], new Error('Connection refused')],
    [['connect', newEndpoint], 'connected'],
    [['-s', newEndpoint, 'shell', 'getprop', 'ro.serialno'], serial],
  ]);
  assert.equal(connectWatch({ serial, run }), newEndpoint);
  assert.deepEqual(calls.filter(call => call[0] === 'connect'), [['connect', oldEndpoint], ['connect', newEndpoint]]);
  assert.equal(calls.some(call => call[0] === 'connect' && call[1] === otherEndpoint), false);
});

test('discovers fully qualified TLS service types and still verifies the hardware serial', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing('')],
    [['mdns', 'services'], mdns(`${service} (2) _adb-tls-connect._tcp. ${newEndpoint}`)],
    [['connect', newEndpoint], 'connected'],
    [['-s', newEndpoint, 'shell', 'getprop', 'ro.serialno'], serial],
  ]);
  assert.equal(connectWatch({ serial, run }), newEndpoint);
  assert.deepEqual(calls, [['devices', '-l'], ['mdns', 'services'], ['connect', newEndpoint],
    ['-s', newEndpoint, 'shell', 'getprop', 'ro.serialno']]);
});

test('the optional service root dot preserves serial, service type and endpoint boundaries', () => {
  const ipv6 = '[2001:db8::10]:30001';
  const rows = [
    `${service} _adb-tls-connect._tcp. ${ipv6}`,
    `adb-${serial}MORE-example _adb-tls-connect._tcp. ${otherEndpoint}`,
    `${service} _adb-tls-pairing._tcp. ${oldEndpoint}`,
    `${service} _adb-tls-connect._tcp.. ${oldEndpoint}`,
    `${service} _adb-tls-connect._tcp.example ${oldEndpoint}`,
    `${service} _adb-tls-connect._tcp. example.invalid:30001`,
    `${service} _adb-tls-connect._tcp. 192.0.2.10:65536`,
    `${service} _adb-tls-connect._tcp. ${newEndpoint} extra`,
  ];
  assert.deepEqual(parseTlsServices(mdns(rows.join('\n')), serial), [{ instance: service, endpoint: ipv6 }]);
});

test('excludes another serial with a matching prefix, pairing, legacy and malformed endpoints', () => {
  const output = mdns([
    `adb-${serial}MORE-example _adb-tls-connect._tcp ${otherEndpoint}`,
    `${service} _adb-tls-pairing._tcp ${oldEndpoint}`,
    `${service} _adb._tcp ${oldEndpoint}`,
    `${service} _adb-tls-connect._tcp example.com:30001`,
    `${service} _adb-tls-connect._tcp 192.0.2.999:30001`,
    `${service} _adb-tls-connect._tcp 192.0.2.10:65536`,
    `${service} _adb-tls-connect._tcp 192.0.2.10:0`,
    `${service} _adb-tls-connect._tcp ${newEndpoint}`,
  ].join('\n'));
  assert.deepEqual(parseTlsServices(output, serial), [{ instance: service, endpoint: newEndpoint }]);
});

test('refuses an identity mismatch without returning or trying further targets', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing(`${named} device`)],
    [['-s', named, 'shell', 'getprop', 'ro.serialno'], 'ANOTHERWATCH'],
  ]);
  assert.throws(() => connectWatch({ serial, run }), /different device/);
  assert.equal(calls.length, 2);
});

test('does not accept connect output alone when the resulting transport is offline', () => {
  const { run } = mock([
    [['devices', '-l'], listing(`${oldEndpoint} offline`)],
    [['mdns', 'services'], mdns(`${service} _adb-tls-connect._tcp ${oldEndpoint}`)],
    [['connect', oldEndpoint], 'already connected'],
    [['-s', oldEndpoint, 'shell', 'getprop', 'ro.serialno'], new Error('device offline')],
  ]);
  assert.throws(() => connectWatch({ serial, run }), /No reachable paired watch/);
});

test('no discovery fails after identity checks without connecting unrelated devices or restarting ADB', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing(`${otherEndpoint} device model:Other`)],
    [['-s', otherEndpoint, 'shell', 'getprop', 'ro.serialno'], 'ANOTHERWATCH'],
    [['mdns', 'services'], mdns(`adb-OTHER-example _adb-tls-connect._tcp ${otherEndpoint}`)],
  ]);
  assert.throws(() => connectWatch({ serial, run }), /No reachable paired watch/);
  assert.deepEqual(calls, [['devices', '-l'], ['-s', otherEndpoint, 'shell', 'getprop', 'ro.serialno'], ['mdns', 'services']]);
});

test('online IP transport succeeds even when mDNS would be unavailable', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing(`${oldEndpoint} device`)],
    [['-s', oldEndpoint, 'shell', 'getprop', 'ro.serialno'], serial],
    [['mdns', 'services'], new Error('mDNS unavailable')],
  ]);
  assert.equal(connectWatch({ serial, run }), oldEndpoint);
  assert.deepEqual(calls, [['devices', '-l'], ['-s', oldEndpoint, 'shell', 'getprop', 'ro.serialno']]);
});

test('online IP identity errors are skipped and invalid or unavailable transports are never queried', () => {
  const ipv6 = '[2001:db8::10]:30001';
  const { run, calls } = mock([
    [['devices', '-l'], listing(`example.invalid:30001 device\n${otherEndpoint} unauthorized\n${oldEndpoint} device\n${newEndpoint} offline\n${ipv6} device`)],
    [['-s', oldEndpoint, 'shell', 'getprop', 'ro.serialno'], new Error('device disconnected')],
    [['-s', ipv6, 'shell', 'getprop', 'ro.serialno'], serial],
  ]);
  assert.equal(connectWatch({ serial, run }), ipv6);
  assert.deepEqual(calls, [['devices', '-l'], ['-s', oldEndpoint, 'shell', 'getprop', 'ro.serialno'], ['-s', ipv6, 'shell', 'getprop', 'ro.serialno']]);
});

test('mDNS errors are explicit and do not trigger alternate protocols', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing('')],
    [['mdns', 'services'], new Error('unsupported')],
  ]);
  assert.throws(() => connectWatch({ serial, run }), /mDNS discovery failed/);
  assert.equal(calls.length, 2);
});

test('ambiguous advertisement floods are bounded before any connection', () => {
  const { run, calls } = mock([
    [['devices', '-l'], listing('')],
    [['mdns', 'services'], mdns(Array.from({ length: 5 }, (_, index) => `${service} (${index + 2}) _adb-tls-connect._tcp 192.0.2.10:${31000 + index}`).join('\n'))],
  ]);
  assert.throws(() => connectWatch({ serial, run }), /Too many matching watch advertisements/);
  assert.equal(calls.length, 2);
});

test('device parsing preserves service names containing spaces and ignores headers', () => {
  assert.deepEqual(parseDevices(listing(`${service} (2)${'._adb-tls-connect._tcp'} device product:test\n${oldEndpoint} offline`)), [
    { transport: `${service} (2)${'._adb-tls-connect._tcp'}`, state: 'device' },
    { transport: oldEndpoint, state: 'offline' },
  ]);
});

test('arguments require an explicit safe target and reject repeated or unknown options', () => {
  assert.deepEqual(parseOptions(['--serial', serial, '--adb', 'C:/Android/platform-tools/adb.exe']), { serial, adb: 'C:/Android/platform-tools/adb.exe' });
  for (const args of [[], ['--serial', '-x'], ['--serial', 'watch;command'], ['--serial', serial, '--serial', serial], ['--serial', serial, '--port', '5555']]) {
    assert.throws(() => parseOptions(args), /Usage/);
  }
  let called = false;
  assert.throws(() => connectWatch({ serial: 'watch;command', run: () => { called = true; } }), /valid hardware serial/);
  assert.equal(called, false);
});
