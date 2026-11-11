import assert from 'node:assert/strict';
import test from 'node:test';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync, readdirSync, existsSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier === '@kit.ArkTS') return { url: 'mock:arkts', shortCircuit: true };
    if (context.parentURL?.endsWith('/models/ExtensionUI.ets')) {
      if (specifier === './ClientModel') return { url: 'mock:client', shortCircuit: true };
      if (specifier === './HubRepository') return { url: 'mock:repository', shortCircuit: true };
      if (specifier === '../design/Strings') return { url: 'mock:strings', shortCircuit: true };
    }
    if (context.parentURL?.endsWith('.ets') && specifier.startsWith('.')) {
      const base = new URL(specifier, context.parentURL); return { url: base.href + (existsSync(new URL(base.href + '.ts')) ? '.ts' : '.ets'), shortCircuit: true };
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url === 'mock:arkts') return { format: 'module', source: "export const url = { URL: { parseURL: value => new URL(value) } }; export const util = { TextEncoder: { create: () => ({ encodeInto: value => new TextEncoder().encode(value) }) }, generateRandomUUID: () => 'id-' + Math.random().toString(16).slice(2) };", shortCircuit: true };
    if (url === 'mock:client') return { format: 'module', source: 'export class ClientModel {}', shortCircuit: true };
    if (url === 'mock:repository') return { format: 'module', source: 'export function segment(value) { return encodeURIComponent(value); }', shortCircuit: true };
    if (url === 'mock:strings') return { format: 'module', source: 'export function tr(value) { return value; }', shortCircuit: true };
    if (url.endsWith('.ets')) {
      const source = readFileSync(new URL(url), 'utf8').replace(/@ObservedV2\s*/g, '').replace(/@Trace\s+/g, '');
      return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
    }
    return next(url, context);
  }
});

const tree = await import('../entry/src/main/ets/core/ExtensionViewTree.ts');
const { ExtensionUIModel } = await import('../entry/src/main/ets/models/ExtensionUI.ets');

function viewWith(item) {
  return { sections: [{ key: 'section', items: [item] }] };
}

function streamSnapshot(view, version = 1, epoch = 'epoch-1') {
  return { protocol: 1, session: 'session-1', epoch, version, root_key: 'root', view };
}

function fixtureClient(options = {}) {
  const manager = options.manager || 'manager-1';
  const state = { handlers: [], sent: [], calls: [], connected: true, generation: 1, request: options.request || (async () => JSON.stringify({ view: { sections: [] } })) };
  const repository = {
    transport: {
      send(raw) { state.sent.push(JSON.parse(raw)); return true; },
      async request(path, method, body) { return state.request(path, method, body); },
      async requestBytes() { return { data: new ArrayBuffer(0), contentType: '' }; }
    },
    async call(path, method, body, op, admit) {
      state.calls.push({ path, method, body, op, admitted: admit ? admit() : true });
      return options.call ? options.call(path, method, body, op, admit) : {};
    }
  };
  const client = {
    profile: { id: 'profile-1', manager },
    get connected() { return state.connected; },
    features: options.features || [],
    connectionGeneration() { return state.generation; },
    scope() { return 'hub:' + manager; },
    owns(generation, scope) { return generation === state.generation && scope === 'hub:' + manager; },
    observeEvents(listener) { state.handlers.push(listener); return () => { state.handlers = state.handlers.filter((item) => item !== listener); }; },
    repo() { return repository; }
  };
  return { client, state, repository };
}

test('all showcase view fixtures remain readable inputs for the form/action adapters', () => {
  const directory = new URL('../../schema/extension/examples/showcase/', import.meta.url);
  const fixtures = readdirSync(directory).filter((name) => name.endsWith('.json'));
  assert.ok(fixtures.length >= 35);
  for (const name of fixtures) {
    const view = JSON.parse(readFileSync(new URL(name, directory), 'utf8'));
    assert.ok(Array.isArray(view.sections), name);
    assert.doesNotThrow(() => tree.extensionUIActionSlotsForView(view, 'root'), name);
    assert.doesNotThrow(() => tree.extensionUIFieldValues(view, new Map()), name);
  }
});

test('action slots use native callback events and hide unknown-node actions', () => {
  const view = {
    search: { action: { id: 'search' } },
    filter: { name: 'filter', action: { id: 'filter' } },
    tabs: { name: 'tab', action: { id: 'tab' } },
    sections: [{ items: [
      { type: 'button', key: 'button', action: { id: 'button' } },
      { type: 'comment_thread', key: 'discussion', reply: { id: 'reply' } },
      { type: 'diff', key: 'patch', comment: { id: 'comment' }, draft: { id: 'draft' }, context: { id: 'context' }, mark_viewed: { id: 'viewed' } },
      { type: 'not-supported', key: 'future', action: { id: 'unsafe' } }
    ] }]
  };
  const slots = tree.extensionUIActionSlotsForView(view, 'root');
  const callback = (id) => slots.find((slot) => slot.action.id === id);
  assert.deepEqual(['search', 'filter', 'tab'].map((id) => callback(id).event), ['change', 'change', 'change']);
  assert.equal(callback('reply').event, 'submit');
  assert.equal(callback('comment').event, 'submit');
  assert.equal(callback('draft').event, 'submit');
  assert.equal(callback('context').event, 'press');
  assert.equal(callback('viewed').event, 'press');
  assert.equal(callback('unsafe'), undefined);
});

test('field collection keeps intentional empty drafts and excludes secret fields', () => {
  const view = viewWith({ type: 'field', name: 'comment', label: 'Comment', kind: 'text' });
  view.sections[0].items.push({ type: 'field', key: 'secret-key', name: 'password', label: 'Password', kind: 'secret', value: 'never-prefill' });
  const values = tree.extensionUIFieldValues(view, new Map([['@item:0:0', ''], ['secret-key', 'secret-draft']]));
  assert.equal(values.comment, '');
  assert.equal(Object.hasOwn(values, 'password'), false);
});

test('stream reducer rejects version gaps and snapshots from a retired epoch', () => {
  const snapshot = streamSnapshot({ sections: [{ key: 'section', items: [{ key: 'text', type: 'text', text: 'hello' }] }] });
  const initial = tree.reduceExtensionUIStream(undefined, 'snapshot', snapshot);
  assert.equal(initial.recover, false);
  const gap = tree.reduceExtensionUIStream(initial.snapshot, 'delta', {
    protocol: 1, session: snapshot.session, epoch: snapshot.epoch, base_version: 0, version: 1, operations: []
  });
  assert.equal(gap.recover, true);
  const staleEpoch = tree.reduceExtensionUIStream(initial.snapshot, 'snapshot', streamSnapshot(snapshot.view, 2, 'old-epoch'), false);
  assert.equal(staleEpoch.recover, true);
});

test('hidden first mount waits, then suspends an open response that races with hiding', () => {
  const f = fixtureClient({ features: ['extension_ui_stream_v1'] });
  const model = new ExtensionUIModel();
  model.bind(f.client, 'demo', 'main', {}, undefined, false, () => {}, false);
  assert.equal(f.state.sent.length, 0);
  model.setVisible(true);
  const open = f.state.sent[f.state.sent.length - 1];
  const requestId = open.request.request_id;
  model.setVisible(false);
  model.receive(JSON.stringify({ type: 'extension_ui_stream', kind: 'snapshot', request_id: requestId,
    snapshot: streamSnapshot({ sections: [{ key: 'section', items: [{ key: 'label', type: 'text', text: 'hidden' }] }] }) }));
  assert.equal(f.state.sent[f.state.sent.length - 1].request.request, 'suspend');
  assert.equal(model.phase, 'suspended');
  model.dispose();
});

test('secret drafts stay local and are cleared when the page hides', () => {
  const f = fixtureClient();
  const model = new ExtensionUIModel();
  const view = { sections: [{ key: 'section', items: [
    { type: 'field', key: 'secret', name: 'password', label: 'Password', kind: 'secret' },
    { type: 'field', key: 'plain', name: 'note', label: 'Note', kind: 'text' }
  ] }] };
  model.bind(f.client, 'demo', 'main', {}, view, false, () => {}, true);
  model.setFieldValue('secret', 'in-memory-only', true);
  model.setFieldValue('plain', 'visible');
  assert.equal(model.fieldValues(false).password, undefined);
  assert.equal(model.fieldValues(true).password, 'in-memory-only');
  model.setVisible(false);
  assert.equal(model.fieldValues(true).password, undefined);
  model.dispose();
});

test('confirmed legacy action is rejected if its captured revision changes', async () => {
  const f = fixtureClient();
  const model = new ExtensionUIModel();
  const view = viewWith({ type: 'button', key: 'delete', title: 'Delete', action: { id: 'delete', confirm: 'Delete this item?' } });
  model.bind(f.client, 'demo', 'main', {}, view, false, () => {}, true);
  const pending = model.performAction('delete', 'delete', 'press', { __role: 'destructive' }, 'Delete');
  const token = model.confirmationToken;
  assert.ok(token);
  model.revision++;
  model.confirmAction(token, true);
  await assert.rejects(pending, /request_updated/);
  assert.equal(f.state.calls.length, 0);
  assert.equal(model.error, 'request_updated');
  model.dispose();
});

test('page push and action return are sent to the parent native navigation owner', async () => {
  const f = fixtureClient();
  const model = new ExtensionUIModel();
  const view = viewWith({ type: 'button', key: 'details', title: 'Details', action: { id: 'open', push: { ui: 'detail', state: '{"id":"42"}', title: 'Item 42' } } });
  const effects = [];
  model.bind(f.client, 'demo', 'main', { placement: 'extension_page' }, view, false, () => {}, true);
  model.setNavigationHandler((effect) => effects.push(effect));
  await model.performAction('details', 'open', 'press');
  assert.equal(effects[0].kind, 'push');
  assert.equal(effects[0].extensionId, 'demo');
  assert.equal(effects[0].uiId, 'detail');
  assert.equal(effects[0].state, '{"id":"42"}');
  assert.deepEqual(effects[0].context, { placement: 'extension_page' });
  model.popPage(true);
  assert.equal(effects[1].kind, 'pop');
  model.dispose();
});

test('ambiguous streamed actions stay locked and retry the same event only after its version is consumed', async () => {
  const originalTimeout = globalThis.setTimeout;
  globalThis.setTimeout = (() => 1);
  try {
    const f = fixtureClient({ features: ['extension_ui_stream_v1'] });
    const model = new ExtensionUIModel();
    const view = viewWith({ type: 'button', key: 'send', title: 'Send', action: { id: 'send' } });
    model.bind(f.client, 'demo', 'main', {}, view, false, () => {}, true);
    const page = model.stack[0];
    page.stream = true;
    page.snapshot = streamSnapshot(view, 1);
    page.view = view;
    model.view = view;
    model.phase = 'ready';
    await model.performAction('send', 'send', 'press');
    const first = f.state.sent[f.state.sent.length - 1].request;
    const interaction = JSON.stringify(first.interaction);
    const eventId = first.interaction.event_id;
    model.receive(JSON.stringify({ type: 'extension_ui_stream', kind: 'receipt', request_id: first.request_id,
      receipt: { session: 'session-1', epoch: 'epoch-1', event_id: eventId, status: 'ambiguous', version: 2 } }));
    assert.equal(model.busy, true);
    assert.equal(model.uncertainEventId, '');
    model.receive(JSON.stringify({ type: 'extension_ui_stream', kind: 'delta', delta: {
      protocol: 1, session: 'session-1', epoch: 'epoch-1', base_version: 1, version: 2, operations: []
    } }));
    assert.equal(model.busy, false);
    assert.equal(model.uncertainEventId, eventId);
    assert.equal(model.canInteract(), false);
    model.retryUncertainEvent(eventId);
    const replay = f.state.sent[f.state.sent.length - 1].request;
    assert.equal(replay.interaction.event_id, eventId);
    assert.equal(JSON.stringify(replay.interaction), interaction);
    assert.equal(model.busy, true);
    assert.equal(model.uncertainEventId, eventId);
    model.receive(JSON.stringify({ type: 'extension_ui_stream', kind: 'receipt', request_id: replay.request_id,
      receipt: { session: 'session-1', epoch: 'epoch-1', event_id: eventId, status: 'ambiguous', version: 2 } }));
    assert.equal(model.busy, false);
    assert.equal(model.uncertainEventId, eventId);
    model.retryUncertainEvent(eventId);
    assert.equal(model.busy, true);
    model.dispose();
  } finally { globalThis.setTimeout = originalTimeout; }
});

test('legacy refresh failure remains visibly successful after the action reply is acknowledged', async () => {
  const f = fixtureClient({ request: async () => { throw new Error('refresh_failed'); }, call: async () => ({}) });
  const model = new ExtensionUIModel();
  const view = viewWith({ type: 'button', key: 'save', title: 'Save', action: { id: 'save' } });
  model.bind(f.client, 'demo', 'main', {}, view, false, () => {}, true);
  await model.performAction('save', 'save', 'press');
  assert.equal(f.state.calls.length, 1);
  assert.equal(model.notice, 'extension_ui_action_completed');
  assert.equal(model.error, 'refresh_failed');
  model.dispose();
});


test('unrelated stream errors cannot fail another native extension page', () => {
  const f = fixtureClient(), model = new ExtensionUIModel();
  model.bind(f.client, 'extension', 'home', {}, { sections: [] });
  model.phase = 'ready'; model.receiveError({ error: { code: 'expired' } }, 'another-page-request');
  assert.equal(model.phase, 'ready'); assert.equal(model.error, ''); model.dispose();
});

test('same-owner reconnect adopts the new connection and resumes without replaying an action', async () => {
  const f = fixtureClient({ features: ['extension_ui_stream_v1'] }), model = new ExtensionUIModel();
  model.bind(f.client, 'extension', 'home');
  const page = model.currentPage(); page.stream = true; page.snapshot = streamSnapshot({ sections: [] });
  model.phase = 'ready'; f.state.generation++; model.connectionChanged();
  assert.equal(model.generation, f.state.generation); assert.equal(model.phase, 'recovering');
  assert.equal(f.state.sent.at(-1).request.request, 'resume');
  assert.equal(f.state.sent.filter(frame => frame.request.request === 'event').length, 0);
  model.dispose();
});


test('a same-key ordinary replacement cannot reveal an earlier secret draft', () => {
  const f = fixtureClient(), model = new ExtensionUIModel();
  model.bind(f.client, 'extension', 'home', {}, viewWith({type:'field',kind:'secret',key:'credential',name:'credential'}));
  model.setFieldValue('credential', 'private-value', true);
  assert.equal(model.fieldValue('credential'), 'private-value');
  const page = model.currentPage(); page.view = viewWith({type:'field',kind:'text',key:'credential',name:'credential'}); model.publishPage(page, true);
  assert.equal(model.fieldValue('credential'), ''); assert.equal(model.secrets.has('credential'), false); model.dispose();
});


test('an unsupported initial component open falls back to a legacy read exactly once', async () => {
  const f = fixtureClient({ features: ['extension_ui_stream_v1'] }), model = new ExtensionUIModel();
  let reads = 0; f.state.request = async () => { reads++; return JSON.stringify({ view: { title: 'Legacy page', sections: [] } }); };
  model.bind(f.client, 'extension', 'legacy'); const request = f.state.sent.at(-1).request;
  model.receiveError({error:{code:'rejected'}},request.request_id);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(reads,1); assert.equal(model.currentPage().preferLegacy,true); assert.equal(model.phase,'ready'); assert.equal(model.pageTitle,'Legacy page');
  model.receiveError({error:{code:'rejected'}},request.request_id); await Promise.resolve(); assert.equal(reads,1); model.dispose();
});

test('an initial authorization refusal never falls back to HTTP', async () => {
  const f = fixtureClient({ features: ['extension_ui_stream_v1'] }), model = new ExtensionUIModel(); let reads=0;
  f.state.request = async () => { reads++; return '{}'; };
  model.bind(f.client,'extension','private'); const request=f.state.sent.at(-1).request;
  model.receiveError({error:{code:'forbidden'}},request.request_id); await Promise.resolve();
  assert.equal(reads,0); assert.equal(model.phase,'failed'); model.dispose(); assert.equal(f.state.handlers.length,0);
});


test('stream delta shape and escaped version numbers reject invalid precision',()=>{
 const initial=tree.reduceExtensionUIStream(undefined,'snapshot',streamSnapshot({sections:[]})).snapshot;
 for(const operations of [undefined,{}]){const delta={protocol:1,session:initial.session,epoch:initial.epoch,base_version:1,version:2};if(operations!==undefined)delta.operations=operations;assert.equal(tree.reduceExtensionUIStream(initial,'delta',delta).recover,true);}
 assert.throws(()=>tree.parseExtensionUIFrame('{"type":"extension_ui_stream","snapshot":{"\\u0076ersion":9007199254740991.1}}'));
 const fresh=tree.reduceExtensionUIStream(initial,'snapshot',streamSnapshot({sections:[]},1,'new-epoch'),true);assert.equal(fresh.recover,false);assert.equal(fresh.snapshot.epoch,'new-epoch');
});

test('repeated identical route binding keeps the latest server state',()=>{
 const f=fixtureClient(),model=new ExtensionUIModel();model.bind(f.client,'extension','home',{}, {state:'latest',sections:[]},false,()=>{},true,'initial');
 const page=model.currentPage();model.bind(f.client,'extension','home',{},undefined,false,()=>{},true,'initial');assert.equal(model.currentPage(),page);assert.equal(model.pageState(),'latest');model.dispose();
});

test('the first multichoice toggle preserves server preselection',()=>{
 const f=fixtureClient(),model=new ExtensionUIModel();model.bind(f.client,'extension','home',{},viewWith({type:'field',kind:'multichoice',key:'choices',name:'choices',value:['a','b']}));
 assert.deepEqual(JSON.parse(model.toggleValue('choices','a')),['b']);assert.deepEqual(JSON.parse(model.toggleValue('choices','c')),['b','c']);model.dispose();
});


test('legacy timed refresh and targeted invalidation keep state and drafts, and pause offscreen',async()=>{
 const originalTimeout=globalThis.setTimeout,originalClear=globalThis.clearTimeout,originalNow=Date.now;let now=1000,next=0;const timers=new Map();
 globalThis.setTimeout=(callback,delay)=>{const id=++next;timers.set(id,{callback:()=>{timers.delete(id);callback();},delay});return id;};globalThis.clearTimeout=id=>timers.delete(id);Date.now=()=>now;
 const f=fixtureClient();let reads=0;f.state.request=async(_path,_method,body)=>{reads++;if(reads>1)assert.equal(JSON.parse(body).state,'checkpoint');return JSON.stringify({view:{state:'checkpoint',refresh_after:30,sections:[{items:[{type:'field',key:'draft',name:'draft',value:'server'}]}]}});};
 const model=new ExtensionUIModel();
 try{
  model.bind(f.client,'extension','home');await new Promise(resolve=>setImmediate(resolve));model.setFieldValue('draft','Local edit');
  assert.equal(reads,1);assert.equal([...timers.values()][0].delay,30000);now+=30000;[...timers.values()][0].callback();await new Promise(resolve=>setImmediate(resolve));assert.equal(reads,2);assert.equal(model.fieldValue('draft'),'Local edit');
  model.setVisible(false);assert.equal(timers.size,0);model.receive(JSON.stringify({type:'extension_ui',extension:'other',invalidate:true}));assert.equal(model.currentPage().dirty,false);
  model.receive(JSON.stringify({type:'extension_ui',extension:'extension',id:'other',invalidate:true}));assert.equal(model.currentPage().dirty,false);
  model.receive(JSON.stringify({type:'extension_ui',extension:'extension',id:'home',invalidate:true}));assert.equal(model.currentPage().dirty,true);assert.equal(reads,2);
  model.setVisible(true);await new Promise(resolve=>setImmediate(resolve));assert.equal(reads,3);assert.equal(model.fieldValue('draft'),'Local edit');assert.equal(model.currentPage().dirty,false);
 }finally{model.dispose();globalThis.setTimeout=originalTimeout;globalThis.clearTimeout=originalClear;Date.now=originalNow;}
});

test('legacy line replies correlate native code context by page, component, path and side',async()=>{
 const f=fixtureClient({call:async()=>({lines:{path:'file.ts',side:'old',start:2,lines:['hidden line'],total:10}})}),model=new ExtensionUIModel();
 model.bind(f.client,'extension','home',{},viewWith({type:'code',key:'code',path:'file.ts',context:{id:'context'}}));
 await model.performAction('code','context','press',{path:'file.ts',side:'old',from:2,to:2});
 assert.deepEqual(model.lineResultFor('code').lines,['hidden line']);assert.equal(model.lineResultFor('code').side,'old');assert.deepEqual(model.lineResultFor('another'),{});
 model.lineResultJSON='';model.handleLegacyResult(model.currentPage(),{lines:{path:'file.ts',side:'new',start:2,lines:['wrong side']}},'code',{path:'file.ts',side:'old'});
 assert.deepEqual(model.lineResultFor('code'),{});model.dispose();
});


test('a failed timed legacy refresh stops instead of retrying an expired deadline in a loop',async()=>{
 const oldTimeout=globalThis.setTimeout,oldClear=globalThis.clearTimeout;let next=0,reads=0;const timers=new Map();
 globalThis.setTimeout=(callback,delay)=>{const id=++next;timers.set(id,{callback:()=>{timers.delete(id);callback();},delay});return id;};globalThis.clearTimeout=id=>timers.delete(id);
 const f=fixtureClient({request:async()=>{reads++;throw Error('offline');}}),model=new ExtensionUIModel();
 try{model.bind(f.client,'extension','home',{}, {refresh_after:30,sections:[]});assert.equal(timers.size,1);[...timers.values()][0].callback();await new Promise(resolve=>setImmediate(resolve));assert.equal(reads,1);assert.equal(model.phase,'failed');assert.equal(timers.size,0);assert.equal(model.currentPage().refreshDeadline,0);}finally{model.dispose();globalThis.setTimeout=oldTimeout;globalThis.clearTimeout=oldClear;}
});


test('HTTP code context accepts its larger character-based budget and omitted requested side',async()=>{
 for(const lines of [Array.from({length:700},()=> 'x'.repeat(100)),['汉'.repeat(10000)],['🟢'.repeat(12000)]]){
  const f=fixtureClient({call:async()=>({lines:{path:'file.ts',start:2,lines,total:20000}})}),model=new ExtensionUIModel();model.bind(f.client,'extension','home',{},viewWith({type:'code',key:'code',path:'file.ts',context:{id:'context'}}));
  await model.performAction('code','context','press',{path:'file.ts',side:'old',from:2,to:'end'});
  assert.deepEqual(model.lineResultFor('code').lines,lines);assert.equal(model.lineResultFor('code').side,'old');model.dispose();
 }
});

test('streamed code context retains the smaller result budget',()=>{
 const f=fixtureClient(),model=new ExtensionUIModel();model.bind(f.client,'extension','home',{},viewWith({type:'code',key:'code',path:'file.ts',context:{id:'context'}}));
 model.storeLineResult(model.currentPage(),'code','file.ts','new',{path:'file.ts',side:'new',start:1,lines:Array.from({length:700},()=> 'x'.repeat(100))},true);
 assert.deepEqual(model.lineResultFor('code'),{});model.dispose();
});


test('native control confirmation stays pending until cancel or the actual HTTP acknowledgement',async()=>{
 const f=fixtureClient(),model=new ExtensionUIModel(),view=viewWith({type:'button',key:'volume',action:{id:'volume',confirm:'Change volume?'}});
 model.bind(f.client,'extension','home',{},view);let settled=false;
 const cancelled=model.performAction('volume','volume').then(()=>{settled=true;},error=>{settled=true;throw error;});
 await Promise.resolve();assert.equal(settled,false);model.confirmAction(model.confirmationToken,false);await assert.rejects(cancelled,/cancelled/);assert.equal(f.state.calls.length,0);
 let finish;f.repository.call=async()=>new Promise(resolve=>{finish=resolve;});settled=false;
 const accepted=model.performAction('volume','volume').then(()=>{settled=true;});model.confirmAction(model.confirmationToken,true);
 await Promise.resolve();assert.equal(settled,false);finish({toast:'Saved'});await accepted;assert.equal(settled,true);model.dispose();
});
