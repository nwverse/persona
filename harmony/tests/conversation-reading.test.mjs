import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
const source=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
function method(name){
 const match=new RegExp(name==='feedContextChanged' ? 'feedContextChanged\\(\\): void' : '  private (?:async )?'+name+'\\(').exec(source);assert.ok(match,name);const start=match.index,body=source.indexOf('{',start);let depth=1,quote='',escape=false,i=body+1;
 for(;i<source.length&&depth;i++){const c=source[i];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}
 return source.slice(start,i);
}
function sourceTextBetween(start, end) {
 const at = source.indexOf(start); assert.notEqual(at,-1);
 const until = source.indexOf(end,at); assert.notEqual(until,-1); return source.slice(at,until);
}
class Frame {constructor(action){this.action=action;}onFrame(){this.action();}}
const Reading = new Function('ConversationMarkdownCache', 'ConversationFrame', 'Edge', 'ScrollAlign', 'LengthMetrics',
  'return ' + stripTypeScriptTypes('class Reading {' + ['readingPrefix', 'retirePhoto', 'finishPhoto', 'syncReading',
    'followReading', 'jumpToLatest', 'visibleReadingAnchor', 'loadEarlier', 'feedContextChanged'].map(method).join('\n') + '}')
)(class Cache {}, Frame, { Bottom: 1 }, { START: 1 }, { vp: value => value });
function fixture() {
  const v = new Reading(), frames = [], scrolls = [], readKeys = [];
  Object.assign(v, { active: true, hiddenProcessAnchors: new Map(), readingKey: '', readingEpoch: 0,
    readingMotion: 0, inspectionRevision: 0, followEpoch: -1, lines: [], feed: { error: '', nextCursor: '' },
    model: { selected: { kind: 'session' }, scope: () => 'owner', target: () => 'chat', hasMoreMessages: false,
      connected: true, captureView: key => { readKeys.push(key); return {read: async () => '', valid: () => true}; } },
    scroller: { scrollEdge: () => scrolls.push('bottom') },
    getUIContext: () => ({ postFrameCallback: frame => frames.push(frame) }) });
  return {v, scrolls, readKeys, flush: async () => { while (frames.length) frames.shift().onFrame(); await Promise.resolve(); }};
}

test('opening starts at the latest row without fetching a previous visit or waiting for history', async () => {
  const {v, readKeys, flush, scrolls} = fixture();
  v.atEnd = false;
  v.syncReading();
  await flush();
  assert.deepEqual(readKeys, ['next-step:chat']);
  assert.equal(v.atEnd, true);
  assert.deepEqual(scrolls, ['bottom']);
  v.lines = [{ id: 'latest' }];
  v.followReading(); v.followReading();
  await flush();
  assert.deepEqual(scrolls, ['bottom', 'bottom']);
});

test('reopening the same target follows again, a refresh in place preserves manual scrolling', async () => {
  const {v, flush, scrolls} = fixture();
  Object.assign(v, { syncFeed() {}, syncRevision() {}, syncAttachments() {} });
  v.syncReading(); await flush();
  v.atEnd = false;
  v.feedContextChanged();
  assert.equal(v.atEnd, false);
  v.active = false; v.feedContextChanged();
  v.active = true; v.feedContextChanged();
  await flush();
  assert.deepEqual(scrolls, ['bottom', 'bottom']);
  assert.equal(v.atEnd, true);
});

test('assistant reading identity survives prepending the user at the start of its turn',()=>{
 const begin=source.indexOf('class ConversationLine {'),end=source.indexOf('\n}',begin)+2;
 const Line=new Function('return '+stripTypeScriptTypes(source.slice(begin,end)))();
 const initial=new Line('message:visible-assistant',{id:'visible-assistant',itemId:'assistant'});
 const paged=new Line('message:visible-user',{id:'visible-user',itemId:'assistant'});
 assert.notEqual(initial.id,paged.id);assert.equal(initial.readingId,paged.readingId);
 assert.equal(paged.readingId, 'message:assistant');
});
function manualFixture(){
 const f=fixture(),{v}=f;let generation=1;Object.assign(v,{readingLoading:false,readingMotion:0,firstVisibleIndex:0,readingKey:JSON.stringify(['owner','chat']),syncTimeline:()=>{},syncFeed:()=>{},syncAttachments:()=>{},syncRevision:()=>{},lines:[{id:'row40',readingId:'message40'}]});
 Object.assign(v.model,{hasMoreMessages:true,connectionGeneration:()=>generation,owns:g=>g===generation,loadOlder:async()=>{v.lines.unshift({id:'row20',readingId:'message20'});v.model.hasMoreMessages=false;return true;}});
 const calls=[];Object.assign(v.scroller,{getItemRect:()=>({y:48,height:120}),scrollToIndex:(...args)=>calls.push(args)});
 return {...f,calls,bump:()=>generation++};
}
test('tapping older history reveals a newly inserted entry when the loading control disappears',async()=>{
 const {v,calls,flush}=manualFixture();await v.loadEarlier();assert.deepEqual(calls,[]);await flush();assert.deepEqual(calls,[[0,false,1,{extraOffset:-0}]]);assert.equal(v.readingLoading,false);
});
test('jumping back during a page load cancels its pending scroll and follows subsequent layout changes', async () => {
 const {v, calls, flush, scrolls} = manualFixture();
 await v.loadEarlier(); v.userScrolling = true; v.unseenLatest = true;
 v.jumpToLatest(); await flush();
 assert.deepEqual(calls, []);
 assert.equal(v.atEnd, true); assert.equal(v.userScrolling, false); assert.equal(v.unseenLatest, false);
 v.followReading(); await flush();
 assert.equal(scrolls.at(-1), 'bottom');
});

test('a later motion, owner or generation change revokes the queued native pagination restore',async()=>{
 for(const change of [v=>v.readingMotion++,v=>v.model={...v.model},v=>v.active=false,'generation']){const {v,calls,flush,bump}=manualFixture();await v.loadEarlier();if(change==='generation')bump();else change(v);await flush();assert.deepEqual(calls,[]);}
});
test('manual paging has one active run and paginates only sources advertising history',async()=>{
 const {v,flush,calls}=manualFixture();let release,reads=0;const pending=new Promise(r=>release=r);v.model.loadOlder=async()=>{reads++;await pending;return false;};v.feed.loadMore=async()=>{throw Error('no cursor');};
 const first=v.loadEarlier();await v.loadEarlier();assert.equal(reads,1);release();await first;await flush();assert.equal(calls.length,1);
});
test('a failed sibling still restores surviving content, while an unavailable anchor never causes a fallback fetch',async()=>{
 const {v,calls,flush}=manualFixture();v.feed.nextCursor='older';v.feed.loadMore=async()=>false;await v.loadEarlier();await flush();assert.equal(calls.length,1);
 for(const rect of [{y:48,height:0},{y:NaN,height:20}]){const f=manualFixture();f.v.scroller.getItemRect=()=>rect;await f.v.loadEarlier(false);await f.flush();assert.deepEqual(f.calls,[]);assert.equal(f.v.lines.length,2);}
 const missing=manualFixture();missing.v.model.loadOlder=async()=>{missing.v.lines=[];return true;};await missing.v.loadEarlier();await missing.flush();assert.deepEqual(missing.calls,[]);
});

test('a same-target directory refresh keeps manual restore, while hiding and returning retires it',async()=>{
 const stable=manualFixture();await stable.v.loadEarlier();stable.v.model.selected={kind:'session',id:'same-session'};stable.v.feedContextChanged();await stable.flush();assert.equal(stable.calls.length,1);
 const hidden=manualFixture();await hidden.v.loadEarlier();hidden.v.active=false;hidden.v.feedContextChanged();hidden.v.active=true;hidden.v.feedContextChanged();await hidden.flush();assert.deepEqual(hidden.calls,[]);assert.deepEqual(hidden.scrolls,['bottom']);
});

test('new content follows only the current conversation while the reader stays at the end', () => {
 const Follow = new Function('ConversationFrame', 'Edge',
   'return ' + stripTypeScriptTypes('class Follow {' + method('followReading') + '}'))(Frame, { Bottom: 1 });
 const view = new Follow(), frames = [], scrolls = [];
 Object.assign(view, { active: true, atEnd: false, readingEpoch: 1,
   getUIContext: () => ({ postFrameCallback: frame => frames.push(frame) }),
   scroller: { scrollEdge: edge => scrolls.push(edge) } });
 view.followReading();
 assert.equal(frames.length, 0);
 view.atEnd = true;
 view.followReading();
 view.atEnd = false;
 frames.shift().onFrame();
 view.atEnd = true;
 view.followReading();
 view.readingEpoch++;
 frames.shift().onFrame();
 assert.deepEqual(scrolls, []);
 view.followReading();
 frames.shift().onFrame();
 assert.deepEqual(scrolls, [1]);
});

test('manual scrolling immediately offers the jump, and a later update marks it as new', () => {
  const source = {DRAG:0, FLING:1, OTHER_USER_INPUT:3, SCROLL_BAR:4, SCROLL_BAR_FLING:5, SCROLLER:6};
  const Controller = new Function('ScrollSource', 'return ' + stripTypeScriptTypes('class Controller {' +
    ['userScroll','markLatest','reachedLatest'].map(method).join('') + '}'))(source);
  const view = new Controller(), row = {id:'m',readingId:'m'};
  Object.assign(view,{active:true,atEnd:true,userScrolling:false,unseenLatest:false,
    readingMotion:0,tailMarker:'',lines:[row],
    scroller:{isAtEnd:()=>false,getItemRect:()=>({y:15})},model:{messages:[{id:'m',text:'first'}]}});
  view.markLatest([row]);
  view.userScroll(-20,source.SCROLLER);
  assert.equal(view.atEnd,true,'relayout and programmatic offsets keep the tail attached');
  view.userScroll(-20,source.DRAG);view.markLatest([{id:'older'},row]);
  assert.equal(view.atEnd,false);assert.equal(view.unseenLatest,false,'prepending does not mark the latest as unread');
  const overlay = sourceTextBetween('if (!this.atEnd && this.lines.length > 0)', ".id('chat.latest')");
  assert.match(overlay, /ButtonType.Circle/);
  assert.doesNotMatch(overlay, /if \(this.unseenLatest/);
  view.model.messages[0].text+=' delta';view.markLatest([row]);
  assert.equal(view.unseenLatest,true,'a streamed extension of the same message counts');
  view.scroller.isAtEnd=()=>true;view.reachedLatest();assert.equal(view.atEnd,true);assert.equal(view.unseenLatest,false);
});

test('work disclosure restores only the selected chat even when two turns have the same id', async () => {
 const Work = new Function('Wire', 'return ' + stripTypeScriptTypes(
   'class Work {' + method('syncWork') + method('toggleWork') + '}'))({
   parse: JSON.parse, strings: (data, key) => data[key], payload: (keys, values) => ({ [keys[0]]: values[0] }),
 });
 const view = new Work(), reads = [], writes = [];
 let target = 'first';
 Object.assign(view, { active: true, model: { target: () => target, expandWork: () => {}, captureView: key => ({
   valid: () => true, read: () => new Promise(resolve => reads.push(resolve)),
   write: async value => { writes.push([key, JSON.parse(value)]); },
 }) } });
 const old = view.syncWork();
 target = 'second';
 const current = view.syncWork();
 reads[1]('');
 await current;
 view.toggleWork({ id: 'shared', ownerTarget: 'second' });
 reads[0](JSON.stringify({ expanded: ['previous-chat'] }));
 await old;
 view.toggleWork({ id: 'shared', ownerTarget: 'first' });
 assert.deepEqual(view.expanded, ['shared']);
 assert.deepEqual(writes, [['work:second', { expanded: ['shared'] }]]);
});

test('expanding a work detail holds the visible transcript anchor and yields to an intervening gesture', () => {
 const Hold=new Function('ConversationFrame','Tokens','ScrollAlign','LengthMetrics',
   'return '+stripTypeScriptTypes('class Hold {'+method('holdWorkReading')+'}'))(Frame,{hairline:1},{START:1},{vp:n=>n});
 const view=new Hold(),frames=[],scrolls=[];
 Object.assign(view,{active:true,readingEpoch:1,readingMotion:0,inspectionRevision:0,hiddenProcessAnchors:new Map(),lines:[{readingId:'keep'}],
   visibleReadingAnchor:()=>({id:'keep',offset:-10,atEnd:false}),readingPrefix:()=>0,
   getUIContext:()=>({postFrameCallback:frame=>frames.push(frame)}),
   scroller:{getItemRect:()=>({y:-70,height:120}),scrollToIndex:(...args)=>scrolls.push(args)}});
 let opened=false;
 view.holdWorkReading(()=>{opened=true;});
 assert.ok(opened&&view.inspectingWork);
 frames.shift().onFrame();
 assert.deepEqual(scrolls,[[0,false,1,{extraOffset:10}]]);
 view.holdWorkReading(()=>{});view.readingMotion++;frames.shift().onFrame();
 view.holdWorkReading(()=>{});view.readingEpoch++;frames.shift().onFrame();
 assert.equal(scrolls.length,1);assert.equal(view.inspectingWork,false);
});

test('scrolling through earlier turns coalesces incoming display updates and the visible tail stays live',()=>{
  const body=method('finishScroll');
  const text=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets', import.meta.url),'utf8');
  const start=text.indexOf('messagesChanged(): void {'), end=text.indexOf("\n  }",start)+4;
  const Controller=new Function('return '+stripTypeScriptTypes('class Controller {'+text.slice(start,end)+body+'}'))();
  const v=new Controller(), shown=[];
  Object.assign(v,{active:true,userScrolling:true,atEnd:false,lines:[{},{},{}],
    lastVisibleIndex:0,model:{selected:{kind:'session'}},readingPrefix:()=>0,readingEpoch:1,
    syncRevision(){},refreshFeed(){},followReading(){},reachedLatest(){},tailLayoutChanged(){},
    syncTimeline(){this.timelinePending=false;shown.push(this.model.frame);}});
  for(const frame of [1,2,3]){v.model.frame=frame;v.messagesChanged();}
  assert.deepEqual(shown,[]);v.finishScroll();assert.deepEqual(shown,[3]);
  v.userScrolling=true;v.lastVisibleIndex=2;v.model.frame=4;v.messagesChanged();assert.deepEqual(shown,[3,4]);
});

test('prefetched rows keep the old anchor while a manual tap reads through hidden pages to an earlier question',async()=>{
  const passive=manualFixture();await passive.v.loadEarlier(false);await passive.flush();
  assert.deepEqual(passive.calls,[[1,false,1,{extraOffset:-48}]]);
  const {v,calls,flush}=manualFixture();let pages=0;
  v.model.oldestTranscriptSeq=100;
  v.model.loadOlder=async()=>{
    v.model.oldestTranscriptSeq-=20;
    if(++pages===3) v.lines.unshift({id:'new-prompt',readingId:'q',message:{role:'user'}},{id:'new-answer',readingId:'a'});
    return true;
  };
  await v.loadEarlier();await flush();
  assert.equal(pages,3);assert.deepEqual(calls, [[1,false,1,{extraOffset:-0}]]);
});
test('unchanged cursor stops the pager and leaves a retryable status',async()=>{
  const {v,flush}=manualFixture();let calls=0;v.model.loadOlder=async()=>{calls++;return true;};
  await v.loadEarlier();await flush();assert.equal(calls,1);assert.equal(v.earlierFailed,true);
});
