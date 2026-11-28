import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { workRowExcerpt, workToolStatus } from '../entry/src/main/ets/core/WorkPresentation.ts';
import { AssistantMessagePart, assistantMessageParts } from '../entry/src/main/ets/core/AssistantCardText.ts';

const source = readFileSync(new URL('../entry/src/main/ets/models/RowBinding.ets', import.meta.url), 'utf8');
const executable = stripTypeScriptTypes(source.replace(/@ObservedV2\s*/g, '').replace(/@Trace\s*/g, '').replace(/\bexport\s+/g, ''));
const { BoundRow, reconcileBoundRows } = new Function(executable + '\nreturn { BoundRow, reconcileBoundRows };')();

test('same owner and scope retain the wrapper while replacing its immutable row value', () => {
  const owner = {}, original = Object.freeze({ id: 'a', title: 'Old', state: 'working', raw: '{"status":"working"}' });
  const initial = reconcileBoundRows([], [original], row => row.id, undefined, owner, '', 'hub/a');
  const binding = initial[0];
  const replacement = Object.freeze({ id: 'a', title: 'New', state: 'needs_you', raw: '{"status":"needs_you"}' });
  const next = reconcileBoundRows(initial, [replacement], row => row.id, owner, owner, 'hub/a', 'hub/a');
  let opened;
  const onClick = () => { opened = next[0].value; };
  onClick();

  assert.equal(next[0], binding);
  assert.equal(next[0].key, binding.key);
  assert.equal(next[0].value, replacement);
  assert.equal(next[0].value.title, 'New');
  assert.equal(next[0].value.state, 'needs_you');
  assert.equal(next[0].value.raw, '{"status":"needs_you"}');
  assert.equal(opened, replacement);
  assert.deepEqual(original, { id: 'a', title: 'Old', state: 'working', raw: '{"status":"working"}' });
});

test('reorder, removal, and duplicate keys preserve distinct wrappers without stale values', () => {
  const owner = {};
  const original = [
    Object.freeze({ id: 'dup', title: 'first' }),
    Object.freeze({ id: 'other', title: 'other' }),
    Object.freeze({ id: 'dup', title: 'second' })
  ];
  const initial = reconcileBoundRows([], original, row => row.id, undefined, owner, '', 'scope');
  const replacements = [
    Object.freeze({ id: 'dup', title: 'first refreshed' }),
    Object.freeze({ id: 'dup', title: 'second refreshed' }),
    Object.freeze({ id: 'other', title: 'other refreshed' })
  ];
  const reordered = reconcileBoundRows(initial, replacements, row => row.id, owner, owner, 'scope', 'scope');

  assert.equal(new Set(initial).size, initial.length);
  assert.equal(new Set(initial.map(binding => binding.key)).size, initial.length);
  assert.equal(reordered[0], initial[0]);
  assert.equal(reordered[1], initial[2]);
  assert.equal(reordered[2], initial[1]);
  assert.deepEqual(reordered.map(binding => binding.key), [initial[0].key, initial[2].key, initial[1].key]);
  assert.deepEqual(reordered.map(binding => binding.value.title), ['first refreshed', 'second refreshed', 'other refreshed']);
  assert.notEqual(reordered[0], reordered[1]);

  const removed = reconcileBoundRows(reordered, [Object.freeze({ id: 'dup', title: 'remaining' })], row => row.id,
    owner, owner, 'scope', 'scope');
  assert.equal(removed.length, 1);
  assert.equal(removed[0], initial[0]);
  assert.equal(removed[0].value.title, 'remaining');
});

test('owner or scope changes create fresh wrappers', () => {
  const owner = {}, otherOwner = {};
  const initial = reconcileBoundRows([], [{ id: 'a' }], row => row.id, undefined, owner, '', 'scope/a');
  const newOwner = reconcileBoundRows(initial, [{ id: 'a' }], row => row.id, owner, otherOwner, 'scope/a', 'scope/a');
  const newScope = reconcileBoundRows(newOwner, [{ id: 'a' }], row => row.id, otherOwner, otherOwner, 'scope/a', 'scope/b');

  assert.notEqual(newOwner[0], initial[0]);
  assert.notEqual(newScope[0], newOwner[0]);
  assert.equal(newOwner[0].identity, initial[0].identity);
  assert.equal(newScope[0].identity, newOwner[0].identity);
  assert.notEqual(newOwner[0].key, initial[0].key);
  assert.notEqual(newScope[0].key, newOwner[0].key);
});

function pageMethods(file, names, helpers = {}) {
  const source = readFileSync(new URL('../entry/src/main/ets/components/' + file + '.ets', import.meta.url), 'utf8');
  const methods = names.map(name => {
    const start = source.indexOf(name + '(): '); assert.ok(start >= 0, name);
    const body = source.indexOf('{', start); let depth = 1, quote = '', escaped = false, end = body + 1;
    for (; end < source.length && depth; end++) {
      const char = source[end];
      if (quote) { if (escaped) escaped = false; else if (char === '\\') escaped = true; else if (char === quote) quote = ''; }
      else if (char === '"' || char === "'" || char === '`') quote = char;
      else if (char === '{') depth++; else if (char === '}') depth--;
    }
    assert.equal(depth, 0, name); return source.slice(start, end);
  }).join('\n');
  const code = stripTypeScriptTypes('class NativeRows {\n' + methods + '\n}');
  const wire = { text: (row, key) => typeof row[key] === 'string' ? row[key] : '', rows: (row, key) => row[key] || [],
    payload: (keys, values) => Object.fromEntries(keys.map((key, index) => [key, values[index]])) };
  return new Function('reconcileBoundRows', 'Wire', ...Object.keys(helpers), code + '\nreturn new NativeRows();')(
    reconcileBoundRows, wire, ...Object.values(helpers));
}
const model = () => ({ profile: { id: 'hub', manager: 'work' }, scope: () => 'hub/work', selected: { key: () => 'agent' } });

test('native settings retain current schedule and field bindings and retire them on a new target', () => {
  const page = pageMethods('SettingsPage', ['syncManagementRows', 'syncInputRows']);
  page.model = model(); page.page = 'schedules'; page.managementRowBindings = []; page.scheduleRunBindings = []; page.channelFeatureBindings = [];
  const names = ['extensionConfigFields', 'extensionSecretFields', 'authFields', 'connectorFields', 'channelFields'];
  page.management = { rows: [{ id: 'task', title: 'Old' }], scheduleRuns: [], selectedId: 'extension-a', featureData: [{ id: 'read', title: 'Read', on: false }], channelData: () => ({ features: page.management.featureData }) };
  for (const name of names) { page.management[name] = [{ key: 'value', value: 'old' }]; page[name + 'Bindings'] = []; }
  page.syncManagementRows(); page.syncInputRows();
  const feature = page.channelFeatureBindings[0], row = page.managementRowBindings[0], field = page.extensionConfigFieldsBindings[0], currentAction = () => row.value;
  page.management.rows = [Object.freeze({ id: 'task', title: 'New' })];
  page.management.extensionConfigFields = [Object.freeze({ key: 'value', value: 'new' })];
  page.management.featureData = [{ id: 'read', title: 'Read current', on: true }];
  page.syncManagementRows(); page.syncInputRows();
  assert.equal(page.managementRowBindings[0], row); assert.equal(currentAction().title, 'New');
  assert.equal(page.channelFeatureBindings[0], feature); assert.equal(feature.value.title, 'Read current'); assert.equal(feature.value.on, true);
  assert.equal(page.extensionConfigFieldsBindings[0], field); assert.equal(field.value.value, 'new');
  page.management.selectedId = 'extension-b'; page.syncInputRows(); assert.notEqual(page.extensionConfigFieldsBindings[0].key, field.key);
  page.model.profile.manager = 'other'; page.syncManagementRows(); assert.notEqual(page.managementRowBindings[0].key, row.key);
});

test('native workspace rows retain latest file, Git, stash and preview values across refresh', () => {
  const page = pageMethods('SessionTools', ['syncRowBindings']); page.model = model();
  page.workspace = { cwd: '/demo', currentPath: '/demo', entries: [Object.freeze({ path: '/demo/a', size: 1 })],
    gitFiles: [{ path: 'a', mark: 'M' }], gitStashes: [{ oid: 'oid', index: 0 }], previewServers: [{ name: 'app', listening: false }] };
  for (const name of ['fileRowBindings', 'gitRowBindings', 'stashRowBindings', 'previewRowBindings']) page[name] = [];
  page.syncRowBindings();
  const file = page.fileRowBindings[0], git = page.gitRowBindings[0], stash = page.stashRowBindings[0], preview = page.previewRowBindings[0], original = file.value;
  page.workspace.entries = [{ path: '/demo/a', size: 20 }]; page.workspace.gitFiles = [{ path: 'a', mark: 'A' }];
  page.workspace.gitStashes = [{ oid: 'oid', index: 2 }]; page.workspace.previewServers = [{ name: 'app', listening: true }]; page.syncRowBindings();
  assert.equal(page.fileRowBindings[0], file); assert.equal(file.value.size, 20); assert.equal(original.size, 1);
  assert.equal(page.gitRowBindings[0], git); assert.equal(git.value.mark, 'A');
  assert.equal(page.stashRowBindings[0], stash); assert.equal(stash.value.index, 2);
  assert.equal(page.previewRowBindings[0], preview); assert.equal(preview.value.listening, true);
  page.model.selected = { key: () => 'another-agent' }; page.syncRowBindings();
  assert.notEqual(page.fileRowBindings[0].key, file.key); assert.notEqual(page.gitRowBindings[0].key, git.key);
});

test('the generic native resource list keeps a retained row current through refresh and filtering', () => {
  const page = pageMethods('ResourcePage', ['syncRows', 'rows']); page.model = model(); page.kind = 'todo';
  page.rowBindings = []; page.search = ''; page.source = 0;
  page.model.resourceRows = [{ key: () => 'a', title: 'Old', detail: '' }]; page.syncRows(); const row = page.rowBindings[0];
  page.model.resourceRows = [{ key: () => 'a', title: 'Renamed', detail: 'Updated' }]; page.syncRows();
  assert.equal(page.rowBindings[0], row); assert.equal(row.value.detail, 'Updated');
  page.search = 'missing'; page.syncRows(); assert.deepEqual(page.rowBindings, []);
});

test('streamed Persona text reaches the same rendered paragraph and a new source gets a fresh binding', () => {
  const page = pageMethods('AssistantMessage', ['syncSource'], {
    AssistantMessagePart, assistantMessageParts, parseAssistantViewBlocks: () => [], tr: key => key,
  });
  page.model = { target: () => 'chat', selected: { id: 'chat', kind: 'conversation' }, profile: { manager: '' } };
  page.parts = [];
  page.message = { itemId: 'reply', role: 'assistant', ownerTarget: 'chat', text: 'First sentence.' };
  page.syncSource();
  const paragraph = page.parts[0];
  page.message = { ...page.message, text: 'First sentence. Final sentence.' };
  page.syncSource();
  assert.equal(page.parts[0], paragraph);
  assert.equal(paragraph.value.text, page.message.text);
  page.message = { ...page.message, itemId: 'other' };
  page.syncSource();
  assert.notEqual(page.parts[0].key, paragraph.key);
});

function workGroup() {
  return pageMethods('ConversationWork', ['syncSteps','stopDisclosure','finishDisclosure','moveDisclosure','disclosurePreferenceChanged']);
}

test('an open work step receives its result in place and retires when the same ID belongs to another chat', () => {
  const page = workGroup();
  page.expanded = true; page.ownerTarget = 'A'; page.steps = [];
  page.work = { workKey: 'w', inspectionSteps: [{ id: 'call', kind: 'tool', use: { tool_name: 'Bash' }, reports: [], images: [], result: null }] };
  page.syncSteps();
  const tool = page.steps[0];
  const renderer = pageMethods('ConversationWork', ['get step']); renderer.binding = tool;
  page.work = { ...page.work, inspectionSteps: [{ id: 'call', kind: 'tool', use: { tool_name: 'Bash' }, reports: [], images: [], result: { text: '8 passed' } }] };
  page.syncSteps();
  assert.equal(page.steps[0], tool);
  assert.equal(renderer.step.result.text, '8 passed');
  page.ownerTarget = 'B';
  page.syncSteps();
  assert.notEqual(page.steps[0].key, tool.key);
});

test('a folded group has no child bindings and opens each call and its latest diagnostic once', () => {
  const page = workGroup();
  const pending = { id:'call', kind:'tool', use:{tool_name:'Bash'}, results:[], images:[], reports:[] };
  const other = { ...pending, id:'other' };
  Object.assign(page, { ownerTarget:'chat', expanded:false, steps:[], work:{workKey:'turn',running:true,inspectionSteps:[pending,other]} });
  page.syncSteps(); assert.deepEqual(page.steps, []);
  const result = { id:'receipt', text:'test failed', outcome:{ status:'failed', exit_code:1 } };
  const failed = { ...pending, result, results:[result] };
  page.work = { ...page.work, inspectionSteps:[failed,other] };
  page.syncSteps(); assert.deepEqual(page.steps, []);
  page.expanded = true; page.syncSteps();
  assert.deepEqual(page.steps.map(row => row.value.id), ['call','other']);
  const row = pageMethods('ConversationWork', ['get step','get status','get shortOutput'], { workRowExcerpt, workToolStatus });
  Object.assign(row, { binding:page.steps[0], live:true, showLogPreview:true });
  assert.equal(row.shortOutput, 'test failed');
  page.work = { ...page.work, inspectionSteps:[{ ...failed, result:{ ...result, text:'final diagnostic' } },other] };
  page.syncSteps(); assert.equal(row.binding, page.steps[0]); assert.equal(row.shortOutput, 'final diagnostic');
  page.expanded = false; page.syncSteps(); assert.deepEqual(page.steps, []);
});

test('tool disclosure reverses from its current height and discards cold or reduced-motion transitions', () => {
  const page = workGroup();
  const animations = [];
  page.getUIContext = () => ({ createAnimator: options => {
    const animator = { options, play() {}, cancel() { this.cancelled = true; }, setExpectedFrameRateRange() {} };
    animations.push(animator); return animator;
  } });
  const call = { id:'call', kind:'tool', use:{ tool_name:'Read' } };
  Object.assign(page, { active:true, expanded:false, reveal:0, steps:[], ownerTarget:'chat',
    work:{ workKey:'turn', inspectionSteps:[call] }, allowDisclosureMotion:true });
  page.syncSteps();
  const toggle = open => { page.userToggled = true; page.expanded = open; page.syncSteps(); };
  toggle(true);
  const opening = animations.at(-1), retained = page.steps[0];
  assert.equal(opening.options.begin, 0); assert.equal(opening.options.end, 1);
  opening.onFrame(0.6);
  page.work = { ...page.work, inspectionSteps:[{ ...call, result:{text:'new output'} }] }; page.syncSteps();
  assert.equal(page.steps[0], retained); assert.equal(animations.length, 1);
  toggle(false);
  const closing = animations.at(-1);
  assert.equal(opening.cancelled, true); assert.equal(closing.options.begin, 0.6); assert.equal(closing.options.end, 0);
  opening.onFrame(1); opening.onFinish(); assert.equal(page.reveal, 0.6);
  closing.onFrame(0.25); assert.equal(page.steps[0], retained);
  toggle(true);
  assert.equal(closing.cancelled, true); assert.equal(animations.at(-1).options.begin, 0.25);
  closing.onFinish(); animations.at(-1).onFinish(); assert.equal(page.reveal, 1); assert.equal(page.steps[0], retained);
  toggle(false); const last = animations.at(-1);
  page.allowDisclosureMotion = false; page.disclosurePreferenceChanged();
  assert.equal(last.cancelled, true); assert.equal(page.reveal, 0); assert.deepEqual(page.steps, []);
  toggle(true); assert.equal(page.reveal, 1); assert.equal(animations.length, 4);
  page.allowDisclosureMotion = true; toggle(false);
  page.ownerTarget = 'another chat'; page.syncSteps(); assert.deepEqual(page.steps, []); assert.equal(page.reveal, 0);
});

test('only a visible running activity with a recorded start owns an elapsed clock', () => {
  const timers = new Set();
  const helpers = {setInterval:fn=>{timers.add(fn); return fn;}, clearInterval:fn=>timers.delete(fn),
    Date:{now:()=>11000}, recordedDuration:ms=>String(ms)};
  const row = pageMethods('ConversationWork',['syncClock','stopClock','get elapsed'],helpers);
  Object.assign(row,{clockTimer:-1,visible:true,active:true,group:{active:1,startedAt:0},now:0});
  row.syncClock();
  assert.deepEqual([timers.size,row.elapsed],[0,'']);
  row.group.startedAt=6000; row.syncClock();row.syncClock();
  assert.deepEqual([timers.size,row.elapsed],[1,'5000']);
  row.visible=false;row.syncClock();
  assert.equal(timers.size,0);
  row.group.active=0;
  assert.equal(row.elapsed,'');
});


test('title sheen owns one animator only while visible and enabled', () => {
  const created=[];
  const row=pageMethods('RunningToolTitle',['syncSweep','stopSweep','aboutToDisappear']);
  row.getUIContext=()=>({createAnimator:()=>{
    const animator={play(){this.playing=true;},cancel(){this.playing=false;},setExpectedFrameRateRange(){}};
    created.push(animator); return animator;
  }});
  Object.assign(row,{animate:true,visible:false,highlightPosition:-1});
  row.syncSweep();
  assert.equal(created.length,0);
  row.visible=true;row.syncSweep();row.syncSweep();
  assert.equal(created.length,1);assert.equal(created[0].playing,true);
  created[0].onFrame(0.5);assert.equal(row.highlightPosition,0.5);
  row.animate=false;row.syncSweep();created[0].onFrame(0.7);
  assert.equal(created[0].playing,false);assert.equal(row.highlightPosition,-1);
  row.animate=true;row.syncSweep();row.visible=false;row.syncSweep();
  assert.equal(created[1].playing,false);
  row.visible=true;row.syncSweep();row.aboutToDisappear();
  assert.equal(created[2].playing,false);
});

test('an open native tool sheet follows a late receipt and releases its selection on navigation or removal', () => {
  const pending = { kind:'tool', id:'use' };
  const returned = { ...pending, result:{ text:'8 passed' } };
  const page = pageMethods('ConversationWork', ['syncInspector','dismissInspector']);
  function open() {
    const binding = new BoundRow(pending.id, { step:pending, live:true });
    Object.assign(page, { selection:binding, detailOpen:true, active:true, expanded:true, ownerTarget:'chat',
      work:{ workKey:'turn', running:true, inspectionSteps:[pending] }, detailOwner:JSON.stringify(['chat','turn']) });
    return binding;
  }
  const binding = open();
  page.work = { ...page.work, running:false, inspectionSteps:[returned] };
  page.syncInspector();
  assert.equal(page.detailOpen, true);
  assert.equal(page.selection, binding);
  assert.deepEqual(binding.value, { step:returned, live:false });
  for (const change of [() => { page.expanded = false; }, () => { page.active = false; }, () => { page.ownerTarget = 'another'; },
    () => { page.work.workKey = 'other turn'; }, () => { page.work.inspectionSteps = []; }]) {
    open(); change(); page.syncInspector();
    assert.equal(page.detailOpen, false);
    assert.equal(page.selection, undefined);
  }
});

test('only visible, running calls with an alternate icon play their own frame sequence', () => {
  const nativeEnvironment = { reduceMotion:false };
  const sheets = { command:{ frames:100, duration:1667 }, read:{ frames:36, duration:600 } };
  const icon = pageMethods('../design/ToolGlyph',
    ['get sheet','get motionEnabled','syncMotion','stopMotion','aboutToDisappear'], { nativeEnvironment, sheets });
  const animations = [];
  icon.getUIContext = () => ({ createAnimator: options => {
    const animation = { options, play() {}, cancel() { this.cancelled = true; }, setExpectedFrameRateRange() {} };
    animations.push(animation);
    return animation;
  } });
  Object.assign(icon, { running:true, visible:false, motion:'command', frame:0 });
  icon.syncMotion();
  assert.equal(animations.length, 0);
  icon.visible = true; icon.syncMotion(); icon.syncMotion();
  const terminal = animations[0];
  assert.equal(animations.length, 1);
  assert.equal(terminal.options.duration, 1667);
  terminal.onFrame(0.41); assert.equal(icon.frame, 41);
  terminal.onFrame(1); assert.equal(icon.frame, 99);
  icon.motion = 'read'; icon.syncMotion();
  assert.equal(terminal.cancelled, true);
  assert.equal(animations.at(-1).options.duration, 600);
  terminal.onFrame(0.5); assert.equal(icon.frame, 0);
  animations.at(-1).onFrame(0.5); assert.equal(icon.frame, 18);
  for (const stop of [() => { icon.running = false; }, () => { nativeEnvironment.reduceMotion = true; },
    () => { icon.visible = false; }, () => { icon.motion = undefined; }]) {
    const current = animations.at(-1);
    stop(); icon.syncMotion();
    assert.equal(current.cancelled, true);
    current.onFrame(0.4); assert.equal(icon.frame, 0);
    icon.running = true; icon.visible = true; nativeEnvironment.reduceMotion = false; icon.motion = 'read'; icon.syncMotion();
  }
  icon.motion = undefined; icon.syncMotion();
  const pulse = animations.at(-1);
  assert.equal(pulse.options.duration,1200);
  pulse.onFrame(0.5);assert.ok(icon.strength < 0.6,'fallback animates the original tool glyph');
  icon.visible=false;icon.syncMotion();assert.equal(icon.strength,1);
  icon.motion = 'read'; icon.visible = true; icon.syncMotion();
  const last = animations.at(-1);
  icon.aboutToDisappear(); last.onFrame(0.41);
  assert.equal(last.cancelled, true);
  assert.equal(icon.frame, 0);
});
