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
const { awaitDeliveredEffects, drainCore } = await import('../entry/src/ohosTest/ets/test/NativeCore.test.ets');

function fetchEffect(chat = 'fixture') {
  return {kind: 'fetch', chat, request: {kind: 'fetch_latest', fold_work: false}};
}

function patchWithInsert(json) {
  state.patch = wire.encodeChatPatch({version: 1, covered: 1, history: null, has_more: null,
    loaded_once: true, verified: false, acked: null,
    ops: [{kind: 'insert', index: 0, id: '1#0', json}]});
}

test('effect delivery waits for a queued callback, not a synchronous effect array', {timeout: 1000}, async () => {
  const effects = [];
  let completed = false;
  const pending = awaitDeliveredEffects(effects, 1, 'delayed effect', 200).then(() => { completed = true; });
  setTimeout(() => { effects.push(fetchEffect()); }, 15);
  assert.equal(completed, false, 'the wait must yield before its predicate is satisfied');
  await pending;
  assert.deepEqual(effects, [fetchEffect()]);
});

test('zero-effect steps yield to the event loop and reject stale callbacks', async () => {
  const effects = [];
  let delivered = false;
  setTimeout(() => { delivered = true; }, 0);
  await awaitDeliveredEffects(effects, 0, 'no effects', 25);
  assert.equal(delivered, true, 'a zero-effect step must still yield before clearing the shared array');
  await assert.rejects(awaitDeliveredEffects([fetchEffect()], 0, 'stale effect', 25), /unexpected extra or stale effects/);
});

test('missing effects fail within the supplied bound instead of passing an empty request comparison', async () => {
  const started = Date.now();
  await assert.rejects(awaitDeliveredEffects([], 1, 'missing effect', 30), /expected 1 effects after flush, received 0/);
  const elapsed = Date.now() - started;
  assert.ok(elapsed >= 20 && elapsed < 1000, `bounded wait took ${elapsed}ms`);
});

test('drainCore flushes, waits for the action effects, then drains and applies its patch', {timeout: 1000}, async () => {
  state.flushes = 0;
  state.drains = 0;
  state.effectsAtDrain = [];
  const effects = state.effects = [];
  const list = [];
  const json = '{"id":"1#0","text":"updated"}';
  patchWithInsert(json);
  const pending = drainCore(1, 'fixture', list, effects, 1, 'delayed drain');
  assert.equal(state.flushes, 1);
  assert.equal(state.drains, 0, 'a synchronous drain would observe no queued NAPI effects');
  setTimeout(() => { effects.push(fetchEffect()); }, 15);
  const patch = await drainCore(1, 'fixture', list, effects, 1, 'already delivered');
  const delayed = await pending;
  assert.equal(state.flushes, 2);
  assert.equal(state.drains, 2);
  assert.deepEqual(state.effectsAtDrain, [1, 1]);
  assert.equal(patch.ops[0].kind, 'insert');
  assert.equal(delayed.ops[0].json, json);
  assert.deepEqual(list, [json, json]);
});
