import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(spec, ctx, next) {
    if (spec.startsWith('.') && ctx.parentURL?.endsWith('.ets'))
      return {url:new URL(spec+'.ets',ctx.parentURL).href,shortCircuit:true};
    return next(spec,ctx);
  },
  load(url,ctx,next) {
    if (!url.endsWith('.ets')) return next(url,ctx);
    const src=readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*|@Trace\s+/g,'');
    return {format:'module',source:stripTypeScriptTypes(src),shortCircuit:true};
  }
});
const {phonePlaceAtRoot,phonePanIntent,finishesPhonePan}=await import('../entry/src/main/ets/models/PhoneNavigation.ets');
const {PhoneSearchIndex,PhoneChatMatch,mergeChatMatches,localChatMatches}=await import('../entry/src/main/ets/models/PhoneChatSearch.ets');
const {AgentLaunchChoices,LaunchChoice,RecentAgentSetup,rememberAgentSetup,loadAgentSetups}=await import('../entry/src/main/ets/models/AgentLaunchChoices.ets');
const {ProjectWorktree}=await import('../entry/src/main/ets/models/ProjectCatalog.ets');
const {ResourceRow,Wire}=await import('../entry/src/main/ets/models/Wire.ets');

test('drawer roots and drill-downs leave always exactly one leading action',()=>{
  assert.equal(phonePlaceAtRoot(4,[],false),true);assert.equal(phonePlaceAtRoot(4,['project'],false),false);
  assert.equal(phonePlaceAtRoot(0,['conversation'],true),true);assert.equal(phonePlaceAtRoot(0,['conversation'],false),false);
  assert.equal(phonePlaceAtRoot(1,[],false),true);assert.equal(phonePlaceAtRoot(1,['session'],false),false);
  for(const [s,r] of [[2,'resources'],[3,'settings_root'],[5,'resources'],[6,'resources'],[7,'resources'],[8,'settings'],[9,'settings'],[10,'recordings']]){
    assert.equal(phonePlaceAtRoot(s,[r],false),true);assert.equal(phonePlaceAtRoot(s,[r,'other'],false),false);
  }
});
test('horizontal drawer and peer gestures leave pushed pages, vertical scroll and the composer alone',()=>{
  const pan=(section,root,open,x,y,dx,dy)=>phonePanIntent(section,root,open,x,y,390,840,dx,dy);
  assert.equal(pan(0,true,false,25,200,14,2),'open');assert.equal(pan(4,true,true,240,150,-15,2),'close');
  assert.equal(pan(0,true,false,280,200,-18,0),'agents');assert.equal(pan(1,true,false,130,210,18,0),'persona');
  for(const args of [[1,false,false,7,80,18,0],[0,true,false,280,800,-18,0],[0,true,false,280,180,-18,20],[0,true,false,150,150,-18,0]])
    assert.equal(pan(...args),'');
  assert.equal(finishesPhonePan(31,800,390),true);assert.equal(finishesPhonePan(32,-900,390),false);
  assert.equal(finishesPhonePan(18,0,390),false);assert.equal(finishesPhonePan(-110,0,390),true);
});
function makeOwner(call) {
  return {generation:1,connected:true,profile:{id:'home',manager:'agent'},features:['assistant_search'],conversations:[],
    scope(){return this.profile.id+':'+this.profile.manager},connectionGeneration(){return this.generation},
    owns(g,s){return g===this.generation&&s===this.scope()},repo(){return{call}}};
}
function gate(){let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}}
test('folder choices retain only resolved directories from the current machine and connection',async()=>{
  const pending=new Map();const owner=makeOwner(p=>{const g=gate();pending.set(p,g);return g.promise});
  const choices=new AgentLaunchChoices();const a=choices.folder(owner,'work/mac','/one'),b=choices.folder(owner,'work/mac','/two');
  const [p1,p2]=[...pending.keys()];assert.equal(p1,'/api/v1/machines/work%2Fmac/dirs?path=%2Fone');
  pending.get(p2).resolve({path:'/two',parent:'/',entries:[{name:'project',path:'/two/project',is_git:true}],recent:[{name:'actual',path:'/recent'}]});await b;
  pending.get(p1).resolve({path:'/one',entries:[]});await a;
  assert.equal(choices.path,'/two');assert.equal(choices.directories[0].detail,'Git');assert.equal(choices.recentDirectories[0].id,'/recent');
  const c=choices.folder(owner,'new','/unknown');owner.generation++;
  [...pending.values()].at(-1).resolve({path:'/old-scope'});await c;assert.equal(choices.path,'');
});
test('available models, compatible efforts and launcher-supplied model locks follow the node',async()=>{
  const replies=[{agent:'codex',models:[{id:'m1',name:'Model one',default:true,efforts:['medium','high']},{id:'m2',efforts:['low']}]},
    {agent:'claude',locked:'configured by launcher',models:[]}];
  const paths=[];const owner=makeOwner(async p=>{paths.push(p);return replies.shift()});const c=new AgentLaunchChoices();
  await c.loadModels(owner,'Mac One','code/dev');assert.equal(paths[0],'/api/v1/machines/Mac%20One/launchers/code%2Fdev/models?purpose=agent.new');
  assert.equal(c.acceptsTypedModel,true);assert.deepEqual(c.effortNames,['medium','high']);c.updateEfforts('m2');assert.deepEqual(c.effortNames,['low']);
  await c.loadModels(owner,'Mac One','managed');assert.equal(c.acceptsTypedModel,false);assert.equal(c.locked,'configured by launcher');assert.deepEqual(c.effortNames,[]);
});
test('recent setups deduplicate exact workspace/launcher/model choices and never retain prompt text',()=>{
  const first=Object.assign(new RecentAgentSetup(),{machine:'owner+node',cwd:'/workspace',launcher:'test',model:'m',effort:'high',tree:'w'});
  const other=Object.assign(new RecentAgentSetup(),{machine:'owner+node',cwd:'/else'});
  assert.deepEqual(rememberAgentSetup([other,first],first),[first,other]);
  assert.deepEqual(loadAgentSetups('{"entries":[{"machine":"m","cwd":"/a","prompt":"private"},{"machine":""}]}').map(x=>[x.machine,x.cwd,x.prompt]),[['m','/a',undefined]]);
});

test('the model picker preserves the host default as one choice and shows the launcher identity and description',async()=>{
  const choices=new AgentLaunchChoices(),owner=makeOwner(async()=>({agent:'codex',models:[
    {id:'first',name:'First',default:true,efforts:['low']},
    {id:'case/second',name:'Second',description:'Use this for reviews',efforts:['high']}
  ]}));
  await choices.loadModels(owner,'mac','coding');
  assert.equal(choices.defaultModelName(),'First');assert.equal(choices.selectedModelId('first'),'');
  assert.deepEqual(choices.models.map(m=>[m.id,m.title,m.detail,m.description]),[
    ['case/second','Second','coding · case/second','Use this for reviews']]);
  choices.updateEfforts('case/second');assert.deepEqual(choices.effortNames,['high']);
});

// Run the real phone form methods under Node, as navigation-context does for the native navigator.
// Only persistence and post-selection presentation are outside this fixture's boundary.
const formSource = readFileSync(new URL('../entry/src/main/ets/pages/Index.ets', import.meta.url), 'utf8');
const formMethods = new Map();
for (const match of formSource.matchAll(/^  private (?:async )?(\w+)\(/gm)) {
  const lineEnd = formSource.indexOf('\n', match.index);
  const end = formSource.slice(match.index, lineEnd).trimEnd().endsWith('}') ? lineEnd : formSource.indexOf('\n  }', lineEnd) + 4;
  formMethods.set(match[1], formSource.slice(match.index, end));
}
const externalEffects = new Set(['saveAgentDraft', 'loadRecentSetups', 'closeAgentPanel']);
const required = new Set(['pickerOptions', 'chooseAgentOption', 'selectedAgentTree', 'openAgentPicker', 'pickAgentMachine',
  'canStartAgent', 'browseAgentFolder', 'loadAgentLaunchers']);
for (const name of required) {
  const body = formMethods.get(name);
  assert.ok(body, name);
  for (const [, callee] of body.matchAll(/\bthis\.(\w+)\(/g)) {
    if (formMethods.has(callee) && !externalEffects.has(callee)) required.add(callee);
  }
}
const NativeAgentForm = new Function('Wire', 'LaunchChoice', 'tr', 'return ' + stripTypeScriptTypes(
  'class NativeAgentForm {\n' + [...required].map(name => formMethods.get(name)).join('\n') + '\n}'))(Wire, LaunchChoice, key => key);

function clientForForm(group, manager, calls) {
  const owner = makeOwner(async path => {
    calls.push([group, manager, path]);
    if (path.includes('/dirs?')) return { path: '/chosen', parent: '/', entries: [] };
    if (path.includes('/models?')) return { agent: 'codex', models: [{ id: 'independent' }] };
    return { launchers: [{ name: 'codex' }] };
  });
  owner.profile = { id: group, manager };
  owner.operationBlocked = () => false;
  return owner;
}
function formMachine(owner, id) {
  const machine = ResourceRow.from({ id, name: id, online: true, manager: owner.profile.manager }, 'machine');
  machine.group = owner.profile.id;
  return machine;
}
function formTree(machine, id, source = 'orca') {
  return Object.assign(new ProjectWorktree(), {
    group: machine.group, manager: machine.manager, machine: machine.id, source, sourceId: id, online: true, startAllowed: true,
    key: JSON.stringify([machine.group, machine.manager, machine.id, source, id]), title: id, cwd: '/work/' + id
  });
}
function phoneForm(root, selectedMachine, machines, trees) {
  root.directoryMachines = machines;
  const form = Object.assign(new NativeAgentForm(), {
    model: root, agentModel: root, agentPanel: '', agentFormRevision: 1, machineIntent: 1, launcherRevision: 0,
    newTask: 'test', directory: '', launcher: 'codex', agentModelId: '', pickedAgentTree: '', launchListReady: false, blockedLaunchers: [],
    showSheet: true, sheetKind: 'newagent', switchingAgentMachine: false, creatingAgent: false,
    launchChoices: new AgentLaunchChoices(), projectCatalog: { loading: false, projects: [{ trees }], refresh() {} },
    saveAgentDraft() {}, loadRecentSetups() {}, closeAgentPanel() { this.agentPanel = ''; }
  });
  form.agentMachineKey = form.machineKey(selectedMachine);
  return form;
}

test('opening the workspace picker refreshes an occupied catalog and accepts only this machine’s bound worktrees', async () => {
  const owner = clientForForm('group-A', 'manager-A', []), foreign = clientForForm('group-B', 'manager-A', []);
  const machine = formMachine(owner, 'same-id'), otherMachine = formMachine(owner, 'second'), otherHub = formMachine(foreign, 'same-id');
  const inferred = formTree(machine, 'session-folder', ''), directory = formTree(machine, 'known-directory', 'session-directory');
  const herdr = formTree(machine, 'terminal', 'herdr'), registered = formTree(machine, 'login');
  const old = [inferred, directory, herdr, formTree(otherMachine, 'foreign-machine'), formTree(otherHub, 'foreign-group')];
  const form = phoneForm(owner, machine, [machine, otherMachine, otherHub], old);
  const release = gate(); let refreshes = 0;
  form.projectCatalog.refresh = async root => {
    assert.equal(root, owner);
    refreshes++;
    await release.promise;
    form.projectCatalog.projects = [{ trees: [...old, registered, Object.assign(formTree(machine,'offline'), { online:false })] }];
  };
  form.openAgentPicker('workspace');
  assert.equal(refreshes, 1);
  assert.deepEqual(form.pickerOptions().map(x => x.id), ['']);
  release.resolve(); await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(form.pickerOptions().map(x => x.id), ['', registered.key]);
  for (const wrong of [inferred, directory, herdr, old[3], old[4]]) {
    form.chooseAgentOption(wrong.key);
    assert.equal(form.pickedAgentTree, '');
  }
  form.chooseAgentOption(registered.key);
  assert.equal(form.selectedAgentTree(), registered);
  assert.equal(form.directory, registered.cwd);
  form.openAgentPicker('workspace');
  assert.equal(refreshes, 2, 'a previous successful registration does not suppress the next catalog read');
});

test('switching between matching machine IDs isolates owner reads and retries a failed selection', async () => {
  for (const [otherGroup, otherManager] of [['group-B','manager-A'], ['group-A','manager-B']]) {
    const calls = [], a = clientForForm('group-A','manager-A',calls), b = clientForForm(otherGroup,otherManager,calls);
    const localMachine = formMachine(a, 'same-node'), otherMachine = formMachine(b,'same-node');
    const local = formTree(localMachine,'left'), remote = formTree(otherMachine,'right','paseo');
    const form = phoneForm(a, localMachine, [localMachine, otherMachine], [local,remote]);
    const failure = gate(); let attempts = 0;
    a.viewFor = async target => {
      assert.equal(target, otherMachine);
      if (++attempts === 1) { await failure.promise; throw Error('disconnected'); }
      return b;
    };
    const selecting = form.pickAgentMachine(otherMachine);
    form.openAgentPicker('folder');
    form.openAgentPicker('model');
    assert.deepEqual(calls, [], 'the prior Hub cannot serve reads during a switch');
    failure.resolve(); await selecting;
    assert.equal(attempts, 1);
    // A form may roll the highlight back, or retain the failed machine so the user can retry it.
    if (form.chosenAgentMachine() === otherMachine) {
      assert.equal(form.canStartAgent(), false);
      assert.deepEqual(form.availableAgentTrees(), [], 'retained options cannot expose the preceding owner');
      form.openAgentPicker('workspace');
      assert.equal(form.pickerOptions().some(option => option.id === local.key), false);
      form.openAgentPicker('folder');
      form.openAgentPicker('model');
      form.agentPanel = 'folder';
      form.browseAgentFolder('/previously-visible-folder');
      assert.deepEqual(calls, [], 'a failed new owner must never read folders or models from the previous owner');
    }
    await form.pickAgentMachine(otherMachine);
    assert.equal(attempts, 2, 'tapping the same failed machine retries its owner lookup');
    assert.equal(form.chosenAgentMachine(), otherMachine);
    assert.equal(form.agentModel, b);
    form.openAgentPicker('workspace');
    assert.deepEqual(form.pickerOptions().map(option => option.id), ['',remote.key]);
    form.openAgentPicker('folder');
    form.openAgentPicker('launcher');
    form.chooseAgentOption('codex');
    form.openAgentPicker('model');
    assert.ok(calls.some(([group,manager,path]) => group === otherGroup && manager === otherManager && path.includes('/dirs?')));
    assert.ok(calls.some(([group,manager,path]) => group === otherGroup && manager === otherManager && path.includes('/codex/models?')));
    assert.ok(calls.every(([group,manager]) => group === otherGroup && manager === otherManager));
  }
});
test('matches local titles and previews immediately; remote message snippet and id enrich the existing chat',()=>{
  const a=ResourceRow.from({id:'a',title:'Release',machine_id:'mac',last_text:'Zoom quality',updated_at:'2026-11-20'},'conversation');
  const b=ResourceRow.from({id:'b',machine_id:'mac',title:'Archived',archived:true},'conversation');
  const list=localChatMatches([b,a],'release quality',false);assert.equal(list.length,1);assert.equal(list[0].row,a);
  const merged=mergeChatMatches(list,[new PhoneChatMatch(a,'This sentence matches','actual-item')]);assert.equal(merged.length,1);assert.equal(merged[0].subtitle,'This sentence matches');assert.equal(merged[0].hitId,'actual-item');
});
test('full-text requests are debounced, and clearing, switching scope or hiding search discards late hits',async()=>{
  const calls=[];const owner=makeOwner(async p=>{const g=gate();calls.push([p,g]);return g.promise});
  const index=new PhoneSearchIndex();index.schedule(owner,'a',false);index.schedule(owner,'snow & rain',false);
  await new Promise(r=>setTimeout(r,250));assert.equal(calls.length,1);assert.match(calls[0][0],/q=snow%20%26%20rain/);
  index.close();calls[0][1].resolve({results:[{machine_id:'m',session_id:'old',title:'Old'}]});await new Promise(r=>setTimeout(r,5));assert.deepEqual(index.matches,[]);
  index.schedule(owner,'fresh',false);await new Promise(r=>setTimeout(r,230));owner.generation++;
  calls[1][1].resolve({results:[{machine_id:'m',session_id:'other'}]});await new Promise(r=>setTimeout(r,5));assert.deepEqual(index.matches,[]);
  index.close();index.schedule(owner,'agent',true);assert.equal(index.busy,false);index.close();
});

test('closing the compact drawer with a row under the finger keeps the current page', () => {
  const methods = ['drawerAcceptsTap', 'endPhonePan'].map(name => formMethods.get(name));
  const Drawer = new Function('finishesPhonePan', 'return ' + stripTypeScriptTypes(
    'class NativeDrawer {\n' + methods.join('\n') + '\n}'))(finishesPhonePan);
  const drawer = new Drawer();
  Object.assign(drawer, {wide: () => false, spacesShown: true, gestureKind: 'close',
    gestureScope: 'paired', gesturePlace: 4, phoneWidth: 360, drawerTapBlockedUntil: 0,
    model: {scope: () => 'paired', section: 4}});
  assert.equal(drawer.drawerAcceptsTap(), false);
  drawer.endPhonePan(-170, -620);
  assert.equal(drawer.spacesShown, false);
  assert.equal(drawer.drawerAcceptsTap(), false);
  // A late click still belongs to the completed drag, even if it reaches the retained row.
  drawer.spacesShown = true;
  assert.equal(drawer.drawerAcceptsTap(), false);
  drawer.drawerTapBlockedUntil = Date.now() - 1;
  assert.equal(drawer.drawerAcceptsTap(), true);
});

test('Reduce Motion switches a compact drawer directly and retires an older completion', () => {
  const methods = ['settlePhoneDrawer', 'movePhonePan'].map(name => formMethods.get(name));
  const Drawer = new Function('Curve', 'return ' + stripTypeScriptTypes(
    'class NativeDrawer {\n' + methods.join('\n') + '\n}'))({EaseOut: 'ease-out'});
  const drawer = new Drawer();
  let completion;
  Object.assign(drawer, {
    drawerAnimation: 0, drawerMounted: true, spacesShown: false, drawerFraction: 1,
    environment: {reduceMotion: false}, gestureKind: 'open', drawerWidth: () => 300, drawerFrom: 0,
    getUIContext: () => ({animateTo(options, update) { completion = options.onFinish; update(); }})
  });
  drawer.settlePhoneDrawer(false);
  assert.equal(typeof completion, 'function');
  drawer.environment.reduceMotion = true;
  drawer.getUIContext = () => { throw new Error('unexpected native animation'); };
  drawer.movePhonePan(120);
  assert.equal(drawer.drawerFraction, 0);
  drawer.spacesShown = true;
  drawer.settlePhoneDrawer(true);
  completion();
  assert.equal(drawer.drawerMounted, true);
  assert.equal(drawer.drawerFraction, 1);
  drawer.spacesShown = false;
  drawer.settlePhoneDrawer(false);
  assert.equal(drawer.drawerMounted, false);
  assert.equal(drawer.drawerFraction, 0);
});

test('only a workspace opened over the same owned session inherits its phone header and sibling tabs', () => {
  const start = formSource.indexOf('get phoneToolParent()');
  const end = formSource.indexOf('private phonePageName()', start);
  assert.ok(start >= 0 && end > start);
  const Shell = new Function('return ' + stripTypeScriptTypes(
    'class PhoneShell {\n' + formSource.slice(start, end) + '\n}'))();
  const shell = new Shell();
  const row = (machine, manager, id) => {
    const value = new ResourceRow();
    value.kind = 'session'; value.group = 'hub'; value.machine = machine; value.manager = manager; value.id = id;
    return value;
  };
  const a = row('desktop','personal','same');
  const b = row('laptop','personal','same');
  const otherManager = row('desktop','shared','same');
  const routes = new Map([['chat',a],['opened-from-chat',a],['different-computer',b],['different-manager',otherManager]]);
  Object.assign(shell,{phoneTop:'tools',phonePath:['project','session','tools'],phoneParam:'opened-from-chat',
    stack:{getParamByIndex:index => index === 1 ? 'chat' : undefined},routeRow:param => routes.get(param)});
  assert.equal(shell.phoneToolParent,'chat');
  for (const param of ['different-computer','different-manager']) {
    shell.phoneParam = param;
    assert.equal(shell.phoneToolParent,'');
  }
  shell.phoneParam = 'opened-from-chat';
  shell.phonePath = ['project','tools'];
  assert.equal(shell.phoneToolParent,'');
});
