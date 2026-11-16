// Production ArkTS vs the real Rust core. Host evidence only: never a NAPI, HAP or physical-device claim.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { HostCore, helperPath } from './host.mjs';

registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.includes('/entry/src/main/ets/')) {
      const url = new URL(specifier, context.parentURL);
      if (url.pathname.endsWith('/Wire')) url.pathname += '.ets';
      else if (!/\.[a-z]+$/i.test(url.pathname)) url.pathname += '.ts';
      return {url: url.href, shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.endsWith('.ts') || url.endsWith('.ets')) return {format: 'module', shortCircuit: true,
      source: stripTypeScriptTypes(readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, ''))};
    return next(url, context);
  },
});
const {NativeAgentListAdapter, coreAgentSortInput} = await import('../../entry/src/main/ets/core/AgentListAdapter.ts');
const {differingAgentSortRules} = await import('../../entry/src/main/ets/core/AgentListProjection.ts');

const fixtureDir = new URL('../../../spec/fixtures/sort/', import.meta.url);
const documents = readdirSync(fixtureDir).filter((f) => f.endsWith('.json')).sort().map((file) => ({
  file, ...JSON.parse(readFileSync(new URL(file, fixtureDir), 'utf8')),
}));
const cases = documents.filter((doc) => doc.operation === 'agents_list').flatMap((doc) => doc.cases);
assert.equal(cases.length, 30, 'the exact checked-in projection corpus');
assert.equal(new Set(cases.map((c) => c.name)).size, 30, 'unique case names');
const prefs = {pinsSpaces: [], pinsSessions: [], mutesSpaces: [], mutesSessions: [], mutesAllowed: []};

function nativeInput(c) {
  const input = c.input;
  const rows = input.sessions.map((raw) => ({id: raw.session_id, raw: structuredClone(raw), source: {id: raw.session_id}}));
  const machines = new Map(input.machines.map((m) => [`${m.group ?? ''}|${m.id}`, m.name]));
  return {
    rows,
    history: false, reading: false, machines: input.machines,
    grouping: {state: 'flat', space: 'repository', machine: 'machine'}[input.settings.grouping],
    quick: {all: 'all', needsYou: 'needs_you', unread: 'unread', working: 'working', backgroundWaiting: 'working'}[input.settings.quick],
    machineFilter: input.settings.machine_filter.split('|').at(-1) ?? '',
    query: input.settings.query,
    kept: input.settings.kept,
    coreSettings: {...input.settings},
    preferencesFor: () => ({...prefs, pinsSpaces: input.pins.spaces, pinsSessions: input.pins.sessions,
      mutesSpaces: input.mutes.spaces, mutesSessions: input.mutes.sessions, mutesAllowed: input.mutes.allowed}),
    machineIdentity: (r) => r.raw.machine_id,
    machineName: (r) => machines.get(`${r.raw.app_hub_group ?? ''}|${r.raw.machine_id}`) ?? r.raw.machine_id,
  };
}

// This is deliberately a complete structural projection, not an ID-only comparison. The existing corpus is iOS-derived
// and predates the native-only grouping choices. Every mismatch is retained as an issue reference (#8966), not erased
// by relabelling native groups as iOS groups or weakening the fixture.
function nativeAsCore(input) {
  const groups = new NativeAgentListAdapter().project(input);
  const counts = {needsYou: 0, unread: 0, working: 0, backgroundWaiting: 0};
  const state = (raw) => raw.state ?? raw.status ?? 'idle';
  input.rows.filter((r) => state(r.raw) !== 'offline').forEach((r) => {
    if (state(r.raw) === 'needs_you') counts.needsYou++;
    if (state(r.raw) === 'unread') counts.unread++;
    if (state(r.raw) === 'working') counts.working++;
    if (r.raw.background_waiting) counts.backgroundWaiting++;
  });
  const id = (r) => `${r.raw.app_hub_group ? `${r.raw.app_hub_group}|` : ''}${r.raw.machine_id}/${r.raw.session_id}`;
  const result = {counts, groups: groups.map((g) => ({id: g.id, title: g.machineName,
    rows: g.rows.map(id), visible_rows: g.rows.map(id), folded: false})),
    unread_shown: groups.flatMap((g) => g.rows.filter((r) => state(r.raw) === 'unread').map(id))};
  if (input.grouping === 'repository') result.quiet_title = null;
  return result;
}

test('all 30 fixtures reach both native ArkTS and the real core; differences are reported, not hidden', async () => {
  const host = new HostCore(helperPath);
  try {
    const results = [];
    for (const c of cases) {
      const input = nativeInput(c);
      const coreInput = coreAgentSortInput(input);
      assert.ok(coreInput, `${c.name}: supported native-to-core input`);
      // Send exactly the same session/registry/preferences values. The native-only history/mode adaptations are named
      // in #8966, not treated as proof that production accepts core rows yet.
      assert.deepEqual(coreInput.sessions.map((s) => s.session_id), c.input.sessions.map((s) => s.session_id), c.name);
      const core = await host.sort(coreInput);
      assert.deepEqual(core, c.expected, `${c.name}: real core remains pinned to the unchanged fixture`);
      const native = nativeAsCore(input);
      const rules = differingAgentSortRules(native, core);
      results.push({name: c.name, matched: rules.length === 0, rules});
      if (rules.length === 0) assert.deepEqual(native, c.expected, c.name);
    }
    const matched = results.filter((r) => r.matched).length;
    const rules = [...new Set(results.flatMap((r) => r.rules))].sort();
    console.log(`  production ArkTS/core projection: ${matched}/${results.length} equal; ${results.length - matched} recorded by #8966; rules=${rules.join(',')}`);
    assert.equal(results.length, 30);
    assert.ok(matched < results.length, 'the parity oracle must not silently erase the known native/iOS mismatch (#8966)');
    assert.deepEqual(rules, ['counts', 'group-order', 'unread-shown']);
    assert.equal(matched, 1, 'current host baseline; #8966 must update this expectation as parity improves');
  } finally { await host.end(); }
});

test('the production comparison rejects a wrong expectation and native failure without content', async () => {
  const host = new HostCore(helperPath);
  try {
    const c = cases[0];
    const native = nativeAsCore(nativeInput(c));
    assert.notDeepEqual(native, {...c.expected, counts: {needsYou: 0, unread: 0, working: 0, backgroundWaiting: 0}});
    await assert.rejects(() => host.sort({private: 'value must not be repeated'}), /sort/);
  } finally { await host.end(); }
});
