import test from 'node:test';
import assert from 'node:assert/strict';
import { registerHooks, stripTypeScriptTypes } from 'node:module';
import { readFileSync, existsSync } from 'node:fs';

registerHooks({
  resolve(specifier, context, next) {
    if (specifier.startsWith('@kit.')) return {url:'client-boundary:'+specifier,shortCircuit:true};
    if (context.parentURL?.includes('/entry/src/main/ets/') && specifier.startsWith('.')) {
      const base = new URL(specifier + (specifier.endsWith('.ts') || specifier.endsWith('.ets') ? '' : '.ets'), context.parentURL);
      const url = existsSync(base) ? base : new URL(base.href.replace(/\.ets$/, '.ts'));
      return {url: url.href, shortCircuit: true};
    }
    return next(specifier, context);
  },
  load(url, context, next) {
    if (url.startsWith('client-boundary:')) return {format:'module',shortCircuit:true,source:`
      export const common = {};
      export const bundleManager = {getBundleInfoForSelf(){return {name:'com.example.persona'};}};
      export const cert = {};
      export const cryptoFramework = {};
      export const deviceInfo = {};
      export const hilog = {debug(){},info(){},warn(){},error(){}};
      export const i18n = {getSystemLanguage(){return 'zh-CN';}};
      export const rcp = {};
      export const socket = {};
      export const connection = {};
      export const relationalStore = {};
      export const util = {TextEncoder:class {static create(){return new this();}encodeInto(value){return new TextEncoder().encode(value);}},generateRandomUUID:()=> 'synthetic-id'};
      export const tr = key => key;
      export const url = {URL:{parseURL:value=>new URL(value)}};
      export const segment = encodeURIComponent;
      export const withSourceContext = text => text;
      export const messageDisplay = text => text;
      export const nativeNotifications = {async clear(){},features(){return ['elicitation'];}};
    `};
    if (url.endsWith('.ets')) return {format:'module', shortCircuit:true, source:stripTypeScriptTypes(readFileSync(new URL(url),'utf8').replace(/@ObservedV2\s*/g,'').replace(/@Trace\s+/g,''))};
    return next(url, context);
  },
});

const {TranscriptModel} = await import('../entry/src/main/ets/models/TranscriptModel.ets');

function fixture() {
  const calls = [];
  const sent = [];
  const gate = {};
  gate.promise = new Promise((resolve, reject) => { gate.resolve = resolve; gate.reject = reject; });
  const repository = {
    async call(path) {
      calls.push(path);
      if (path.includes('after_seq=')) {
        const after = Number(new URLSearchParams(path.split('?')[1]).get('after_seq'));
        return {items:[{id:`${after + 1}#0`,seq:after + 1,role:'assistant',kind:'text',text:'filled'}],history:'h',from_seq:after,to_seq:after + 1};
      }
      return gate.promise;
    },
    transport: {send(text) { sent.push(JSON.parse(text)); }},
  };
  const model = new TranscriptModel({machine:'machine-one',session:'session-one',id:'session-one'}, repository,
    {async get(){return JSON.stringify({items:[{id:'1#0',seq:1,role:'assistant',kind:'text',text:'cached'}],covered:1,history:'h',hasMore:false,loadedOnce:true});},async put(){return true;}}, 'test', () => {}, error => { throw error; });
  return {model,calls,sent,gate};
}

test('a gap heard during a pending latest page is fetched after it completes', async () => {
  const {model,calls,gate} = fixture();
  model.reducer.restore({items:[{id:'1#0',seq:1,role:'assistant',kind:'text',text:'cached'}],covered:1,history:'h',hasMore:false,loadedOnce:true});
  const initial = model.latest();
  model.receive({type:'items',machine_id:'machine-one',session_id:'session-one',items:[{id:'4#0',seq:4,role:'assistant',kind:'text',text:'later'}],history:'h',from_seq:3,to_seq:4});
  gate.resolve({items:[{id:'1#0',seq:1,role:'assistant',kind:'text',text:'first'}],history:'h',to_seq:1,has_more:false});
  await initial;
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(calls.some(path => path.includes('after_seq=')));
});

test('a failed latest page releases the fill gate for a later gap', async () => {
  const {model,calls,gate} = fixture();
  model.reducer.restore({items:[{id:'1#0',seq:1,role:'assistant',kind:'text',text:'cached'}],covered:1,history:'h',hasMore:false,loadedOnce:true});
  const initial = model.latest();
  gate.reject(new Error('synthetic offline'));
  await assert.rejects(initial);
  model.receive({type:'items',machine_id:'machine-one',session_id:'session-one',items:[{id:'4#0',seq:4,role:'assistant',kind:'text',text:'later'}],history:'h',from_seq:3,to_seq:4});
  await new Promise(resolve => setImmediate(resolve));
  assert.ok(calls.some(path => path.includes('after_seq=')));
});

function historyPage(history, sequences, hasMore = false) {
  return {
    items: sequences.map(seq => ({id: `${seq}#0`, seq, role: 'user', kind: 'text', text: `${history} ${seq}`})),
    history, to_seq: sequences.at(-1), has_more: hasMore,
  };
}

function historyFixture(call) {
  const visible = [], snapshots = [], errors = [];
  const model = new TranscriptModel({machine:'machine-one', session:'session-one'},
    {call, transport:{send(){}}},
    {async get(){return '';}, async put(_key, value){snapshots.push(JSON.parse(value));}},
    'history-test', (items, hasMore, ready) => visible.push({items, hasMore, ready}), error => errors.push(error.message));
  return {model, visible, snapshots, errors};
}

test('an older page from a rewritten transcript discards the superseded cache even if the latest read fails', async () => {
  const old = historyPage('old', [5, 6], true), replacement = historyPage('new', [1, 2, 3]);
  let reads = 0;
  const {model, visible, snapshots, errors} = historyFixture(async path => {
    if (path.includes('before_seq=5')) return historyPage('new', [1, 2]);
    if (++reads === 1) return old;
    if (reads === 2) throw new Error('replacement temporarily unavailable');
    return replacement;
  });
  await model.open();
  await model.older();
  assert.deepEqual(visible.at(-1), {items: [], hasMore: false, ready: false});
  assert.equal(snapshots.at(-1).history, 'new');
  assert.deepEqual(snapshots.at(-1).items, []);
  assert.equal(snapshots.at(-1).covered, 0);
  assert.deepEqual(errors, ['replacement temporarily unavailable']);
  await model.latest();
  assert.deepEqual(visible.at(-1).items, replacement.items);
  assert.equal(snapshots.at(-1).covered, 3);
});

for (const read of ['latest', 'older', 'fill', 'fold']) {
  test(`a pending ${read} response cannot restore the old transcript after a replacement push`, async () => {
    const old = historyPage('old', [5, 6], true), replacement = historyPage('new', [1, 2, 3]);
    const fold = {id:'fold:2#0', seq:2, kind:'fold', fold:{through_seq:4, through_id:'4#0', tools:1, thoughts:0}};
    if (read === 'fold') {
      old.items.unshift(fold);
      replacement.items.splice(1, 1, fold);
    }
    let release, reads = 0;
    const pending = new Promise(resolve => {release = resolve;});
    const {model, visible, snapshots} = historyFixture(async () => {
      if (++reads === 1) return old;
      return reads === 2 ? pending : replacement;
    });
    await model.open();
    const target = {type:'items', machine_id:'machine-one', session_id:'session-one'};
    const start = {
      latest: () => model.latest(), older: () => model.older(),
      fill: () => model.receive({...target, ...historyPage('old', [9]), from_seq:8}),
      fold: () => model.unfold(fold.id),
    };
    const late = {
      latest: old, older: historyPage('old', [1, 2]), fill: {...historyPage('old', [7, 8]), from_seq:6},
      fold: {items:[{id:'2#0', seq:2, kind:'tool_use', tool_use_id:'old-call'}], more:false},
    };
    const request = start[read]();
    assert.equal(reads, 2);
    model.receive({...target, ...replacement, from_seq:0});
    const firstReplacement = visible.length - 1;
    release(late[read]);
    await request;
    await new Promise(resolve => setImmediate(resolve));
    for (const state of visible.slice(firstReplacement)) assert.deepEqual(state.items, replacement.items);
    for (const snapshot of snapshots.slice(firstReplacement)) {
      assert.equal(snapshot.history, 'new');
      assert.equal(snapshot.covered, 3);
      assert.deepEqual(snapshot.items, replacement.items);
    }
  });
}

test('an older page without a history identifier can still extend a legacy transcript', async () => {
  const recent = historyPage('legacy', [3, 4], true), older = historyPage('legacy', [1, 2]);
  delete recent.history; delete older.history;
  const {model, snapshots} = historyFixture(async path => path.includes('before_seq=3') ? older : recent);
  await model.open();
  await model.older();
  assert.deepEqual(snapshots.at(-1).items, [...older.items, ...recent.items]);
  assert.equal(snapshots.at(-1).hasMore, false);
});

test('a replacement that needs catch-up can fetch its tail while the old fill is still in flight', async () => {
  let release;
  const oldFill = new Promise(resolve => {release = resolve;});
  const {model, snapshots} = historyFixture(async path => {
    if (path.includes('after_seq=6')) return oldFill;
    if (path.includes('after_seq=1')) return {...historyPage('new', [2, 3]), from_seq:1};
    return historyPage('old', [5, 6], true);
  });
  await model.open();
  const target = {type:'items', machine_id:'machine-one', session_id:'session-one'};
  model.receive({...target, ...historyPage('old', [9]), from_seq:8});
  model.receive({...target, ...historyPage('new', [1]), from_seq:0, more_after:true});
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(snapshots.at(-1).covered, 3);
  release({...historyPage('old', [7, 8]), from_seq:6});
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(snapshots.at(-1).items, historyPage('new', [1, 2, 3]).items);
});

test('an in-flight latest page matching the replacement push can finish the open without another read', async () => {
  const replacement = historyPage('current', [1, 2]);
  let allowPage, started, reads = 0;
  const pending = new Promise(resolve => {allowPage = resolve;});
  const requested = new Promise(resolve => {started = resolve;});
  const {model, snapshots} = historyFixture(async () => {reads++; started(); return pending;});
  const opening = model.open();
  await requested;
  model.receive({...replacement, type:'items', machine_id:'machine-one', session_id:'session-one', from_seq:0});
  allowPage(replacement);
  await opening;
  assert.equal(reads, 1);
  assert.deepEqual(snapshots.at(-1).items, replacement.items);
});

test('live reply frames replace in place, stay out of saved history and hand off to the durable reply', () => {
  const seen=[], stored=[];
  const model=new TranscriptModel({ machine:'phone',session:'one',kind:'session' }, { transport:{ send() {} } },
    { async put(_key,value) { stored.push(JSON.parse(value)); } }, 'scope', ()=>{}, err=>{ throw err; }, text=>seen.push(text));
  const target={machine_id:'phone',session_id:'one'};
  const delta=text=>model.receive({...target,type:'delta',text});
  const push=(seq,item)=>model.receive({...target,type:'items',history:'h',from_seq:seq-1,to_seq:seq,items:[{id:String(seq)+'#0',seq,...item}]});
  push(1,{role:'user',kind:'text',text:'Please check'});
  model.receive({...target,type:'delta',session_id:'wrong',text:'foreign'}); delta('first'); delta('first'); delta('first line');
  assert.deepEqual(seen,['first','first line']); assert.equal(stored.length,1);
  delta(''); assert.equal(seen.at(-1),'first line');
  push(2,{role:'assistant',kind:'text',text:'first line'}); assert.equal(seen.at(-1),'');
  const originalItems = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(model.reducer),'items').get;
  Object.defineProperty(model.reducer,'items',{configurable:true,get(){throw new Error('copied raw history on a text-only delta');}});
  try { delta('first line'); assert.equal(seen.at(-1),''); }
  finally { Object.defineProperty(model.reducer,'items',{configurable:true,get:originalItems}); }
  assert.deepEqual(stored.at(-1).items.map(i=>i.text),['Please check','first line']);
  delta('second reply'); push(3,{kind:'turn_duration',role:'system'}); assert.equal(seen.at(-1),'');
  delta('first line'); assert.equal(seen.at(-1),'');
  delta('next'); model.close(); assert.equal(seen.at(-1),'');
  delta('too late'); assert.equal(seen.at(-1),'');
});

const planHeadline = {done:1, total:2, current:'Checking', sourceId:'', complete:false, steps:[]};
const planCall = {id:'plan-use',seq:1,kind:'tool_use',tool_name:'TodoWrite',tool_use_id:'p',tool_input:{todos:[
  {content:'Read logs',status:'completed'},{content:'Check output',activeForm:'Checking',status:'in_progress'},
]}};
const planReceipt = {id:'plan-result',seq:2,kind:'tool_result',tool_use_id:'p',text:'accepted'};

test('expanding a summary recovers calls and results across pages without moving chat history or opening its folds', async () => {
  const requests = [], current = historyPage('history', [9], true);
  current.items = [{id:'fold:9',seq:9,kind:'fold',fold:{through_seq:10,tools:2}}];
  const {model, visible, snapshots} = historyFixture(async path => {
    requests.push(path);
    if (path.includes('after_seq=0')) return {history:'history',items:[planCall],from_seq:0,to_seq:1,more_after:true};
    if (path.includes('after_seq=1')) return {history:'history',items:[planReceipt],from_seq:1,to_seq:9};
    return current;
  });
  await model.open();
  const before = model.reducer.snapshot(), updates = visible.length, cached = snapshots.length;
  const plan = await model.readPlanDetails(planHeadline, () => true);
  assert.deepEqual(plan.steps.map(s => s.status), ['completed','in_progress']);
  assert.equal(plan.current, 'Checking');
  assert.deepEqual(model.reducer.snapshot(), before);
  assert.equal(visible.length, updates);
  assert.equal(snapshots.length, cached);
  assert.equal(requests.filter(p => p.includes('after_seq=')).length, 2);
  assert.ok(requests.slice(-2).every(p => p.includes('lazy_images=1') && !p.includes('fold_work')));
  assert.equal(await model.readPlanDetails({...planHeadline,current:'A newer step'}, () => true), undefined);
});

test('a collapsed, superseded or incomplete plan read cannot present stale steps', async () => {
  for (const ending of ['collapsed','rewritten','stalled','scope closed']) {
    let release, open = true, reads = 0;
    const pending = new Promise(resolve => { release = resolve; });
    const {model} = historyFixture(async path => {
      if (!path.includes('after_seq=')) return historyPage('history', [9], true);
      reads++;
      return pending;
    });
    await model.open();
    const reading = model.readPlanDetails(planHeadline, () => open);
    assert.equal(reads, 1);
    if (ending === 'collapsed') open = false;
    if (ending === 'scope closed') model.close();
    release({history:ending === 'rewritten' ? 'replacement' : 'history',items:[planCall,planReceipt],
      from_seq:0,to_seq:ending === 'stalled' ? 0 : 2,more_after:ending === 'stalled'});
    assert.equal(await reading, undefined, ending);
    assert.equal(reads, 1);
  }
});

test('native history reads bounded pages with public progress paragraphs intact and keeps images lazy', async () => {
  const requests = [], messages = [], initial = historyPage('history',[3,4],true);
  const model = new TranscriptModel({machine:'m',session:'s'},
    {async call(path){requests.push(new URL(path,'http://local').searchParams);return path.includes('before_seq')?historyPage('history',[1,2]):initial;},
      transport:{send(text){messages.push(JSON.parse(text));}}},
    {async get(){return '';},async put(){}},'history',()=>{},e=>{throw e;});
  await model.open(); await model.older();
  assert.equal(requests.length,2);
  assert.equal(requests[0].get('limit'),'80');assert.equal(requests[0].has('fold_work'),false);
  assert.equal(requests[1].get('turns'),'3');assert.equal(requests[1].get('fold_work'),'1');
  for (const page of requests) assert.equal(page.get('lazy_images'),'1');
  assert.ok(messages.some(m=>m.type==='subscribe'&&!m.fold_work));
});

test('live tools are scoped snapshots, independent of streamed prose, cursors and offline cache', () => {
  const changes = [], cache = [], previews = [];
  const model = new TranscriptModel({machine:'m',session:'s'}, {transport:{send(){}}},
    {async put(_key,value){cache.push(JSON.parse(value));}}, 'test', () => {}, assert.fail,
    text => previews.push(text), () => changes.push(model.liveTools.map(tool => tool.name)));
  const push = (tools, extra={}) => model.receive({type:'live_tools',machine_id:'m',session_id:'s',tools,...extra});
  const one = {id:'c1', name:'functions.exec'};
  push([one], {machine_id:'other'}); push([one], {session_id:'other'}); push(null);
  assert.deepEqual(changes, []);
  push([one,one,{name:'malformed'}]); push([one]);
  assert.deepEqual(changes, [['functions.exec']]);
  model.receive({type:'delta',machine_id:'m',session_id:'s',text:'Streaming'});
  model.receive({type:'delta',machine_id:'m',session_id:'s',text:''});
  push([{id:'c2',name:'Read'},one]); push([]);
  assert.deepEqual(previews,['Streaming']);
  assert.deepEqual(changes,[['functions.exec'],['Read','functions.exec'],[]]);
  assert.equal(cache.length,0);
  push([one]); model.receive({type:'items',machine_id:'m',session_id:'s',items:[],from_seq:0,to_seq:0,history:'first'});
  assert.deepEqual(cache.at(-1).items, []);
  assert.equal(model.liveTools.length,1);
  model.receive({type:'items_replace',machine_id:'m',session_id:'s',items:[],from_seq:0,to_seq:0,history:'replacement'});
  assert.deepEqual(model.liveTools, []);
  push([one]);model.clearLive();assert.deepEqual(model.liveTools,[]);
  push([one]);model.close();push([one]);assert.deepEqual(model.liveTools,[]);
});

test('public live summaries, provider thinking and active-only frames clear on the owning turn or disconnect', () => {
  const updates = [], writes = [];
  const model = new TranscriptModel({machine:'m',session:'s'}, {transport:{send(){}}}, {async put(k,v){writes.push(v);}},
    'key', () => {}, assert.fail, () => {}, () => updates.push([model.liveSummary,model.liveThinking,model.liveActive]));
  const send = (type,data) => model.receive({type,machine_id:'m',session_id:'s',...data});
  send('items',{items:[{id:'u#0',seq:1,kind:'text',role:'user',text:'check'}],history:'h',from_seq:0,to_seq:1});
  send('live_tools',{tools:[],summary:'Checking tests',active:true});
  send('live_tools',{tools:[],summary:'Checking tests',active:true});
  assert.deepEqual(updates,[['Checking tests',false,true]]);
  send('live',{live:{verb:'private token',thinking:true,tokens_down:900}});
  assert.deepEqual(updates.at(-1),['Checking tests',true,true]);
  send('live',{live:null});assert.deepEqual(updates.at(-1),['Checking tests',false,true]);
  send('live_tools',{tools:[],thinking:true,active:true});
  assert.deepEqual(updates.at(-1),['',true,true]);
  send('items',{items:[{id:'e',seq:2,kind:'turn_duration',turn_duration:{user_item_id:'u#0',duration_ms:2}}],history:'h',from_seq:1,to_seq:2});
  assert.deepEqual(updates.at(-1),['',false,false]);
  assert.ok(writes.every(snapshot => !snapshot.includes('Checking tests') && !snapshot.includes('private token')));
  send('live',{live:{thinking:true}});assert.deepEqual(updates.at(-1),['',true,false]);
  send('live_tools',{tools:[]});assert.deepEqual(updates.at(-1),['',false,false]);
});

test('provider retry progress only reads a valid current attempt and resets on recovery, without mutating transport status', () => {
  const observed = [];
  const model = new TranscriptModel({machine:'m',session:'s'}, {transport:{send(){}}},
    {async put(){}}, 'key', () => {}, assert.fail, () => {}, () => observed.push(model.liveRetryCount));
  const frame = extra => model.receive({type:'live_tools',machine_id:'m',session_id:'s',tools:[],active:true,...extra});
  frame({retry_attempt:2,retry_max:5});
  assert.equal(model.liveRetryCount,'2/5');
  model.receive({type:'live',machine_id:'m',session_id:'s',live:{thinking:false}});
  assert.equal(model.liveRetryCount,'2/5');
  frame({retry_attempt:3,retry_max:5});assert.equal(model.liveRetryCount,'3/5');
  frame({summary:'Checking results'});assert.equal(model.liveRetryCount,'');assert.equal(model.liveSummary,'Checking results');
  for (const pair of [[2,undefined],[6,5],[-1,5],[2.5,5],['2',5]]) {
    frame({retry_attempt:pair[0],retry_max:pair[1]});assert.equal(model.liveRetryCount,'');
  }
  frame({compacting:true});assert.equal(model.liveCompacting,true);
  model.clearLive();assert.equal(model.liveCompacting,false);
  model.receive({type:'live',machine_id:'m',session_id:'s',live:{verb:'Compacting'}});
  assert.equal(model.liveCompacting,true);
  model.receive({type:'live',machine_id:'m',session_id:'s',live:null});assert.equal(model.liveCompacting,false);
});

test('a warmed previous page stays invisible and is shared by simultaneous reads',async()=>{
  let release;const queries=[];const {model,visible}=historyFixture(async path=>{
    if(!path.includes('before_seq='))return historyPage('abc',[40,41],true);
    queries.push(path);return new Promise(r=>{release=r});
  });
  await model.open();const count=visible.length;
  model.prefetchOlder(40);model.prefetchOlder(40);assert.equal(queries.length,1);
  assert.match(queries[0],/turns=3/);assert.match(queries[0],/fold_work=1/);
  assert.equal(visible.length,count);
  const a=model.older(), b=model.older();assert.equal(queries.length,1);
  release(historyPage('abc',[10,11]));await Promise.all([a,b]);
  assert.deepEqual(visible.at(-1).items.map(i=>i.seq),[10,11,40,41]);
});
test('failed speculative history is retried on demand without displaying an error before a tap',async()=>{
  let calls=0;const {model,errors}=historyFixture(async path=>{
    if(!path.includes('before_seq='))return historyPage('h',[3],true);
    if(++calls===1)throw Error('temporary');
    return historyPage('h',[1,2]);
  });
  await model.open();model.prefetchOlder(3);await new Promise(r=>setImmediate(r));
  model.prefetchOlder(3);assert.equal(calls,1);assert.deepEqual(errors,[]);
  await model.older();assert.equal(calls,2);assert.equal(model.reducer.firstSeq,1);
});

test('a history scroll records pushes immediately and publishes one fresh snapshot on release', async()=>{
 const {model,visible,snapshots}=historyFixture(async()=>historyPage('history',[1]));
 await model.open();const n=visible.length,k=snapshots.length;
 const release=model.holdHistoryRendering(),end=model.holdHistoryRendering();
 for(const seq of [2,3,4]) model.receive({type:'items',machine_id:'machine-one',session_id:'session-one',
   ...historyPage('history',[seq]),from_seq:seq-1,to_seq:seq});
 assert.equal(model.reducer.covered,4);assert.equal(visible.length,n);release();release();assert.equal(visible.length,n);
 end();assert.equal(visible.length,n+1);assert.equal(snapshots.length,k+1);assert.equal(visible.at(-1).items.at(-1).seq,4);
});

test('unchanged latest pages reuse published records, same-id corrections update both view and disk', async()=>{
 let page=historyPage('history',[1,2]);const {model,visible,snapshots}=historyFixture(async()=>page);
 await model.open();const prior=visible.at(-1).items,count=snapshots.length;
 await model.latest();assert.equal(visible.at(-1).items,prior);assert.equal(snapshots.length,count);
 page=historyPage('history',[1,2]);page.items[1].text='corrected';
 await model.latest();assert.notEqual(visible.at(-1).items,prior);assert.equal(snapshots.at(-1).items[1].text,'corrected');
});

test('the activity reader loads history without publishing seen, replacing a chat subscription or writing its cache', async () => {
  const requests = [], sent = [], writes = [], snapshots = [];
  const latest = historyPage('quiet-history',[8,9],true);
  const older = historyPage('quiet-history',[6,7],false);
  const model = new TranscriptModel({machine:'mac', session:'main'},
    {async call(path) { requests.push(path); return path.includes('before_seq=') ? older : latest; },
      transport:{send(payload) { sent.push(payload); }}},
    {async get(){return '';},async put(key){writes.push(key);}}, 'same-as-visible-chat',
    (items,more)=>snapshots.push({ids:items.map(item=>item.id),more}),error=>assert.fail(error), undefined, undefined, false);
  await model.open();
  await model.older();
  model.close();
  assert.equal(requests.length,2);
  assert.ok(requests.every(path=>path.startsWith('/api/v1/sessions/mac/main/items?')));
  assert.ok(requests[1].includes('before_seq='));
  assert.equal(snapshots.at(-1).ids.length,4);
  assert.deepEqual(sent,[]);
  assert.deepEqual(writes,[]);
});
