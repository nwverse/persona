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
 const match=new RegExp('  private (?:async )?'+name+'\\(').exec(source);assert.ok(match,name);const start=match.index,body=source.indexOf('{',start);let depth=1,quote='',escape=false,i=body+1;
 for(;i<source.length&&depth;i++){const c=source[i];if(quote){if(escape)escape=false;else if(c==='\\')escape=true;else if(c===quote)quote='';}else if(c==='"'||c==="'"||c==='`')quote=c;else if(c==='{')depth++;else if(c==='}')depth--;}
 return source.slice(start,i);
}
class Frame {constructor(action){this.action=action;}onFrame(){this.action();}}
const Reading=new Function('parseReadingAnchor','readingAnchorIndex','ConversationFrame','Edge','ScrollAlign','LengthMetrics','return '+stripTypeScriptTypes('class Reading {'+['readingProgress','readingPrefix','saveReading','syncReading','scheduleReading','restoreReading'].map(method).join('\n')+'}'))(parseReadingAnchor,readingAnchorIndex,Frame,{Bottom:1},{START:1},{vp:v=>v});
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
