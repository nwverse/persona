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
    stripTypeScriptTypes('class Page {\n' + ['isRowSelected','syncRowBindings', 'bindAgentRows', 'updateAgentRowPresentation', 'rowTime', 'rowStatus', 'rowStatusColor', 'needsStatusLabel', 'rowPlace','rowSpoken','subagentLine']
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

test('state group headers replace repeated status words, while muted and machine sections retain each Agent’s state',()=>{
 const {page}=fixture();
 for(const group of ['pinned','state/needs','state/idle','state/working']) assert.equal(page.needsStatusLabel(group),false);
 for(const group of ['state/muted','machine/home','space/project']) assert.equal(page.needsStatusLabel(group),true);
 page.mixedMachines=true;page.workspace={groupingMode:'state'};
 const display={brand:'claude',provider:'claude',spaceName:'login-form',metadata:['Hub','MacBook']};
 assert.equal(page.rowPlace(display),'Claude · MacBook · login-form');
 page.workspace.groupingMode='machine';assert.equal(page.rowPlace(display),'Claude · login-form');
 const row=source.slice(source.indexOf('  @Builder agentRow('),source.indexOf('  @Builder rowContext('));
 assert.match(row,/\.accessibilityText\(this\.rowSpoken\(binding\)\)/);
});

test('phone search starts folded, reveals only from a downward top pull, and a saved query survives leaving',()=>{
 const TouchType={Down:0,Move:1,Up:2};
 const Search=new Function('TouchType','return '+stripTypeScriptTypes('class Search {'+
   ['searchVisible','setSearchEditing','resetSearchReveal','trackPull','refreshAgents'].map(methodCode).join('\n')+'}'))(TouchType);
 const page=new Search(); page.refreshGroupingSupport=()=>{};page.reloadDecisions=()=>{};
 let top=0,refreshes=0;
 page.workspace={search:'',setReadingDetail(){},refresh(){refreshes++;return Promise.resolve();},isBoundTo(){return true;}};
 page.agentScroller={currentOffset:()=>({yOffset:top})};page.compactRoot=true;page.active=true;page.onSearchMode=()=>{};page.resetSearchReveal();
 const gesture=(x,y)=>{page.trackPull({type:0,touches:[{windowX:0,windowY:10}]});page.trackPull({type:1,touches:[{windowX:x,windowY:10+y}]});};
 assert.equal(page.searchVisible(),false);
 top=180;gesture(0,80);assert.equal(page.searchVisible(),false);
 top=0;gesture(90,55);assert.equal(page.searchVisible(),false);
 gesture(0,30);assert.equal(page.searchVisible(),false);
 gesture(1,60);assert.equal(page.searchVisible(),true);page.refreshAgents();assert.equal(refreshes,0);
 page.trackPull({type:0,touches:[{windowX:0,windowY:10}]});page.refreshAgents();assert.equal(refreshes,1);
 page.searchOpen=true;gesture(0,-60);assert.equal(page.searchVisible(),false,'scrolling the empty resting field folds it');
 page.searchOpen=true;page.searchEditing=true;gesture(0,-60);assert.equal(page.searchVisible(),true,'the active editor stays available');
 page.workspace.search='login';page.resetSearchReveal();assert.equal(page.searchVisible(),true);
 gesture(0,-100);assert.equal(page.searchVisible(),true,'an unfocused query remains visible while scrolling');
 page.workspace.search='';page.compactRoot=false;assert.equal(page.searchVisible(),true);
 assert.match(source,/accessibilityCustomActions\(\[\{name:tr\('search_sessions'\),onAction:\(\)=>this.activateSearch\(\)\}\]\)/);
 assert.match(source,/MenuItem\(\{content:tr\('search_sessions'\)\}\).*id\('agents.search_reveal'\)/);
});

test('choosing an Agents group keeps the resting header, active query, and list position', () => {
  const groupRows = source.slice(source.indexOf('@Builder groupingMenu()'), source.indexOf(".id('agents.grouping.'+key)"));
  const action = groupRows.slice(groupRows.indexOf('.onClick(') + '.onClick('.length).trim().slice(0, -1);
  const pick = new Function('index', 'return (' + action + ')');
  const Menu = new Function('return (' + stripTypeScriptTypes('class Menu {' + methodCode('selectGrouping') + '}') + ')')();
  for (const [revealed, editing, query] of [[false,false,''], [true,false,''], [true,true,'weather']]) {
    const page = new Menu();
    page.availableGroupings = ['repo','state'];
    page.searchOpen = revealed;
    page.searchEditing = editing;
    page.workspace = { groupingMode: 'repo', search: query, setGroupingMode(key) { this.groupingMode = key; } };
    const scrollRequests = [];
    page.agentScroller = { scrollToIndex(index) { scrollRequests.push(index); } };
    page.resetSearchReveal = () => { page.searchOpen = false; page.searchEditing = false; };
    for (const index of [0,1,1]) {
      pick.call(page,index)();
      assert.equal(page.workspace.groupingMode,page.availableGroupings[index]);
      assert.deepEqual([page.searchOpen,page.searchEditing,page.workspace.search],[revealed,editing,query]);
      assert.deepEqual(scrollRequests,[]);
    }
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

test('the horizontal filter retains the selection as live counts disappear and reappear', () => {
  const getter = block(source.indexOf('  @Computed get shownQuickFilters'), 'shownQuickFilters').replace('@Computed ', '');
  const keys = ['all','needsYou','unread','working','backgroundWaiting','feishu','terminal'];
  const View = new Function('QUICK_FILTERS','Wire','return '+stripTypeScriptTypes('class Filters {'+getter+'}'))(keys,
    {number:(counts,key) => counts[key] || 0});
  const page = new View();
  page.workspace = {quickFilter:'working', quickCountsKnown:()=>false, filterCounts:{}};
  assert.deepEqual(page.shownQuickFilters,keys);
  page.workspace.quickCountsKnown=()=>true;
  page.workspace.filterCounts={working:2, unread:1};
  assert.deepEqual(page.shownQuickFilters,['all','unread','working']);
  page.workspace.filterCounts={terminal:1};
  assert.deepEqual(page.shownQuickFilters,['all','working','terminal']);
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
  const header = block(source.indexOf('  @Builder groupHeader('),'groupHeader');
  assert.match(header,/binding\.value\.totalRowCount/);
  assert.match(header,/this\.toggleGroup\(binding\.value\.id\)/);
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

test('list actions tap on folds and filters, Interrupt taps once it is sent, and Mark all as read succeeds once', async () => {
  const feedback = [];
  const Actions = methodsClass(['toggleGroup', 'toggleFamilyRow', 'toggleIdle', 'foldAll', 'interruptRow', 'markAllCount', 'markAllLabel', 'markAllSeen'],
    { Haptics: { tap: () => feedback.push('tap'), success: () => feedback.push('success') }, tr: (key) => key });
  const page = new Actions(), calls = [];
  let interruptResult = true, markResult = true, canFold = true;
  page.filterSheetOpen = true;
  page.workspace = {
    quickFilter: 'all', unread: ['a', 'b'],
    toggleFold: (id) => calls.push('fold ' + id), toggleFamily: (row) => calls.push('family ' + row.id), toggleIdleRows: (id) => calls.push('idle ' + id),
    canFoldAll: () => canFold, foldAll: (fold) => calls.push('all ' + fold),
    interrupt: async (row) => { calls.push('interrupt ' + row.id); return interruptResult; },
    unreadToMark() { return this.unread; }, markAllSeen: async () => { calls.push('mark'); return markResult; }
  };
  page.toggleGroup('state/working'); page.toggleFamilyRow({ id: 'main' }); page.toggleIdle('state/idle'); page.toggleIdle('machine/m1');
  assert.deepEqual(calls.splice(0), ['fold state/working', 'family main', 'fold state/idle', 'idle machine/m1']);
  assert.deepEqual(feedback.splice(0), ['tap', 'tap', 'tap', 'tap']);

  canFold = false; page.foldAll(true);
  assert.deepEqual([calls.length, feedback.length, page.filterSheetOpen], [0, 0, true], 'nothing to fold: no tap and the sheet stays');
  canFold = true; page.foldAll(false);
  assert.deepEqual([calls.splice(0), feedback.splice(0), page.filterSheetOpen], [['all false'], ['tap'], false]);

  page.interruptRow({ id: 'run' }); await new Promise((resolve) => setTimeout(resolve));
  interruptResult = false; page.interruptRow({ id: 'gone' }); await new Promise((resolve) => setTimeout(resolve));
  assert.deepEqual([calls.splice(0), feedback.splice(0)], [['interrupt run', 'interrupt gone'], ['tap']]);

  assert.equal(page.markAllCount(), 0, 'only under the Unread filter');
  page.workspace.quickFilter = 'unread';
  assert.equal(page.markAllCount(), 2);
  assert.equal(page.markAllLabel(2), '2 agent_group_unread', 'the count reads as the group headers do');
  page.markAllSeen(); await new Promise((resolve) => setTimeout(resolve));
  markResult = false; page.markAllSeen(); await new Promise((resolve) => setTimeout(resolve));
  assert.deepEqual([calls.splice(0), feedback.splice(0)], [['mark', 'mark'], ['success']]);
});

test('Interrupt, Mark all as read and Collapse / Expand all sit where iOS puts them, and haptics are touch vibration', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/AgentListPage.ets', import.meta.url), 'utf8');
  const menu = source.slice(source.indexOf('  @Builder sessionMenu('), source.indexOf('  @Builder groupHeader('));
  assert.ok(menu.indexOf("tr('agent_interrupt')") > menu.indexOf("'agent_unpin' : 'agent_pin'"), 'after Pin, as iOS');
  assert.match(menu,/if \(this\.workspace\.canInterrupt\(row\)\) \{\s*MenuItem\(\{ content: tr\('agent_interrupt'\)/);
  const list = source.slice(source.indexOf('  build() {'));
  assert.ok(list.indexOf('this.markAllCount() > 0') < list.indexOf('ForEach(this.groupBindings'), 'Mark all as read heads the list');
  assert.ok(list.includes(".id('agents.mark_all_seen')"));
  const sheet = source.slice(source.indexOf('  @Builder filterSheet('), source.indexOf('  private isRowSelected('));
  assert.ok(sheet.includes('this.foldAllButton(true, false); this.foldAllButton(false, false)'));
  assert.ok(sheet.includes('this.environment.fontScale >= 1.5'), 'stacked with large text');
  assert.ok(source.includes('.enabled(this.workspace.canFoldAll(fold))'));
  const haptics = readFileSync(new URL('../entry/src/main/ets/platform/Haptics.ets', import.meta.url), 'utf8');
  assert.ok(haptics.includes("{ usage: 'touch' }"), 'the phone touch-vibration setting governs it');
  assert.ok(haptics.includes('isSupportEffectSync'));
  const module = readFileSync(new URL('../entry/src/main/module.json5', import.meta.url), 'utf8');
  assert.ok(module.includes('"ohos.permission.VIBRATE"'));
});

test('Agents keeps its horizontal filter buttons keyed by status during a cold start', () => {
  const render = block(source.indexOf('  @Builder filters('), 'filters');
  assert.match(render, /ForEach\(this.shownQuickFilters/);
  assert.match(render, /\(key: string\) => key/);
  assert.match(render, /scrollable\(ScrollDirection.Horizontal\)/);
  assert.doesNotMatch(render, /CapsuleSegmentButtonV2|\bSelect\(/);
});


test('Needs You surfaces follow the visible oldest-first order as the decisions change', () => {
  const getter=block(source.indexOf('  @Computed get orderedNeedsRows'), 'orderedNeedsRows').replace('@Computed ', '');
  const methods=['displayRows','startsSurface','endsSurface'].map(methodCode).join('\n');
  const Page=new Function('return '+stripTypeScriptTypes('class Page {\n'+getter+'\n'+methods+'\n}'))();
  const page=new Page(), a={first:true,last:false,bound:{value:{waiting:20}}}, b={first:false,last:true,bound:{value:{waiting:10}}};
  const group={value:{id:'state/needs'},rows:[a,b]};
  page.groupBindings=[group];page.compactRoot=true;page.rail=false;
  page.decisions={waitingSince:(_,row)=>row.waiting};
  assert.deepEqual(page.displayRows(group),[b,a]);
  assert.equal(page.startsSurface(b,group.value.id),true);assert.equal(page.endsSurface(b,group.value.id),false);
  assert.equal(page.endsSurface(a,group.value.id),true);
  a.bound.value.waiting=5;
  assert.deepEqual(page.displayRows(group),[a,b]);
  assert.equal(page.startsSurface(a,group.value.id),true);assert.equal(page.endsSurface(b,group.value.id),true);
  group.rows=[b];
  assert.equal(page.startsSurface(b,group.value.id),true);assert.equal(page.endsSurface(b,group.value.id),true);
  page.rail=true;assert.equal(page.startsSurface(b,group.value.id),false);
  assert.equal(page.endsSurface(b,'state/working'),true);
});
