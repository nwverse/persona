import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync } from 'node:fs';

const state = {
  effects: [],
  patch: null,
  flushes: 0,
  drains: 0,
};

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@ohos/hypium') return {url: 'fake-hypium:', shortCircuit: true};
    if (specifier === '@kit.ArkTS') return {url: 'fake-arkts:', shortCircuit: true};
    if (specifier === 'libpersona_core_ohos.so') return {url: 'fake-persona-core:', shortCircuit: true};
    if (specifier.endsWith('/Wire')) {
      return {url: new URL('../entry/src/main/ets/core/rustcore/Wire.ts', import.meta.url).href, shortCircuit: true};
    }
    if (specifier.endsWith('/NativeContext')) return {url: 'fake-native-context:', shortCircuit: true};
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'fake-hypium:') {
      return {format: 'module', shortCircuit: true, source: `
        export const describe = (_name, body) => body();
        export const it = (_name, _options, _body) => {};
        export const expect = (value) => ({
          assertTrue() { if (value !== true) throw new Error('expected true'); },
          assertEqual(expected) { if (!Object.is(value, expected)) throw new Error('values differ'); },
        });
      `};
    }
    if (url === 'fake-arkts:') {
      return {format: 'module', shortCircuit: true, source: 'export const util = {TextDecoder: {create() { return new TextDecoder(); }}};'};
    }
    if (url === 'fake-native-context:') {
      return {format: 'module', shortCircuit: true, source: 'export function nativeTestContext() { throw new Error("no host resource context"); }'};
    }
    if (url === 'fake-persona-core:') {
      return {format: 'module', shortCircuit: true, source: `
        export default {
          coreConfig() { return new Uint8Array(); },
          coreCreate(config, changed, effects) { globalThis.nativeCoreCallbacks = {changed, effects}; return 1; },
          coreCreateWithChat(config, changed, changedChat, effects) {
            globalThis.nativeCoreCallbacks = {changed, changedChat, effects};
            return 2;
          },
          coreFlush() { globalThis.nativeCoreState.flushes++; return true; },
          coreDrain() {
            globalThis.nativeCoreState.drains++;
            globalThis.nativeCoreState.effectsAtDrain ??= [];
            globalThis.nativeCoreState.effectsAtDrain.push(globalThis.nativeCoreState.effects.length);
            return globalThis.nativeCoreState.patch;
          },
        };
      `};
    }
    if (url.endsWith('.ets') || url.endsWith('.ts')) {
      return {format: 'module', shortCircuit: true, source: stripTypeScriptTypes(readFileSync(new URL(url), 'utf8'))};
    }
    return next(url, context);
  },
});

globalThis.nativeCoreState = state;
const wire = await import('../entry/src/main/ets/core/rustcore/Wire.ts');
const { Delivery, drainCore } = await import('../entry/src/ohosTest/ets/test/NativeCore.test.ets');

function fetchEffect(chat = 'fixture') {
  return {kind: 'fetch', chat, request: {kind: 'fetch_latest', fold_work: false}};
}

function setPatch(version) {
  state.patch = wire.encodeChatPatch({version, covered: 1, history: null, has_more: null,
    loaded_once: true, verified: false, acked: null,
    ops: [{kind: 'insert', index: 0, id: '1#0', json: '{"id":"1#0"}'}]});
}

function wake(delivery, chat = 'fixture') {
  setTimeout(() => { delivery.changedChat(chat, 1); }, 0);
}

test('effect delivery uses the later wake as its barrier, not a synchronous array', {timeout: 1000}, async () => {
  const delivery = new Delivery();
  setPatch(1);
  const pending = drainCore(1, 'fixture', [], delivery, 'delayed effect', 200);
  let completed = false;
  pending.then(() => { completed = true; });
  setTimeout(() => {
    delivery.effect(wire.encodeEffect(fetchEffect()));
    delivery.changedChat('fixture', 1);
  }, 15);
  const patch = await pending;
  assert.equal(patch.version, 1);
  assert.equal(completed, true);
});

test('a no-op step needs no effect and no five-second empty-array wait', {timeout: 1000}, async () => {
  const delivery = new Delivery();
  setPatch(0);
  const started = Date.now();
  const patch = await drainCore(1, 'fixture', [], delivery, 'no effect', 200);
  assert.equal(patch.version, 0);
  assert.ok(Date.now() - started < 150);
});

test('a zero-effect step still waits for the wake that orders its callbacks', {timeout: 1000}, async () => {
  const delivery = new Delivery();
  setPatch(1);
  const pending = delivery.drain(1, 'fixture', [], 'delayed wake', 200);
  let completed = false;
  pending.then(() => { completed = true; });
  setTimeout(() => { wake(delivery); }, 30);
  const step = await pending;
  assert.deepEqual(step.effects, []);
  assert.equal(completed, true);
});

test('a missing wake fails within its bound instead of draining silently', async () => {
  const delivery = new Delivery();
  setPatch(1);
  const started = Date.now();
  await assert.rejects(delivery.drain(1, 'fixture', [], 'missing wake', 30), /expected wake 1 for fixture, received 0/);
  const elapsed = Date.now() - started;
  assert.ok(elapsed >= 20 && elapsed < 1000, `bounded wait took ${elapsed}ms`);
});

test('a changed patch cannot be claimed by an old or other-chat wake', async () => {
  const delivery = new Delivery();
  setPatch(1);
  const pending = delivery.drain(1, 'fixture', [], 'other-chat wake', 30);
  setTimeout(() => { delivery.changedChat('other-chat', 1); }, 5);
  await assert.rejects(pending, /expected wake 1 for fixture, received 1/);
});

test('drainCore flushes, drains, then waits for the action effects before applying', {timeout: 1000}, async () => {
  state.flushes = 0;
  state.drains = 0;
  state.effectsAtDrain = [];
  const delivery = new Delivery();
  const list = [];
  setPatch(1);
  const pending = drainCore(1, 'fixture', list, delivery, 'delayed drain');
  assert.equal(state.flushes, 1);
  assert.equal(state.drains, 1);
  setTimeout(() => {
    delivery.effect(wire.encodeEffect(fetchEffect()));
    delivery.changedChat('fixture', 1);
  }, 15);
  await pending;
  assert.equal(list.length, 1);
  assert.deepEqual(delivery.effects, []);
});

test('per-step effects are removed from the shared queue rather than discarded by array replacement', async () => {
  const delivery = new Delivery();
  delivery.effect(wire.encodeEffect(fetchEffect()));
  const first = delivery.effects.splice(0, delivery.effects.length);
  assert.deepEqual(first, [fetchEffect()]);
  assert.deepEqual(delivery.effects, []);
});
