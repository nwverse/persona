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
    'relatedReadingPosition', 'restoreRelationAnchor', 'reachedLatest', 'followReading', 'jumpToLatest', 'visibleReadingAnchor', 'loadEarlier', 'feedContextChanged'].map(method).join('\n') + '}')
)(class Cache {}, Frame, { Bottom: 1 }, { START: 1 }, { vp: value => value });
function fixture() {
  const v = new Reading(), frames = [], scrolls = [], readKeys = [];
  Object.assign(v, { takeReturnAnchor:()=>undefined, active: true, hiddenProcessAnchors: new Map(), readingKey: '', readingEpoch: 0,
    readingMotion: 0, inspectionRevision: 0, followEpoch: -1, lines: [], feed: { error: '', nextCursor: '' },
    model: { selected: { kind: 'session' }, scope: () => 'owner', target: () => 'chat', hasMoreMessages: false,
      connected: true, captureView: key => { readKeys.push(key); return {read: async () => '', valid: () => true}; } },
    scroller: { scrollEdge: () => scrolls.push('bottom'), scrollBy: () => {} },
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
test('tapping older history reveals a newly inserted entry below the leading rows, which stay when history ends',async()=>{
 const {v,calls,flush}=manualFixture();await v.loadEarlier();assert.deepEqual(calls,[]);await flush();assert.deepEqual(calls,[[2,false,1,{extraOffset:-0}]]);assert.equal(v.readingLoading,false);
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
  assert.deepEqual(passive.calls,[[3,false,1,{extraOffset:-48}]]);
  const {v,calls,flush}=manualFixture();let pages=0;
  v.model.oldestTranscriptSeq=100;
  v.model.loadOlder=async()=>{
    v.model.oldestTranscriptSeq-=20;
    if(++pages===3) v.lines.unshift({id:'new-prompt',readingId:'q',message:{role:'user'}},{id:'new-answer',readingId:'a'});
    return true;
  };
  await v.loadEarlier();await flush();
  assert.equal(pages,3);assert.deepEqual(calls, [[2,false,1,{extraOffset:-0}]]);
});
test('unchanged cursor stops the pager and leaves a retryable status',async()=>{
  const {v,flush}=manualFixture();let calls=0;v.model.loadOlder=async()=>{calls++;return true;};
  await v.loadEarlier();await flush();assert.equal(calls,1);assert.equal(v.earlierFailed,true);
});

test('returning from a linked chat restores the stable message and viewport offset after paging', async () => {
  const {v, calls, flush} = manualFixture();
  v.model.transcriptHistoryReady = true;
  v.model.oldestTranscriptSeq = 40;
  v.scroller.isAtEnd = () => true;
  v.relationAnchor = {id:'message20', offset:-18};
  const page = v.model.loadOlder;
  v.model.loadOlder = async () => { await page(); v.model.oldestTranscriptSeq = 20; return true; };
  v.syncTimeline = () => v.restoreRelationAnchor();
  v.restoreRelationAnchor();
  v.reachedLatest();
  assert.equal(v.atEnd, false);
  await new Promise(resolve => setImmediate(resolve));
  await flush();
  assert.deepEqual(calls, [[2,false,1,{extraOffset:18}]]);
  assert.equal(v.relationAnchor, undefined);
  assert.equal(v.atEnd, false);
});

test('tapping latest supersedes a queued return to a related-chat reading anchor', async () => {
  const {v,calls,scrolls,flush} = manualFixture();
  v.model.transcriptHistoryReady = true;
  v.relationAnchor = {id:'message40', offset:-8};
  v.restoreRelationAnchor();
  v.jumpToLatest();
  await flush();
  assert.deepEqual(calls, []);
  assert.equal(v.atEnd, true);
  assert.equal(scrolls.at(-1), 'bottom');
});

test('returning to an expanded process restores its tool line and offset without paging through history', async () => {
  const {v,calls,flush,scrolls}=manualFixture();
  const result={id:'result',itemId:'result',processIds:['tool'],leadIds:['tool']};
  const rows=[{readingId:'message:tool',message:{id:'tool',itemId:'tool',work:{}}},
    {readingId:'message:result',message:result}];
  const Project=new Function('ConversationLine','return '+stripTypeScriptTypes('class Project {'+method('processLines')+'}'))(
    class { constructor(id){ this.id=id; this.readingId=id; } });
  const project=()=>Project.prototype.processLines.call(v,rows);
  v.model.messages=rows.map(row=>row.message);
  v.model.transcriptHistoryReady=true;
  v.model.loadOlder=async()=>assert.fail('the tool message is already loaded');
  v.openProcesses=['result']; v.expanded=['tool']; v.closedSoloGroups=[]; v.atEnd=false;
  v.lines=project(); v.firstVisibleIndex=v.readingPrefix()+v.lines.findIndex(row=>row.readingId==='message:tool');
  v.scroller.getItemRect=()=>({y:-17,height:100});
  const saved=v.relatedReadingPosition();
  v.openProcesses.length=0; v.expanded.length=0;
  v.readingKey=''; v.takeReturnAnchor=()=>saved;
  v.syncReading();
  v.lines=project();
  v.restoreRelationAnchor();
  await flush();
  assert.deepEqual(v.openProcesses,['result']);
  assert.deepEqual(v.expanded,['tool']);
  assert.deepEqual(calls, [[v.readingPrefix()+1,false,1,{extraOffset:17}]]);
  assert.deepEqual(scrolls,[]);
});

test('a tool line now folded into a process restores its visible header without loading more history', async () => {
  const {v,calls,flush}=manualFixture();
  v.model.transcriptHistoryReady=true;
  v.model.loadOlder=async()=>assert.fail('the header is already visible');
  v.hiddenProcessAnchors.set('message:tool','process:finished');
  v.lines=[{id:'process:finished',readingId:'process:finished'}];
  v.relationAnchor={id:'message:tool',offset:-90};
  v.restoreRelationAnchor(); await flush();
  assert.deepEqual(calls,[[v.readingPrefix(),false,1,{extraOffset:0}]]);
  assert.equal(v.atEnd,false);
});


test('a related return waits for history and corrects the measured drift after virtual rows are laid out', async () => {
  const {v,calls,flush}=manualFixture(), adjustments=[];
  v.model.transcriptHistoryReady=true;
  v.model.chatHistoryLoading=true;
  v.relationAnchor={id:'message40',offset:-80};
  v.scroller.getItemRect=()=>({height:460,y:-48});
  v.scroller.scrollBy=(x,y)=>adjustments.push([x,y]);
  v.restoreRelationAnchor(); await flush();
  assert.deepEqual(calls,[]);
  v.model.chatHistoryLoading=false;
  v.restoreRelationAnchor(); await flush();
  assert.equal(calls.length,1);
  assert.deepEqual(adjustments,[[0,32]]);
  assert.equal(v.relationAnchor,undefined);
});

test('a user motion between the initial lookup and the layout correction cancels the correction', async () => {
  const {v,flush}=manualFixture(), adjustments=[];
  v.model.transcriptHistoryReady=true;
  v.relationAnchor={id:'message40',offset:-45};
  v.scroller.scrollToIndex=()=>v.jumpToLatest();
  v.scroller.scrollBy=(x,y)=>adjustments.push([x,y]);
  v.restoreRelationAnchor(); await flush();
  assert.deepEqual(adjustments,[]);
  assert.equal(v.atEnd,true);
});

test('the leading rows keep their count when a notice or Load more comes and goes',()=>{
 const {v}=fixture();
 for(const [more,error,cursor] of [[false,'',''],[true,'failed','next'],[true,'','']]){
  v.model.hasMoreMessages=more;v.feed.error=error;v.feed.nextCursor=cursor;assert.equal(v.readingPrefix(),2);
 }
 v.model.selected={kind:'conversation'};assert.equal(v.readingPrefix(),3);
 const rows=sourceTextBetween('          ListItem() {\n            if (this.feed.error', ".id('chat.history.more')");
 assert.match(rows,/ListItem\(\) \{\n            if \(this.model.hasMoreMessages \|\| this.feed.nextCursor\) Row/);
});

test('rows added above keep the reader on the same row, and the latest is pinned in the same pass',()=>{
 const Place=new Function('Edge','ScrollAlign','LengthMetrics','return '+stripTypeScriptTypes('class Place {'+
   ['readingPrefix','visibleReadingAnchor','readingPlace','keepReadingPlace','pinLatest'].map(method).join('\n')+'}'))({Bottom:1},{START:1},{vp:n=>n});
 const v=new Place(),scrolls=[];
 Object.assign(v,{active:true,atEnd:false,inspectingWork:false,userScrolling:false,photoOpen:false,firstVisibleIndex:3,
   model:{selected:{kind:'session'}},lines:[{readingId:'a'},{readingId:'b'}],
   scroller:{getItemRect:()=>({y:-40,height:300}),scrollToIndex:(...args)=>scrolls.push(args),scrollEdge:edge=>scrolls.push(['edge',edge])}});
 const place=v.readingPlace();assert.deepEqual(place,{id:'b',offset:-40});
 v.relationAnchor={id:'b',offset:0};assert.equal(v.readingPlace(),undefined,'a linked-chat return keeps its own anchor');v.relationAnchor=undefined;
 v.lines.unshift({readingId:'older1'},{readingId:'older2'});
 v.keepReadingPlace(place,1);assert.deepEqual(scrolls,[[5,false,1,{extraOffset:40}]]);
 v.keepReadingPlace(place,3);assert.equal(scrolls.length,1,'a row that did not move is left alone');
 v.inspectingWork=true;assert.equal(v.readingPlace(),undefined,'work disclosure keeps its own anchor');v.inspectingWork=false;
 v.pinLatest();assert.equal(scrolls.length,1,'a reader away from the end is not moved');
 v.atEnd=true;assert.equal(v.readingPlace(),undefined);v.pinLatest();assert.deepEqual(scrolls.at(-1),['edge',1]);
 v.userScrolling=true;v.pinLatest();assert.equal(scrolls.length,2,'a gesture is never taken over');
});

test('a page read during a gesture lands when it settles instead of moving the rows under the finger',async()=>{
 const {v,flush,calls}=manualFixture();let synced=0;v.syncTimeline=()=>{synced++;};v.userScrolling=true;
 await v.loadEarlier(false);await flush();
 assert.equal(synced,0);assert.equal(v.timelinePending,true);assert.equal(v.earlierFailed,false);assert.deepEqual(calls,[]);
 const tapped=manualFixture();let shown=0;tapped.v.syncTimeline=()=>{shown++;};tapped.v.userScrolling=true;
 await tapped.v.loadEarlier();assert.equal(shown,1,'a tap on Load more shows the page at once');
});

test('the next page is read once near the top, again after a failed read, and on a pull at the very top',()=>{
 const Pager=new Function('historyRetryMs','return '+stripTypeScriptTypes('class Pager {'+
   ['readingPrefix','approachHistory','pullHistory','reachedEarliest','pageHistory'].map(method).join('\n')+'}'))(3000);
 const v=new Pager(),reads=[];
 Object.assign(v,{model:{selected:{kind:'session'},oldestTranscriptSeq:90,hasMoreMessages:true},feed:{},userScrolling:true,atEnd:false,
   readingLoading:false,autoPageCursor:-1,earlierFailed:false,earlierFailedAt:0,firstVisibleIndex:6,loadEarlier:manual=>reads.push(manual)});
 v.approachHistory(9);assert.deepEqual(reads,[],'moving toward the latest');
 v.approachHistory(5);assert.deepEqual(reads,[false]);
 v.firstVisibleIndex=5;v.approachHistory(4);assert.equal(reads.length,1,'one read per page');
 v.earlierFailed=true;v.earlierFailedAt=Date.now();v.firstVisibleIndex=1;v.pullHistory(-12);assert.equal(reads.length,1,'not straight after a failure');
 v.earlierFailedAt=Date.now()-3000;v.pullHistory(12);assert.equal(reads.length,1,'only a pull toward earlier rows');
 v.pullHistory(-12);assert.equal(reads.length,2,'a pull at the top reads the failed page again');
 v.earlierFailed=false;v.model.oldestTranscriptSeq=60;v.reachedEarliest();assert.equal(reads.length,3,'reaching the top reads the next page');
 v.userScrolling=false;v.model.oldestTranscriptSeq=30;v.reachedEarliest();assert.equal(reads.length,3,'the first layout at the top is not a pull');
});

test('an earlier page that cannot be read stays on the Load more row unless pairing or permission is lost',async()=>{
 const text=readFileSync(new URL('../entry/src/main/ets/models/ClientModel.ets',import.meta.url),'utf8');
 const start=text.indexOf('  async loadOlder(): Promise<boolean> {');assert.notEqual(start,-1);
 let depth=0,end=text.indexOf('{',start);for(;end<text.length;end++){if(text[end]==='{')depth++;else if(text[end]==='}'&&--depth===0)break;}
 class HttpFailure extends Error{constructor(status){super('failed');this.status=status;}}
 const Model=new Function('HttpFailure','return '+stripTypeScriptTypes('class Model {'+text.slice(start,end+1)+'}'))(HttpFailure);
 for(const [error,reported] of [[new Error('transport_failed_1007900028'),0],[new HttpFailure(504),0],[new HttpFailure(403),1],[new HttpFailure(401),1]]){
  const model=new Model(),notices=[];Object.assign(model,{transcript:{older:async()=>{throw error;}},failure:e=>notices.push(e)});
  assert.equal(await model.loadOlder(),false);assert.equal(notices.length,reported);
 }
 const label=method('historyLabel');assert.match(label,/'chat_history_failed'/);
});
