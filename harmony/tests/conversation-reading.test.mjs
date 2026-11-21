import test from 'node:test';
import assert from 'node:assert/strict';
import { parseReadingAnchor, readingAnchorIndex } from '../entry/src/main/ets/core/ConversationReading.ts';

test('reading returns to the same message after older and newer messages are inserted',()=>{
 const anchor=parseReadingAnchor(JSON.stringify({id:'read-message',offset:-18,atEnd:false}));
 assert.equal(readingAnchorIndex(['older','before','read-message','after','newest'],anchor),2);
 assert.equal(anchor.offset,-18);assert.equal(anchor.atEnd,false);
});
test('missing history does not silently turn a saved reading position into newest',()=>{
 const anchor=parseReadingAnchor('{"id":"older-message","offset":0,"atEnd":false}');
 assert.equal(readingAnchorIndex(['recent'],anchor),-1);assert.equal(anchor.atEnd,false);
});
test('unusable saved reading positions fall back to newest without admitting invalid offsets',()=>{
 for(const value of ['', '{}','null','{"id":"a","offset":"3","atEnd":false}'])assert.deepEqual(parseReadingAnchor(value),{id:'',offset:0,atEnd:true});
});

import {readFileSync} from 'node:fs';
import {stripTypeScriptTypes} from 'node:module';
const source=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
function method(name){
 const match=new RegExp(name==='feedContextChanged' ? 'feedContextChanged\\(\\): void' : '  private (?:async )?'+name+'\\(').exec(source);assert.ok(match,name);const start=match.index,body=source.indexOf('{',start);let depth=1,quote='',escape=false,i=body+1;
 for(;i<source.length&&depth;i++){const c=source[i];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}
 return source.slice(start,i);
}
class Frame {constructor(action){this.action=action;}onFrame(){this.action();}}
const Reading=new Function('parseReadingAnchor','readingAnchorIndex','ConversationFrame','Edge','ScrollAlign','LengthMetrics','return '+stripTypeScriptTypes('class Reading {'+['readingProgress','showWelcome','readingPrefix','saveReading','syncReading','scheduleReading','restoreReading','visibleReadingAnchor','loadEarlier','feedContextChanged'].map(method).join('\n')+'}'))(parseReadingAnchor,readingAnchorIndex,Frame,{Bottom:1},{START:1},{vp:v=>v});
function fixture(){let resolve;const read=new Promise(r=>resolve=r),frames=[],scrolls=[],writes=[];const v=new Reading();Object.assign(v,{active:true,readingKey:'',readingEpoch:0,readingRestoring:true,readingAnchorReady:false,readingFetching:false,readingCursors:new Set(),readingFailed:false,anchor:{id:'',offset:0,atEnd:true},lines:[],feed:{error:'',nextCursor:''},feedKey:'',model:{selected:{kind:'session'},scope:()=> 'owner',target:()=> 'chat',hasMoreMessages:false,transcriptHistoryReady:false,transcriptItems:()=>v.lines,connected:true,captureView:()=>({read:()=>read,valid:()=>true,write:async s=>writes.push(s)})},scroller:{scrollEdge:()=>scrolls.push('bottom'),scrollToIndex:i=>scrolls.push(i)},getUIContext:()=>({postFrameCallback:f=>frames.push(f)})});return {v,resolve,scrolls,writes,flush:async()=>{while(frames.length)frames.shift().onFrame();await Promise.resolve();}};}

test('earlier message frames cannot finalize reading before the saved anchor resolves',async()=>{
 const {v,resolve,scrolls,flush}=fixture();v.syncReading();v.lines=[{id:'old',readingId:'old'},{id:'saved',readingId:'saved'},{id:'latest',readingId:'latest'}];v.scheduleReading();await flush();assert.deepEqual(scrolls,[]);assert.equal(v.readingRestoring,true);
 resolve(JSON.stringify({id:'saved',offset:-12,atEnd:false}));await Promise.resolve();await flush();assert.deepEqual(scrolls,[1]);assert.equal(v.readingRestoring,false);assert.equal(v.anchor.offset,-12);
});
test('a bounded cached page cannot declare a deep reading anchor missing on reconnect',async()=>{
 const {v,resolve,scrolls,flush}=fixture();v.syncReading();resolve(JSON.stringify({id:'deep',offset:0,atEnd:false}));await Promise.resolve();v.lines=[{id:'recent',readingId:'recent'}];await flush();assert.deepEqual(scrolls,[]);assert.equal(v.readingRestoring,true);
 v.model.hasMoreMessages=true;v.model.transcriptHistoryReady=true;v.model.loadOlder=async()=>{v.lines=[{id:'deep',readingId:'deep'},{id:'recent',readingId:'recent'}];v.model.hasMoreMessages=false;};await v.restoreReading();await flush();assert.deepEqual(scrolls,[0]);assert.equal(v.anchor.id,'deep');assert.equal(v.atEnd,false);
});


test('assistant reading identity survives prepending the user at the start of its turn',()=>{
 const begin=source.indexOf('class ConversationLine {'),end=source.indexOf('\n}',begin)+2;
 const Line=new Function('return '+stripTypeScriptTypes(source.slice(begin,end)))();
 const initial=new Line('message:visible-assistant',{id:'visible-assistant',itemId:'assistant'});
 const paged=new Line('message:visible-user',{id:'visible-user',itemId:'assistant'});
 assert.notEqual(initial.id,paged.id);assert.equal(initial.readingId,paged.readingId);
 assert.equal(readingAnchorIndex([paged.readingId],{id:initial.readingId,offset:-10,atEnd:false}),0);
});
test('a retired pagination completion cannot block or unlock the replacement reading epoch',async()=>{
 const {v,resolve,flush}=fixture();let release;const older=new Promise(r=>release=r);v.model.hasMoreMessages=true;v.model.transcriptHistoryReady=true;v.model.loadOlder=()=>older;
 v.syncReading();resolve(JSON.stringify({id:'deep',offset:0,atEnd:false}));await Promise.resolve();await flush();assert.equal(v.readingFetching,true);
 v.model.target=()=> 'replacement';v.model.captureView=()=>({read:async()=>JSON.stringify({id:'replacement',offset:0,atEnd:false}),valid:()=>true});v.lines=[{id:'new-display',readingId:'replacement'}];v.model.hasMoreMessages=false;v.syncReading();await Promise.resolve();await flush();assert.equal(v.readingRestoring,false);assert.equal(v.anchor.id,'replacement');
 release(true);await Promise.resolve();await Promise.resolve();assert.equal(v.anchor.id,'replacement');assert.equal(v.readingFetching,false);
});


test('a cursor cycle stops restoration while preserving the saved anchor',async()=>{
 const {v,resolve,flush}=fixture();v.model.transcriptHistoryReady=true;v.feed.nextCursor='A';let reads=0;
 v.feed.loadMore=async()=>{reads++;v.feed.nextCursor=v.feed.nextCursor==='A'?'B':'A';return true;};
 v.syncReading();resolve(JSON.stringify({id:'absent',offset:0,atEnd:false}));await Promise.resolve();await flush();await flush();await flush();
 assert.equal(reads,2);assert.equal(v.readingFailed,true);assert.equal(v.anchor.id,'absent');assert.equal(v.readingRestoring,true);
});

function manualFixture(){
 const f=fixture(),{v}=f;let generation=1;Object.assign(v,{readingRestoring:false,readingLoading:false,readingMotion:0,firstVisibleIndex:0,readingKey:JSON.stringify(['owner','chat']),syncFeed:()=>{},syncAttachments:()=>{},syncRevision:()=>{},lines:[{id:'row40',readingId:'message40'}]});
 Object.assign(v.model,{hasMoreMessages:true,connectionGeneration:()=>generation,owns:g=>g===generation,loadOlder:async()=>{v.lines.unshift({id:'row20',readingId:'message20'});v.model.hasMoreMessages=false;return true;}});
 const calls=[];Object.assign(v.scroller,{getItemRect:()=>({y:48,height:120}),scrollToIndex:(...args)=>calls.push(args)});
 return {...f,calls,bump:()=>generation++};
}
test('explicit older loading preserves the visible content row when its first loading control disappears',async()=>{
 const {v,calls,flush}=manualFixture();await v.loadEarlier();assert.deepEqual(calls,[]);await flush();assert.deepEqual(calls,[[1,false,1,{extraOffset:-48}]]);assert.deepEqual(v.anchor,{id:'message40',offset:48,atEnd:false});assert.equal(v.readingLoading,false);
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
 for(const rect of [{y:48,height:0},{y:NaN,height:20}]){const f=manualFixture();f.v.scroller.getItemRect=()=>rect;await f.v.loadEarlier();await f.flush();assert.deepEqual(f.calls,[]);assert.equal(f.v.lines.length,2);}
 const missing=manualFixture();missing.v.model.loadOlder=async()=>{missing.v.lines=[];return true;};await missing.v.loadEarlier();await missing.flush();assert.deepEqual(missing.calls,[]);
});

test('a same-target directory refresh keeps manual restore, while hiding and returning retires it',async()=>{
 const stable=manualFixture();await stable.v.loadEarlier();stable.v.model.selected={kind:'session',id:'same-session'};stable.v.feedContextChanged();await stable.flush();assert.equal(stable.calls.length,1);
 const hidden=manualFixture();await hidden.v.loadEarlier();hidden.v.active=false;hidden.v.feedContextChanged();hidden.v.active=true;hidden.v.feedContextChanged();await hidden.flush();assert.deepEqual(hidden.calls,[]);
});

function hitView() {
 const View=new Function('ConversationFrame','ScrollAlign','Wire','ScrollSource','return '+stripTypeScriptTypes('class HitView {'+
   ['hitScope','findSearchLine','clearSearchHighlight','seekSearchHit','restoreReading','trackScrollMotion'].map(method).join('\n')+'}'))(Frame,{CENTER:2},{record:v=>v||{},text:(v,k)=>typeof v[k]==='string'?v[k]:''},{SCROLLER:6,SCROLLER_ANIMATION:7,EDGE_EFFECT:2});
 const frames=[],points=[];let owner='A',scope='hub';
 const v=Object.assign(new View(),{active:true,readingEpoch:1,readingMotion:0,searchHitId:'hit42',readingRestoring:true,searchedHit:'',seekingHit:'',searchHighlightTimer:-1,highlightedLine:'',lines:[],
   model:{target:()=>owner,scope:()=>scope,connectionGeneration:()=>3,connected:true,hasMoreMessages:false},getUIContext:()=>({postFrameCallback:f=>frames.push(f)}),
   scroller:{scrollToIndex:(i,animated,align)=>points.push([i,align])},readingPrefix:()=>1,scheduleReading:()=>{},syncTimeline:()=>{}});
 return {v,points,flush:()=>{while(frames.length)frames.shift().onFrame();},swap:()=>{scope='other';owner='B';}};
}
test('a search hit on an assistant item scrolls to its emitted timeline row and highlights it', async()=>{
 const {v,points,flush}=hitView();
 v.lines=[{id:'a',message:{id:'a'}},{id:'projected',message:{id:'visible-a',itemId:'hit42'}}];
 await v.seekSearchHit(v.hitScope());flush();
 assert.deepEqual(points,[[2,2]]);assert.equal(v.highlightedLine,'projected');assert.equal(v.readingRestoring,false);v.clearSearchHighlight();
});
test('a matching item inside a work fold highlights only its own conversation and stops after six earlier pages', async()=>{
 const {v,points,flush,swap}=hitView();let reads=0;
 v.model.hasMoreMessages=true;
 v.model.loadOlder=async()=>{reads++;return true;};
 await v.seekSearchHit(v.hitScope());flush();assert.equal(reads,6);assert.equal(points.length,0);
 v.lines=[{id:'work',message:{id:'w',work:'summary',raw:'[{"id":"hit42","kind":"tool_use"}]'}}];
 await v.seekSearchHit(v.hitScope());swap();flush();assert.equal(points.length,0);assert.equal(v.highlightedLine,'');
});
test('manual reading takes precedence over a search request waiting on older history', async()=>{
 const {v,points,flush}=hitView();
 const gate=Promise.withResolvers();v.model.hasMoreMessages=true;v.model.loadOlder=()=>gate.promise;
 const seeking=v.seekSearchHit(v.hitScope());v.trackScrollMotion(3,0);v.lines=[{id:'row',message:{id:'hit42'}}];gate.resolve(true);
 await seeking;flush();assert.equal(points.length,0);assert.equal(v.highlightedLine,'');assert.equal(v.readingRestoring,false);
});

test('an explicit hit still opens after a reading-position error and survives duplicate layout callbacks',async()=>{
 const {v,points,flush}=hitView();
 Object.assign(v,{readingAnchorReady:false,readingFailed:true});v.model.transcriptHistoryReady=true;
 v.lines=[{id:'visible',message:{id:'hit42'}}];
 const first=v.restoreReading();const second=v.restoreReading();
 v.trackScrollMotion(40,6);v.trackScrollMotion(40,7);v.trackScrollMotion(5,2);
 await Promise.all([first,second]);flush();
 assert.deepEqual(points,[[1,2]]);assert.equal(v.highlightedLine,'visible');v.clearSearchHighlight();
});

test('the latest arrow follows distance in an unchanged conversation and jumps without a new message', () => {
  const Jump = new Function('Edge', 'return ' + stripTypeScriptTypes('class Jump {' +
    ['updateJumpAvailability', 'jumpToLatest'].map(method).join('\n') + '}'))({ Bottom: 1 });
  const scrolled = [];
  let rect = { y: 20, height: 405 };
  const v = Object.assign(new Jump(), {
    atEnd: false, anchor: { atEnd: false }, lines: [{}, {}, {}, {}], transcriptHeight: 400,
    lastVisibleIndex: 2, readingMotion: 3, readingPrefix: () => 1,
    scroller: { getItemRect: index => { assert.equal(index, 4); return rect; }, scrollEdge: edge => scrolled.push(edge) }
  });
  v.updateJumpAvailability();
  assert.equal(v.jumpAvailable, true);
  v.lastVisibleIndex = 4;
  v.updateJumpAvailability();
  assert.equal(v.jumpAvailable, false);
  rect = { y: -10, height: 530 };
  v.updateJumpAvailability();
  assert.equal(v.jumpAvailable, true);
  v.jumpToLatest();
  assert.equal(v.jumpAvailable, false);
  assert.equal(v.atEnd, true);
  assert.equal(v.anchor.atEnd, true);
  assert.equal(v.readingMotion, 4);
  assert.deepEqual(scrolled, [1]);
});

test('a streaming reply can expose the latest button without scrolling or changing the last visible row', () => {
  const Timeline = new Function('ConversationFrame', 'Edge', 'return ' + stripTypeScriptTypes('class Timeline {' +
    ['timelinePositionChanged', 'updateJumpAvailability'].map(method).join('\n') + '}'))(Frame, { Bottom: 1 });
  const frames = [], scrolls = [];
  let height = 430;
  const view = Object.assign(new Timeline(), {
    active: true, atEnd: false, readingEpoch: 2, readingMotion: 1, readingRestoring: false, searchHitId: '',
    showWelcome: () => false, readingPrefix: () => 0, lines: [{}], transcriptHeight: 400, lastVisibleIndex: 0,
    getUIContext: () => ({ postFrameCallback:frame => frames.push(frame) }),
    scroller: { getItemRect: () => ({ y:0, height }), scrollEdge:edge => scrolls.push(edge) }
  });
  view.updateJumpAvailability();
  assert.equal(view.jumpAvailable,false);
  view.timelinePositionChanged();
  height=560;
  frames.shift().onFrame();
  assert.equal(view.jumpAvailable,true);
  assert.deepEqual(scrolls,[]);
  view.jumpAvailable=false;
  view.timelinePositionChanged();
  view.readingEpoch++;
  frames.shift().onFrame();
  assert.equal(view.jumpAvailable,false);
  view.atEnd=true;
  view.timelinePositionChanged();
  view.readingMotion++;
  frames.shift().onFrame();
  assert.deepEqual(scrolls,[]);
  view.timelinePositionChanged();
  frames.shift().onFrame();
  assert.deepEqual(scrolls,[1]);
});

test('keyboard resize remeasures the same last row and preserves readers who are at the end', () => {
  const Timeline = new Function('ConversationFrame','Edge','return '+stripTypeScriptTypes('class Timeline {'+
    ['transcriptViewportChanged','timelinePositionChanged','updateJumpAvailability'].map(method).join('\n')+'}'))(Frame,{Bottom:1});
  const frames=[],scrolls=[];
  const view=Object.assign(new Timeline(),{
    active:true,readingRestoring:false,readingEpoch:4,readingMotion:1,searchHitId:'',atEnd:false,
    lines:[{}],lastVisibleIndex:0,readingPrefix:()=>0,transcriptHeight:550,showWelcome:()=>false,
    scroller:{getItemRect:()=>({y:-200,height:800}),scrollEdge:edge=>scrolls.push(edge)},
    getUIContext:()=>({postFrameCallback:f=>frames.push(f)})
  });
  view.updateJumpAvailability();assert.equal(view.jumpAvailable,false);
  view.transcriptViewportChanged(360);frames.shift().onFrame();
  assert.equal(view.jumpAvailable,true);assert.deepEqual(scrolls,[]);
  view.transcriptViewportChanged(550);frames.shift().onFrame();
  assert.equal(view.jumpAvailable,false);
  view.transcriptViewportChanged(550);view.transcriptViewportChanged(0);view.transcriptViewportChanged(NaN);
  assert.equal(frames.length,0);assert.equal(view.transcriptHeight,550);
  view.atEnd=true;view.transcriptViewportChanged(360);frames.shift().onFrame();
  assert.deepEqual(scrolls,[1]);
});
