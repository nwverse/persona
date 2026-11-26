import { onlyPlanUpdates } from '../entry/src/main/ets/core/ConversationPlan.ts';
import { groupUserImages } from '../entry/src/main/ets/core/MessageImageGallery.ts';
import test from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { stripTypeScriptTypes } from 'node:module';
import { projectTranscriptRows, projectToolTurn, transcriptRecord } from '../entry/src/main/ets/core/Domain.ts';
import { isAgentInput, recordedDuration } from '../entry/src/main/ets/core/AgentEvents.ts';
import { workStatusFailed, workToolArguments, workToolName, workToolStatus, workToolSubject } from '../entry/src/main/ets/core/WorkPresentation.ts';
import { stepsToItems } from '../../integrations/antigravity/lib/map.js';

const source = readFileSync(new URL('../entry/src/main/ets/models/ClientModel.ets', import.meta.url), 'utf8');

function method(name, input = source) {
  const match = new RegExp('  (?:private )?(?:async )?' + name + '\\(').exec(input);
  assert.ok(match, name);
  const start = match.index, body = input.indexOf('{', start);
  let depth = 1, quote = '', escape = false, index = body + 1;
  for (; index < input.length && depth; index += 1) {
    const character = input[index];
    if (quote) {
      if (escape) escape = false;
      else if (character === '\\') escape = true;
      else if (character === quote) quote = '';
    } else if (character === '"' || character === "'" || character === '`') quote = character;
    else if (character === '{') depth += 1;
    else if (character === '}') depth -= 1;
  }
  return input.slice(start, index);
}

class ChatMessage {
  constructor(id, role, text = '') { Object.assign(this, { id, role, text }); }
}

const ProjectionHarness = new Function(
  'groupUserImages', 'projectTranscriptRows', 'isAgentInput', 'recordedDuration', 'transcriptRecord', 'ChatMessage', 'messageDisplay', 'parseMessageContent',
  'Wire', 'return ' + stripTypeScriptTypes('class ProjectionHarness {' + method('projectMessages') + method('expandWork') + '}'),
)(
  groupUserImages, projectTranscriptRows, isAgentInput, recordedDuration, transcriptRecord,
  ChatMessage,
  text => ({ text, sources: [] }),
  text => ({ text, images: [], files: [] }),
  {text:(record,key) => record[key] || ''},
);

const configs = [
  { reading_case: true },
  { reading_case: true, reading_prepend: true },
  { reading_case: true, reading_prepend: true, reading_append: true },
];
const fixturePath = fileURLToPath(new URL('./native-ui-fixture.py', import.meta.url));
const python = [
  'import importlib.util, json, sys',
  "spec = importlib.util.spec_from_file_location('native_ui_fixture', sys.argv[1])",
  'fixture = importlib.util.module_from_spec(spec)',
  'spec.loader.exec_module(fixture)',
  'controls = json.loads(sys.argv[2])',
  'print(json.dumps([fixture.transcript(control=control)["items"] for control in controls]))',
].join('; ');
const transcripts = JSON.parse(execFileSync(
  'python3', ['-c', python, fixturePath, JSON.stringify(configs)], { encoding: 'utf8' },
));

function project(items, running = false, agent = '') {
  const model = Object.assign(new ProjectionHarness(), {
    target: () => 'fixture-reading-target',
    sessionRow: () => ({data:() => ({agent})}),
    expandedAttachments:new Map(),
    itemTime: () => 0,
    workText: step => step.item?.text ?? '',
  });
  return model.projectMessages(items, running);
}

test('a live turn has a readable work row before its first tool, and a finished text-only turn has none', () => {
  const user = { id: 'input', seq: 1, role: 'user', kind: 'text', text: 'Check the tests' };
  const live = project([user], true);
  assert.equal(live[1].work.running, true);
  assert.equal(live[1].work.inspectionSteps.length, 0);
  const finished = project([user, { id: 'reply', seq: 2, role: 'assistant', kind: 'text', text: 'Ready' }]);
  assert.deepEqual(finished.map(row => row.role), ['user', 'assistant']);
});

test('a completed exchange keeps wire tool names, arguments, results and folded counts out of the reply', () => {
  const rows = project([
    { id: 'question', seq: 1, role: 'user', kind: 'text', text: 'Check the settings' },
    { id: 'fold', seq: 2, kind: 'fold', fold: { through_seq: 6, through_id: 'six', tools: 3, thoughts: 1 } },
    { id: 'call', seq: 7, role: 'assistant', kind: 'tool_use', tool_use_id: 'sh', tool_name: 'Bash', tool_input: { command: 'npm test' } },
    { id: 'result', seq: 8, role: 'user', kind: 'tool_result', tool_use_id: 'sh', text: '1 failed', outcome: { status: 'failed' } },
    { id: 'answer', seq: 9, role: 'assistant', kind: 'text', text: 'One test failed.' },
  ]);
  assert.deepEqual(rows.map(row => row.role), ['user', 'work', 'assistant']);
  assert.equal(rows[2].text, 'One test failed.');
  const { work, ownerTarget } = rows[1];
  assert.equal(ownerTarget, 'fixture-reading-target');
  assert.equal(work.toolCount, 4);
  assert.deepEqual(work.inspectionSteps.map(step => step.kind), ['fold', 'tool']);
  const call = work.inspectionSteps[1];
  assert.equal(workToolName(call.use), 'Bash');
  assert.equal(workToolSubject(call.use), 'npm test');
  assert.deepEqual(JSON.parse(workToolArguments(call.use)), { command: 'npm test' });
  assert.equal(call.results[0].text, '1 failed');
  assert.equal(workStatusFailed(workToolStatus(call, false)), true);
});

test('older tool spellings remain inspectable and stopped or denied tools are not reported as running or failed', () => {
  const use = { id: 'read', seq: 1, kind: 'tool_use', tool: 'Read', input: { file_path: 'src/settings.ts' } };
  assert.equal(workToolName(use), 'Read');
  assert.equal(workToolSubject(use), 'src/settings.ts');
  assert.deepEqual(JSON.parse(workToolArguments(use)), use.input);
  assert.equal(workToolArguments({ ...use, input: undefined, text: 'legacy input' }), 'legacy input');
  const step = projectToolTurn('test', [use], true).inspectionSteps[0];
  assert.equal(workToolStatus(step, true), 'working');
  assert.equal(workToolStatus(step, false), 'work_no_result');
  assert.equal(workToolStatus({ ...step, failed: true, result: { outcome: { status: 'denied' } } }, false), 'work_denied');
});

test('a delayed fold expansion cannot request the next range on a replacement transcript', async () => {
  const model = new ProjectionHarness(), calls = [];
  let release;
  model.target = () => 'first';
  model.transcript = { unfold(id) { calls.push(id); return new Promise(resolve => { release = resolve; }); } };
  const message = { ownerTarget: 'first', work: { inspectionSteps: [{ kind: 'fold', id: 'a' }, { kind: 'fold', id: 'b' }] } };
  const expanding = model.expandWork(message);
  model.target = () => 'second';
  model.transcript = { async unfold(id) { calls.push('wrong: ' + id); } };
  release();
  await expanding;
  await model.expandWork(message);
  assert.deepEqual(calls, ['a']);
});

test('native transcript projection keeps a middle stable ID across prepend and append phases', () => {
  const middleId = 'visible-fixture-reading-50#0';
  const initial = project(transcripts[0]);
  assert.equal(initial.length, 20);
  assert.ok(initial.some(message => message.id === middleId));

  const prepended = project(transcripts[1]);
  assert.equal(prepended.length, 40);
  assert.equal(prepended[31].id, middleId);

  const appended = project(transcripts[2]);
  assert.equal(appended.length, 45);
  assert.equal(appended[31].id, middleId);
  assert.equal(appended[31].itemId, 'fixture-reading-51#0');
});

test('an Antigravity wake keeps the finished reply and its attachment in the conversation', () => {
  const raw = readFileSync(new URL('../../integrations/antigravity/test/fixtures/autonomous-wake.jsonl', import.meta.url), 'utf8');
  const steps = raw.trim().split('\n').map(line => JSON.parse(line));
  const prior = project(stepsToItems(steps.slice(0, 3)).items);
  const history = stepsToItems(steps).items;
  const after = project(history);
  const replies = after.filter(row => row.role === 'assistant');
  assert.deepEqual(replies.map(row => row.text), [steps[2].content, steps[5].content, steps[8].content]);
  assert.deepEqual(replies[0], prior.find(row => row.role === 'assistant'));
  assert.match(replies[0].text, /file: \/synthetic\/chart\.png/);
  assert.equal(after.filter(row => row.role === 'user').length, 2);
  assert.ok(after.every(row => !row.work?.includes(steps[2].content)));
});

test('completed turns collapse independently; expanding preserves the reply, original order and child work keys', () => {
  const chat=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
  const start=chat.indexOf('class ConversationLine {'),end=chat.indexOf('\n}',start)+2;
  const Line=new Function('return '+stripTypeScriptTypes(chat.slice(start,end)))();
  const methods=chat.slice(chat.indexOf('  private processLines('),chat.indexOf('  private toggleProcess('));
  const View=new Function('ConversationLine','return '+stripTypeScriptTypes('class View {'+methods+'}'))(Line);
  const view=new View();view.openProcesses=[];view.hiddenProcessAnchors=new Map();view.model={messages:[]};
  const items=[
    {id:'q',seq:1,kind:'text',role:'user',text:'first'},
    {id:'progress',seq:2,kind:'text',role:'assistant',text:'update',narration:true},
    {id:'tool',seq:3,kind:'tool_use',tool_use_id:'t',tool_name:'Read'},
    {id:'receipt',seq:4,kind:'tool_result',tool_use_id:'t',text:'ok'},
    {id:'final',seq:5,kind:'text',role:'assistant',text:'answer'},
  ];
  const rows=(live=false)=>view.processLines(project(items,live).map(m=>new Line(m.id,m)));
  assert.equal(rows(true).length,4,'a tool receipt and a still-streaming answer leave the process visible');
  items.push({id:'duration',seq:6,kind:'turn_duration',turn_duration:{duration_ms:399000}},
    {id:'q2',seq:7,kind:'text',role:'user',text:'second'},
    {id:'note2',seq:8,kind:'text',role:'assistant',text:'more',narration:true},
    {id:'final2',seq:9,kind:'text',role:'assistant',text:'answer2'});
  assert.deepEqual(rows().map(l=>l.processReply?'header':l.message.text),['first','header','answer','second','header','answer2']);
  const header=rows()[1];assert.equal(header.processReply.turnMilliseconds,399000);
  assert.equal(view.hiddenProcessAnchors.get('message:progress'),header.id);
  view.openProcesses=['final'];
  const opened=rows();assert.deepEqual(opened.map(l=>l.processReply?'header':l.message.role),['user','header','assistant','work','assistant','user','header','assistant']);
  assert.equal(opened[3].message.work.inspectionSteps[0].result.id,'receipt');
  assert.equal(opened[4].hasProcess,true);assert.equal(opened[6].processReply.itemId,'final2');
  view.openProcesses=[];assert.deepEqual(rows().map(l=>l.id),['q','process:final','visible-q','q2','process:final2','visible-q2']);
  assert.equal(project([{id:'plain',seq:1,kind:'text',role:'assistant',text:'simple'}])[0].processIds,undefined);
  assert.ok(project(items.slice(0,4)).every(m=>!m.processIds),'unfinished work has no disclosure');
});

test('steers stay visible when collapsed, and expanding a single batch makes its calls available immediately', () => {
  const chat=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
  const start=chat.indexOf('class ConversationLine {'),end=chat.indexOf('\n}',start)+2;
  const Line=new Function('return '+stripTypeScriptTypes(chat.slice(start,end)))();
  const methods=chat.slice(chat.indexOf('  private processLines('),chat.indexOf('  private toggleProcess('));
  const View=new Function('ConversationLine','return '+stripTypeScriptTypes('class View {'+methods+'}'))(Line);
  const view=new View();Object.assign(view,{openProcesses:[],hiddenProcessAnchors:new Map(),model:{hasMoreMessages:false}});
  const messages=[{id:'q',itemId:'q',role:'user'},{id:'w',role:'work',work:{}},
    {id:'add',role:'user',itemId:'add'},{id:'reply',itemId:'f',role:'assistant',processIds:['w']}];
  const visible=()=>view.processLines(messages.map(m=>new Line(m.id,m)));
  assert.deepEqual(visible().map(l=>l.id),['q','add','process:f','reply']);
  view.openProcesses=['f'];const open=visible();
  assert.deepEqual(open.map(l=>l.id),['q','process:f','w','add','reply']);assert.equal(open[2].soleProcessGroup,true);
  view.openProcesses=[];view.model.hasMoreMessages=true;
  const only=new Line('result',{id:'result',itemId:'f',role:'assistant',partialProcess:true,processIds:[]});
  assert.deepEqual(view.processLines([only]).map(l=>l.id),['process:f','result']);
});

test('large open disclosures reuse stable virtual batches and closing removes every continuation',()=>{
 const Controller=new Function('ConversationLine','inlineWorkLimit','workBatchSize','return '+stripTypeScriptTypes('class C {'+method('virtualWorkLines',readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8'))+'}'))(
   class Line{constructor(id,message){this.id=id;this.message=message;}},18,6);
 const view=new Controller();Object.assign(view,{expanded:['work'],closedSoloGroups:[],workBatches:new WeakMap()});
 const steps=Array.from({length:240},(_,i)=>({id:'c'+i,kind:'tool'})),message={id:'work',work:{inspectionSteps:steps}};
 const raw=()=>[{id:'header',readingId:'message:work',message}];
 const open=view.virtualWorkLines(raw());assert.equal(open.length,40);
 assert.deepEqual(open.flatMap(l=>l.workBatch.map(x=>x.id)),steps.map(x=>x.id));
 assert.equal(new Set(open.map(l=>l.id)).size,40);assert.equal(open[1].workBatch,view.virtualWorkLines(raw())[1].workBatch);
 view.expanded=[];assert.equal(view.virtualWorkLines(raw()).length,1);
});

test('the native Claude message view keeps a tool-bound text update in progress prose', () => {
  const rows = project([
    {id:'u',seq:1,kind:'text',role:'user',text:'inspect'},
    {id:'p',seq:2,kind:'text',role:'assistant',text:'Reading the logs'},
    {id:'b',seq:3,kind:'tool_use',tool_name:'Bash',tool_use_id:'b',tool_input:{description:'Wait for statistics'}},
  ],true,'claude');
  assert.equal(rows.find(row=>row.itemId==='p').narration,true);
  assert.equal(rows.at(-1).work.agent,'claude');
});

test('a plan on its own has no completed-process toggle; mixed turns retain their plan and tools', () => {
  const chat=readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
  const from=chat.indexOf('class ConversationLine {'), end=chat.indexOf('\n}',from)+2;
  const Line=new Function('return '+stripTypeScriptTypes(chat.slice(from,end)))();
  const Controller=new Function('ConversationLine','return '+stripTypeScriptTypes('class View {'+method('processLines',chat)+'}'))(Line);
  const view=new Controller();view.hiddenProcessAnchors=new Map();
  const prompt={id:'u',seq:1,role:'user',kind:'text',text:'Review'};
  const planUse={id:'p',seq:2,role:'assistant',kind:'tool_use',tool_name:'TodoWrite',tool_use_id:'p',tool_input:{todos:[{content:'Prepare',status:'completed'}]}};
  const receipt={id:'r',seq:3,kind:'tool_result',tool_use_id:'p',text:'ok'};
  const reply={id:'a',seq:10,kind:'text',role:'assistant',text:'Result'};
  const plan={sourceId:'p'};
  const render=(extra=[],done=true,card=true,receiptItem=receipt)=>{
    const msgs=project([prompt,planUse,receiptItem,...extra,...(done?[reply]:[])],!done,'claude');
    view.model={messages:msgs,hasMoreMessages:false};
    const lines=msgs.map(m=>new Line(m.id,m)).filter(l=>!onlyPlanUpdates(l.message.work));
    if(card){const c=new Line('plan');c.plan=plan;lines.splice(1,0,c);}
    return view.processLines(lines,card?plan:undefined).map(l=>l.processReply?'process':l.plan?'plan':l.message.role);
  };
  for(const open of [[],['a']]){
    view.openProcesses=open;
    assert.deepEqual(render(),['user','assistant']);
    assert.deepEqual(render([],true,false),['user','assistant']);
    assert.ok(render([],false).includes('plan'));
  }
  const command={id:'b',seq:4,kind:'tool_use',role:'assistant',tool_name:'Bash',tool_use_id:'b'};
  assert.deepEqual(render([command,{id:'c',seq:5,kind:'tool_result',tool_use_id:'b',text:'ok'}]),
    ['user','process','plan','work','assistant']);
  assert.ok(render([],true,false,{...receipt,is_error:true}).includes('work'));
});

test('known plans open as checklists and refill their rows immediately on a second tap or scope change', () => {
  const source = readFileSync(new URL('../entry/src/main/ets/components/ConversationPlanCard.ets',import.meta.url),'utf8');
  const methods = ['receivePlan','syncRows','toggle'].map(name => method(name,source)).join('\n');
  const Card = new Function('reconcileBoundRows','return '+stripTypeScriptTypes('class Card {'+methods+'}'))(
    (_previous,values) => values.map(value=>({value})));
  const plan = {sourceId:'p',at:1,done:1,total:2,current:'Verify',complete:true,steps:[
    {id:'1',title:'Read',status:'completed'}, {id:'2',title:'Verify',status:'in_progress'}]};
  const card = Object.assign(new Card(),{scope:'chat',plan,shown:plan,expanded:false,active:true,request:0,
    hold:change=>change(),onExpansion:()=>{}});
  // ArkUI may still return the previous @Computed value during a state mutation.
  card.receivePlan();assert.deepEqual(card.rows.map(r=>r.value.id),['1','2']);
  card.expanded=true;card.toggle();assert.equal(card.rows.length,0);
  card.expanded=false;card.toggle();assert.deepEqual(card.rows.map(r=>r.value.id),['1','2']);
  card.userExpanded=false;card.scope='next';card.receivePlan();
  assert.deepEqual(card.rows.map(r=>r.value.id),['1','2']);
});

test('next-step buttons send once and retain the accepted choice on the original card', async () => {
  const chat = readFileSync(new URL('../entry/src/main/ets/components/Conversation.ets',import.meta.url),'utf8');
  const methods = ['selectedNextStep','nextStepCurrent','canSendNextStep','useNextStep'].map(name => method(name,chat)).join('\n');
  const View = new Function('return '+stripTypeScriptTypes('class V {'+methods+'}'))();
  for (const outcome of ['accepted','failed','switch','updated','pending','readonly']) {
    const message = {id:'wrap',ownerTarget:'chat',nextStep:{choices:[{label:'继续'}]}};
    const sent = [], receipts = [];
    let finish, scope = 'chat', admission;
    const model = {draft:'已有草稿',target:() => scope,messages:[message],canSend:() => outcome !== 'readonly',
      captureView(){return {write: async value => {receipts.push(JSON.parse(value));}};},
      operationState:outcome === 'pending' ? 'unknown' : '',send(current,text) {
        assert.ok(current()); sent.push(text); admission = current; return new Promise(resolve => {finish=resolve;});
      }};
    const view = Object.assign(new View(),{model,sendCurrent:() => true,composerOwnerKey:() => scope,nextStepStateReady:true});
    const pending = view.useNextStep(message,'继续');
    await view.useNextStep(message,'另一项');
    assert.deepEqual(receipts,[]);
    if (outcome === 'switch') scope = 'another chat';
    if (outcome === 'updated') model.messages = [{...message,nextStep:{choices:[{label:'更新'}]}}];
    if (admission) assert.equal(admission(),!['switch','updated'].includes(outcome));
    if (finish) finish(outcome !== 'failed');
    await pending;
    if (outcome === 'accepted') await view.useNextStep(message,'继续');
    assert.deepEqual(sent,['pending','readonly'].includes(outcome) ? [] : ['继续']);
    assert.deepEqual(receipts,outcome === 'accepted' ? [{id:'wrap',answer:'继续'}] : []);
    assert.equal(view.selectedNextStep(message),outcome === 'accepted' ? '继续' : '');
    assert.equal(model.draft,'已有草稿');
  }
});

test('the new-session picker refreshes Codex on its owner and keeps named 6.2 choices in order',async()=>{
  const page=readFileSync(new URL('../entry/src/main/ets/pages/Index.ets',import.meta.url),'utf8');
  const methods=['canTypeStartModel','startModelOptions','modelLabel','pickStartModel','readStartModels','offeredEfforts','chosenEffort','keepStartEffort'].map(name=>method(name,page)).join('\n');
  const {launcherModelLabel,startEfforts}=await import('../entry/src/main/ets/core/ModelLabel.ts');
  const View=new Function('Wire','tr','launcherModelLabel','startEfforts','return '+stripTypeScriptTypes('class View {'+methods+'}'))({text:(r,k)=>r[k]||''},k=>k,launcherModelLabel,startEfforts);
  const calls=[];let release;
  const owner={connectionGeneration:()=>1,scope:()=> 'hub',owns:()=>true,repo:()=>({sessionModels(machine,launcher,refresh){
    calls.push([machine,launcher,refresh]);return new Promise(resolve=>{release=resolve;});
  }})};
  const view=Object.assign(new View(),{agentModel:owner,agentMachineId:'office',launcher:'codex',sheetKind:'newagent',showSheet:true,
    modelRequest:0,startModel:'',startEffort:'',startModels:[],saveAgentDraft(){},agentMachine(){return {id:this.agentMachineId};},
    chosenLauncher(){return {name:this.launcher,agent:'codex'};}});
  const first=view.readStartModels(), prior=release;
  view.agentMachineId='mini'; const second=view.readStartModels();
  release({models:[{id:'gpt-6.2-sol',name:'GPT-6.2-Sol',default:true,efforts:['low','high','xhigh']},{id:'gpt-6.2-astra',name:'GPT-6.2-Astra',efforts:['low','high']}],locked:false});await second;
  prior({models:[{id:'old'}],locked:false});await first;
  assert.deepEqual(calls,[['office','codex',true],['mini','codex',true]]);
  assert.deepEqual(view.startModelOptions().map(o=>o.value),['agent_default_model','GPT-6.2-Sol','GPT-6.2-Astra','agent_custom_model']);
  assert.deepEqual(view.offeredEfforts(),['low','high','xhigh'],'with no pick, the default model\'s efforts');
  view.pickStartModel(1);assert.equal(view.startModel,'gpt-6.2-sol');
  view.startEffort='xhigh';assert.equal(view.chosenEffort(),'xhigh');
  view.pickStartModel(2);assert.equal(view.modelLabel(),'GPT-6.2-Astra');
  assert.equal(view.startEffort,'','a model without the picked effort drops it');
  view.startEffort='high';view.pickStartModel(1);assert.equal(view.chosenEffort(),'high');
  view.pickStartModel(0);assert.equal(view.startModel,'');
  view.pickStartModel(3);view.startModel='my-new-model';
  const reload=view.readStartModels(true);release({models:[],locked:false});await reload;
  assert.equal(view.typingStartModel,true);assert.equal(view.startModel,'my-new-model');
  assert.deepEqual(view.offeredEfforts(),[]);assert.equal(view.chosenEffort(),'','a typed model sends no effort nobody listed for it');
  view.launcher='codex-app';const locked=view.readStartModels();release({models:[],locked:true});await locked;
  assert.equal(view.canTypeStartModel(),false);assert.equal(view.startModel,'');
});
