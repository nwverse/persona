import test from 'node:test';
import assert from 'node:assert/strict';
import { existsSync, readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { deflateSync, inflateSync } from 'node:zlib';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@kit.AssetStoreKit') return { url: 'persona-screen-lifecycle:asset', shortCircuit: true };
    if (specifier === '@kit.ImageKit') return { url: 'persona-screen-lifecycle:image', shortCircuit: true };
    if (specifier === '@kit.IMEKit') return { url: 'persona-screen-lifecycle:ime', shortCircuit: true };
    if (specifier === '@kit.ArkTS') return { url: 'persona-screen-lifecycle:arkts', shortCircuit: true };
    if (specifier === '@kit.CryptoArchitectureKit') return { url: 'persona-screen-lifecycle:crypto', shortCircuit: true };
    if (specifier === '@kit.BasicServicesKit') return { url: 'persona-screen-lifecycle:zlib', shortCircuit: true };
    if (context.parentURL?.endsWith('/ComputerScreen.ets')) {
      if (specifier === './ClientModel') return { url: 'persona-screen-lifecycle:client', shortCircuit: true };
      if (specifier === './HubRepository') return { url: 'persona-screen-lifecycle:repository', shortCircuit: true };
      if (specifier === '../platform/HubTransport') return { url: 'persona-screen-lifecycle:transport', shortCircuit: true };
    }
    if (context.parentURL?.endsWith('.ts') && specifier.startsWith('.') && !specifier.endsWith('.ts'))
      return { url: new URL(specifier + '.ts', context.parentURL).href, shortCircuit: true };
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.')) {
      const base = new URL(specifier, context.parentURL);
      for (const extension of ['.ets', '.ts']) {
        const candidate = new URL(base.href + extension);
        if (existsSync(candidate)) return { url: candidate.href, shortCircuit: true };
      }
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'persona-screen-lifecycle:asset') return { format: 'module', shortCircuit: true, source: `
      export const asset = { Tag: { ALIAS:1, SECRET:2, DATA_LABEL_CRITICAL_1:3, DATA_LABEL_NORMAL_1:4,
        RETURN_TYPE:5, ACCESSIBILITY:6, SYNC_TYPE:7, CONFLICT_RESOLUTION:8, RETURN_LIMIT:9 },
        ReturnType: { ALL:10, ATTRIBUTES:11 }, Accessibility: { DEVICE_UNLOCKED:12 },
        SyncType: { NEVER:13 }, ConflictResolution: { OVERWRITE:14 },
        query: q => globalThis.screenAssets.query(q), add: q => globalThis.screenAssets.add(q), remove: q => globalThis.screenAssets.remove(q) };
    ` };
    if (url === 'persona-screen-lifecycle:ime') return { format: 'module', shortCircuit: true, source: `
      export const inputMethod = { getController: () => globalThis.screenTestIme,
        TextInputType: { TEXT: 0 }, EnterKeyType: { NEWLINE: 8 }, KeyboardStatus: { HIDE: 1, SHOW: 2 },
        Direction: { CURSOR_UP: 1, CURSOR_DOWN: 2, CURSOR_LEFT: 3, CURSOR_RIGHT: 4 } };
    ` };
    if (url === 'persona-screen-lifecycle:image') return { format: 'module', shortCircuit: true, source:
      'export const image = { PixelMapFormat: { BGRA_8888: 1 }, createPixelMapFromPixels: (...args) => globalThis.screenTestBitmap(...args) };' };
    if (url === 'persona-screen-lifecycle:arkts') return { format: 'module', shortCircuit: true, source: `
      class Encoder { encodeInto(value) { return new globalThis.TextEncoder().encode(value); }
        static create() { return new Encoder(); } }
      class Decoder { constructor(options = {}) { this.decoder = new globalThis.TextDecoder('utf-8', options); }
        decodeToString(value) { return this.decoder.decode(value); } }
      export const taskpool = {
        Task: class { constructor(fn, ...args) { this.fn = fn; this.args = args; } setTransferList() {} },
        execute: async task => task.fn(...task.args)
      };
      export const util = { TextEncoder: Encoder, TextDecoder: { create(_encoding, options = {}) { return new Decoder(options); } } };
    ` };
    if (url === 'persona-screen-lifecycle:crypto') return { format: 'module', shortCircuit: true, source: `import { createHash } from 'node:crypto'; export const cryptoFramework = { createMd() { const hash = createHash('sha256'); return { updateSync({ data }) { hash.update(data); }, digestSync() { return {data: new Uint8Array(hash.digest())}; } }; } };` };
    if (url === 'persona-screen-lifecycle:zlib') return { format: 'module', shortCircuit: true, source: `
      export const zlib = { createZipSync() { return { getZStream: async () => ({}), inflateInit2: async () => 0,
        inflate: async () => 0, inflateEnd: async () => 0 }; }, ReturnStatus: { OK: 0, BUF_ERROR: -5 }, CompressFlushMode: { NO_FLUSH: 0 } };
    ` };
    if (url === 'persona-screen-lifecycle:client') return { format: 'module', shortCircuit: true, source: 'export class ClientModel {};' };
    if (url === 'persona-screen-lifecycle:repository') return { format: 'module', shortCircuit: true, source: 'export function segment(value) { return encodeURIComponent(value); } export class HubRepository {};' };
    if (url === 'persona-screen-lifecycle:transport') return { format: 'module', shortCircuit: true, source: `
      export class HubTransport {}
      export class HttpFailure extends Error { constructor(status, code) { super(code); this.status = status; this.code = code; } }
    ` };
    if (url.endsWith('.ets') || url.endsWith('.ts')) {
      let source = readFileSync(new URL(url), 'utf8');
      if (url.endsWith('.ets')) source = source.replace(/@(?:ObservedV2|Concurrent)\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const { ComputerScreen } = await import('../entry/src/main/ets/models/ComputerScreen.ets');
const { ResourceRow } = await import('../entry/src/main/ets/models/Wire.ets');
const { ScreenDisplays } = await import('../entry/src/main/ets/models/ScreenDisplays.ets');
const { ScreenPresenter } = await import('../entry/src/main/ets/platform/ScreenPresenter.ets');
const { ScreenCredentialStore } = await import('../entry/src/main/ets/platform/ScreenCredentialStore.ets');
const { ScreenKeyboard } = await import('../entry/src/main/ets/platform/ScreenKeyboard.ets');
const { RFBFramebuffer } = await import('../entry/src/main/ets/core/RFBFramebuffer.ts');
const { ScreenPoint, ScreenViewport } = await import('../entry/src/main/ets/models/ScreenViewport.ets');

function deferred() {
  let resolve;
  const promise = new Promise((yes) => { resolve = yes; });
  return { promise, resolve };
}

async function waitFor(predicate) {
  for (let index = 0; index < 500; index++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  assert.fail('timed out waiting for the screen lifecycle event');
}

class TestStream {
  closed = false;
  onState;
  onBinary;
  connectGate;
  failBeforeConnectResolves = false;
  sent = [];

  async connect(onMessage, onState, onBinary) {
    this.onState = onState;
    this.onBinary = onBinary;
    if (this.failBeforeConnectResolves) {
      onMessage('unexpected text frame');
      await this.connectGate.promise;
    }
  }

  sendBinary(bytes) { if (this.closed) return false; this.sent.push([...new Uint8Array(bytes)]); return true; }
  close() { this.closed = true; }
  receive(bytes) {
    const copy = new Uint8Array(bytes);
    this.onBinary?.(copy.buffer);
  }
  state(value) { this.onState?.(value); }
}

function screenFixture(stream) {
  const machine = ResourceRow.from({ id: 'mac-1', manager: 'work', name: 'Mac', online: true, trust: 'confirmed', os: 'mac' }, 'machine');
  const row = ResourceRow.from({ id: 'mac-1', manager: 'work', name: 'Mac', online: true, trust: 'confirmed', os: 'mac' }, 'machine');
  const repository = {
    transport: { stream: () => stream, request: async () => ({}) },
    async call() { return { screen_sharing: true }; }
  };
  const parent = {
    demo: false,
    connected: true,
    profile: { manager: 'work' },
    machines: [machine],
    async viewFor() { return this; },
    connectionGeneration() { return 7; },
    scope() { return 'hub:work'; },
    owns(generation, scope) { return this.connected && generation === 7 && scope === 'hub:work'; },
    repo() { return repository; }
  };
  return { screen: new ComputerScreen(), parent, row, repository };
}

function be32(value) { return [(value >>> 24) & 255, (value >>> 16) & 255, (value >>> 8) & 255, value & 255]; }

function serverHandshake(width = 2, height = 1) {
  const name = [...new TextEncoder().encode('Mac')];
  return new Uint8Array([...new TextEncoder().encode('RFB 003.008\n'), 1, 1, 0, 0, 0, 0,
    (width >>> 8) & 255, width & 255, (height >>> 8) & 255, height & 255,
    ...new Array(16).fill(0), ...be32(name.length), ...name]);
}

function rawUpdate() {
  return new Uint8Array([0, 0, 0, 1, 0, 0, 0, 0, 0, 2, 0, 1, 0, 0, 0, 0, 1, 2, 3, 0, 4, 5, 6, 0]);
}

test('a port-stream failure while connect is pending invalidates the attempt before RFB can start', async () => {
  const stream = new TestStream();
  stream.failBeforeConnectResolves = true;
  stream.connectGate = deferred();
  const { screen, parent, row } = screenFixture(stream);
  const opening = screen.open(parent, row);

  await waitFor(() => screen.phase === 'failed');
  assert.equal(screen.socketReady, false);
  assert.equal(screen.client, undefined);
  stream.connectGate.resolve();
  await opening;

  assert.equal(screen.phase, 'failed');
  assert.equal(screen.socketReady, false);
  assert.equal(screen.client, undefined);
  assert.equal(stream.closed, true);
  screen.close();
});

test('an established stream failure clears the frame snapshot and advances its revision', async () => {
  const stream = new TestStream();
  const { screen, parent, row } = screenFixture(stream);
  const opening = screen.open(parent, row);
  const initialRevision = screen.frameRevision;

  await waitFor(() => stream.onBinary !== undefined);
  stream.receive(serverHandshake());
  await waitFor(() => screen.phase === 'live');
  stream.receive(rawUpdate());
  await waitFor(() => screen.frameRevision > initialRevision);
  const revision = screen.frameRevision;

  stream.state('closed');
  await waitFor(() => screen.phase === 'failed');
  await opening;

  assert.ok(screen.frameRevision > revision);
  assert.equal(screen.remoteWidth, 0);
  assert.equal(screen.remoteHeight, 0);
  assert.equal(screen.presentationSource(), undefined);
  assert.equal(screen.controlAllowed(), false);
  screen.close();
});

async function openCredentialForm() {
  const stream = new TestStream();
  const { screen, parent, row } = screenFixture(stream);
  const opening = screen.open(parent, row);
  await waitFor(() => stream.onBinary !== undefined);
  const version = new TextEncoder().encode('RFB 003.008\n');
  stream.receive([...version, 1, 19, 0, 2, 0, 1, 0, 0, 1, 0]);
  await waitFor(() => screen.credentialsRequested);
  return { screen, stream, parent, opening };
}

test('login waits for the user, submits once, and reports a stalled authentication', async (t) => {
  const { screen, stream, opening } = await openCredentialForm();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  t.mock.timers.tick(60000);
  assert.equal(screen.credentialsRequested, true);
  assert.equal(screen.submitCredentials('fixture', 'sample'), true);
  assert.equal(screen.credentialsRequested, false);
  assert.equal(screen.phase, 'authenticating');
  assert.equal(screen.submitCredentials('fixture', 'sample'), false);
  t.mock.timers.tick(20000);
  assert.equal(screen.messageKey, 'computer_screen_auth_timeout');
  assert.equal(stream.closed, true);
  await opening;
  screen.close();
});

test('rejecting a login reports the refusal and cancels the pending deadline', async (t) => {
  const { screen, stream, opening } = await openCredentialForm();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  screen.submitCredentials('fixture', 'sample');
  stream.receive([...be32(1), ...be32(0)]);
  await opening;
  assert.equal(screen.phase, 'auth_failed');
  t.mock.timers.tick(30000);
  assert.equal(screen.messageKey, 'computer_screen_auth_failed');
  screen.close();
});

test('losing the owner while the form is open dismisses it without sending credentials', async () => {
  const { screen, parent, opening } = await openCredentialForm();
  parent.connected = false;
  assert.equal(screen.submitCredentials('fixture', 'sample'), false);
  assert.equal(screen.credentialsRequested, false);
  assert.equal(screen.messageKey, 'computer_screen_connection_lost');
  await opening;
  screen.close();
});

test('an authenticated computer that sends no first frame has a separate timeout', async (t) => {
  const stream = new TestStream();
  const { screen, parent, row } = screenFixture(stream);
  const opening = screen.open(parent, row);
  await waitFor(() => stream.onBinary !== undefined);
  t.mock.timers.enable({ apis: ['setTimeout'] });
  stream.receive(serverHandshake());
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(screen.messageKey, 'computer_screen_waiting_frame');
  t.mock.timers.tick(20000);
  assert.equal(screen.messageKey, 'computer_screen_frame_timeout');
  await opening;
  screen.close();
});

function u16(value) { return [value >>> 8, value & 255]; }

function solidDisplayFrame(view, blue) {
  const tiles = Math.ceil(view.width / 64) * Math.ceil(view.height / 64);
  const samples = Buffer.alloc(tiles * 4, Buffer.from([1, blue, 7, 9]));
  const data = deflateSync(samples);
  return [0, 0, 0, 1, ...u16(view.x), ...u16(view.y), ...u16(view.width), ...u16(view.height),
    ...be32(16), ...be32(data.length), ...data];
}

const layouts = [
  { name: 'offset Retina pair', width: 3840, height: 4394, displays: [
    { id: 1, main: true, x: 0, y: 0, width: 1728, height: 1117 },
    { id: 2, x: -87, y: -1080, width: 1920, height: 1080 }], expectedOrigins: [[87, 2160], [0, 0]] },
  ...['horizontal', 'vertical'].map((direction) => ({
    name: 'three 4K screens arranged ' + direction,
    width: direction === 'horizontal' ? 11520 : 3840, height: direction === 'horizontal' ? 2160 : 6480,
    displays: [0, 1, 2].map(i => ({ id: i + 1, main: i === 1, width: 3840, height: 2160,
      x: direction === 'horizontal' ? (i - 1) * 3840 : 0, y: direction === 'vertical' ? (i - 1) * 2160 : 0 }))
  }))
];

for (const layout of layouts) {
  test(layout.name + ' requests full Mac updates and renders and controls the selected display on one login', async () => {
    const stream = new TestStream();
    const { screen, parent, row, repository } = screenFixture(stream);
    repository.call = async () => ({ screen_sharing: true, displays: layout.displays });
    const opening = screen.open(parent, row);
    try {
      await waitFor(() => stream.onBinary !== undefined);
      screen.client.zrle = { inflate: async bytes => inflateSync(bytes), close() {} };
      stream.receive(serverHandshake(layout.width, layout.height));
      await waitFor(() => screen.phase === 'live');
      assert.equal(screen.displays.length, layout.displays.length);
      assert.ok(screen.displays[0].main);
      if (layout.expectedOrigins) assert.deepEqual(screen.displays.map(d => [d.x, d.y]), layout.expectedOrigins);
      for (let index = 0; index < screen.displays.length; index++) {
        const display = screen.displays[index];
        if (index) screen.selectDisplay(index);
        assert.equal(screen.selectedDisplay, display.key);
        assert.equal(screen.hasFrame, false);
        assert.equal(screen.presentationSource(), undefined);
        assert.deepEqual(stream.sent.at(-1), [3, 0, 0, 0, 0, 0, ...u16(layout.width), ...u16(layout.height)]);
        assert.ok(screen.client.framebuffer.pixels.byteLength <= 32 * 1024 * 1024);
        if (index) {
          stream.receive(solidDisplayFrame(screen.displays[index - 1], 100));
          await new Promise(resolve => setImmediate(resolve));
          assert.equal(screen.hasFrame, false);
        }
        stream.receive(solidDisplayFrame(display, index + 1));
        await waitFor(() => screen.hasFrame);
        const image = screen.presentationSource();
        assert.deepEqual([...image.pixels.slice(0, 4)], [index + 1, 7, 9, 255]);
        assert.deepEqual([...image.pixels.slice(-4)], [index + 1, 7, 9, 255]);
        assert.equal(screen.grantControl(), true);
        assert.equal(screen.controlling, true);
        assert.equal(screen.pointerFromView(200, 150, 400, 300, 0), true);
        assert.deepEqual(stream.sent.at(-1), [5, 0, ...u16(display.x + Math.floor(display.width / 2)), ...u16(display.y + Math.floor(display.height / 2))]);
        screen.releaseControl();
        assert.equal(screen.controlling, false);
      }
    } finally { screen.close(); await opening; }
  });
}

test('adjoining displays in a vertical column keep their shared edge independent of enumeration order', () => {
  const rows = [
    { id: 1, main: true, x: 0, y: 0, width: 1117, height: 1728, scale: 2 },
    { id: 2, x: -1080, y: 870, width: 1080, height: 1920, scale: 2 },
    { id: 3, x: -1080, y: -1050, width: 1080, height: 1920, scale: 2 }
  ];
  for (const displays of [rows, rows.toReversed()]) {
    const mapped = ScreenDisplays.layout(4394, 7680, [], ScreenDisplays.machine({ displays }));
    assert.deepEqual(mapped.map(d => [d.key, d.x, d.y]), [['1',2160,2970], ['3',0,0], ['2',0,3840]]);
  }
});

test('mixed display scales and RFB display IDs preserve the main monitor selection', () => {
  const machine = ScreenDisplays.machine({ displays: [
    { id: 5, x: 0, y: 0, width: 1728, height: 1117, scale: 2, main: true },
    { id: 9, x: -1920, y: 0, width: 1920, height: 1080, scale: 1 }] });
  const displays = ScreenDisplays.layout(5376, 2234, [], machine);
  assert.deepEqual(displays.map(d => [d.key, d.x, d.width]), [['5', 1920, 3456], ['9', 0, 1920]]);
  const reported = displays.toReversed().map((d, id) => ({ ...d, id }));
  assert.deepEqual(ScreenDisplays.layout(5376, 2234, reported, machine).map(d => d.key), ['5', '9']);
});

test('a dense rectangle decoded for the previous monitor cannot overwrite the new monitor', async () => {
  const stream = new TestStream(), gate = deferred();
  const { screen, parent, row, repository } = screenFixture(stream);
  repository.call = async () => ({ screen_sharing: true, displays: [
    { id: 1, x: 0, y: 0, width: 512, height: 512, main: true }, { id: 2, x: 512, y: 0, width: 512, height: 512 }] });
  const opening = screen.open(parent, row);
  try {
    await waitFor(() => stream.onBinary !== undefined);
    screen.client.zrle = { inflate: async bytes => inflateSync(bytes), close() {} };
    const decode = screen.client.decodePatch;
    let calls = 0;
    screen.client.decodePatch = async (...args) => { calls++; if (calls === 1) await gate.promise; return decode(...args); };
    stream.receive(serverHandshake(1024, 512));
    await waitFor(() => screen.isLive());
    const tile = Buffer.from([2, 1, 2, 3, 7, 8, 9, ...new Array(512).fill(0xaa)]);
    const data = deflateSync(Buffer.alloc(tile.length * 128, tile));
    const update = [0, 0, 0, 1, 0, 0, 0, 0, ...u16(1024), ...u16(512), ...be32(16), ...be32(data.length), ...data];
    stream.receive(update);
    await waitFor(() => calls === 1);
    screen.selectDisplay(1);
    gate.resolve();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(screen.hasFrame, false);
    stream.receive(update);
    await waitFor(() => screen.hasFrame);
    assert.equal(calls, 2);
    assert.deepEqual(screen.presentationSource().pixel(512, 0), [7, 8, 9, 255]);
    assert.deepEqual(screen.presentationSource().pixel(513, 0), [1, 2, 3, 255]);
  } finally { screen.close(); gate.resolve(); await opening; }
});

test('pinching stays anchored, pans locally, and cannot click the Mac when either finger lifts', () => {
  const v = new ScreenViewport(), sent = [];
  const point = (x, y) => new ScreenPoint(x, y);
  const event = (kind, changed, fingers, control = true) =>
    v.touch(kind, changed, fingers, control, (...packet) => sent.push(packet), () => {});
  v.layout(400, 300, 2);
  assert.deepEqual([v.rect.width, v.rect.height, v.rect.y], [400, 200, 50]);
  event('down', point(80, 150), [point(80, 150)]);
  event('down', point(120, 150), [point(80, 150), point(120, 150)]);
  event('move', point(60, 150), [point(60, 150), point(140, 150)]);
  assert.equal(v.zoom, 2);
  assert.equal(v.rect.x, -100);
  event('up', point(60, 150), [point(140, 150)]);
  event('up', point(140, 150), []);
  assert.deepEqual(sent, []);
  event('down', point(100, 150), [point(100, 150)]);
  event('up', point(100, 150), []);
  assert.deepEqual(sent, [[0.25, 0.5, 1], [0.25, 0.5, 0]]);
  event('down', point(150, 150), [point(150, 150)], false);
  event('move', point(90, 150), [point(90, 150)], false);
  assert.equal(v.rect.x, -160);
  event('up', point(90, 150), [], false);
  v.layout(300, 400, 2);
  assert.equal(v.zoom, 2);
  assert.equal(v.rect.y, 50);
  v.reset();
  assert.deepEqual([v.zoom, v.rect.width, v.rect.height, v.rect.x, v.rect.y], [1, 300, 150, 0, 125]);
});

test('read-only pinches stop at the zoom bounds and letterboxing does not send a pointer', () => {
  const v = new ScreenViewport(), sent = [];
  v.layout(400, 400, 2);
  const p = (x, y = 200) => new ScreenPoint(x, y);
  const event = (kind, changed, fingers, control = false) => v.touch(kind, changed, fingers, control, (...a) => sent.push(a), () => {});
  event('down', p(200, 50), [p(200, 50)], true);
  event('up', p(200, 50), [], true);
  event('down', p(200), [p(200)]);
  event('down', p(215), [p(185), p(215)]);
  event('move', p(395), [p(5), p(395)]);
  assert.equal(v.zoom, 5);
  event('move', p(202), [p(198), p(202)]);
  assert.equal(v.zoom, 1);
  assert.equal(sent.length, 0);
});

test('one finger pans a magnified desktop on both axes, including immediately after the pinch, in either control state', () => {
  for (const control of [false, true]) {
    const v = new ScreenViewport(), sent = [], p = (x, y) => new ScreenPoint(x, y);
    v.layout(400, 800, 2);
    let clock = 0;
    const event = (kind, changed, fingers) => v.touch(kind, changed, fingers, control, (...packet) => sent.push(packet), () => {}, clock += 12);
    event('down', p(150, 400), [p(150, 400)]);
    event('down', p(250, 400), [p(150, 400), p(250, 400)]);
    event('move', p(100, 400), [p(100, 400), p(300, 400)]);
    assert.deepEqual([v.zoom, v.rect.x, v.rect.y], [2, -200, 200]);
    event('up', p(300, 400), [p(100, 400)]);
    event('move', p(175, 470), [p(175, 470)]);
    assert.deepEqual([v.rect.x, v.rect.y], [-125, 270]);
    event('up', p(175, 470), []);
    event('down', p(190, 400), [p(190, 400)]);
    event('move', p(115, 310), [p(115, 310)]);
    event('up', p(115, 310), []);
    assert.deepEqual([v.rect.x, v.rect.y], [-200, 180]);
    assert.equal(v.displayedZoom, 1);
    v.publish();
    assert.deepEqual([v.displayedZoom, v.displayed.x, v.displayed.y], [2, -200, 180]);
    assert.equal(sent.length, 0);
    if (control) {
      event('down', p(200, 400), [p(200, 400)]);
      clock += 500;
      event('move', p(220, 400), [p(220, 400)]);
      event('up', p(220, 400), []);
      assert.deepEqual(sent.map(packet => packet[2]), [1, 1, 0]);
      assert.equal(v.rect.x, -200);
    }
  }
});

test('remote controls accept incremental updates and local viewing resumes one pending refresh', async () => {
  const stream = new TestStream();
  const { screen, parent, row } = screenFixture(stream);
  const opening = screen.open(parent, row);
  try {
    await waitFor(() => stream.onBinary !== undefined);
    stream.receive(serverHandshake());
    await waitFor(() => screen.isLive());
    const calls = stream.sent.length;
    stream.receive(rawUpdate());
    await waitFor(() => screen.hasFrame);
    assert.equal(stream.sent.length, calls + 1);
    assert.deepEqual(stream.sent.at(-1).slice(0, 2), [3, 1]);
    const before = stream.sent.length;
    screen.setViewingGesture(true);
    screen.refresh(); screen.refresh();
    assert.equal(stream.sent.length, before);
    screen.setViewingGesture(false); screen.setViewingGesture(false);
    assert.equal(stream.sent.length, before + 1);
    assert.deepEqual(stream.sent.at(-1).slice(0, 2), [3, 0]);
  } finally { screen.close(); await opening; }
});

const pageSource = readFileSync(new URL('../entry/src/main/ets/components/ComputerScreenPage.ets', import.meta.url), 'utf8').replace(/@Monitor\([^)]*\)\s*/g, '');
function componentMethod(source, name) {
  const start = source.search(new RegExp('^  (?:private )?(?:async )?' + name + '\\(', 'm'));
  assert.ok(start >= 0, name); const body = source.indexOf('{',start); let depth=1,quote='',escaped=false,at=body+1;
  for(; at<source.length && depth; at++){const c=source[at]; if(quote){if(escaped)escaped=false;else if(c==='\\')escaped=true;else if(c===quote)quote='';}else if(c==="'"||c==='"'||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}
  return source.slice(start,at);
}
const pageMethod = name => componentMethod(pageSource, name);
test('stalled WebSocket writes replace waiting mouse motion while preserving every button and keyboard transition', async () => {
  const src = readFileSync(new URL('../entry/src/main/ets/platform/HubTransport.ets', import.meta.url), 'utf8');
  const methods = ['sendBinary', 'enqueueFrame'].map(name => componentMethod(src, name));
  const crypto = { createRandom: () => ({ generateRandom: async () => ({ data: new Uint8Array(4) }) }) };
  const Transport = new Function('cryptoFramework', 'MAX_FRAME_BYTES', 'toArrayBuffer', 'concatArrays',
    'return ' + stripTypeScriptTypes('class Transport {' + methods.join('\n') + '}'))(
    crypto, 1024 * 1024, array => Uint8Array.from(array).buffer, arrays => Buffer.concat(arrays));
  const tx = new Transport(), gate = deferred(), written = [];
  tx.sendQueue = gate.promise; tx.upgraded = true; tx.queuedFrames = 0; tx.queuedBytes = 0; tx.generation = 1;
  tx.tlsSocket = { send: async bytes => written.push([...new Uint8Array(bytes).subarray(6)]) };
  tx.withTimeout = promise => promise;
  const pointer = (x, button) => new Uint8Array([5, button, 0, x, 0, 3]).buffer;
  tx.sendBinary(pointer(5, 1));
  for (let x = 6; x <= 90; x++) tx.sendBinary(pointer(x, 1), true);
  tx.sendBinary(new Uint8Array([4, 1, 0, 0, 0, 0, 0, 65]).buffer);
  tx.sendBinary(pointer(91, 1), true); tx.sendBinary(pointer(92, 1), true);
  tx.sendBinary(pointer(93, 0));
  assert.equal(tx.queuedFrames, 5);
  gate.resolve(); await tx.sendQueue;
  assert.deepEqual(written.map(packet => [packet[0], packet[1], packet[3]]),
    [[5, 1, 5], [5, 1, 90], [4, 1, 0], [5, 1, 92], [5, 0, 93]]);
});

test('a system touch cancellation with no remaining fingers releases the pointer and cancels navigation', () => {
  const Subject = new Function('TouchType', 'ScreenPoint', 'return ' +
    stripTypeScriptTypes('class Subject {' + pageMethod('touchFrame') + '}'))({ Cancel: 3 }, ScreenPoint);
  const page = new Subject();
  let released = 0, cancelled = 0;
  page.pointerDown = true;
  page.screen = { releasePointer: () => released++ };
  page.viewport = { cancel: () => cancelled++ };
  page.setViewingGesture = value => assert.equal(value, false);
  page.touchFrame({ type: 3, changedTouches: [], touches: [] });
  assert.equal(page.pointerDown, false);
  assert.equal(released, 1);
  assert.equal(cancelled, 1);
});

const PageBehavior = new Function('tr','return '+stripTypeScriptTypes('class PageBehavior {'+
 ['phaseChanged','clearFrame','clearFields','closeKeyboard','confirmControl','isVisible'].map(pageMethod).join('\n')+'}'))(key=>key);

test('leaving a sideways fullscreen computer restores orientation before releasing the full window', (t) => {
  const source = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8').replace(/@Monitor\([^)]*\)\s*/g, '');
  const methods = ['exitComputerFullscreen', 'finishComputerFullscreen', 'computerWindowChanged'].map(name => componentMethod(source, name));
  const WindowBehavior = new Function('return ' + stripTypeScriptTypes('class WindowBehavior {' + methods.join('\n') + '}'))();
  const page = new WindowBehavior();
  t.mock.timers.enable({ apis: ['setTimeout'] });
  let orientation = 'landscape';
  page.computerExitTimer = -1;
  page.fullscreenComputer = 'computer';
  page.computerReturnOrientation = 'portrait';
  page.environment = { viewingOrientation: () => orientation, setComputerImmersive() {} };
  page.exitComputerFullscreen();
  assert.equal(page.computerOrientation, 'portrait');
  assert.equal(page.fullscreenComputer, 'computer');
  orientation = 'portrait';
  page.computerWindowChanged();
  assert.equal(page.fullscreenComputer, '');
  assert.equal(page.computerExitTimer, -1);
  orientation = 'landscape';
  page.fullscreenComputer = 'computer';
  page.exitComputerFullscreen();
  t.mock.timers.tick(1000);
  assert.equal(page.fullscreenComputer, '');
});

test('the screen publishes the latest pending change and rejects a stale buffer after switching displays', async () => {
  const jobs = [];
  const RenderPage = new Function('return ' + stripTypeScriptTypes('class RenderPage {' +
    ['decodeFrame', 'renderFrames', 'clearFrame'].map(pageMethod).join('\n') + '}'))();
  const page = new RenderPage();
  let width = 1;
  page.screen = { isLive: () => true, hasFrame: true, connectionRevision: () => 1, displayAspect: () => width,
    presentationSource: () => ({ storedWidth: width, storedHeight: 1 }) };
  page.presenter = { update: async source => { const job = deferred(); jobs.push({ job, width: source.storedWidth }); return job.promise; }, clear() {} };
  page.previewStep = () => 1;
  page.isVisible = () => true; page.viewport = { layout() {} }; page.frameEpoch = 0;
  page.decodeFrame();
  width = 2; page.decodeFrame(); width = 3; page.decodeFrame();
  assert.deepEqual(jobs.map(x => x.width), [1]);
  jobs[0].job.resolve({ id: 1 }); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(jobs.map(x => x.width), [1, 3]);
  page.clearFrame();
  jobs[1].job.resolve({ id: 3 }); await new Promise(resolve => setImmediate(resolve));
  assert.equal(page.framePixels, undefined);
  assert.equal(page.rendering, false);
});

test('alternating native screen buffers erase old window positions and carry over every missed dirty region', async () => {
  const source = new RFBFramebuffer(640, 128), presenter = new ScreenPresenter();
  const buffers = [], written = [];
  globalThis.screenTestBitmap = async (data, options) => {
    const pixels = new Uint8Array(data);
    const map = { pixels, released: 0, release() { this.released++; }, async writePixels(patch) {
      written.push(patch.pixels.byteLength);
      const { x, y, size } = patch.region, bytes = new Uint8Array(patch.pixels);
      for (let row = 0; row < size.height; row++) pixels.set(bytes.subarray(row * patch.stride, (row + 1) * patch.stride),
        ((y + row) * options.size.width + x) * 4);
    }};
    assert.equal(options.editable, true); buffers.push(map); return map;
  };
  try {
    let old = 0;
    for (const next of [24, 210, 400, 50, 290]) {
      for (const [x, color] of [[old, 0], [next, 240]]) {
        source.fill(x, 10, 12, 12, color, color, color); source.changed(x, 10, 12, 12);
      }
      const shown = await presenter.update(source);
      assert.deepEqual(shown.pixels, source.pixels);
      assert.equal(await presenter.update(source), undefined);
      old = next;
    }
    assert.equal(buffers.length, 2);
    assert.ok(written.length > 0 && written.every(size => size < 640 * 128 * 4));
    for (let y = 0; y < source.height; y += 8) source.fill(0, y, source.width, 2, 225, 225, 225);
    source.changed(0, 0, source.width, source.height);
    for (let i = 0; i < 5; i++) {
      source.copy(0, 0, source.width, source.height - 3, 0, 3);
      source.fill(0, source.height - 3, source.width, 3, 17, 17, 17);
      source.changed(0, 0, source.width, source.height - 3);
      source.changed(0, source.height - 3, source.width, 3);
      assert.deepEqual((await presenter.update(source)).pixels, source.pixels);
    }
    for (const step of [2, 1, 2, 1]) {
      source.fill(11, 21, 7, 5, step * 23, 40, 71); source.changed(11, 21, 7, 5);
      const shown = await presenter.update(source, step);
      const w = Math.ceil(source.width / step);
      const actual = new Uint32Array(shown.pixels.buffer), expected = new Uint32Array(source.pixels.buffer);
      for (let y = 0; y < source.height; y += step) for (let x = 0; x < source.width; x += step) {
        assert.equal(actual[(y / step) * w + x / step], expected[y * source.width + x]);
      }
    }
    presenter.clear();
    assert.ok(buffers.length === 4 && buffers.every(b => b.released === 1));
  } finally { presenter.clear(); delete globalThis.screenTestBitmap; }
});

function pageFixture(){
 const page=new PageBehavior();let revision=1;let grants=0;let dialog;
 page.active=true;page.mounted=true;page.frameEpoch=0;page.row={id:'mac',title:'Mac',key:()=> 'mac'};
 page.screen={phase:'live',isLive:()=>page.screen.phase==='live',controlAllowed:()=>false,connectionRevision:()=>revision,releasePointer:()=>{page.remoteReleaseCount=(page.remoteReleaseCount||0)+1;},grantControl:()=>grants++};
 page.getUIContext=()=>({showAlertDialog:value=>dialog=value});
 return {page,bump:()=>revision++,grants:()=>grants,confirm:()=>dialog.secondaryButton.action()};
}
test('a failed connection immediately releases its native frame and credential drafts',()=>{
 const {page}=pageFixture();let released=0;page.presenter={clear:()=>{released++;}};page.framePixels={};page.accountDraft='synthetic';page.passwordDraft='synthetic-secret';page.keyboardShown=true;let detached=0;page.keyboard={close:()=>detached++};
 page.screen.phase='credentials';page.phaseChanged();assert.equal(page.passwordDraft,'synthetic-secret');assert.equal(released,1);
 page.screen.phase='failed';page.phaseChanged();assert.equal(page.accountDraft,'');assert.equal(page.passwordDraft,'');assert.equal(detached,1);assert.equal(page.keyboardShown,false);assert.equal(page.framePixels,undefined);
});
test('a delayed control confirmation cannot authorize a replacement or hidden session',()=>{
 const f=pageFixture();f.page.confirmControl();f.bump();f.confirm();assert.equal(f.grants(),0);
 f.page.confirmControl();f.page.active=false;f.confirm();assert.equal(f.grants(),0);
 f.page.active=true;f.page.confirmControl();f.confirm();assert.equal(f.grants(),1);
});
async function keyboardFixture(t) {
  const stream = new TestStream();
  const { screen, parent, row, repository } = screenFixture(stream);
  const opening = screen.open(parent, row);
  t.after(async () => { screen.close(); await opening; });
  await waitFor(() => stream.onBinary !== undefined);
  stream.receive(serverHandshake());
  await waitFor(() => screen.phase === 'live');
  assert.ok(screen.keysReadyAt > Date.now());
  assert.equal(screen.grantControl(), true);
  return { screen, repository, stream };
}

test('the phone IME attaches before subscribing, opens with no lost first commit, and detaches on hide', async t => {
  const handlers = new Map(), typed = [], pressed = [], calls = [];
  let attached = false;
  globalThis.screenTestIme = {
    async attach(show) { assert.equal(show, false); attached = true; calls.push('attach'); },
    on(name, fn) { assert.ok(attached); handlers.set(name, fn); },
    off(name, fn) { assert.equal(handlers.get(name), fn); handlers.delete(name); },
    async showTextInput() {
      calls.push('show');
      handlers.get('sendKeyboardStatus')(1);
      handlers.get('sendKeyboardStatus')(2);
      handlers.get('insertText')('你好');
    },
    async detach() { assert.equal(handlers.size, 0); attached = false; calls.push('detach'); }
  };
  t.after(() => { delete globalThis.screenTestIme; });
  const keyboard = new ScreenKeyboard(text => typed.push(text), (key, count) => pressed.push([key, count]), () => keyboard.close());
  await keyboard.open();
  assert.deepEqual(typed, ['你好']);
  assert.equal(handlers.has('setPreviewText'), false);
  handlers.get('deleteLeft')(2);
  handlers.get('deleteRight')(1);
  handlers.get('moveCursor')(3);
  handlers.get('sendFunctionKey')({});
  assert.deepEqual(pressed, [[0xff08, 2], [0xffff, 1], [0xff51, 1], [0xff0d, 1]]);
  const stale = handlers.get('insertText');
  handlers.get('sendKeyboardStatus')(1);
  stale('late');
  await waitFor(() => !attached);
  assert.deepEqual(typed, ['你好']);
  assert.deepEqual(calls, ['attach', 'show', 'detach']);
});

function keyDowns(stream) {
  const keys = [];
  for (const packet of stream.sent.filter(bytes => bytes[0] === 4)) {
    for (let i = 0; i < packet.length; i += 8) {
      if (packet[i + 1] === 1) keys.push(Buffer.from(packet).readUInt32BE(i + 4));
    }
  }
  return keys;
}

test('early input waits for Mac sign-in and later keys follow acknowledged IME commits in order', async t => {
  const { screen, repository, stream } = await keyboardFixture(t);
  const gate = deferred(), writes = [];
  repository.transport.request = async (path, method, payload) => {
    assert.match(path, /computer\/clipboard$/);
    assert.equal(method, 'POST');
    writes.push(JSON.parse(payload).text);
    return gate.promise;
  };
  screen.keysReadyAt = Date.now() + 40;
  const sent = [screen.sendText('a'), screen.sendText('中文🦦'), screen.pressKey(0xff08), screen.pressKey(0xff0d)];
  await new Promise(resolve => setTimeout(resolve, 2));
  assert.deepEqual(keyDowns(stream), []);
  await waitFor(() => writes.length === 1);
  assert.deepEqual(keyDowns(stream), [97]);
  gate.resolve({});
  assert.deepEqual(await Promise.all(sent), [true, true, true, true]);
  assert.deepEqual(writes, ['中文🦦']);
  assert.deepEqual(keyDowns(stream), [97, 0xffeb, 0x76, 0xff08, 0xff0d]);
  assert.equal(await screen.sendText('b'.repeat(300)), true);
  assert.deepEqual(keyDowns(stream).slice(5), Array(300).fill(98));
});

test('revocation discards pending text and keys even after regrant; clipboard errors never paste stale text', async t => {
  const { screen, repository, stream } = await keyboardFixture(t);
  screen.keysReadyAt = 0;
  const gate = deferred();
  let pendingWrite = false;
  repository.transport.request = () => { pendingWrite = true; return gate.promise; };
  const paste = screen.sendText('旧'), enter = screen.pressKey(0xff0d);
  await waitFor(() => pendingWrite);
  screen.releaseControl();
  assert.equal(await screen.pressKey(0xff09), false);
  screen.grantControl();
  gate.resolve({});
  assert.deepEqual(await Promise.all([paste, enter]), [false, false]);
  assert.deepEqual(keyDowns(stream), []);
  repository.transport.request = async () => { throw new Error('offline'); };
  assert.deepEqual(await Promise.all([screen.sendText('新'), screen.pressKey(0xff09)]), [false, true]);
  assert.equal(screen.keyboardInputFailed, true);
  assert.deepEqual(keyDowns(stream), [0xff09]);
});


test('saved Mac logins are unlocked-only atomic assets scoped to a pairing, manager and machine', async t => {
  const records = new Map(), alias = q => Buffer.from(q.get(1)).toString('hex');
  const copy = m => new Map([...m].map(([k,v]) => [k,v instanceof Uint8Array ? v.slice() : v]));
  globalThis.screenAssets = {
    async add(q) {
      assert.equal(q.get(6),12); assert.equal(q.get(7),13); assert.equal(q.get(8),14);
      assert.ok(q.get(2).byteLength <= 1024); records.set(alias(q),copy(q));
    },
    async query(q) {
      if (q.has(1)) return records.has(alias(q)) ? [copy(records.get(alias(q)))] : [];
      assert.equal(q.get(5),11);
      return [...records.values()].filter(r => Buffer.from(r.get(3)).equals(q.get(3))).map(r => new Map([[1,r.get(1)]]));
    },
    async remove(q) { records.delete(alias(q)); }
  };
  t.after(() => { delete globalThis.screenAssets; });
  const store = (profile='first', manager='manager', machine='mac', uid='one', authority='pin') =>
    new ScreenCredentialStore(profile,'hub',authority,manager,machine,uid);
  const first = store(), other = store('second'), elsewhere = store('first','work');
  const login = {account:'界'.repeat(256),password:'🔒'.repeat(128)};
  await first.save(login);
  const saved = await first.load();
  assert.deepEqual([saved.account,saved.password],[login.account,login.password]);
  for (const distinct of [other,elsewhere,store('first','manager','another'),store('first','manager','mac','two'),store('first','manager','mac','one','another-pin')])
    assert.equal(await distinct.load(),undefined);
  await first.save({account:'updated', password:'new'}); await first.forget(login);
  assert.equal((await first.load()).password,'new');
  await elsewhere.save(login);await other.save(login);
  await ScreenCredentialStore.forgetProfile('first');
  assert.equal(await first.load(),undefined);assert.equal(await elsewhere.load(),undefined);
  assert.equal((await other.load()).password,login.password);
  await other.forget();assert.equal(records.size,0);
});

test('only a successful opted-in Mac sign-in persists credentials; the new session is read-only', async () => {
  for (const [remember, accepted] of [[true,true],[false,true],[true,false]]) {
    const { screen, stream, opening } = await openCredentialForm();
    const writes = [];
    let removed = 0;
    screen.loginStore = { save: async value => { writes.push(value.password); }, forget: async () => { removed++; } };
    try {
      assert.equal(screen.submitCredentials('fixture','sample',remember),true);
      assert.deepEqual(writes,[]);
      stream.receive(accepted ? [...be32(0),...serverHandshake().slice(18)] : [...be32(1),...be32(0)]);
      await waitFor(() => screen.phase === (accepted ? 'live' : 'auth_failed'));
      await new Promise(resolve => setImmediate(resolve));
      assert.deepEqual(writes, remember && accepted ? ['sample'] : []);
      assert.equal(removed, accepted && !remember ? 1 : 0);
      assert.equal(screen.loginRemembered,remember && accepted);
      assert.equal(screen.controlAllowed(),false);
    } finally { screen.close(); await opening; }
  }
});

test('an outstanding local password read cannot answer a hidden connection', async () => {
  const stream = new TestStream(), pending = deferred(), { screen,parent,row } = screenFixture(stream);
  const opening = screen.open(parent,row);
  await waitFor(() => stream.onBinary !== undefined);
  let looking = false;
  screen.loginStore = { load: async () => { looking = true; return pending.promise; } };
  stream.receive([...new TextEncoder().encode('RFB 003.008\n'),1,19,0,2,0,1,0,0,1,0]);
  await waitFor(() => looking);
  assert.equal(screen.credentialsRequested,false);
  screen.hide(); const sent = stream.sent.length;
  pending.resolve({ account:'fixture',password:'sample' });
  await opening;
  assert.equal(stream.sent.length,sent);
  assert.equal(screen.credentialsRequested,false);
});

test('one legal full-screen update can arrive as a burst above the pre-login queue limit', async t => {
  const stream = new TestStream();
  const {screen, parent, row} = screenFixture(stream);
  const opening = screen.open(parent,row);
  t.after(async () => { screen.close(); await opening; });
  await waitFor(() => !!stream.onBinary);
  const width = 2304, height = 1024;
  stream.receive(serverHandshake(width, height));
  await waitFor(() => screen.isLive());
  stream.receive([0,0,0,1,0,0,0,0,width >> 8,width & 255,height >> 8,height & 255,0,0,0,0]);
  const bytes = new Uint8Array(width * height * 4);
  bytes[bytes.length - 4] = 123;
  for (let i = 0; i < bytes.length; i += 256 * 1024) stream.onBinary(bytes.buffer.slice(i, i + 256 * 1024));
  await waitFor(() => screen.hasFrame);
  assert.equal(screen.presentationSource().pixels.at(-4),123);
});

test('primary taps survive, a two-finger gesture never clicks, and it releases a previously held drag',async()=>{
 const {ScreenWheel,ScreenTwoFingers,screenScrollFactor}=await import('../entry/src/main/ets/core/ScreenWheel.ts');
 const touchType={Down:0,Up:1,Move:2,Cancel:3};
 const Behavior=new Function('TouchType','screenScrollPreference','screenScrollFactor','return '+stripTypeScriptTypes('class TouchBehavior {'+['stopWheel','twoTouches','touchFrame'].map(pageMethod).join('\n')+'}'))(touchType,{choice:'default'},screenScrollFactor);
 const page=new Behavior(),sent=[];Object.assign(page,{wheel:new ScreenWheel(),fingerMotion:new ScreenTwoFingers(),wheelTimer:-1,
   frameAreaWidth:300,frameAreaHeight:180,paceWheel(){},screen:{controlAllowed:()=>true,scrollPointsInView:()=>1,
     pointerFromView(x,y,w,h,btn){sent.push(btn);return true},releasePointer(){sent.push(0)}}});
 function event(type,points){const touches=points.map(([x,y])=>({x,y,type}));page.touchFrame({touches,changedTouches:touches});}
 event(0,[[30,50]]);event(1,[[30,50]]);assert.deepEqual(sent.splice(0),[1,0]);
 event(0,[[30,50]]);event(0,[[30,50],[60,50]]);event(2,[[30,60],[60,60]]);event(1,[[60,60]]);event(1,[[60,60]]);
 assert.deepEqual(sent,[]);assert.equal(page.wheel.busy,true);
 event(0,[[30,50]]);assert.equal(page.wheel.busy,false);event(2,[[35,55]]);event(0,[[35,55],[85,55]]);
 assert.deepEqual(sent,[1,1,0]);assert.equal(page.pointerDown,false);
});
