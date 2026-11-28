import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { sameMessageImageSource } from '../entry/src/main/ets/core/MessageImageGallery.ts';

const component = name => readFileSync(new URL('../entry/src/main/ets/components/' + name + '.ets',import.meta.url),'utf8');
const source = component('ConversationPhotoViewer');
const body = source.slice(source.indexOf('export struct ConversationPhotoViewer'),source.indexOf('  @Builder stage()'))
  .replace('export struct ConversationPhotoViewer','class Viewer')
  .replace(/@(?:Require|Param|Local|Event|Computed)\s+/g,'').replace(/@Monitor\([^)]*\)\s*/g,'');
function setup() {
  const loads=[], releases=[];
  const image={ createImageSource(data){return {
    getImageInfo:async()=>({size:{width:300,height:200}}),
    createPixelMap:async()=>({data,release:async()=>releases.push(data)}), release:async()=>{}
  };} };
  const Viewer=new Function('image','loadMessageImage','sameMessageImageSource','util',
    stripTypeScriptTypes(body+'\n}')+'; return Viewer;')(image, (_model,id)=>new Promise(resolve=>loads.push({id,resolve})), sameMessageImageSource,{Base64Helper:class { async encodeToString(bytes){return Buffer.from(bytes).toString('base64');} }});
  const v=new Viewer(),entries=['a','b','c'].map(id=>({key:id,itemId:id,remotePath:'',target:'chat'}));
  const model={connected:true,profile:{hubId:'hub'},target:()=> 'chat',scope:()=> 'scope',connectionGeneration:()=>1};
  v.model=model;v.request={scope:'scope',hub:'hub',gallery:{entries,selectedIndex:1,signature:'list'}};
  v.cached=()=>new Uint8Array([7]).buffer;v.onClose=()=>v.aboutToDisappear();
  return {v,model,loads,releases};
}
const frame=()=>new Promise(r=>setImmediate(r));
test('the overlay paints a thumbnail immediately, then only fetches the selected original',async()=>{
  const {v,loads}=setup();v.aboutToAppear();await frame();
  assert.equal(v.index,1);assert.ok(v.pixels);assert.equal(v.full,false);assert.deepEqual(loads.map(x=>x.id),['b']);
  v.select(2);await frame();assert.deepEqual(loads.map(x=>x.id),['b','c']);
  const old=new Uint8Array([1]).buffer, current=new Uint8Array([2]).buffer;
  loads[0].resolve({data:old});await frame();assert.notEqual(v.pixels.data,old);
  loads[1].resolve({data:current});await frame();assert.equal(v.pixels.data,current);assert.equal(v.full,true);
  v.aboutToDisappear();assert.equal(v.pixels,undefined);
});
test('offline photos stay in the chat, and leaving the owner closes the overlay',async()=>{
  const {v,model,loads}=setup();model.connected=false;v.aboutToAppear();await frame();
  assert.ok(v.pixels);assert.equal(v.full,false);assert.equal(loads.length,0);
  model.target=()=> 'other';v.ownerChanged();assert.equal(v.pixels,undefined);
});
test('opening a transcript photo uses a modal over the active conversation and both group titles fit one line',()=>{
  assert.doesNotMatch(component('TranscriptImage'),/previewMessageImageGallery|filePreview\.openPreview/);
  assert.match(component('Conversation'),/presentCustomDialog\(\(\) => this\.photoContent/);
  assert.doesNotMatch(component('Conversation'),/bindContentCover\(/);
  assert.match(component('ConversationWork'),/Text\(this\.group\.title\)[^\n]*\n\s*\.maxLines\(1\)\.textOverflow/);
  assert.match(component('RunningToolTitle'),/maxLines\(1\)\.textOverflow/);
});

test('returning from a photo preserves the reading position and ignores a retired dialog dismissal', () => {
  const chat = component('Conversation');
  const methods = chat.slice(chat.indexOf('  private finishPhoto('), chat.indexOf('  @Builder photoContent('));
  const Owner = new Function('return ' + stripTypeScriptTypes('class Owner {' + methods + '}'))();
  const owner = new Owner();
  let closed = 0;
  const first = { close() { closed++; } };
  const second = { close() { closed++; } };
  Object.assign(owner, { photoDialog:first, photoOpen:true, atEnd:true, unseenLatest:true });
  owner.retirePhoto();
  assert.equal(closed, 1);
  assert.equal(owner.photoOpen, false);
  assert.equal(owner.atEnd, false);
  owner.photoDialog = second;
  owner.photoOpen = true;
  owner.finishPhoto(first);
  assert.equal(owner.photoDialog, second);
  assert.equal(owner.photoOpen, true);
  owner.finishPhoto(second);
  assert.equal(owner.photoOpen, false);
});

test('the selected GIF keeps the encoded original for animation and releases it on selection and dismissal',async()=>{
  const {v,loads}=setup();v.aboutToAppear();await frame();
  const bytes=new Uint8Array([71,73,70,56,57,97]);
  loads[0].resolve({data:bytes.buffer,mime:'image/gif'});await frame();
  assert.equal(v.animationSource,'data:image/gif;base64,R0lGODlh');
  v.select(2);assert.equal(v.animationSource,'');await frame();
  loads[1].resolve({data:bytes.buffer,mime:'image/gif'});await frame();
  v.aboutToDisappear();assert.equal(v.animationSource,'');
});
