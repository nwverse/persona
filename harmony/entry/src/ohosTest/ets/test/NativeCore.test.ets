import { describe, it, expect } from '@ohos/hypium';
import { util } from '@kit.ArkTS';
import native from 'libpersona_core_ohos.so';
import { nativeTestContext } from './NativeContext';
import type { Action, ChatEvent, ChatPatch, ChatSnapshot, Effect, Request, WireBatch } from '../../main/ets/core/rustcore/Wire';
import { decodeChatPatch, decodeEffect, encodeAction } from '../../main/ets/core/rustcore/Wire';

interface FixtureEvent {
  kind?: string;
  items?: Object[];
  history?: string;
  from_seq?: number;
  to_seq?: number;
  backlog?: boolean;
  has_more?: boolean;
  more_after?: boolean;
  more?: boolean;
  covered?: number;
  loaded_once?: boolean;
}
interface FixtureCase {
  name: string;
  input: { initial: FixtureEvent; events: FixtureEvent[] };
  expected: Expected[];
}
interface Expected {
  items: Object[];
  covered: number;
  history: string | null;
  has_more: boolean | null;
  loaded_once: boolean;
  actions: Object[];
}
interface FixtureDocument { cases: FixtureCase[]; }
interface FixtureFile { file: string; document: FixtureDocument; }
interface ExpectedRequest {
  kind: string;
  after_seq?: number;
  fold_work?: boolean;
  since_seq?: number;
  before_seq?: number;
  from_seq?: number;
  through_seq?: number;
}

function text(event: FixtureEvent, key: string): string | null {
  return key === 'history' && event.history !== undefined ? event.history : null;
}
function number(event: FixtureEvent, key: string): number | null {
  if (key === 'from_seq' && event.from_seq !== undefined) return event.from_seq;
  if (key === 'to_seq' && event.to_seq !== undefined) return event.to_seq;
  return null;
}
function boolean(event: FixtureEvent, key: string): boolean | null {
  if (key === 'backlog' && event.backlog !== undefined) return event.backlog;
  if (key === 'has_more' && event.has_more !== undefined) return event.has_more;
  if (key === 'more_after' && event.more_after !== undefined) return event.more_after;
  if (key === 'more' && event.more !== undefined) return event.more;
  return null;
}
function batch(event: FixtureEvent): WireBatch {
  return {items_json: JSON.stringify(event.items || []), history: text(event, 'history'),
    from_seq: number(event, 'from_seq'), to_seq: number(event, 'to_seq'), backlog: event.backlog === true,
    has_more: boolean(event, 'has_more'), more_after: event.more_after === true};
}
function eventAction(event: FixtureEvent, chat: string): Action {
  if (event.kind === 'continue_fill') return {kind: 'continue_fill', chat};
  if (event.kind === undefined) throw new Error('missing fixture event kind');
  let eventValue: ChatEvent;
  if (event.kind === 'http_latest') eventValue = {kind: 'latest', batch: batch(event)};
  else if (event.kind === 'ws_items') eventValue = {kind: 'pushed', batch: batch(event)};
  else if (event.kind === 'http_after') eventValue = {kind: 'after', batch: batch(event)};
  else if (event.kind === 'http_reload') eventValue = {kind: 'reloaded', items_json: JSON.stringify(event.items || []),
    history: text(event, 'history'), to_seq: number(event, 'to_seq'), has_more: event.has_more === true};
  else if (event.kind === 'http_older') eventValue = {kind: 'older', items_json: JSON.stringify(event.items || []), has_more: event.has_more === true};
  else if (event.kind === 'http_range') eventValue = {kind: 'range', fold_id: null, items_json: JSON.stringify(event.items || []), more: event.more === true};
  else if (event.kind === 'reconnect') eventValue = {kind: 'reconnected'};
  else if (event.kind === 'load_older') eventValue = {kind: 'load_older'};
  else if (event.kind === 'open_fold') eventValue = {kind: 'open_fold', fold_id: event.fold_id || ''};
  else throw new Error('unknown fixture event: ' + event.kind);
  return {kind: 'chat', chat, event: eventValue};
}
function requestObject(request: Request): ExpectedRequest {
  if (request.kind === 'fetch_after') return {kind: request.kind, after_seq: request.after_seq};
  if (request.kind === 'fetch_latest') return {kind: request.kind, fold_work: request.fold_work};
  if (request.kind === 'subscribe') {
    const result: ExpectedRequest = {kind: request.kind, fold_work: request.fold_work};
    if (request.since_seq !== null) result.since_seq = request.since_seq;
    return result;
  }
  if (request.kind === 'fetch_older') return {kind: request.kind, before_seq: request.before_seq, fold_work: request.fold_work};
  return {kind: request.kind, from_seq: request.from_seq, through_seq: request.through_seq};
}

const effectWaitMillis: number = 5000;
const effectPollMillis: number = 10;

export async function awaitDeliveredEffects(effects: Effect[], expectedCount: number, label: string,
  timeoutMillis: number = effectWaitMillis): Promise<void> {
  // Do not mistake the first event-loop turn for a delivery barrier: a different NAPI callback may still be
  // queued ahead of the action's effects. Yield until the action's complete callback set has arrived. A zero-effect
  // step uses the same bounded window to reject late or stale callbacks without weakening its action assertions.
  await new Promise<void>((resolve: () => void) => setTimeout(resolve, 0));
  const deadline: number = Date.now() + timeoutMillis;
  while (Date.now() < deadline) {
    if (effects.length === expectedCount && effectRequestCount(effects) === expectedCount) return;
    if (effects.length > expectedCount || effectRequestCount(effects) > expectedCount) break;
    await new Promise<void>((resolve: () => void) => setTimeout(resolve, effectPollMillis));
  }
  if (effects.length < expectedCount || effectRequestCount(effects) < expectedCount) {
    throw new Error(label + ': expected ' + expectedCount + ' effects after flush, received ' + effects.length);
  }
  throw new Error(label + ': unexpected extra or stale effects: expected ' + expectedCount + ', received ' + effects.length);
}

function effectRequestCount(effects: Effect[]): number {
  let count: number = 0;
  for (const effect of effects) { if (effect.kind === 'fetch') count++; }
  return count;
}

function openCore(effect: (bytes: Uint8Array) => void): number {
  // The Rust callback is ThreadsafeFunction<Buffer, ..., false>: one Uint8Array, not (null, Array<number>).
  return native.coreCreate(native.coreConfig(), (_version: number): void => {},
    (bytes: Uint8Array): void => { effect(bytes); });
}

function openTargetedCore(effect: (bytes: Uint8Array) => void): number {
  return native.coreCreateWithChat(native.coreConfig(), (_version: number): void => {},
    (_chat: string, _version: number): void => {}, (bytes: Uint8Array): void => { effect(bytes); });
}

export async function drainCore(handle: number, chat: string, list: string[], effects: Effect[],
  expectedCount: number, label: string): Promise<ChatPatch> {
  expect(native.coreFlush(handle, 5000)).assertTrue();
  await awaitDeliveredEffects(effects, expectedCount, label);
  const patch: ChatPatch = decodeChatPatch(native.coreDrain(handle, chat));
  for (const op of patch.ops) {
    if (op.kind === 'remove') list.splice(op.index, 1);
    else if (op.kind === 'insert') list.splice(op.index, 0, op.json);
    else list[op.index] = op.json;
  }
  return patch;
}

export default function nativeCoreTests(): void {
  describe('NativePersonaCore', (): void => {
    it('replays_every_shared_fixture_through_the_real_napi_binding', 0, async (): Promise<void> => {
      const manager = nativeTestContext().resourceManager;
      const files = JSON.parse(util.TextDecoder.create('utf-8').decodeToString(
        await manager.getRawFileContent('core-fixtures/continuity.json'))) as FixtureFile[];
      let cases = 0, steps = 0;
      for (const file of files) {
        for (const fixture of file.document.cases) {
          cases++;
          let effects: Effect[] = [];
          const handle = openTargetedCore((bytes: Uint8Array): void => { effects.push(decodeEffect(bytes)); });
          try {
            const chat = 'fixture/continuity';
            const initial: ChatSnapshot = {items_json: JSON.stringify(fixture.input.initial.items || []),
              history: text(fixture.input.initial, 'history'), covered: fixture.input.initial.covered || 0,
              has_more: boolean(fixture.input.initial, 'has_more'), loaded_once: fixture.input.initial.loaded_once === true};
            expect(native.coreDispatch(handle, encodeAction({kind: 'open_chat', chat, snapshot: initial}))).assertTrue();
            const list: string[] = [];
            const drain = async (expectedCount: number, label: string): Promise<ChatPatch> => {
              return await drainCore(handle, chat, list, effects, expectedCount, label);
            };
            await drain(0, fixture.name + ' open');
            effects = [];
            for (let index = 0; index < fixture.input.events.length; index++) {
              const expected = fixture.expected[index];
              expect(native.coreDispatch(handle, encodeAction(eventAction(fixture.input.events[index], chat)))).assertTrue();
              const patch: ChatPatch = await drain(expected.actions.length, fixture.name + ' step ' + index);
              const requests: ExpectedRequest[] = [];
              for (const effect of effects) {
                if (effect.kind === 'rejected') throw new Error(fixture.name + ': ' + effect.reason);
                expect(effect.chat).assertEqual(chat);
                requests.push(requestObject(effect.request));
              }
              effects = [];
              expect(JSON.stringify(list.map((json: string): Object => JSON.parse(json) as Object))).assertEqual(JSON.stringify(expected.items));
              expect(patch.covered).assertEqual(expected.covered);
              expect(patch.history).assertEqual(expected.history);
              expect(patch.has_more).assertEqual(expected.has_more);
              expect(patch.loaded_once).assertEqual(expected.loaded_once);
              expect(JSON.stringify(requests)).assertEqual(JSON.stringify(expected.actions));
              steps++;
            }
          } finally { native.coreClose(handle); }
        }
      }
      expect(cases).assertEqual(38);
      expect(steps).assertEqual(94);
    });
    it('a_repeated_item_is_one_update_and_close_is_idempotent', 0, async (): Promise<void> => {
      const chat = 'update';
      const effects: Effect[] = [];
      const handle = openCore((bytes: Uint8Array): void => { effects.push(decodeEffect(bytes)); });
      const snapshot: ChatSnapshot = {items_json: '[{"id":"1#0","seq":1,"role":"assistant","kind":"text","text":"old"}]',
        history: 'h', covered: 1, has_more: false, loaded_once: true};
      try {
        expect(native.coreDispatch(handle, encodeAction({kind: 'open_chat', chat, snapshot}))).assertTrue();
        const list: string[] = [];
        await drainCore(handle, chat, list, effects, 0, 'open repeated item');
        const batch: WireBatch = {items_json: '[{"id":"1#0","seq":1,"role":"assistant","kind":"text","text":"newest"}]',
          history: 'h', from_seq: 0, to_seq: 1, backlog: false, has_more: null, more_after: false};
        expect(native.coreDispatch(handle, encodeAction({kind: 'chat', chat, event: {kind: 'pushed', batch}}))).assertTrue();
        const patch: ChatPatch = await drainCore(handle, chat, list, effects, 0, 'update repeated item');
        expect(patch.ops.length).assertEqual(1);
        expect(patch.ops[0].kind).assertEqual('update');
        expect(patch.ops[0].index).assertEqual(0);
      } finally { native.coreClose(handle); native.coreClose(handle); }
    });
  });
}
