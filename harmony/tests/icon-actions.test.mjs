import test from 'node:test';
import fs from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';

const w=fileURLToPath(new URL('../entry/src/main/ets',import.meta.url));

test('action icons distinguish approvals, uncertain writes and the operation that is actually sending',()=>{
const inbox=fs.readFileSync(w+'/components/InboxWorkspacePage.ets','utf8');
function method(src,name){
 const hit=src.indexOf('private '+name+'(): string {');assert(hit>=0,name);const start=src.indexOf('{',hit);let level=0,quote='',escaped=false;
 for(let i=start;i<src.length;i++){const ch=src[i];if(quote){if(escaped)escaped=false;else if(ch==='\\')escaped=true;else if(ch===quote)quote='';continue;}if(ch==='"'||ch==="'"){quote=ch;continue;}if(ch==='{')level++;if(ch==='}'&&--level===0)return new Function(src.slice(start+1,i));}throw new Error(name);
}

const context=state=>({workspace:{writeState:'',attachmentBusy:false,handoffBusy:false,mailComposeState:'',mailComposeBusy:false,...state}});
const cases=[
 ['conversationSendKey',{},'send'],['conversationSendKey',{writeState:'sending'},'sending'],
 ['conversationSendKey',{writeState:'unknown'},'result_unknown'],['conversationSendKey',{writeState:'pending'},'pending'],
 ['conversationSendKey',{writeState:'sending',attachmentBusy:true},'send'],
 ['attachmentSendKey',{writeState:'sending',attachmentBusy:false},'inbox_send_attachment'],
 ['attachmentSendKey',{writeState:'sending',attachmentBusy:true},'sending'],
 ['mailSendKey',{mailComposeState:'sending',mailComposeBusy:false},'sending'],
 ['mailSendKey',{mailComposeState:'draft',mailComposeBusy:true,mailComposeReady:false},'loading'],
 ['mailSendKey',{mailComposeState:'draft',mailComposeBusy:true,mailComposeReady:true},'sending'],
 ['mailSendKey',{mailComposeState:'unknown'},'result_unknown'],['mailSendKey',{mailComposeState:'pending'},'pending'],
 ['handoffSendKey',{writeState:'sending',handoffBusy:false},'inbox_handoff_send'],
 ['handoffSendKey',{writeState:'sending',handoffBusy:true},'sending'],
 ['handoffSendKey',{writeState:'unknown'},'result_unknown']
];for(const [name,state,expected]of cases)assert.equal(method(inbox,name).call(context(state)),expected,name+JSON.stringify(state));
let icons=fs.readFileSync(w+'/design/IconActions.ets','utf8').replace(/^import .*\n/gm,'').replace('export class','class').replace(/: (?:string|boolean|ResourceColor|Resource)\b/g,'');
const IconActions=vm.runInNewContext(icons+'; IconActions',{$r:s=>s,Colors:{onAction:'onAction',danger:'danger',link:'link'}});
for(const [key,glyph] of [['assistant_feed_start','play_fill'],['send','arrow_up'],['assistant_detail_retry','arrow_clockwise'],['approve','checkmark_shield'],['deny','nosign'],['confirm','checkmark'],['inbox_send_attachment','doc_text_badge_arrow_up'],['result_unknown','exclamationmark_circle']])assert.equal(IconActions.symbol(key),'sys.symbol.'+glyph,key);
assert.equal(IconActions.busy('unknown'),false);assert.equal(IconActions.busy('result_unknown'),false);
const decision=fs.readFileSync(w+'/components/InteractionDecisionForm.ets','utf8');
for(const kind of ['permission','plan'])for(const selected of ['approve','deny',''])assert.equal(method(decision,'submitIconKey').call({request:{kind},selected}),selected||'confirm');
assert.equal(method(decision,'submitIconKey').call({request:{kind:'screen'},selected:'deny'}),'confirm');

});


test('every quick filter keeps its complete localized accessible label and applies in one tap on narrow or magnified screens',()=>{
 const source=fs.readFileSync(w+'/components/AgentListPage.ets','utf8');
 const filters=source.slice(source.indexOf('  @Builder filters()'),source.indexOf('  @Builder renameForm()'));
 const keys=source.match(/const QUICK_FILTERS: string\[\] = \[([^\]]+)\]/)[1].match(/'([^']+)'/g);
 const labels=source.match(/const QUICK_LABELS: string\[\] = \[([^\]]+)\]/)[1].match(/'([^']+)'/g);
 assert.deepEqual(keys,["'all'","'needsYou'","'unread'","'working'","'backgroundWaiting'"]);
 assert.deepEqual(labels,["'all'","'needs_you'","'agent_group_unread'","'working'","'agent_group_background'"]);
 assert.match(filters,/ForEach\(QUICK_FILTERS,[\s\S]*Button\(\{ type: ButtonType.Capsule \}\)[\s\S]*Text\(tr\(QUICK_LABELS\[index\]\)/);
 assert.match(filters,/accessibilityText\(tr\(QUICK_LABELS\[index\]\)\)/);
 assert.match(filters,/accessibilityDescription\(Wire.number\(this.workspace.filterCounts,key\)/);
 assert.match(filters,/accessibilitySelected\(this\.workspace\.quickFilter === key\)/);
 assert.match(filters,/responseRegion\([\s\S]*Math\.max\(44, [^)]*this\.environment\.fontScale/);
 assert.match(filters,/onClick\([\s\S]*this\.workspace\.setQuickFilter\(key\)[\s\S]*\.id\('agents\.quick\.' \+ key\)/);
 assert.match(filters,/\.scrollable\(ScrollDirection\.Horizontal\)/);
 assert.ok(filters.indexOf('this.filterControl()')>filters.indexOf('.scrollable(ScrollDirection.Horizontal)'));
});

test('magnified project and Agent views keep symbols inside their slots and give titles and fields room to grow', () => {
 const read = name => fs.readFileSync(w + '/' + name + '.ets', 'utf8');
 const projects = read('components/ProjectsPage'), agents = read('components/AgentListPage');
 const picker = read('components/AgentStartPicker'), phone = read('design/PhoneStyle');
 const index = read('pages/Index');
 const launch = index.slice(index.indexOf('  @Builder agentOptionRow('), index.indexOf('  private async loadAgentLaunchers'));
 for (const source of [phone, projects, agents, picker, launch]) {
   assert.doesNotMatch(source, /SymbolGlyph\([^\n]*?\)\s*\.fontSize\(\d+\)/, 'a fixed symbol must use vp independent of system text scaling');
 }
 assert.match(projects, /nativeEnvironment\.fontScale >= 1\.5[\s\S]{0,120}Column\(\{space:3\}\)[\s\S]{0,110}Text\(line\.tree!\.title\)/);
 assert.match(projects, /GridRow\(\{columns:nativeEnvironment\.fontScale >= 1\.5 \? 1 : 2/);
 assert.match(agents, /Text\(tr\(QUICK_LABELS\[index\]\)\)\.fontSize\(14\)\.maxFontScale\(Tokens\.navigationTextMaxScale\)/);
 assert.match(picker, /TextInput\(\{text:this\.address[^\n]+\.height\(Math\.max\(46, 22 \* nativeEnvironment\.fontScale \+ 14\)\)/);
 assert.match(launch, /width\(this\.environment\.fontScale >= 1\.5 \? Math\.min\(250,130 \* this\.environment\.fontScale\) : 122\)/);
 assert.match(launch, /this\.machineChoice\(m\)[\s\S]{0,160}scrollable\(ScrollDirection\.Horizontal\)/);
 assert.match(launch, /Text\(tr\('new_agent'\)\)\.fontSize\(18\)\.maxFontScale\(Tokens\.navigationTextMaxScale\)/);
});
