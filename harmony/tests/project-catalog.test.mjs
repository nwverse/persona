import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import test from 'node:test';

registerHooks({
  resolve(specifier, context, next) {
    if (context.parentURL?.endsWith('.ets') && ['./Wire','./ProjectCatalog','./SessionSpace'].includes(specifier)) {
      return { url: new URL(specifier + '.ets', context.parentURL).href, shortCircuit: true };
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (!url.endsWith('.ets')) return next(url, context);
    const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
    return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
  }
});
const { ProjectCatalog, ProjectBrowserState, projectDirectory } = await import('../entry/src/main/ets/models/ProjectCatalog.ets');
const { projectOutline, projectHosts, worktreeSessionLines } = await import('../entry/src/main/ets/models/ProjectOutline.ets');
const { ResourceRow } = await import('../entry/src/main/ets/models/Wire.ets');
const { SessionSpace, sessionSpace } = await import('../entry/src/main/ets/models/SessionSpace.ets');
function row(kind, value, group = 'home', manager = 'work') {
  const item = ResourceRow.from(value, kind);
  item.group = group;
  item.manager = manager;
  return item;
}
function workspace(id, extra = {}) {
  return row('worktree', { machine_id: 'mac', source: 'orca', source_id: id, title: id, cwd: '/repo/' + id, ...extra });
}
function session(id, extra = {}) {
  return row('session', { session_id: id, machine_id: 'mac', cwd: '/repo/feature', title: id, ...extra });
}
const machine = row('machine', { id: 'mac', name: 'Studio', online: true });

test('a mounted worktree card opens the newest scoped row and rejects cards removed or shown under another checkout',()=>{
  const source=readFileSync(new URL('../entry/src/main/ets/components/ProjectsPage.ets',import.meta.url),'utf8');
  const method=source.slice(source.indexOf('  private openSpaceCard('),source.indexOf('  @Builder spaceCard('));
  const Page=new Function('return '+stripTypeScriptTypes('class Page {'+method+'}'))();
  const live=session('same'),foreign=session('same');foreign.group='elsewhere';
  const current={row:live};let agents=[current];const opened=[];
  const page=Object.assign(new Page(),{page:'project_tree',target:'checkout',catalog:{tree:key=>key==='checkout'?{agents}:undefined},chooseAgent:agent=>opened.push(agent)});
  page.openSpaceCard('checkout',foreign.key());page.openSpaceCard('another',live.key());assert.deepEqual(opened,[]);
  page.openSpaceCard('checkout',live.key());assert.deepEqual(opened,[current]);
  agents=[];page.openSpaceCard('checkout',live.key());assert.deepEqual(opened,[current]);
});

function sibling(id,slot,extra={},group='home',manager='work') {
  return row('session',{session_id:id,title:id,machine_id:'mac',cwd:'/repo',status:'idle',state:'idle',space_id:'w1',
    space_kind:'herdr',space_name:'weather',slot_id:'tab'+slot,slot_index:slot,slot_name:String(slot),...extra},group,manager);
}

test('the space strip follows native tab and pane order, keeps the current session in its pane and excludes foreign lookalikes',()=>{
  const current=sibling('review',3,{slot_name:'review',pane_id:'w1:p3',updated_at:'2026-11-15T00:00:00Z'});
  const other=sibling('after-clear',3,{pane_id:'w1:p3',updated_at:'2026-11-16T00:00:00Z'});
  const valid=[current,sibling('widget',1,{slot_name:'widget'}),sibling('tests',2,{slot_name:'tests'}),sibling('release',4)];
  const rejected=[other,sibling('wrong-machine',1,{machine_id:'other'}),sibling('other-hub',1,{},'elsewhere'),
    sibling('other-manager',1,{},'home','personal'),sibling('other-space',1,{space_id:'w2'}),sibling('other-host',1,{space_kind:'orca'}),
    sibling('stopped',1,{status:'offline'}),sibling('hidden',1,{assistant:true})];
  const result=sessionSpace(current,[...rejected,...valid],[],'home','work');
  assert.equal(result.title,'weather');assert.equal(result.current,'review');
  assert.deepEqual(result.tabs.map(t=>t.id),['widget','tests','review','release']);
  assert.equal(result.tabs.at(-1).label,'#4');
  const duplicates=sessionSpace(current,[current,sibling('c',5,{pane_id:'w1:pC',pane_index:2,agent:'codex'}),
    sibling('b',5,{pane_id:'w1:pB',pane_index:1,agent:'codex'}),sibling('a',5,{pane_id:'w1:pA',pane_index:0,agent:'claude'})],[],'home','work').tabs.slice(1);
  assert.deepEqual(duplicates.map(t=>[t.label,t.starter]),[['#5 · Claude',true],['#5 · Codex 1',false],['#5 · Codex 2',false]]);
});

test('a chat without a host space can show its registered project and never invents tabs from matching paths',()=>{
  const first=session('one'),second=session('two');
  const projects=projectDirectory([workspace('feature')],[first,second],[machine]);
  const result=sessionSpace(first,[first,second],projects,'home','work');
  assert.equal(result.title,projects[0].title);assert.deepEqual(result.tabs,[]);
  assert.equal(sessionSpace(first,[first,second],projects,'other','work').title,'');
  assert.deepEqual(sessionSpace(first,[],projects,'home','work').tabs,[]);
});

test('switching an agent tab replaces only the owning route and a stale tap cannot navigate',async()=>{
  const source=readFileSync(new URL('../entry/src/main/ets/pages/Index.ets',import.meta.url),'utf8');
  const code=source.slice(source.indexOf('  @Computed get phoneSessionSpace()'),source.indexOf('  private phoneLargeTitle()')).replace('@Computed ','');
  const {Wire}=await import('../entry/src/main/ets/models/Wire.ets');
  const Shell=new Function('SessionSpace','sessionSpace','Wire','return '+stripTypeScriptTypes('class Shell {'+code+'}'))(SessionSpace,sessionSpace,Wire);
  const current=sibling('review',3),next=sibling('tests',2),foreign=sibling('outside',2,{},'other');
  const names=['project_tree','session'],params=['weather','original'];let saved=0;
  const v=Object.assign(new Shell(),{model:{section:4},phoneTop:'session',phoneParam:'original',activeChild:'original',chatReady:true,
    targetReady:()=>v.chatReady,childKey:(_name,param)=>param,projectCatalog:{projects:[],browser:{}},
    detailModel:{connected:true,profile:{id:'home',manager:'work'},selected:current,sessions:[current,next,foreign]},
    stack:{size:()=>2,getAllPathName:()=>names,getParamByIndex:i=>params[i],replacePath:spec=>{params[1]=spec.param;}},
    routeRow:()=>current,routeTarget:(row,root)=>ResourceRow.from({...row.data(),navigation_root:root},'session'),
    savePath:()=>saved++});
  const key=v.phoneSessionSpace.key;
  v.switchSessionTab('wrong','review','tests');v.switchSessionTab(key,'else','tests');
  v.switchSessionTab(key,'review','outside');v.switchSessionTab(key,'review','review');
  assert.equal(saved,0);
  v.switchSessionTab(key,'review','tests');
  assert.equal(saved,1);assert.equal(params[0],'weather');
  const route=JSON.parse(params[1]);assert.equal(route.id,'tests');assert.equal(route.group,'home');assert.equal(route.manager,'work');
  assert.equal(JSON.parse(route.raw).navigation_root,false);
  v.switchSessionTab(key,'review','tests');assert.equal(saved,1);
});

test('a restored offline detail adopts live permissions without replacing session rows, and old menus recheck their scoped checkout', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/ProjectsPage.ets', import.meta.url), 'utf8');
  const methods = ['menuTarget','menuEntries','headerIdentity','startFromTree','openEditor'].map(name => {
    const start = source.indexOf('  private ' + name + '(');
    assert.notEqual(start, -1, name);
    const end = source.indexOf('\n  }', start);
    return source.slice(start, end + 4);
  });
  const Page = new Function('return ' + stripTypeScriptTypes('class Page {\n' + methods.join('\n') + '\n}'))();
  const catalog = new ProjectCatalog();
  const update = (online, cwd, agents = []) => {
    const current = workspace('shared', { online: true, available: true, cwd });
    if (!online) current.state = 'offline';
    const other = workspace('shared', { online:true, available:true, cwd });
    other.manager = 'different-owner';
    catalog.projects = projectDirectory([current,other], agents, [machine]);
    return catalog.projects.flatMap(p => p.trees).find(t => t.manager === 'work' && t.sourceId === 'shared');
  };
  const cached = update(false, '/repo/one');
  const opened = [], starts = [];
  const ui = Object.assign(new Page(), { catalog, page:'project_tree', target:cached.key, model:{},
    onStart: tree => starts.push(tree), editor:{busy:false, open: (_model,tree,action) => opened.push([tree,action])} });
  const oldKey = ui.headerIdentity(cached);
  assert.equal(ui.menuEntries(cached.key)[0].online, false);
  const online = update(true, '/repo/one');
  assert.notEqual(cached, online);
  assert.notEqual(ui.headerIdentity(online), oldKey);
  assert.equal(ui.menuEntries(cached.key)[0],online);
  ui.openEditor(cached,'create');
  assert.deepEqual(opened, [[online,'create']]);
  ui.startFromTree(cached.key);
  assert.deepEqual(starts,[online]);
  const changedAgents = update(true, '/repo/one', [session('added', { cwd:'/repo/one', workspace_id:'shared', space_kind:'orca' })]);
  assert.equal(ui.headerIdentity(changedAgents), ui.headerIdentity(online), 'activity alone does not recreate the header or menu');
  update(false, '/repo/one');
  ui.openEditor(online,'rename');
  update(true, '/repo/moved');
  ui.openEditor(online,'clean');
  assert.equal(opened.length,1,'the original menu cannot rename offline or remove a relocated checkout');
  ui.target = catalog.projects.flatMap(p => p.trees).find(t => t.manager === 'different-owner').key;
  assert.deepEqual(ui.menuEntries(cached.key),[]);
  ui.startFromTree(cached.key);
  assert.equal(starts.length,1,'the same host workspace id in another owner is a different route');
});
test('empty worktrees, independent roots and nested host worktrees retain stable identities', () => {
  const catalog = [
    workspace('main', { project_id: 'a', project_name: 'Repo', is_main: true }),
    workspace('feature', { project_id: 'b', parent_source_id: 'main' }),
    workspace('deep', { parent_source_id: 'feature' }),
    workspace('other', { project_id: 'a', project_name: 'Repo' }),
    workspace('other-engine', { source: 'paseo', project_id: 'a', project_name: 'Another repo' })
  ];
  const result = projectDirectory(catalog, [session('agent', { workspace_id: 'deep', space_kind: 'orca' })], [machine]);
  assert.equal(result.length, 2);
  const project = result.find(item => item.title === 'Repo');
  assert.deepEqual(project.roots.map(tree => tree.title), ['main', 'other']);
  assert.equal(project.sessionCount, 1);
  const child = project.roots[0].children[0].children[0];
  assert.equal(child.breadcrumb, 'main › feature › deep');
  assert.equal(child.sessions[0].id, 'agent');
  assert.equal(project.roots[1].canStart, true);
});
test('explicit engine IDs and ambiguous directories never capture a different workspace', () => {
  const a = workspace('one', { cwd: '/repo', project_id: 'repo' });
  const b = workspace('two', { cwd: '/repo', project_id: 'repo' });
  const other = workspace('one', { cwd: '/else', project_id: 'private' });
  other.group = 'another';
  const result = projectDirectory([a, b, other], [
    session('explicit', { workspace_id: 'one', space_kind: 'orca', cwd: '/else' }),
    session('ambiguous', { cwd: '/repo' }),
    session('foreign-engine', { workspace_id: 'one', space_kind: 'paseo', cwd: '/repo' })
  ], [machine]);
  const trees = result.flatMap(p => p.trees);
  assert.deepEqual(trees.find(t => t.group === 'home' && t.sourceId === 'one').sessions.map(s => s.id), ['explicit']);
  assert.equal(trees.find(t => t.group === 'another').sessions.length, 0);
  assert.deepEqual(trees.filter(t => !t.source).flatMap(t => t.sessions.map(s => s.id)).sort(), ['ambiguous', 'foreign-engine']);
});
test('older session directories group by repository and keep offline inventory visible', () => {
  const dir = workspace('dir', { source: 'session-directory', cwd: '/repo/feature', available: false });
  const projects = projectDirectory([dir], [session('s', { repo_root: '/repo', space_kind: 'codex_app' })], [machine]);
  assert.equal(projects.length, 1);
  assert.equal(projects[0].title, 'repo');
  assert.equal(projects[0].trees[0].canStart, false);
  assert.equal(projects[0].trees[0].sessions.length, 1);
});
test('cycles in host parent IDs leave every worktree reachable without a loop', () => {
  const result = projectDirectory([workspace('a', {parent_source_id: 'b'}), workspace('b', {parent_source_id: 'a'})], [], [machine]);
  assert.equal(result.length, 1);
  assert.equal(result[0].roots.length, 1);
  assert.equal(result[0].roots[0].children[0].children.length, 0);
});
test('a stale manager reply cannot repopulate a different account', async () => {
  let complete;
  const owner = {
    profile: {id: 'home', manager: 'work'}, connected: true,
    scope: () => 'home/work', connectionGeneration: () => 1, owns: () => true,
    repo: () => ({call: () => new Promise(resolve => { complete = resolve; })})
  };
  const model = { ...owner, directoryMachines: [machine], directorySessions: [], directoryUnavailable: 0,
    refreshDirectory: async () => {}, directoryOwners: () => [owner] };
  const store = new ProjectCatalog();
  const request = store.refresh(model);
  await new Promise(resolve => setImmediate(resolve));
  store.clear();
  complete({workspaces: [workspace('old').data()]});
  await request;
  assert.deepEqual(store.projects, []);
  assert.equal(store.loading, false);
});
test('a failed read keeps last confirmed workspaces and exposes partial availability', async () => {
  let valid = true;
  const owner = {
    profile: {id: 'home', manager: 'work'}, connected: true, scope: () => 'home/work',
    connectionGeneration: () => 1, owns: () => true, repo: () => ({call: async () => {
      if (!valid) throw new Error('offline');
      return {workspaces: [workspace('confirmed').data()]};
    }})
  };
  const model = {...owner, directoryMachines: [machine], directorySessions: [], directoryUnavailable: 0,
    refreshDirectory: async () => {}, directoryOwners: () => [owner]};
  const store = new ProjectCatalog();
  await store.refresh(model);
  valid = false;
  await store.refresh(model);
  assert.equal(store.partial, true);
  assert.equal(store.projects[0].trees[0].sourceId, 'confirmed');
});

test('cold restoration shows saved scoped sessions offline, then accepts an authoritative empty session list', async () => {
  const tree = workspace('root', {cwd: '/repo'});
  const saved = JSON.stringify({
    workspaces: [tree.data()],
    sessions: [session('saved', {cwd: '/repo', workspace_id: 'root', space_kind: 'orca'}).data()],
    machines: [machine.data()]
  });
  const model = {
    connected: false, sessionsReady: false, profile: {id: 'home', manager: 'work'},
    scope: () => 'home/work', connectionGeneration: () => 1, owns: () => true,
    directorySessions: [], directoryMachines: [], directoryUnavailable: 0,
    directoryOwners() { return [this]; },
    refreshDirectory: async () => {}, loadView: async () => saved, saveView: async () => {},
    repo: () => ({call: async () => ({workspaces: [tree.data()]})})
  };
  const catalog = new ProjectCatalog();
  await catalog.refresh(model);
  assert.equal(catalog.projects[0].trees[0].sessions[0].state, 'offline');
  assert.equal(catalog.projects[0].trees[0].canStart, false);
  model.connected = true;
  model.sessionsReady = true;
  model.directoryMachines = [machine];
  await catalog.refresh(model);
  assert.equal(catalog.projects[0].trees[0].sessions.length, 0);
  assert.equal(catalog.projects[0].trees[0].canStart, true);
});

test('a worktree remains browsable while its computer is missing, offline or awaiting trust', () => {
  const tree = workspace('checkout', {cwd: '/repo'});
  for (const machines of [[], [row('machine', {id: 'mac', online: false})], [row('machine', {id: 'mac', online: true, trust: 'pending'})]]) {
    const project = projectDirectory([tree], [], machines)[0];
    assert.equal(project.trees.length, 1);
    assert.equal(project.trees[0].canStart, false);
  }
});

test('loose Git rows use the node checkout, and two repositories claiming its path never share row or route identities', () => {
  const rows = [
    session('project-scope',{cwd:'/workspace/repo/src',worktree:'/workspace/repo',repo_root:'/workspace'}),
    session('main',{cwd:'/workspace/repo/src',worktree:'/workspace/repo',repo_root:'/workspace/repo'})
  ];
  const project = input => projectDirectory([],input,[machine]).map(p => [p.title,p.roots[0].key,p.roots[0].main,p.roots[0].sessions.map(s => s.id)]);
  assert.deepEqual(project(rows),project(rows.toReversed()));
  const result = project(rows);
  assert.equal(result.length,2);
  assert.notEqual(result[0][1],result[1][1]);
  assert.equal(result.find(([title]) => title === 'repo')[2],true);
  assert.deepEqual(result.find(([title]) => title === 'workspace')[3],['project-scope']);
});

test('the phone outline puts busy agents directly below a lone main checkout and unfolds only matching search ancestors', () => {
  const make = (id,state,minute) => session(id,{cwd:'/repo/code',repo_root:'/repo',state,title:id,model:'model-'+id,updated_at:'2026-11-20T12:'+minute+':00Z'});
  const rows = [make('idle','idle','55'),make('older','working','01'),make('answer','needs_you','12'),make('newer','working','34')];
  const projects = projectDirectory([],rows,[machine]), state = new ProjectBrowserState();
  const visible = () => projectOutline(projects,state);
  assert.deepEqual(visible().map(l => l.kind),['project','session','session','session','idle-agents']);
  assert.deepEqual(visible().filter(l => l.agent).map(l => l.agent.row.id),['answer','newer','older']);
  state.toggle(visible()[0].key,true);
  assert.deepEqual(visible().map(l => l.kind),['project']);
  state.query = 'model-idle';
  assert.deepEqual(visible().map(l => l.kind),['project','session']);
  assert.equal(visible()[1].agent.row.id,'idle');
  state.query = '';
  assert.deepEqual(visible().map(l => l.kind),['project']);
});

test('nested empty workspaces keep their own controls and a search walks only matching children', () => {
  const projects = projectDirectory([workspace('main',{cwd:'/repo',is_main:true}),workspace('child',{parent_source_id:'main'}),
    workspace('grandchild',{parent_source_id:'child'}),workspace('sibling',{parent_source_id:'main'})],
    [session('wanted',{state:'working',title:'Fix the parser',workspace_id:'grandchild',space_kind:'orca'})],[machine]);
  const state = new ProjectBrowserState(), visible = () => projectOutline(projects,state);
  assert.deepEqual(visible().map(l => l.kind),['project','tree','children']);
  const disclosure = visible()[2]; state.toggle(disclosure.key,false);
  assert.ok(visible().some(l => l.tree?.sourceId === 'sibling'));
  state.query = 'parser';
  assert.deepEqual(visible().filter(l => l.kind==='tree').map(l => l.tree.sourceId),['main','child','grandchild']);
  assert.equal(visible().at(-1).agent.row.id,'wanted');
  assert.ok(!visible().find(l => l.tree?.sourceId === 'sibling'));
});

test('machine filters and tree folding distinguish matching names across owners and follow host order', () => {
  const other = row('machine',{id:'other',name:'Studio',online:true},'another','work');
  const otherAgent = row('session',{id:'o',machine_id:'other',cwd:'/same',state:'working'},'another','work');
  const projects = projectDirectory([],[session('one',{cwd:'/same',state:'working'}),otherAgent],[other,machine]);
  const hosts = projectHosts(projects);
  assert.equal(hosts.length,2); assert.equal(hosts[0].name,hosts[1].name); assert.notEqual(hosts[0].key,hosts[1].key);
  assert.equal(projects[0].roots[0].group,'another');
  const state=new ProjectBrowserState(); state.machine=hosts[1].key;
  assert.deepEqual(projectOutline(projects,state).filter(l => l.agent).map(l => l.agent.row.id),['one']);
  state.toggle('project/'+projects[1].key,true);state.clear();
  assert.equal(state.machine,'');assert.equal(state.preferences.length,0);
});

test('large projects expose a reachable Show All without clipping downstream worktrees and locale numeric naming is stable', () => {
  const sessions = Array.from({length:29},(_,i) => session('agent-'+i,{cwd:'/repo/a',state:'working',repo_root:'/repo'}));
  const known = [workspace('10',{project_id:'group',project_name:'Z',title:'app10'}),workspace('2',{project_id:'group',project_name:'Z',title:'app2'})];
  const list = projectDirectory(known,sessions,[machine]), state = new ProjectBrowserState();
  const quiet = list.find(p => p.title==='Z');assert.deepEqual(quiet.roots.map(t => t.title),['app2','app10']);
  const project = list.find(p => p.title==='repo');
  let lines = projectOutline(list,state);
  assert.equal(lines.filter(l => l.kind==='session').length,20);
  assert.equal(lines.find(l => l.kind==='more').count,29);
  state.showAll(project.roots[0].key);lines=projectOutline(list,state);
  assert.equal(lines.filter(l => l.kind==='session').length,29);assert.ok(!lines.find(l=>l.kind==='more'));
});

test('a host tab groups real pane sessions in pane order, shares the fold with worktree detail, and reveals a searched child', () => {
  const pane = (id, overrides) => session(id, {
    cwd:'/repo/weather', space_kind:'herdr', space_id:'w1', space_name:'weather', slot_id:'w1:t4', slot_index:4,
    slot_name:'release', agent:'claude', ...overrides
  });
  const rows = [
    pane('child-z',{ pane_id:'w1:p2E', pane_index:2, state:'needs_you', title:'Review deployment' }),
    pane('first',{ pane_id:'w1:pW', pane_index:0, state:'idle', title:'Write notes' }),
    pane('child-a',{ pane_id:'w1:pX', pane_index:1, state:'working', title:'Check changelog' }),
    pane('single',{ slot_id:'w1:t8', slot_index:8, state:'working', title:'Another release' }),
    pane('no-slot',{ slot_id:null, state:'working', title:'Loose terminal' })
  ];
  const projects=projectDirectory([],rows,[machine]), state=new ProjectBrowserState(), tree=projects[0].roots[0];
  const visible=()=>projectOutline(projects,state), detail=()=>worktreeSessionLines(projects[0],tree,state);
  const group=visible().find(line=>line.kind==='tab');
  assert.equal(group.tabTitle,'release');assert.equal(group.count,3);assert.equal(group.tabLead.row.id,'child-z');
  const members=visible().filter(line=>line.paneName);
  assert.deepEqual(members.map(line=>[line.agent.row.id,line.paneName,line.starter]),[
    ['first','Claude',true],['child-a','Claude 1',false],['child-z','Claude 2',false]
  ]);
  assert.equal(members[1].level,members[0].level+1);
  assert.deepEqual(visible().filter(line=>line.kind==='session').map(line=>line.agent.row.id).sort(),rows.map(row=>row.id).sort());
  state.toggle(group.key,true);
  assert.deepEqual(visible().filter(line=>line.kind==='session').map(line=>line.agent.row.id).sort(),['no-slot','single']);
  assert.equal(detail().find(line=>line.kind==='tab').open,false);
  state.query='deployment';
  const hit=visible().filter(line=>line.kind==='session');assert.equal(hit.length,1);
  assert.equal(hit[0].agent.row,rows[0]);assert.equal(hit[0].paneName,'Claude 2');assert.equal(visible().find(line=>line.kind==='tab').open,true);
  state.query='';assert.equal(visible().find(line=>line.kind==='tab').open,false);
});

test('tab identity is scoped to checkouts, hosts, managers and source spaces, and two sessions sharing a pane identifier stay accessible', () => {
  const rows=[];
  const pair=(scope,extra,group='home',manager='work')=>{
    for(const suffix of ['a','b']) rows.push(row('session',{
      session_id:scope+suffix,cwd:'/r',machine_id:'mac',space_kind:'herdr',space_id:'shared',
      slot_id:'s4',slot_name:'release',pane_id:'w1:p4',state:'working',agent:'claude',...extra
    }, group,manager));
  };
  pair('root',{});pair('other-tab',{slot_id:'s8'});pair('other-place',{cwd:'/x'});pair('other-space',{space_id:'w2'});
  pair('other-host',{machine_id:'other'});pair('other-hub',{},'second');pair('other-manager',{},'home','personal');
  const projects=projectDirectory([],rows,[machine]),state=new ProjectBrowserState();
  const view=()=>projectOutline(projects,state), groups=view().filter(line=>line.kind==='tab');
  assert.equal(groups.length,7);assert.equal(new Set(groups.map(line=>line.key)).size,7);
  assert.ok(groups.every(line=>line.count===2 && line.tabTitle==='release'));
  const visible=view().filter(line=>line.kind==='session');
  assert.equal(new Set(visible.map(line=>line.agent.row.key())).size,14);
  const target=groups.find(line=>line.tabLead.row.id==='roota');
  assert.deepEqual(visible.filter(line=>line.agent.row.id.startsWith('root')).map(line=>line.paneName),['Claude 1','Claude 2']);
  state.toggle(target.key,true);assert.equal(view().filter(line=>line.kind==='session').length,12);
});

test('one mounted tab header keeps its arrow, spoken action and handler in sync through repeated taps and a search round trip', () => {
  const source=readFileSync(new URL('../entry/src/main/ets/components/ProjectsPage.ets',import.meta.url),'utf8');
  const functions=['tabOpen','toggleTab','tabDescription'].map(name=>
    source.match(new RegExp('^  private '+name+'\\([\\s\\S]+?^  \\}', 'm'))[0]);
  const getter=source.match(/^  @Computed get tabRows\([\s\S]+?^  \}/m)[0].replace('@Computed ','');
  const query=source.match(/^  private filteringText\([^\n]+$/m)[0];
  const Page=new Function('tr','return '+stripTypeScriptTypes('class Page {\n'+[getter,query,...functions].join('\n')+'\n}'))(
    key=>key==='project_agent_count'?'{count} agents':key);
  const state=new ProjectBrowserState(), received=[];
  const pane=id=>session(id,{cwd:'/repo',state:'working',space_kind:'herdr',space_id:'w1',slot_id:'w1:t4',slot_name:'release',agent:'claude'});
  let projects=projectDirectory([],[pane('one'),pane('two')],[machine]);
  const page=Object.assign(new Page(),{page:'projects',model:{},catalog:{browser:state,toggleBrowserFold(model,key,open){
    assert.equal(model,page.model);received.push(open);state.toggle(key,open);
  }}});
  Object.defineProperty(page,'lines',{get:()=>projectOutline(projects,state)});
  Object.defineProperty(page,'detailSessions',{get:()=>worktreeSessionLines(projects[0],projects[0].roots[0],state)});
  const original=page.lines.find(row=>row.kind==='tab'), key=original.key;
  // Hold on to this one callback as ArkUI does for the reusable native row. Rebuilding it on each tap hides the bug.
  const click=()=>page.toggleTab(key);
  const verify=(open,panes)=>{
    assert.equal(page.tabOpen(key),open);
    assert.equal(page.lines.filter(row=>row.kind==='session').length,panes);
    assert.ok(page.tabDescription(key).endsWith(open?'agent_collapse':'agent_expand'));
  };
  verify(true,2);
  for (let i=0;i<2;i++){click();verify(false,0);click();verify(true,2);}
  assert.equal(original.open,true);
  click();verify(false,0);
  state.query='release';assert.equal(page.tabOpen(key),true);assert.equal(page.lines.filter(row=>row.kind==='session').length,2);
  click();assert.equal(received.length,5);assert.ok(!page.tabDescription(key).includes('agent_collapse'));
  state.query='';verify(false,0);click();verify(true,2);
  page.page='project_tree';click();assert.equal(page.detailSessions.filter(row=>row.kind==='session').length,0);
  page.page='projects';projects=[];click();assert.equal(received.length,7);assert.equal(page.tabDescription(key),'');
  const builder=source.slice(source.indexOf('  @Builder tabLine('),source.indexOf('  @Builder counts('));
  assert.equal((builder.match(/\.id\('projects\.tab'\)/g)||[]).length,1);
  assert.match(builder,/onClick\(\(\) => this\.toggleTab\(key\)\)/);
  assert.match(builder,/rotate\(\{angle:this\.tabOpen\(key\)/);
});
