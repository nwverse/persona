import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';

const source = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
const rowBindingSource = readFileSync(new URL('../entry/src/main/ets/models/RowBinding.ets', import.meta.url), 'utf8');
const rowBindingCode = stripTypeScriptTypes(rowBindingSource.replace(/@ObservedV2\s*/g, '').replace(/@Trace\s*/g, '').replace(/\bexport\s+/g, ''));
const { reconcileBoundRows } = new Function(rowBindingCode + '\nreturn { reconcileBoundRows };')();
const { agentRowPreview, agentRowTone, runningSubagents } = await import('../entry/src/main/ets/core/AgentRowPreview.ts');

function block(start, name) {
  const body = source.indexOf('{', start);
  assert.ok(body >= 0, name + ' body');
  let depth = 1, quote = '', escaped = false, end = body + 1;
  for (; end < source.length && depth; end++) {
    const char = source[end];
    if (quote) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === quote) quote = '';
    } else if (char === '"' || char === "'" || char === '`') quote = char;
    else if (char === '{') depth++;
    else if (char === '}') depth--;
  }
  assert.equal(depth, 0, name + ' closes');
  return source.slice(start, end);
}

function classCode(name) {
  const start = source.indexOf('@ObservedV2\nclass ' + name + ' {');
  assert.ok(start >= 0, name);
  return block(start, name).replace(/@ObservedV2\s*/g, '').replace(/@Trace\s*/g, '');
}

function methodCode(name) {
  const start = source.indexOf('  private ' + name + '(');
  assert.ok(start >= 0, name);
  return block(start, name);
}

const AgentRowPresentation = new Function('return ' + stripTypeScriptTypes(classCode('AgentRowPresentation')) + ';')();
const BoundAgentRow = new Function('AgentRowPresentation', 'return ' + stripTypeScriptTypes(classCode('BoundAgentRow')) + ';')(AgentRowPresentation);
const BoundAgentGroup = new Function('return ' + stripTypeScriptTypes(classCode('BoundAgentGroup')) + ';')();

function pageClass(wire, translate) {
  return new Function('BoundAgentRow', 'AgentRowPresentation', 'BoundAgentGroup', 'Wire', 'tr', 'Colors', 'reconcileBoundRows',
    'agentRowPreview', 'agentRowTone', 'runningSubagents',
    stripTypeScriptTypes('class Page {\n' + ['isRowSelected','syncRowBindings', 'bindAgentRows', 'updateAgentRowPresentation', 'rowTime', 'rowStatus', 'rowStatusColor', 'rowSpoken', 'subagentLine']
      .map(methodCode).join('\n') + '\n}\n') + '\nreturn Page;')(
        BoundAgentRow, AgentRowPresentation, BoundAgentGroup, wire, translate,
        { needs: 'needs', working: 'working', unread: 'unread', secondary: 'secondary' }, reconcileBoundRows,
        agentRowPreview, agentRowTone, runningSubagents);
}

function methodsClass(names, globals) {
  const keys = Object.keys(globals);
  return new Function(...keys, stripTypeScriptTypes('class Methods {\n' + names.map(methodCode).join('\n') + '\n}') + '\nreturn Methods;')(...keys.map((key) => globals[key]));
}

function row(raw, fields = {}) {
  const value = { raw: JSON.stringify(raw), detail: '', state: '', machine: '', path: '', manager: 'work', group: 'hub', kind: 'session', id: 's1', ...fields };
  value.key = () => JSON.stringify(value.group ? [value.group, value.kind, value.manager, value.machine, value.id] : [value.kind, value.manager, value.machine, value.id]);
  return value;
}

const family = {
  relations: {root:()=>undefined,children:()=>[]},
  foldFamilies:()=>true, foldedChildren:()=>[], isPinned:()=>false, familyExpanded:()=>false, displayRows:group=>group.rows, familyState:row=>row.state
};
function fixture() {
  let parseCount = 0;
  const translations = { language: 'en', en: { just: 'just now', minute: 'm', hour: 'h', day: 'd' }, zh: { just: '现在', minute: '分', hour: '时', day: '天' } };
  const wire = {
    parse(raw) { parseCount++; return JSON.parse(raw); },
    text(data, key, fallback = '') { return typeof data[key] === 'string' ? data[key] : fallback; }
  };
  const translate = key => {
    const words = translations[translations.language];
    return key === 'agent_time_just_now' ? words.just : key === 'agent_time_minute_suffix' ? words.minute :
      key === 'agent_time_hour_suffix' ? words.hour : key === 'agent_time_day_suffix' ? words.day : key;
  };
  const page = new (pageClass(wire, translate))();
  page.workspace = { ...family, isMuted: () => false };
  page.mixedHubs = false;
  page.model = {
    directoryOwnerFor: () => ({ machineName: id => 'Computer ' + id }),
    groupName: () => 'Hub',
    shortPath: path => path.replace(/\\/g, '/').split('/').filter(Boolean).slice(-2).join('/')
  };
  return { page, translations, get parseCount() { return parseCount; } };
}

test('identity changes refresh selection without decoding an unchanged payload', () => {
  const context = fixture(), { page } = context;
  const value = row({ machine_id: 'raw-machine', session_id: 'raw-session', last_text: 'Same payload' });
  const bound = { identity: 'retained-binding', key: 'retained-key', value };
  const bindings = page.bindAgentRows([], [bound], true), binding = bindings[0];
  page.selectionVisible = true;
  page.workspace = { ...family, isMuted: () => false, selectedKey: binding.presentation.selectionKey };
  assert.equal(page.isRowSelected(binding), true);
  for (const [field, replacement] of [['group', 'other-hub'], ['kind', 'conversation'],
    ['manager', 'other-manager'], ['machine', 'other-machine'], ['session', 'other-session']]) {
    value[field] = replacement;
    assert.equal(page.bindAgentRows(bindings, [bound], true)[0], binding);
    const machine = value.machine || 'raw-machine', session = value.session || 'raw-session';
    assert.equal(binding.presentation.selectionKey, JSON.stringify([value.group, value.kind, value.manager, machine, session]));
    assert.equal(page.isRowSelected(binding), false, field + ' retires the old highlight');
    page.workspace.selectedKey = binding.presentation.selectionKey;
    assert.equal(page.isRowSelected(binding), true);
  }
  assert.equal(context.parseCount, 1);
  value.group = '';value.raw = '{}';value.session = '';
  page.bindAgentRows(bindings, [bound], true);
  page.workspace.selectedKey = binding.presentation.selectionKey;
  value.id = 'fallback-session';page.bindAgentRows(bindings, [bound], true);
  assert.equal(binding.presentation.selectionKey, JSON.stringify([value.kind,value.manager,value.machine,value.id]));
  assert.equal(page.isRowSelected(binding), false);
  assert.equal(context.parseCount, 2, 'changing only the fallback id does not decode again');
});
test('same-key session changes move the native highlight through iOS group reconciliation',()=>{
 for(const groupingMode of ['state','machine','space']){
  const context=fixture(),{page}=context;
  const value=row({machine_id:'m1',session_id:'s1',last_text:'Same payload'},{machine:'m1',session:'s1'});
  const group={id:'machines',rows:[value]},workspace={...family,isMuted:()=>false,groupingMode,selectedKey:JSON.stringify(['hub','session','work','m1','s1']),
   groups:[group],visibleRows(){return this.groups.flatMap(item=>item.rows);}};
  Object.assign(page,{workspace,selectionVisible:true,groupBindings:[],rowBindingOwner:undefined,rowBindingScope:'',groupBindingSequence:0});
  page.model.profile={id:'hub',manager:'work'};page.model.scope=()=> 'hub/work';
  page.syncRowBindings();const binding=page.groupBindings[0].rows[0];
  assert.equal(page.isRowSelected(binding),true);assert.equal(context.parseCount,1);
  group.rows=[row(JSON.parse(value.raw),{machine:'m1',session:'s2'})];
  page.syncRowBindings();const refreshed=page.groupBindings[0].rows[0];
  assert.equal(refreshed,binding);assert.equal(page.isRowSelected(refreshed),false);
  workspace.selectedKey=JSON.stringify(['hub','session','work','m1','s2']);assert.equal(page.isRowSelected(refreshed),true);
  assert.equal(context.parseCount,1);
 }
});

test('projection decodes once, keeps BoundRow identity, and refreshes display fields for same-key raw updates', () => {
  const context = fixture(), { page } = context;
  const firstRow = row({ machine_id: 'm1', repo_root: '/work/persona', status: 'working', last_text: 'First',
    updated_at: new Date(Date.now() - 5 * 60_000).toISOString(), space_name: 'space' });
  const bound = { identity: '["session/m1/s1",0]', key: 'stable-bound-key', value: firstRow };
  const initial = page.bindAgentRows([], [bound], true);
  const presentationBinding = initial[0];
  const projection = presentationBinding.presentation;

  assert.equal(context.parseCount, 1);
  assert.equal(presentationBinding.bound, bound);
  assert.equal(presentationBinding.key, 'stable-bound-key');
  assert.equal(projection.recentContent, 'First');
  assert.equal(projection.status, 'working');
  assert.equal(projection.spaceName, 'space');
  assert.deepEqual(projection.metadata, ['Hub', 'Computer m1', 'work/persona', 'working', 'First']);
  assert.equal(projection.place, 'Computer m1', 'one Hub: the place line names only the computer');

  bound.value = row({ machine_id: 'm1', repo_root: '/other/project', status: 'needs_you', last_text: 'Changed',
    updated_at: new Date(Date.now() - 60_000).toISOString(), space_name: 'new-space' });
  const refreshed = page.bindAgentRows(initial, [bound], true);

  assert.equal(refreshed[0], presentationBinding);
  assert.equal(refreshed[0].bound, bound);
  assert.equal(refreshed[0].key, 'stable-bound-key');
  assert.equal(refreshed[0].presentation, projection);
  assert.equal(context.parseCount, 2);
  assert.equal(projection.recentContent, 'Changed');
  assert.equal(projection.status, 'needs_you');
  assert.equal(projection.spaceName, 'new-space');
  assert.deepEqual(projection.metadata, ['Hub', 'Computer m1', 'other/project', 'needs_you', 'Changed']);
  assert.equal(page.rowStatus(projection), 'needs_you');
  assert.equal(page.rowStatusColor(projection), 'needs');
});

test('time formatting reads the projected timestamp, follows locale changes, and does not decode raw data', () => {
  const context = fixture(), { page, translations } = context;
  const bound = { identity: 's1', key: 'stable-key', value: row({ status: 'unread', updated_at: new Date(Date.now() - 5 * 60_000).toISOString() }) };
  const binding = page.bindAgentRows([], [bound], true)[0];

  assert.equal(context.parseCount, 1);
  assert.equal(page.rowTime(binding.presentation), '5m');
  translations.language = 'zh';
  assert.equal(page.rowTime(binding.presentation), '5分');
  assert.equal(context.parseCount, 1);
});

test('full row binding decodes missing machine and session once and updates across iOS group changes', () => {
  const context = fixture(), { page } = context;
  const initialRow = row({ machine_id: 'm1', session_id: 's1', status: 'working', last_text: 'Current' });
  const group = { id: 'machines', rows: [initialRow] };
  const workspace = { ...family,
    isMuted: () => false, groupingMode: 'state', selectedKey: JSON.stringify(['hub', 'session', 'work', 'm1', 's1']),
    groups: [group], visibleRows() { return this.groups.flatMap(item => item.rows); }
  };
  page.model = {
    profile: { id: 'hub', manager: 'work' }, scope: () => 'hub/work',
    directoryOwnerFor: () => ({ machineName: id => 'Computer ' + id }),
    groupName: () => 'Hub', shortPath: path => path
  };
  page.workspace = workspace;
  page.groupBindings = [];
  page.groupBindings = [];
  page.rowBindingOwner = undefined;
  page.rowBindingScope = '';
  page.groupBindingSequence = 0;

  page.syncRowBindings();
  assert.equal(context.parseCount, 1, 'the active core group projects each row once');
  const flatProjection = page.groupBindings[0].rows[0].presentation;
  assert.equal(flatProjection.selectionKey, workspace.selectedKey);
  assert.equal(flatProjection.selectionKey, JSON.stringify(['hub', 'session', 'work', 'm1', 's1']));
  assert.equal(workspace.selectedKey === flatProjection.selectionKey, true);

  workspace.groupingMode = 'machine';
  page.syncRowBindings();
  assert.equal(context.parseCount, 1, 'switching groups preserves the existing projected row');
  const groupBinding = page.groupBindings[0].rows[0];
  assert.equal(groupBinding.presentation.selectionKey, workspace.selectedKey);
  assert.equal(workspace.selectedKey === groupBinding.presentation.selectionKey, true);

  group.rows = [row({ machine_id: 'm1', session_id: 's1', status: 'needs_you', last_text: 'Refreshed' })];
  page.syncRowBindings();
  assert.equal(context.parseCount, 2, 'same-key row data updates are projected once in the active mode');
  assert.equal(page.groupBindings[0].rows[0].presentation.selectionKey, workspace.selectedKey);
  assert.equal(page.groupBindings[0].rows[0].presentation.status, 'needs_you');

  workspace.groupingMode = 'space';
  page.syncRowBindings();
  assert.equal(context.parseCount, 2, 'switching back does not decode unchanged row data');
  assert.equal(page.groupBindings[0].rows[0].presentation.selectionKey, workspace.selectedKey);
  assert.equal(page.groupBindings[0].rows[0].presentation.recentContent, 'Refreshed');
  assert.equal(workspace.selectedKey === page.groupBindings[0].rows[0].presentation.selectionKey, true);
});

test('row rendering reads the projection instead of parsing ResourceRow data or recomputing workspace metadata', () => {
  const start = source.indexOf('  @Builder agentRow(');
  const end = source.indexOf('  @Builder searchControl()', start);
  assert.ok(start >= 0 && end > start);
  const render = source.slice(start, end);

  assert.match(render, /binding\.presentation\.place/);
  assert.match(render, /this\.rowSpoken\(binding\)/);
  assert.match(render, /binding\.presentation\.recentContent/);
  assert.match(render, /this\.rowTime\(binding\.presentation\)/);
  assert.match(render, /this\.isRowSelected\(binding\)/);
  const selection = methodCode('isRowSelected');
  assert.match(selection, /this\.workspace\.selectedKey === binding\.presentation\.selectionKey/);
  assert.doesNotMatch(selection, /\.data\(\)|Wire\.parse|workspace\.isSelected\(/);
  assert.doesNotMatch(render, /workspace\.isSelected\(/);
  assert.doesNotMatch(render, /\.data\(\)|Wire\.parse|rowMetadata\(/);

  const menuStart = source.indexOf('  @Builder rowMenu(');
  const menuEnd = source.indexOf('  @Builder groupHeader(', menuStart);
  const menu = source.slice(menuStart, menuEnd);
  assert.match(menu, /binding\.presentation\.spaceName/);
  assert.doesNotMatch(menu, /\.data\(\)|Wire\.parse/);
});

test('idle status uses the same localized words for the row and its spoken label',()=>{
 for(const [locale,label] of [['base','Idle'],['en_US','Idle'],['zh_CN','待机'],['ja_JP','待機']]){
  const catalog=JSON.parse(readFileSync(new URL('../entry/src/main/resources/'+locale+'/element/string.json',import.meta.url),'utf8'));
  const labels=new Map(catalog.string.map(item=>[item.name,item.value]));const Page=pageClass({},key=>labels.get(key)||key);const page=new Page();
  assert.equal(page.rowStatus({status:'idle'}),label);
 }
});

test('visible selection can clear on Home without changing the cached selected Agent',()=>{
 const Page=pageClass({},key=>key),page=new Page(),binding={presentation:{selectionKey:'agent'}};page.workspace={selectedKey:'agent'};page.selectionVisible=true;assert.equal(page.isRowSelected(binding),true);page.selectionVisible=false;assert.equal(page.isRowSelected(binding),false);assert.equal(page.workspace.selectedKey,'agent');page.selectionVisible=true;assert.equal(page.isRowSelected(binding),true);
});

test('rows name the Hub and computer, never the manager ID, on screen and when spoken', () => {
  const context = fixture(), { page } = context;
  page.mixedHubs = true;
  const binding = page.bindAgentRows([], [{ identity: 's1', key: 'k', value: row({ machine_id: 'm1', status: 'idle' }, { manager: 'mgr-raw-id' }) }], true)[0];
  assert.equal(binding.presentation.place, 'Hub · Computer m1');
  assert.ok(!binding.presentation.metadata.slice(0, 2).join(' ').includes('mgr-raw-id'));
  const render = source.slice(source.indexOf('  @Builder agentRow('), source.indexOf('  @Builder searchControl()'));
  assert.doesNotMatch(render, /\.manager\)? ?[,\]].*join/);
  assert.match(methodCode('rowSpoken'), /metadata\.slice\(0, 2\)/);
  assert.doesNotMatch(methodCode('updateAgentRowPresentation'), /\[this\.model\.groupName\(row\), row\.manager\]/);
  assert.doesNotMatch(methodCode('machineSelectOptions'), /row\.manager/);
});

test('row times read the page clock and a row newer than the last tick reads just now', () => {
  const context = fixture(), { page } = context;
  const binding = page.bindAgentRows([], [{ identity: 's1', key: 'k', value: row({ status: 'idle', updated_at: new Date(Date.now() - 90 * 60_000).toISOString() }) }], true)[0];
  page.clock = Date.now();
  assert.equal(page.rowTime(binding.presentation), '1h');
  page.clock = Date.now() + 3 * 3600_000;
  assert.equal(page.rowTime(binding.presentation), '4h', 'the 30 s tick moves the time without new row data');
  const fresh = page.bindAgentRows([], [{ identity: 's2', key: 'k2', value: row({ status: 'idle', updated_at: new Date(Date.now() + 20_000).toISOString() }) }], true)[0];
  page.clock = Date.now();
  assert.equal(page.rowTime(fresh.presentation), 'just now');
  assert.equal(context.parseCount, 2);
  assert.match(source, /setInterval\(\(\) => \{ this\.clock = Date\.now\(\); \}, ROW_TIME_REFRESH_MS\)/);
  assert.match(source, /const ROW_TIME_REFRESH_MS: number = 30000;/);
  assert.match(methodCode('stopClock'), /clearInterval/);
});

test('a muted row keeps Needs you but drops other marks and the bold unread title', () => {
  const context = fixture(), { page } = context;
  let muted = false;
  page.workspace = { ...family, isMuted: () => muted };
  const bound = { identity: 's1', key: 'k', value: row({ status: 'unread', last_text: 'Done.' }, { state: 'unread' }) };
  const binding = page.bindAgentRows([], [bound], true)[0];
  assert.equal(binding.presentation.tone, 'unread');
  assert.equal(binding.presentation.muted, false);
  muted = true;
  page.bindAgentRows([binding], [bound], true);
  assert.equal(binding.presentation.tone, '', 'a mute change applies without new row data');
  assert.equal(binding.presentation.muted, true);
  bound.value = row({ status: 'needs_you', attention_reason: 'Approve the plan' }, { state: 'needs_you' });
  page.bindAgentRows([binding], [bound], true);
  assert.equal(binding.presentation.tone, 'needs_you');
  assert.equal(binding.presentation.recentContent, 'Approve the plan');
  const render = source.slice(source.indexOf('  @Builder agentRow('), source.indexOf('  @Builder searchControl()'));
  assert.match(render, /status === 'unread' && !binding\.presentation\.muted \? FontWeight\.Bold : FontWeight\.Medium/);
  assert.match(render, /Symbols\.muted/);
  assert.match(render, /opacity\(binding\.presentation\.muted \? Tokens\.mutedOpacity : 1\)/);
  assert.match(render, /breathing: binding\.presentation\.tone === 'needs_you' && this\.active && !this\.environment\.reduceMotion/);
});

test('background waiting shows its own word instead of the working capsule', () => {
  const context = fixture(), { page } = context;
  const binding = page.bindAgentRows([], [{ identity: 's1', key: 'k', value: row({ status: 'working', background_waiting: true }, { state: 'working' }) }], true)[0];
  assert.equal(binding.presentation.tone, 'background');
  assert.equal(page.rowStatus(binding.presentation), 'agent_group_background');
  assert.equal(page.rowStatusColor(binding.presentation), 'working');
});

test('parent rows count their running subagents', () => {
  const context = fixture(), { page } = context;
  const binding = page.bindAgentRows([], [{ identity: 's1', key: 'k', value: row({ status: 'working', session_id: 'p',
    subagents: [{ id: 'a', running: true }, { id: 'b', running: false }, { id: 'c', session_id: 'p@c', running: true }] }, { state: 'working' }) }], true)[0];
  assert.equal(binding.presentation.subagents, 2);
  assert.match(methodCode('subagentLine'), /agent_subagents_running/);
});

test('quick filters show All, nonempty states with counts, and the chosen one while it empties', () => {
  const Quick = methodsClass(['quickFilters', 'quickLabel', 'quickIndex', 'chooseQuick'], { QUICK_FILTERS: ['all', 'needsYou', 'working', 'unread', 'backgroundWaiting'],
    QUICK_LABELS: { all: 'all', needsYou: 'needs_you', working: 'working', unread: 'unread', backgroundWaiting: 'agent_group_background' }, tr: (key) => key });
  const page = new Quick();
  let chosen = '';
  page.workspace = { quickFilter: 'all', counts: { needsYou: 2, working: 0, unread: 3, backgroundWaiting: 0 }, setQuickFilter(key) { chosen = key; } };
  assert.deepEqual(page.quickFilters(), ['all', 'needsYou', 'unread']);
  assert.equal(page.quickLabel('needsYou'), 'needs_you 2');
  assert.equal(page.quickLabel('all'), 'all');
  page.chooseQuick(2);
  assert.equal(chosen, 'unread');
  page.workspace.quickFilter = 'working';
  assert.deepEqual(page.quickFilters(), ['all', 'needsYou', 'working', 'unread']);
  assert.equal(page.quickIndex(), 2);
});

test('group headers count what folds hide, in the Needs color, and Needs you never folds', () => {
  const Header = methodsClass(['groupTitle', 'groupTotal', 'groupNeeds', 'groupCounts'], { STATE_LABELS: { needs: 'needs_you', 'state/working': 'working' },
    STATE_TOTAL_GROUPS: ['pinned', 'needs'], Wire: { number: (value, key) => typeof value[key] === 'number' ? value[key] : 0 }, tr: (key) => key });
  const page = new Header();
  const machine = { id: 'machine/m1', machineName: 'Studio', folded: true, stateCounts: { needs_you: 2, working: 1 }, totalRowCount: 3, rows: [] };
  assert.equal(page.groupNeeds(machine), '2 needs_you');
  assert.equal(page.groupCounts(machine), '1 working');
  assert.equal(page.groupTotal(machine), '');
  assert.equal(page.groupTotal({ id: 'state/working', totalRowCount: 4 }), '4');
  assert.equal(page.groupTotal({ id: 'needs', totalRowCount: 2 }), '2');
  const header = source.slice(source.indexOf('  @Builder groupHeader('), source.indexOf('  private groupTitle('));
  assert.match(header, /if \(binding\.value\.id !== NEEDS_GROUP\) Button/);
  assert.match(header, /Span\(this\.groupNeeds\(binding\.value\)\)\.fontColor\(Colors\.needs\)/);
});

test('a parent row shows its family state and running count once, and a child says whose it is', () => {
  const context = fixture(), { page } = context;
  const parent = row({ status: 'idle', session_id: 'p', subagents: [{ id: 'a', running: true }] }, { state: 'idle', id: 'p' });
  const kids = [row({ status: 'working' }, { state: 'working', id: 'k1' }), row({ status: 'needs_you' }, { state: 'needs_you', id: 'k2' })];
  page.workspace = { ...family, isMuted: () => false, foldedChildren: (r) => r === parent ? kids : [],
    relations: { root: (r) => kids.includes(r) ? parent : undefined, children: () => kids, kind: (r) => r === kids[1] ? 'side' : 'subtask' },
    familyState: (r) => r === parent ? 'needs_you' : r.state };
  const binding = page.bindAgentRows([], [{ identity: 'p', key: 'p', value: parent }], true)[0];
  assert.equal(binding.presentation.status, 'needs_you');
  assert.equal(binding.presentation.tone, 'needs_you');
  assert.equal(binding.presentation.familySummary, 'relation_side_count · relation_task_count · relation_needs_count · relation_running_count');
  assert.equal(page.subagentLine(binding.presentation), '', 'the family line already counts what runs');
  assert.match(page.rowSpoken(binding), /relation_running_count/);
  const child = page.bindAgentRows([], [{ identity: 'k2', key: 'k2', value: kids[1] }], true)[0];
  assert.match(page.rowSpoken(child), /relation_side · relation_root/);
});

test('the quick filter segments are rebuilt when their set changes, never updated in place', () => {
  // CapsuleSegmentButtonV2 throws in getBackplatePosition when its items change under a selection it has not laid out.
  assert.match(source, /ForEach\(\[this\.quickFilters\(\)\.join\('\|'\)\], \(_set: string\) => \{\s*(?:\/\/[^\n]*\n\s*)*CapsuleSegmentButtonV2\(\{ items: new SegmentButtonV2Items\(this\.quickFilters\(\)/);
  assert.match(source, /buttonPadding: LengthMetrics\.vp\(Tokens\.fine\) \}\)\.width\('100%'\)\s*\}, \(set: string\) => set\)/);
});
