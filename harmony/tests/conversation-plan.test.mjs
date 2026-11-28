import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { ConversationPlanReplay, planPinned, planPosition, onlyPlanUpdates, samePlanSummary } from '../entry/src/main/ets/core/ConversationPlan.ts';

function pair(seq, tool, input, output = 'ok', result = {}) {
  const id = 'call-' + seq;
  return [
    { id: id + '-use', seq, kind: 'tool_use', tool_name: tool, tool_use_id: id, tool_input: input },
    { id: id + '-result', seq: seq + 1, kind: 'tool_result', tool_use_id: id,
      ts: new Date(seq * 1000).toISOString(), text: typeof output === 'string' ? output : JSON.stringify(output), ...result },
  ];
}
const states = plan => plan.steps.map(s => [s.title, s.status]);

test('a Codex plan appears on its successful receipt, updates the same list, and clears on an accepted empty plan', () => {
  const replay = new ConversationPlanReplay();
  const initial = [{step:'Read',status:'completed'}, {step:'Check',status:'in_progress'}, {step:'Summarize',status:'pending'}];
  const [use, receipt] = pair(1, 'functions.update_plan', {plan:initial}, 'Plan updated');
  replay.accept([use]);
  assert.equal(replay.view(), undefined);
  replay.accept([receipt]);
  const plan = replay.view();
  assert.deepEqual([plan.current, plan.done, plan.total, plan.complete, plan.sourceId], ['Check', 1, 3, true, use.id]);
  assert.deepEqual(states(plan), initial.map(s => [s.step, s.status]));
  for (const [i, result] of [{is_error:true}, {outcome:{status:'denied'}}, {outcome:{status:'failed'}}].entries()) {
    replay.accept(pair(3 + i * 2, 'update_plan', {plan:[]}, 'refused', result));
  }
  replay.accept(pair(9, 'update_plan', {plan:[{step:'unfinished'}, {}]}));
  replay.accept(pair(11, 'update_plan', {plan:[]}, {error:'unavailable'}));
  assert.deepEqual(replay.view(), plan);
  replay.accept(pair(13, 'update_plan', {plan:initial.map(s => ({...s,status:'completed'}))}));
  const done = replay.view({at:new Date(1000).toISOString(),done:1,total:3,current:'Check'});
  assert.deepEqual([done.done, done.current], [3, '']);
  replay.accept(pair(15, 'update_plan', {plan:[]}));
  assert.equal(replay.view({at:new Date(1000).toISOString(),done:1,total:3,current:'Check'}), undefined);
});

test('TodoWrite uses the returned list and honest status labels even if no step is in progress', () => {
  const replay = new ConversationPlanReplay();
  replay.accept(pair(1, 'TodoWrite', {todos:[]}, {todos:[
    {content:'Read logs', status:'in_progress', activeForm:'Reading logs'},
    {content:'Review', status:'blocked'}, {content:'Finish', status:'unrecognized'},
  ]}));
  const plan = replay.view(undefined, false);
  assert.equal(plan.complete, true);
  assert.equal(plan.current, 'Reading logs');
  assert.deepEqual(states(plan), [['Read logs','in_progress'],['Review','blocked'],['Finish','unknown']]);
  assert.ok(samePlanSummary(0, 3, 'Reading logs', plan.steps));
  assert.ok(!samePlanSummary(0, 3, '', plan.steps));
  replay.accept(pair(3, 'TodoWrite', {todos:[{content:'Read logs',status:'pending'}]}));
  assert.equal(replay.view().current, '');
  assert.equal(replay.view({at:new Date(5000).toISOString(),done:1,total:2,current:'Review'}).complete, false);
});

test('Claude task edits keep siblings, active labels and order until a deletion or a full list replaces them', () => {
  const replay = new ConversationPlanReplay();
  replay.accept(pair(1, 'TaskCreate', {subject:'Read'}, 'Task #1 created successfully: Read'));
  replay.accept(pair(3, 'TaskCreate', {subject:'Check',activeForm:'Checking'}, {task:{id:2}}));
  replay.accept(pair(5, 'TaskUpdate', {taskId:'1',status:'completed'}));
  replay.accept(pair(7, 'TaskUpdate', {taskId:2,status:'in_progress'}));
  replay.accept(pair(9, 'TaskUpdate', {taskId:8,status:'completed'}, 'Task #8 not found'));
  replay.accept(pair(11, 'TaskUpdate', {taskId:2,status:'completed'}, {success:false}));
  assert.equal(replay.view().current, 'Checking');
  const items = [['Read','completed'],['Check','in_progress']];
  assert.deepEqual(states(replay.view()), items);
  assert.equal(replay.view(undefined, false).complete, false);
  replay.accept(pair(13, 'TaskList', {}, '#1 [completed] Read (owner)\n#2 [in_progress] Check (blocked by #1)'));
  assert.deepEqual(states(replay.view(undefined, false)), items);
  assert.equal(replay.view(undefined, false).complete, true);
  replay.accept(pair(15, 'TaskUpdate', {taskId:1,status:'deleted'}));
  assert.deepEqual(replay.view().steps.map(s => s.id), ['task:2']);
  replay.accept(pair(17, 'TaskList', {}, 'No tasks found.'));
  assert.equal(replay.view(), undefined);
});

test('a folded snapshot produces a complete checklist with the same live state as raw receipts', () => {
  const replay = new ConversationPlanReplay();
  replay.accept([{id:'fold-3',seq:2,kind:'fold',fold:{tasks:[{replace:'todo',entries:[
    {id:'todo:0',subject:'Read',status:'completed'},
    {id:'todo:1',subject:'Check',description:'Checking',status:'in_progress'},
  ]}]}}]);
  const plan = replay.view(undefined, false);
  assert.equal(plan.sourceId, 'fold-3');
  assert.equal(plan.current, 'Checking');
  assert.deepEqual(states(plan), [['Read','completed'],['Check','in_progress']]);
  assert.ok(plan.complete);
  const next = pair(4, 'TodoWrite', {todos:[{content:'Read',status:'completed'},{content:'Check',status:'completed'}]});
  replay.accept([...next, ...next]);
  assert.deepEqual([replay.view().total,replay.view().done], [2,2]);
});

test('an aggregate never invents checklist content and matches the server truncation of a long current step', () => {
  const replay = new ConversationPlanReplay();
  const title = '查'.repeat(122) + '\nextra', short = '查'.repeat(119) + '…';
  const report = {done:0,total:1,current:short};
  assert.deepEqual(replay.view(report, false).steps, []);
  replay.accept(pair(1, 'TaskUpdate', {taskId:7, status:'in_progress',subject:title}));
  assert.ok(replay.view(report, false).complete);
  assert.equal(replay.view(report, false).current, title);
  const next = replay.view({...report,current:'different',at:new Date(8000).toISOString()},false);
  assert.deepEqual([next.current,next.complete,next.steps.length],['different',false,0]);
});

test('the plan has its own timeline position next to the owning activity or in the current exchange', () => {
  const lines = [
    {message:{role:'user'}}, {message:{role:'assistant',work:{inspectionSteps:[{id:'old'}]}}},
    {message:{role:'user'}}, {message:{role:'assistant',work:{inspectionSteps:[{id:'plan-call'}]}}},
    {message:{role:'assistant'}},
  ];
  assert.equal(planPosition(lines, 'plan-call'), 3);
  assert.equal(planPosition(lines, 'old'), 1);
  assert.equal(planPosition(lines, ''), 3);
  assert.equal(planPosition(lines.slice(0,3), ''), 3);
  const revision = {message:{role:'work',work:{inspectionSteps:[{id:'revised-plan'}]}}};
  assert.equal(planPosition([...lines.slice(0,4),revision], 'revised-plan'), 3);
});

test('Claude Desktop commits its items on a successful receipt and ignores a failed revision', () => {
  const plan = new ConversationPlanReplay();
  const rows = ['Inspect','Read','Compare','Report'].map((content, i) => ({content, status:i ? 'pending' : 'in_progress'}));
  const call = {id:'plan',seq:1,kind:'tool_use',tool_use_id:'p',tool_name:'mcp__ccd_turn__update_plan',tool_input:{items:rows,scope:'continuation'}};
  plan.accept([call]);
  assert.equal(plan.view(),undefined);
  plan.accept([{id:'ok',seq:2,kind:'tool_result',tool_use_id:'p',text:'Plan shown (4 steps).'}]);
  assert.deepEqual(plan.view().steps.map(s=>[s.title,s.status]),rows.map(s=>[s.content,s.status]));
  plan.accept([{...call,id:'next',seq:3,tool_use_id:'next',tool_input:{items:[]}},
    {id:'err',seq:4,kind:'tool_result',tool_use_id:'next',is_error:true}]);
  assert.equal(plan.view().total,4);
});

test('a running Claude plan sits above the current action and stays at its update after completion', () => {
  const first = {agent:'claude',turnId:'turn',latestExchange:true,running:true,inspectionSteps:[{id:'plan'}]};
  const last = {...first,inspectionSteps:[{id:'wait'}]};
  const lines = [{message:{role:'user'}}, {message:{role:'work',work:first}},
    {message:{role:'assistant'}}, {message:{role:'work',work:last}}];
  assert.equal(planPosition(lines,'plan'),3);
  assert.equal(planPosition(lines.slice(0,-1),'plan'),3);
  first.running = false;
  assert.equal(planPosition(lines,'plan'),1);
});

test('only bookkeeping is hidden; failed calls or folds with other contents remain available', () => {
  const [use,result] = pair(1,'mcp__ccd_turn__update_plan',{items:[{content:'Read',status:'in_progress'}]});
  const step = {kind:'tool',use,result};
  const hidden = (...inspectionSteps) => onlyPlanUpdates({running:true,inspectionSteps});
  assert.equal(hidden(step),true);
  assert.equal(hidden({...step,result:null}),true);
  assert.equal(hidden({...step,result:{...result,is_error:true}}),false);
  assert.equal(hidden(step,{...step,use:{...use,tool_name:'Bash'}}),false);
  const fold = {tools:1,thoughts:1,items:3,errors:0,tool_names:['TodoWrite'],tasks:[{entries:[]}]};
  const folded = changes => ({kind:'fold',toolCount:1,thoughtCount:1,item:{fold:{...fold,...changes}}});
  assert.equal(hidden(folded({})),true);
  for (const changes of [{errors:1},{items:4},{tasks:[]},{tool_names:['Bash']},{items:undefined}]) {
    assert.equal(hidden(folded(changes)),false);
  }
});

test('Claude desktop full list, advance, then ordinal done/start replay in receipt order', () => {
  const p = new ConversationPlanReplay(), name='mcp__ccd_turn__update_plan';
  const items=Array.from({length:6},(_,i)=>({content:'Step '+(i+1),status:i?'pending':'in_progress'}));
  p.accept(pair(1,name,{items},'Plan shown (6 steps).'));
  for (let i=0;i<3;i++) {
    const events=pair(4+i*2,name,{advance:true},'Plan updated.');
    p.accept(events);p.accept(events);
    assert.equal(p.view().done,i+1);
  }
  p.accept(pair(12,name,{done:[4,5],start:6},'Plan updated: step 6 of 6 is in progress.'));
  p.accept(pair(15,name,{advance:true},'Nothing changed.'));
  p.accept(pair(18,name,{done:[6,8]},'Plan updated.'));
  p.accept(pair(21,name,{advance:true},'Plan updated.',{is_error:true}));
  assert.deepEqual(p.view().steps.map(s=>s.status),['completed','completed','completed','completed','completed','in_progress']);
  assert.equal(p.view().current,'Step 6');
  // A history page containing only relative advances cannot invent the preceding steps.
  const partial=new ConversationPlanReplay();partial.accept(pair(2,name,{advance:true},'Plan updated.'));
  assert.equal(partial.view(),undefined);
});

test('a message sent mid-turn moves the unfinished plan under it, above the work still running, as on desktop', () => {
  const earlier = {agent:'claude',turnId:'turn',latestExchange:false,running:false,inspectionSteps:[{id:'plan'},{id:'save'}]};
  const current = {agent:'claude',turnId:'turn',latestExchange:true,running:true,inspectionSteps:[{id:'edit'}]};
  const lines = [{message:{role:'user'}}, {message:{role:'assistant'}}, {message:{role:'work',work:earlier}},
    {message:{role:'assistant'}}, {message:{role:'user'}}, {message:{role:'work',work:current}}, {message:{role:'live_activity'}}];
  const plan = {sourceId:'plan', done:0, total:5};
  assert.equal(planPinned(lines, plan, true), true);
  assert.equal(planPosition(lines, 'plan', true), 5, 'after the newest message, before the running rows');
  assert.equal(planPosition(lines, 'plan'), 2, 'unpinned, it stays beside its update');
  assert.equal(planPinned(lines, plan, false), false, 'once the Agent stops, the plan goes back to its update');
  assert.equal(planPinned(lines, {...plan, done:5}, true), false, 'a finished plan stays where it finished');
  assert.equal(planPinned(lines.slice(0, 4), plan, true), false, 'no message since the update');
  assert.equal(planPinned(lines, {...plan, sourceId:''}, true), false, 'a summary without its update keeps its own place');
  const answered = [...lines.slice(0, 5), {message:{role:'assistant'}}];
  assert.equal(planPosition(answered, 'plan', true), 6, 'below a reply the Agent already wrote');
});

test('the pinned plan says where it is from and keeps its progress mark running', () => {
  const read = (path) => readFileSync(new URL(path, import.meta.url), 'utf8');
  const page = read('../entry/src/main/ets/components/Conversation.ets');
  assert.ok(page.includes("card.planPinned = planPinned(lines, plan, this.model.turnWorking());"));
  assert.ok(page.includes("planPosition(lines, plan.sourceId, card.planPinned)"));
  assert.ok(page.includes("(line.plan && !line.planPinned ? planOwner?.id || '' : '')"), 'it does not fold with the earlier turn');
  assert.ok(page.includes("running: this.planRunning(entry.item.plan!, entry.item.planPinned)"));
  assert.ok(page.includes("note: entry.item.planPinned ? tr('plan_before_last_message') : ''"));
  const card = read('../entry/src/main/ets/components/ConversationPlanCard.ets');
  assert.ok(card.includes(".id('chat.plan.note')"));
  for (const [locale, text] of [['base', 'Plan from before your last message'], ['zh_CN', '你最新消息之前的计划'], ['ja_JP', '最新のメッセージより前の計画']]) {
    const strings = JSON.parse(read('../entry/src/main/resources/' + locale + '/element/string.json')).string;
    assert.equal(strings.find((entry) => entry.name === 'plan_before_last_message')?.value, text);
  }
});
