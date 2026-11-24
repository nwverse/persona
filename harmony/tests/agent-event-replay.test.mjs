import test from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { projectTranscriptRows } from '../entry/src/main/ets/core/Domain.ts';
import { agentEventDisplay, isAgentInput, recordedDuration, sessionIssueDisplay } from '../entry/src/main/ets/core/AgentEvents.ts';
import { activityHeading, childActivitySummary, workRowExcerpt, workTarget, hasWorkDetails, toolMotion, accessibleToolState, inspectTool, workToolArguments, workAction, workDetails, workExcerpt, workKind, workMetrics, workRuns, workToolStatus } from '../entry/src/main/ets/core/WorkPresentation.ts';
import { conversationImageEntries } from '../entry/src/main/ets/core/MessageImageGallery.ts';

const directory = new URL('../../server/internal/transcript/testdata/', import.meta.url);
const replay = name => JSON.parse(readFileSync(new URL(name + '.golden.json', directory)));
const words = new Map(JSON.parse(readFileSync(new URL('../entry/src/main/resources/zh_CN/element/string.json', import.meta.url))).string.map(x => [x.name, x.value]));
const t = key => { assert.ok(words.has(key), key); return words.get(key); };
const workSteps = rows => rows.flatMap(row => row.kind === 'work' ? row.work.inspectionSteps : []);
const tools = rows => workSteps(rows).filter(step => step.kind === 'tool');
const project = key => projectTranscriptRows(replay(key).items, false);

// The Go Fixture tests check these normalized records against their raw JSONL files.
for (const file of readdirSync(directory).filter(name => name.endsWith('.golden.json')).sort()) {
  test('Harmony renders the recorded events in ' + file, () => {
    const { items } = JSON.parse(readFileSync(new URL(file, directory)));
    const rows = projectTranscriptRows(items, false), steps = workSteps(rows);
    assert.equal(new Set(rows.map(row => row.id)).size, rows.length);
    const represented = new Map();
    const count = id => represented.set(id, (represented.get(id) || 0) + 1);
    for (const row of rows) {
      if (row.kind !== 'work') count(row.item.id);
      if (row.duration) count(row.duration.id);
    }
    for (const step of steps) {
      count(step.id);
      if (step.kind === 'tool') for (const item of [...step.results, ...step.images, ...step.reports]) count(item.id);
    }
    const calls = new Set(items.filter(i => i.kind === 'tool_use').map(i => i.tool_use_id));
    for (const [index, item] of items.entries()) {
      // Orphan receipts have no call on this page. Thinking stays out of the timeline and its disclosures.
      if (item.kind === 'thinking') { assert.ok(!represented.has(item.id)); continue; }
      if (item.kind === 'tool_result' && !calls.has(item.tool_use_id)) continue;
      if (item.kind === 'notice' && item.role === 'system' && item.text === '已从压缩的上下文继续' && items[index - 1]?.compact) {
        assert.equal(represented.get(items[index - 1].id), 1);
        assert.ok(!represented.has(item.id));
        continue;
      }
      assert.equal(represented.get(item.id), 1, item.kind + ' / ' + item.id);
    }
  });
}

test('each Paseo wake keeps its own answer, author and attention state', () => {
  const rows = project('claude-paseo'), receipts = rows.filter(r => r.kind === 'event' && r.item.task);
  assert.ok(rows.some(r => r.kind === 'reply' && r.item.text === 'Ticket 01 accepted.'));
  assert.ok(rows.some(r => r.kind === 'reply' && r.item.text === 'Nothing new since the last review.'));
  assert.equal(receipts.filter(r => r.item.task.status === 'waiting').length, 1);
  for (const row of receipts) {
    const view = agentEventDisplay(row.item, t);
    if (row.item.task.status === 'waiting') assert.equal(view.tone, 'attention');
    if (row.item.task.status === 'failed') assert.equal(view.tone, 'danger');
  }
  const peer = rows.find(r => r.kind === 'input' && r.item.from);
  assert.equal(isAgentInput(peer.item), true);
  assert.equal(isAgentInput({ ...peer.item, from: undefined, client_msg_id: 'mine', text: '<persona-context kind="message-source">{"kind":"conversation"}</persona-context>quoted' }), false);
  assert.equal(isAgentInput(rows.find(r => r.item?.text?.startsWith('Why does this show up?')).item), false);
  const wakeIds = rows.filter(r => r.item?.wake).map(r => r.item.id);
  assert.equal(new Set(wakeIds).size, 3);
});

test('queued follow-ups keep both replies, late output belongs to its call, and recorded zero is measurable', () => {
  const input = [
    { id: 'q', seq: 1, role: 'user', kind: 'text', text: 'First' },
    { id: 'x', seq: 2, kind: 'tool_use', tool_use_id: 't', tool_name: 'Read' },
    { id: 'a', seq: 3, role: 'assistant', kind: 'text', text: 'First reply' },
    { id: 'q2', seq: 4, role: 'user', kind: 'text', queued: true, text: 'Another question' },
    { id: 'y', seq: 5, kind: 'tool_result', tool_use_id: 't', text: 'Late output' },
    { id: 'b', seq: 6, role: 'assistant', kind: 'text', text: 'Second reply' },
    { id: 'end', seq: 7, kind: 'turn_duration', turn_duration: { user_item_id: 'q', duration_ms: 0 } },
  ];
  const rows = projectTranscriptRows(input, false);
  assert.deepEqual(rows.map(r => r.kind), ['input', 'work', 'reply', 'input', 'reply']);
  assert.equal(tools(rows)[0].result.id, 'y');
  assert.equal(rows.at(-1).duration.id, 'end');
  assert.equal(recordedDuration(0), '0s');
  assert.equal(recordedDuration(undefined), '');
  const live = projectTranscriptRows([{ ...input[2], narration: true }], true);
  assert.equal(live.find(r => r.kind === 'narration').item.id, 'a');
  assert.equal(projectTranscriptRows([{ ...input[2], narration: true }], false)[0].item.id, 'a');
});

test('recorded outcomes drive command failures, changed files, answers, Git and bounded output', () => {
  const steps = tools(project('claude-tool-outcomes'));
  const fields = (s) => workDetails(s, t);
  const failed = steps.find(s => s.result.outcome.tail);
  assert.match(workMetrics(failed, t, recordedDuration), /退出码 1/);
  assert.equal(fields(failed).find(f => f.key === 'ev_output_tail').text, failed.result.outcome.tail);
  const edit = steps.find(s => s.result.outcome.patch);
  assert.equal(fields(edit).find(f => f.style === 'diff').text, edit.result.outcome.patch);
  assert.ok(workMetrics(edit, t, recordedDuration).includes('+2 −2'));
  assert.equal(workToolStatus(steps.find(s => s.result.outcome.status === 'denied'), false), 'work_denied');
  assert.equal(workToolStatus(steps.find(s => s.result.outcome.status === 'no_match'), false), 'work_no_match');
  assert.ok(fields(steps.find(s => s.result.outcome.answers)).some(f => f.key === 'ev_answers'));
  assert.ok(fields(steps.find(s => s.result.outcome.git?.pr)).some(f => f.key === 'ev_git'));
  const long = 'one line '.repeat(500);
  assert.ok(workExcerpt(long, false).length < long.length);
  assert.equal(workExcerpt(long, true), long);
});

test('successful edits group in order, a failure or narration interrupts the group, and subagent receipts keep their lifecycle', () => {
  const edits = tools(project('codex-0.163')).filter(s => workKind(s.use) === 'edit');
  assert.equal(edits.length, 12);
  const runs = workRuns(edits);
  assert.equal(runs.get(edits[11].id).first, edits[0].id);
  assert.equal(runs.get(edits[0].id).count, 12);
  const breaking = { ...edits[2], result: { ...edits[2].result, outcome: { status: 'failed' } } };
  assert.equal(workRuns([edits[0], edits[1], breaking, ...edits.slice(3)]).has(breaking.id), false);
  assert.equal(workRuns([edits[0], edits[1], { kind: 'narration', id: 'note' }, edits[2]]).size, 0);
  const children = tools(project('codex-collab')).filter(s => workKind(s.use) === 'agent');
  assert.deepEqual(children.map(s => s.use.tool_input.event), ['started', 'started', 'interacted', 'completed']);
  assert.equal(workAction(children.at(-1).use, t), t('ev_child_ended'));
  assert.equal(childActivitySummary(children, t), '2 个子 Agent · 1 个已结束运行');
  assert.equal(hasWorkDetails({ ...edits[0], use: { tool_input: {} }, result: { outcome: { status: 'ok' } }, results: [] }), false);
});

test('tool screenshots are owned gallery entries and silence after a failed request does not imply success', () => {
  const rows = project('claude-session'), target = 'fixture';
  const imageRows = rows.map(r => ({ id: r.id, ownerTarget: target, work: r.work, imageId: r.kind === 'image' ? r.item.id : '', imagePaths: [] }));
  const gallery = conversationImageEntries(imageRows, target);
  assert.equal(gallery.length, 3);
  assert.equal(new Set(gallery.map(i => i.key)).size, 3);
  assert.deepEqual(conversationImageEntries(imageRows, 'other'), []);
  const failed = replay('codex-turn-failed');
  assert.equal(projectTranscriptRows(failed.items, false).some(r => r.kind === 'reply'), false);
  assert.equal(sessionIssueDisplay(failed.meta, t).tone, 'danger');
  assert.equal(sessionIssueDisplay(failed.meta, t).body, failed.meta.turn_error);
  assert.equal(sessionIssueDisplay({}, t), undefined);
});

test('extension operations keep the service and operation while unfamiliar tools retain inspectable arguments', () => {
  const use = {id:'m', kind:'tool_use', role:'assistant', seq:1, tool_use_id:'r', tool_name:'mcp__example__get_document', tool_input:{query:'Handbook'}};
  const result = {id:'r', kind:'tool_result', role:'user', seq:2, tool_use_id:'r', text:'Recorded output', full_size:5500};
  const step = tools(projectTranscriptRows([use,result],false))[0];
  assert.equal(workKind(step.use),'mcp');
  const fields=workDetails(step,t);
  assert.equal(fields.find(f=>f.key==='ev_action_mcp').text,use.tool_name);
  assert.ok(fields.some(f=>f.key==='ev_output_excerpt'));
  const generic=workDetails({...step,use:{...use,tool_name:'FutureAction'}},t);
  assert.deepEqual(JSON.parse(generic.find(f=>f.key==='work_arguments').text),use.tool_input);
});

test('a fold retains known failure evidence without guessing the contents of an older fold', () => {
  const folded={id:'fold:a',seq:1,kind:'fold',fold:{through_id:'z',through_seq:12,tools:4,thoughts:1,errors:2,tool_names:['Read','Bash']}};
  const work=projectTranscriptRows([folded],false)[0].work;
  assert.equal(work.toolCount,4);
  assert.equal(work.inspectionSteps[0].item.fold.errors,2);
  assert.deepEqual(work.inspectionSteps[0].item.fold.tool_names,['Read','Bash']);
  assert.equal(projectTranscriptRows([{...folded,fold:{through_id:'z',through_seq:12,tools:4}}],false)[0].work.inspectionSteps[0].item.fold.errors,undefined);
});

test('structured compaction, fallback and visualizations retain their distinct receipt types', () => {
  const compact=project('claude-compact').find(row => row.kind === 'event' && row.item.compact);
  assert.equal(agentEventDisplay(compact.item,t).label,t('ev_compaction'));
  const refusal=project('claude-refusal-fallback').find(r=>r.item?.model_refusal);
  assert.equal(agentEventDisplay(refusal.item,t).title,'primary-model → fallback-model');
  const [visual]=projectTranscriptRows([{id:'v',seq:1,kind:'visualization',role:'assistant',visualization:{title:'Coverage',path:'/work/plot.html'}}],false);
  assert.equal(visual.kind,'event');
  assert.equal(agentEventDisplay(visual.item,t).title,'Coverage');
});

test('a completed compaction has one localized caption and reports only a recorded pre-compaction count', () => {
  const boundary = { id:'b', seq:1, kind:'notice', role:'system', text:'上下文已压缩' };
  for (const [trigger, caption] of [['auto','上下文已自动压缩'], ['manual','上下文已手动压缩'],
    ['idle','空闲时已压缩上下文'], ['unknown','上下文已压缩']]) {
    const view = agentEventDisplay({ ...boundary, compact:{trigger, pre_tokens:469214} }, t);
    assert.deepEqual([view.compaction,view.label,view.title,view.body,view.meta],
      [true,caption,'','','压缩前 469,214 tokens']);
  }
  for (const pre_tokens of [undefined,0,-1,NaN,1.5,'469214']) {
    assert.equal(agentEventDisplay({...boundary,compact:{pre_tokens}},t).meta, '');
  }
  const continued = { ...boundary, id:'s', seq:2, text:'已从压缩的上下文继续' };
  const reply = { ...boundary, id:'r', seq:3, kind:'text', role:'assistant' };
  const events = input => projectTranscriptRows(input,false).filter(row => row.kind === 'event');
  assert.deepEqual(events([continued]).map(row => row.id), ['s']);
  assert.deepEqual(events([boundary,continued,reply,{...boundary,id:'b2',seq:4}]).map(row => row.id), ['b','b2']);
  assert.deepEqual(events([boundary,reply,continued]).map(row => row.id), ['b','s']);
  assert.equal(events([{...reply,role:'user'}]).length,0);
  assert.equal(boundary.compact,undefined);
});

test('requested edits remain distinct from applied changes when a call is blocked', () => {
  const step = tools(project('claude-tool-outcomes')).find(s=>s.result.outcome.status==='blocked');
  const fields=workDetails(step,t);
  assert.ok(fields.some(f=>f.key==='ev_replace_after'));
  assert.ok(!fields.some(f=>f.key==='ev_patch'));
  const create=tools(project('codex-0.163')).find(s=>s.use.tool_input.change==='add');
  assert.ok(workDetails(create,t).some(f=>f.key==='ev_proposed_content'&&f.language==='swift'));
});

test('a live exchange retains concurrent tools, an earlier failed call and recent log lines', () => {
  const input = [{ id:'prompt', seq:1, kind:'text', role:'user', text:'检查' }];
  const ask = (id, name) => input.push({ id, seq:input.length+1, kind:'tool_use', role:'assistant', tool_use_id:id, tool_name:'Bash', tool_input:{ command:name } });
  const result = (id, failed=false) => input.push({ id:id+'-r', seq:input.length+1, kind:'tool_result', role:'user', tool_use_id:id,
    text:'one\ntwo\nthree\nfour', outcome:{ status:failed ? 'failed' : 'ok', exit_code:failed ? 7 : 0 }, is_error:failed });
  const current = () => projectTranscriptRows(input, true).find(r => r.kind === 'work').work;
  ask('slow','long command'); ask('failure','first test'); result('failure',true);
  for (let i=0;i<8;i++) { ask('sub'+i,'step '+i); result('sub'+i); }
  ask('other','second parallel call');
  let work = current(), summary = activityHeading(work, t);
  assert.deepEqual([summary.total, summary.returned, summary.active], [11,9,2]);
  const calls = work.inspectionSteps.filter(step => step.kind === 'tool');
  assert.deepEqual(calls.filter(step => workToolStatus(step,true) === 'working').map(step => step.id), ['slow','other']);
  assert.deepEqual(calls.map(step => step.id), ['slow','failure',...Array.from({length:8}, (_,i) => 'sub'+i),'other']);
  assert.equal(workRowExcerpt(calls[1], 'failed'), 'four');
  assert.equal(workRowExcerpt(calls[2], 'done'), '');
  const recorded = calls[1];
  const withTail = { ...recorded, result: { ...recorded.result, outcome: {status:'failed', tail:'one\r\ntwo\n\nthree\nfour'} } };
  assert.equal(workRowExcerpt(withTail, 'failed'), 'four');
  assert.equal(workRowExcerpt(recorded, 'work_blocked'), 'four');
  assert.equal(workRowExcerpt(calls[0], 'working'), '');
  result('slow'); summary = activityHeading(current(), t);
  assert.equal(summary.returned,10); assert.equal(summary.active,1);
  const done=projectTranscriptRows(input.concat({id:'next',seq:100,kind:'text',role:'user',text:'下一个问题'}),true);
  assert.deepEqual(done.filter(r=>r.kind==='work').map(r=>r.work.latestExchange),[false,true]);
});

test('a live introduction stays above the work it announced; a later answer follows the work', () => {
  const rows = [{ id:'q',seq:1,kind:'text',role:'user',text:'check' },
    { id:'intro',seq:2,kind:'text',role:'assistant',narration:true,text:'I will inspect the report.' },
    { id:'call',seq:3,kind:'tool_use',role:'assistant',tool_name:'Read',tool_use_id:'r',tool_input:{file_path:'report.md'} }];
  assert.deepEqual(projectTranscriptRows(rows,true).map(r=>r.kind),['input','narration','work']);
  rows.push({ id:'answer',seq:4,kind:'text',role:'assistant',text:'The report is readable.' });
  assert.deepEqual(projectTranscriptRows(rows,false).map(r=>r.kind),['input','narration','work','reply']);
});

test('narration and natural action summaries alternate in source order across late receipts and live updates', () => {
  const items = [];
  function item(id, kind, fields) { items.push({id,seq:items.length + 1,kind,...fields}); }
  const say = (id, text) => item(id, 'text', {role:'assistant',narration:true,text});
  const call = (id, name, input = {}) => item(id,'tool_use',{tool_name:name,tool_use_id:id,tool_input:input,ts:'2026-11-23T03:00:00Z'});
  const ok = id => item(id+'-result','tool_result',{tool_use_id:id,text:'ok',outcome:{status:'ok'}});
  item('q','text',{role:'user',text:'Check the bridge'});
  say('intro','Inspect the bridge');
  call('read','Read',{file_path:'bridge.ts'}); ok('read');
  call('search','Grep',{pattern:'gateway'}); ok('search');
  say('update','Now compare the protocol');
  call('img','view_image'); ok('img');
  call('one','Bash',{command:'git status'}); ok('one');
  call('two','Bash',{command:'git diff'});
  const before = projectTranscriptRows(items,true);
  assert.deepEqual(before.map(r=>r.kind),['input','narration','work','narration','work','work']);
  const groups = before.filter(r => r.kind === 'work');
  assert.equal(activityHeading(groups[0].work,t).title,'读取了 1 个文件、搜索了 1 次');
  assert.equal(activityHeading(groups[1].work,t).title,'查看了 1 张图像');
  const last = activityHeading(groups[2].work,t);
  assert.deepEqual([last.title,last.active,last.returned,last.total,last.startedAt],
    ['Bash · git diff',1,1,2,Date.parse('2026-11-23T03:00:00Z')]);
  say('later','The diff is still running');
  ok('two'); item('answer','text',{role:'assistant',text:'All checks done'});
  const after = projectTranscriptRows(items,false);
  assert.deepEqual(after.filter(r=>r.kind==='narration').map(r=>r.id),['intro','update','later']);
  assert.deepEqual(after.filter(r=>r.kind==='work').map(r=>r.id), groups.map(r=>r.id));
  const finished = activityHeading(after.filter(r=>r.kind==='work')[2].work,t);
  assert.deepEqual([finished.title,finished.active,finished.startedAt],['运行了 2 条命令',0,0]);
  assert.equal(after.at(-1).item.id, 'answer');
});

test('plan updates and compaction split operations, and a failed call stays distinct from successful actions', () => {
  const items=[
    {id:'q',seq:1,kind:'text',role:'user',text:'review'},
    {id:'a',seq:2,kind:'tool_use',tool_use_id:'a',tool_name:'Bash',tool_input:{command:'git diff'}},
    {id:'ra',seq:3,kind:'tool_result',tool_use_id:'a',text:'ok'},
    {id:'p',seq:4,kind:'tool_use',tool_use_id:'p',tool_name:'TodoWrite',tool_input:{todos:[{content:'review',status:'in_progress'}]}},
    {id:'rp',seq:5,kind:'tool_result',tool_use_id:'p',text:'accepted'},
    {id:'b',seq:6,kind:'tool_use',tool_use_id:'b',tool_name:'Read'},
    {id:'rb',seq:7,kind:'tool_result',tool_use_id:'b',is_error:true,text:'missing'},
    {id:'c',seq:8,kind:'notice',compact:{trigger:'auto'}},
  ];
  const rows=projectTranscriptRows(items,false);
  assert.deepEqual(rows.map(r=>r.kind),['input','work','work','work','event']);
  assert.equal(activityHeading(rows[1].work,t).title,'运行了 git diff');
  assert.equal(rows[2].work.inspectionSteps[0].id,'p');
  assert.equal(activityHeading(rows[3].work,t).status, 'failed');
  assert.equal(activityHeading(rows[3].work,t).failures, 1);
  assert.equal(rows[4].item.id,'c');
});


test('running headings identify the tools actually in flight and follow out-of-order receipts', () => {
  const items = [{id:'q',seq:1,kind:'text',role:'user',text:'inspect'}];
  const start = (id,name,input) => items.push({id,seq:items.length+1,kind:'tool_use',tool_name:name,tool_use_id:id,tool_input:input});
  const finish = id => items.push({id:id+'-ok',seq:items.length+1,kind:'tool_result',tool_use_id:id,text:'ok'});
  const heading = (live=true) => activityHeading(projectTranscriptRows(items,live).find(row => row.kind === 'work').work,t);
  start('a','Bash',{description:'核对输出',command:'node check.mjs'});
  assert.equal(heading().title,'Bash · 核对输出');
  start('r1','Read',{file_path:'lib/one.ts'}); start('r2','Read',{file_path:'lib/two.ts'});
  assert.equal(heading().title,'Read · lib/two.ts');
  start('s','Grep',{pattern:'result'});
  assert.equal(heading().title,'Grep · result');
  finish('r1'); finish('s'); finish('a');
  assert.equal(heading().title,'Read · lib/two.ts');
  assert.equal(heading().returned,3);
  finish('r2');
  assert.equal(heading().active,0);
  assert.doesNotMatch(heading().title,/正在运行/);
  start('m','functions.mcp__source__inspect',{});
  assert.equal(heading().title,'source · inspect');
  assert.equal(heading(false).active,0);
  assert.doesNotMatch(heading(false).title,/正在运行/);
});


test('recorded Bash and Edit calls identify themselves until their actual receipts arrive', () => {
  for (const [file,name] of [['claude-session','Bash'],['claude-tool-outcomes','Edit']]) {
    const {items}=replay(file);
    const use=items.find(i=>i.kind==='tool_use' && i.tool_name===name);
    const receipt=items.find(i=>i.kind==='tool_result' && i.tool_use_id===use.tool_use_id);
    const section = seq => projectTranscriptRows(items.filter(i=>i.seq<=seq),true)
      .find(row=>row.kind==='work' && row.work.inspectionSteps.some(s=>s.id===use.id)).work;
    assert.ok(receipt.seq>use.seq);
    const title = activityHeading(section(use.seq),t).title;
    if (name === 'Bash') assert.equal(title,use.tool_input.description);
    else assert.ok(title.includes(name));
    assert.equal(section(receipt.seq).inspectionSteps.find(s=>s.id===use.id).result.id, receipt.id);
    const after = activityHeading(section(receipt.seq),t);
    if (file === 'claude-session') {
      // Skill started during Bash in the recorded turn and its result arrives one sequence later.
      assert.equal(after.active,1);
      assert.ok(after.title.startsWith('Skill'));
      const skill=items.find(i=>i.kind==='tool_use' && i.tool_name==='Skill' && i.seq>use.seq && i.seq<receipt.seq);
      const landed=items.find(i=>i.kind==='tool_result' && i.tool_use_id===skill.tool_use_id);
      assert.equal(activityHeading(section(landed.seq),t).active,0);
    } else assert.equal(after.active,0);
  }
});

test('the tool sheet leads with the recorded failure or patch and keeps complete input available separately', () => {
  const steps = tools(project('claude-tool-outcomes'));
  const failure = steps.find(s => s.result.outcome.tail);
  const failedView = inspectTool({ step: failure, live: false }, t);
  assert.equal(failedView.status, 'failed');
  assert.equal(failedView.tab, 'tool_inspect_output');
  assert.equal(failedView.fields[0].text, failure.result.text);
  assert.equal(failedView.fields.find(f => f.key === 'ev_command').text, failure.use.tool_input.command);
  assert.ok(!failedView.fields.some(f => f.key === 'work_arguments'));
  assert.deepEqual(JSON.parse(workToolArguments(failure.use)), failure.use.tool_input);

  const edit = steps.find(s => s.result.outcome.patch);
  const confirmed = inspectTool({ step: edit, live: false }, t);
  assert.equal(confirmed.fields[0].text, edit.result.outcome.patch);
  assert.equal(confirmed.tab, 'tool_inspect_changes');
  assert.ok(!confirmed.fields.some(f => f.key === 'ev_proposed_diff'));
  const blocked = steps.find(s => s.result.outcome.status === 'blocked');
  const request = inspectTool({ step: blocked, live: false }, t);
  assert.equal(request.tab, 'tool_inspect_result');
  assert.equal(request.fields[0].text, blocked.result.text);
  const proposed = request.fields.find(f => f.key === 'ev_proposed_diff');
  assert.equal(proposed.text, blocked.use.tool_input.old_string.split('\n').map(l => '-' + l).join('\n') + '\n' +
    blocked.use.tool_input.new_string.split('\n').map(l => '+' + l).join('\n'));
  assert.ok(!request.fields.some(f => f.key === 'ev_patch'));

  const read = steps.find(s => s.result.outcome.read?.start);
  const file = inspectTool({ step: read, live: false }, t);
  assert.equal(file.tab, 'content');
  assert.equal(file.fileResult, true);
  assert.equal(file.firstLine, read.result.outcome.read.start);
  assert.ok(!file.fields.some(f => f.key === 'ev_files'));
  const unchanged = steps.find(s => s.result.outcome.read?.unchanged);
  assert.equal(inspectTool({ step: unchanged, live: false }, t).fileResult, false);
  const create = tools(project('codex-0.163')).find(s => s.use.tool_input.change === 'add');
  const created = inspectTool({ step: create, live: false }, t);
  assert.ok(created.fields.some(f => f.key === 'ev_proposed_content'));
  assert.notEqual(created.tab, 'tool_inspect_changes');
});

test('tool details distinguish pending, missing, empty and structured outputs', () => {
  const use = { id:'use', seq:1, kind:'tool_use', role:'assistant', tool_name:'FutureAction', tool_use_id:'call', tool_input:{ path:'example' } };
  const receipt = { id:'result', seq:2, kind:'tool_result', role:'user', tool_use_id:'call', outcome:{ status:'ok', exit_code:0 } };
  const make = (items, live = false) => {
    const step = tools(projectTranscriptRows(items, live))[0];
    return inspectTool({ step, live }, t);
  };
  assert.equal(make([use], true).status, 'working');
  assert.equal(make([use]).status, 'work_no_result');
  const empty = make([use, receipt]);
  assert.equal(empty.status, 'done');
  assert.equal(empty.hasOutput, false);
  assert.deepEqual(empty.fields, []);
  assert.deepEqual(make([use, { ...receipt, text:'out' }]).fields.map(f => f.text), ['out']);
  const path = 'C:\\work\\api.ts';
  const read = { ...use, tool_name:'Read', tool_input:{ file_path:path } };
  assert.equal(make([read, receipt]).title, 'api.ts');
  assert.equal(make([read, receipt]).path, path);
  assert.equal(make([read, receipt, { ...receipt, seq:3, id:'more', text:'another' }]).fileResult, false);
  const match = { ...receipt, text:'src/thing.ts:4:found' };
  assert.equal(make([{ ...use, tool_name:'functions.search_files' }, match]).fileSearch, true);
  assert.equal(make([{ ...use, tool_name:'WebSearch' }, match]).fileSearch, false);
});

test('the live group and its rows keep their tool artwork, including calls without a dedicated frame sequence', () => {
  const variants = { Bash:'command', 'functions.exec':'command', Read:'read', Grep:'search', Glob:'search', 'functions.web_search':'web',
    'functions.view_image':'scan', 'mcp__image_gen__imagegen':'generate', 'computer_use':'computer',
    'read_internal_knowledge':'knowledge', 'read_memory':'memory', Edit:undefined, Unknown:undefined };
  const events = [];
  for (const [name, motion] of Object.entries(variants)) {
    events.push({ id:name, seq:events.length+1, kind:'tool_use', role:'assistant', tool_use_id:name, tool_name:name });
    const group = projectTranscriptRows(events, true).find(row => row.kind === 'work' && row.work.inspectionSteps.some(step => step.id === name)).work;
    assert.equal(toolMotion(group.inspectionSteps.at(-1)), motion, name);
    assert.equal(activityHeading(group, t).motion, motion, name);
  }
  const row = tools(project('claude-tool-outcomes')).find(s => s.result.outcome.patch);
  assert.equal(toolMotion(row), undefined);
});

test('historical assistant receipts render as events while user text and fenced examples stay literal', () => {
  const browser = '::browser-complete{title="核对文件归属"}';
  const raw = "API Error: Request rejected (429) · You've hit your 5-hour limit · resets 8:40pm (America/Los_Angeles)";
  const items = [{ id:'q',seq:1,kind:'text',role:'user',text:browser },
    { id:'a',seq:2,kind:'text',role:'assistant',narration:true,text:browser },
    { id:'example',seq:3,kind:'text',role:'assistant',text:'```text\n'+browser+'\n```',narration:true },
    { id:'fail',seq:4,kind:'text',role:'assistant',text:raw }];
  const rows = projectTranscriptRows(items,false);
  assert.deepEqual(rows.map(r => r.kind), ['input','event','narration','event']);
  const browserView = agentEventDisplay(rows[1].item,t);
  assert.equal(browserView.title, '核对文件归属'); assert.equal(browserView.body, '');
  assert.equal(rows[0].item.text,browser);assert.ok(rows[2].item.text.includes(browser));
  const error = agentEventDisplay(rows[3].item,t);
  assert.equal(error.label,t('ev_limit'));assert.equal(error.body,raw);assert.equal(error.detailOnly,true);
  assert.match(error.meta,/8:40pm \(America\/Los_Angeles\)/);
  assert.notEqual(sessionIssueDisplay({turn_error:'ordinary failure'},t).label,t('ev_limit'));
  assert.equal(items[1].kind,'text');
});

test('large mixed groups show the total exactly once and failures color their tool without status copy', () => {
  const run = {id:'cmd',seq:1,kind:'tool_use',role:'assistant',tool_name:'Bash',tool_use_id:'cmd',
    tool_input:{command:'set -e\npython verify.py --all'}};
  const result = {id:'x',seq:2,kind:'tool_result',role:'user',tool_use_id:'cmd',is_error:true,outcome:{status:'failed',exit_code:1},
    text:'  File "test.py", line 3\nAssertionError: missing result\n(exit code 1)'};
  const work = projectTranscriptRows([run,result],false)[0].work;
  const heading = activityHeading({...work,inspectionSteps:[{kind:'fold',id:'old',toolCount:112,thoughtCount:2,
    item:{kind:'fold',id:'old',seq:0,fold:{errors:2}}},...work.inspectionSteps]},t);
  assert.equal(heading.title,'113 次工具调用');assert.equal(heading.failures,1);assert.equal(heading.status,'failed');
  assert.equal(workTarget(run),'python verify.py --all');
  assert.equal(workRowExcerpt(work.inspectionSteps[0],'failed'),'AssertionError: missing result');
  assert.match(accessibleToolState('failed',t),/失败/);
  assert.equal(accessibleToolState('done',t),t('tool_inspect_returned'));
});

test('thinking-only history and live frames add no disclosure, including after reopening a fold', () => {
  const thought = {kind:'thinking',role:'assistant',id:'t',seq:2,text:'Private reasoning'};
  const fold = {kind:'fold',id:'fold:t',seq:2,fold:{items:3,tools:0,thoughts:3,latest_thinking:thought}};
  const input = {kind:'text',role:'user',id:'q',seq:1,text:'question'};
  for (const hidden of [thought, fold]) {
    assert.deepEqual(projectTranscriptRows([input,hidden],true).map(r=>r.kind),['input']);
    const rows = projectTranscriptRows([input,hidden,{kind:'text',role:'assistant',id:'a',seq:9,text:'answer'}],false);
    assert.deepEqual(rows.map(r=>r.kind),['input','reply']);
    assert.equal(rows[1].processIds,undefined);
  }
  const proseFold = {...fold, fold:{...fold.fold,items:4}};
  assert.ok(projectTranscriptRows([input,proseFold],false).some(r=>r.kind==='work'));
});

test('recorded Codex question replies keep the question above the answer and preserve trailing attachments', () => {
  const base = {id:'reply', seq:1, kind:'text', role:'user'};
  const show = (text, pairs) => projectTranscriptRows([{...base, text, question_replies:pairs}], false)[0].item.text;
  const files = '\n\n<attached_images>\n/tmp/demo.png\n</attached_images>';
  assert.equal(show('Yes' + files, [{question:'Install SDK?\nOpen preview?'}]), '> Install SDK?\n> Open preview?\n\nYes' + files);
  const pairs = [{question:'Which platform?',answer:'HarmonyOS'}, {question:'Where?',answer:'Local\nPhone'}];
  assert.equal(show('Which platform?：HarmonyOS\nWhere?：Local\nPhone' + files, pairs), '> Which platform?\n\nHarmonyOS\n\n> Where?\n\nLocal\nPhone' + files);
  assert.equal(show('A normal reply', undefined), 'A normal reply');
  assert.equal(show('Another reply', pairs), 'Another reply');
});

test('live inventory displays actual in-flight calls while the session list lags and durable items take over once', () => {
  const prompt = {id:'u#0', seq:1, kind:'text', role:'user', text:'test'};
  const live = [{id:'c1', name:'functions.exec'}, {id:'c2', name:'Read'}];
  const work = rows => rows.filter(row => row.kind === 'work').flatMap(row => row.work.inspectionSteps);
  const initial = work(projectTranscriptRows([prompt], false, live));
  assert.deepEqual(initial.map(step => [step.use.tool_name, workToolStatus(step, false)]),
    [['functions.exec', 'working'], ['Read', 'working']]);
  const use = {id:'real', seq:2, kind:'tool_use', role:'assistant', tool_use_id:'c1', tool_name:'Bash', tool_input:{command:'pwd'}};
  const result = {id:'result', seq:3, kind:'tool_result', role:'user', tool_use_id:'c1', text:'ok'};
  const mixed = work(projectTranscriptRows([prompt, use], false, live));
  assert.deepEqual(mixed.map(step => step.id), ['real','c2#0']);
  assert.equal(workToolStatus(mixed[0], false), 'working');
  assert.equal(mixed[0].use.tool_input.command, 'pwd');
  assert.equal(use.live_tool, undefined);
  const done = work(projectTranscriptRows([prompt, use, result], false, live));
  assert.equal(done.filter(step => step.use.tool_use_id === 'c1').length, 1);
  assert.equal(workToolStatus(done[0], false), 'done');
  assert.equal(work(projectTranscriptRows([prompt, result], false, live)).some(step => step.use.tool_use_id === 'c1'), false);
  assert.equal(work(projectTranscriptRows([prompt], false, [])).length, 0);
  assert.equal(workToolStatus(work(projectTranscriptRows([prompt, use], true))[0], true), 'working');
});

test('a serial successful retry clears only its own header failure across narration, preserving the original receipt', () => {
  const items = [];
  function start(id,name,input) {
    items.push({id,seq:items.length+1,kind:'tool_use',tool_name:name,tool_input:input,tool_use_id:id});
  }
  function finish(id,status,text='') {
    items.push({id:id+'-out',seq:items.length+1,kind:'tool_result',tool_use_id:id,is_error:status==='failed',outcome:{status},text});
  }
  function headings() { return projectTranscriptRows(items,true).filter(row=>row.kind==='work').map(row=>activityHeading(row.work,t)); }
  start('a','Read',{file_path:'a.ts',offset:-1});finish('a','failed','Invalid arguments');
  start('unrelated','Read',{file_path:'b.ts'});finish('unrelated','failed');
  items.push({id:'comment',seq:items.length+1,kind:'text',role:'assistant',narration:true,text:'Retrying with a valid offset'});
  start('retry','Read',{file_path:'a.ts',offset:1});
  assert.equal(headings()[0].failures,2);
  finish('retry','ok');
  assert.equal(headings()[0].failures,1);
  const steps=tools(projectTranscriptRows(items,false));
  assert.equal(workToolStatus(steps[0],false),'failed');
  assert.equal(steps[0].recovered,true);
  assert.equal(steps[1].recovered,undefined);
  start('b2','Read',{file_path:'b.ts'});finish('b2','ok');
  assert.deepEqual([headings()[0].failures,headings()[0].status],[0,'']);
});

test('corrected parameters need a successful subsequent attempt; parallel calls do not recover each other', () => {
  const rows = [
    {id:'a',seq:1,kind:'tool_use',tool_name:'mcp__docs__list',tool_use_id:'a',tool_input:{limit:'many'}},
    {id:'b',seq:2,kind:'tool_use',tool_name:'mcp__docs__list',tool_use_id:'b',tool_input:{limit:10}},
    {id:'ar',seq:3,kind:'tool_result',tool_use_id:'a',is_error:true,text:'Invalid parameters'},
    {id:'br',seq:4,kind:'tool_result',tool_use_id:'b',outcome:{status:'ok'}},
    {id:'c',seq:5,kind:'tool_use',tool_name:'mcp__docs__list',tool_use_id:'c',tool_input:{limit:5}},
    {id:'cr',seq:6,kind:'tool_result',tool_use_id:'c',outcome:{status:'ok'}}
  ];
  const group = items => activityHeading(projectTranscriptRows(items,false)[0].work,t);
  assert.equal(group(rows.slice(0,4)).failures,1);
  const serial=rows.filter(item=>!['b','br'].includes(item.id));
  assert.equal(group(serial.slice(0,-1)).failures,1);
  assert.deepEqual([group(serial).failures,group(serial).total,group(serial).status],[0,2,'done']);
  const separate=[...serial.slice(0,2),{id:'user',seq:4,kind:'text',role:'user',text:'a new task'},...serial.slice(2)];
  assert.equal(group(separate).failures,1);
});

test('a corrected retry of a recorded blocked Claude Edit leaves the guard message on that row', () => {
  const history = replay('claude-tool-outcomes').items;
  const denied = history.find(item => item.kind === 'tool_result' && item.outcome?.status === 'blocked');
  const edit = history.find(item => item.kind === 'tool_use' && item.tool_use_id === denied.tool_use_id);
  const items = [edit, denied,
    {...edit, id:'fixed', seq:denied.seq+1, tool_use_id:'fixed', tool_input:{...edit.tool_input, old_string:'corrected selection'}},
    {kind:'tool_result', id:'accepted', seq:denied.seq+2, tool_use_id:'fixed', outcome:{status:'ok'}}];
  const row = projectTranscriptRows(items,false)[0];
  assert.equal(activityHeading(row.work,t).status,'done');
  assert.equal(workToolStatus(row.work.inspectionSteps[0],false),'work_blocked');
  assert.equal(row.work.inspectionSteps[0].result.text,denied.text);
});

test('mixed historical error counts are neutral until the actual receipts are available', () => {
  const fold = errors => ({id:'f',seq:1,kind:'fold',fold:{tools:3,errors,through_seq:8,through_id:'end'}});
  const group = items => activityHeading(projectTranscriptRows(items,false)[0].work,t);
  assert.equal(group([fold(1)]).failures,0);
  assert.equal(group([fold(3)]).failures,3);
  assert.equal(group([fold(3),{kind:'tool_use',id:'c',seq:9,tool_name:'Read',tool_use_id:'c'},
    {kind:'tool_result',id:'r',seq:10,tool_use_id:'c',outcome:{status:'ok'}}]).failures,0);
});

test('running and historical tool headings exclude thought text and opaque executor names', () => {
  const items=[{id:'q',seq:1,kind:'text',role:'user',text:'review'},
    {id:'old',seq:2,kind:'thinking',role:'assistant',text:'**First action**'},
    {id:'note',seq:3,kind:'thinking',role:'assistant',text:'**Checking GIF frame mismatch**'},
    {id:'exec',seq:4,kind:'tool_use',role:'assistant',tool_name:'functions.exec',tool_use_id:'call'}];
  const group=(items,live=true)=>projectTranscriptRows(items,live).filter(r=>r.kind==='work').at(-1).work;
  assert.equal(activityHeading(group(items),t).title,'正在执行脚本');
  const receipt={id:'ok',seq:5,kind:'tool_result',tool_use_id:'call',outcome:{status:'ok'}};
  assert.equal(activityHeading(group([...items,receipt],false),t).title,'1 次工具调用');
  for (const live of [true,false]) {
    const noThought=[items[0],items[3],...(live?[]:[receipt])];
    assert.equal(activityHeading(group(noThought,live),t).title, live ? '正在执行脚本' : '1 次工具调用');
  }
  const newer=[...items,receipt,{id:'n',seq:6,kind:'text',role:'assistant',text:'new passage',narration:true},
    {...items[3],id:'next',seq:7,tool_use_id:'next',tool_input:{title:'核对真机图标'}}];
  assert.equal(activityHeading(group(newer),t).title,'核对真机图标');
  assert.equal(group(newer).phaseTitle,'');
});

test('a Codex completion includes tools before unmarked steering messages and pages back to the named prompt', () => {
  const input=id=>({id:id+'#0',kind:'text',role:'user',text:id});
  const call=id=>[{id,kind:'tool_use',tool_name:'Bash',tool_use_id:id},{id:id+'r',kind:'tool_result',tool_use_id:id,text:'ok'}];
  const items=[input('q'),{id:'update',kind:'text',role:'assistant',narration:true,text:'initial update'},...call('first'),
    input('steer'),...call('second'),input('again'),...call('third'),
    {id:'final',kind:'text',role:'assistant',text:'done'},{id:'duration',kind:'turn_duration',
      turn_duration:{turn_id:'turn',source:'codex_task_complete',user_item_id:'q#0',duration_ms:12345}}].map((x,i)=>({...x,seq:i+1}));
  for (const start of [0,4,7,10]) {
    const rows=projectTranscriptRows(items.slice(start),false),reply=rows.find(r=>r.kind==='reply');
    assert.equal(reply.duration.id,'duration');assert.equal(reply.partialProcess,start>0);
    if (start===0) {assert.deepEqual(reply.leadIds,['update']);assert.ok(!reply.processIds.includes('update'));}
    const ids=new Set(reply.processIds);
    const calls=tools(rows.filter(r=>ids.has(r.id))).map(step=>step.id);
    assert.deepEqual(calls,items.slice(start).filter(i=>i.kind==='tool_use').map(i=>i.id));
    assert.ok(rows.filter(r=>r.kind==='input').every(r=>!ids.has(r.id)));
  }
  const late=[input('first-q'),...call('one'),{id:'a',role:'assistant',kind:'text',text:'first result'},
    input('new-q'),...call('two'),{id:'old-end',kind:'turn_duration',turn_duration:{source:'codex_task_complete',user_item_id:'first-q#0',duration_ms:1}},
    {id:'b',kind:'text',role:'assistant',text:'second result'}].map((x,i)=>({...x,seq:i+1}));
  const replies=projectTranscriptRows(late,false).filter(r=>r.kind==='reply');
  assert.deepEqual(replies.map(r=>[r.item.id,r.duration?.id]),[['a','old-end'],['b',undefined]]);
  assert.ok(!replies[0].processIds.some(id=>replies[1].processIds.includes(id)));
});

test('completed phases keep parallel calls together, preserve receipts and skip empty caption-only batches', () => {
  const note=(id,text)=>({id,kind:'thinking',role:'assistant',text});
  const call=id=>({id,kind:'tool_use',tool_name:'Bash',tool_use_id:id});
  const end=id=>({id:id+'r',kind:'tool_result',tool_use_id:id});
  const items=[note('h','**Inspect**'),call('a'),call('b'),end('b'),
    note('same','**Inspect**'),end('a'),note('h2','**Edit**'),note('h3','**Verify**'),call('c'),end('c'),
    {id:'reply',kind:'text',role:'assistant',text:'done'}].map((x,i)=>({...x,seq:i+1}));
  const groups=projectTranscriptRows(items,false).filter(r=>r.kind==='work');
  assert.deepEqual(groups.map(r=>r.work.sectionTitle),['','']);
  assert.deepEqual(groups.map(r=>r.work.inspectionSteps.filter(s=>s.kind==='tool').map(s=>[s.id,s.result.id])),
    [[['a','ar'],['b','br']],[['c','cr']]]);
  assert.equal(new Set(groups.map(r=>r.id)).size,2);
  assert.equal(projectTranscriptRows(items,true).filter(r=>r.kind==='work').length,1);
});

test('Claude public progress separates actual calls while private thoughts never name a group', () => {
  const source = [
    { kind:'text', role:'user', text:'Review' },
    { kind:'thinking', role:'assistant', text:'Private thought' },
    { kind:'tool_use', tool_name:'Bash', tool_use_id:'a', tool_input:{description:'Read configuration'} },
    { kind:'tool_result', tool_use_id:'a', text:'ok' },
    { kind:'text', role:'assistant', text:'First public update', narration:true },
    { kind:'text', role:'assistant', text:'Checking the sample' },
    { kind:'thinking', role:'assistant', text:'Another thought' },
    { kind:'tool_use', tool_name:'Bash', tool_use_id:'b', tool_input:{description:'Wait for statistics'} },
  ].map((item,i) => ({ id:String(i), seq:i+1, ...item }));
  const rows = projectTranscriptRows(source,true,[],'claude');
  assert.deepEqual(rows.map(row => row.kind),['input','work','narration','narration','work']);
  const head = activityHeading(rows.at(-1).work,t);
  assert.deepEqual([head.title,head.active,head.motion],['Wait for statistics',1,'command']);
  assert.equal(rows.at(-1).work.phaseTitle,'');
  const done = [...source,{id:'r',seq:9,kind:'tool_result',tool_use_id:'b',text:'ok'},
    {id:'f',seq:10,kind:'text',role:'assistant',text:'Final answer'}];
  const reply = projectTranscriptRows(done,false,[],'claude').at(-1);
  assert.equal(reply.item.id,'f');
  assert.equal(reply.processIds.length,4);
});

test('a reloaded Claude batch names the commands recorded in its fold', () => {
  const fold = {id:'fold-1',seq:1,kind:'fold',fold:{tools:5,tool_names:['Bash']}};
  const [row] = projectTranscriptRows([fold],false,[],'claude');
  assert.equal(activityHeading(row.work,t).title,'运行了 5 条命令');
});

test('Claude keeps the latest command description across receipts, parallel calls and a folded history reload', () => {
  const prompt = {id:'q',seq:1,kind:'text',role:'user',text:'检查智能回复'};
  const use = (id,seq,description) => ({id,seq,kind:'tool_use',role:'assistant',tool_name:'Bash',tool_use_id:id,tool_input:{description}});
  const ok = (id,seq) => ({id:id+'-r',seq,kind:'tool_result',role:'user',tool_use_id:id,text:'ok'});
  const heading = (items,live=true) => activityHeading(projectTranscriptRows([prompt,...items],live,[],'claude').find(r=>r.kind==='work').work,t);
  const first=use('a',2,'查找判断函数'), last=use('b',3,'确认判断用的是哪个模型');
  const stages=[[first,last],[first,last,ok('a',4)],[first,last,ok('a',4),ok('b',5)]];
  for (const [i,items] of stages.entries()) {
    const h=heading(items); assert.deepEqual([h.title,h.active,h.returned],[last.tool_input.description,2-i,i]);
  }
  assert.equal(heading([first,last,ok('b',4)]).title,first.tool_input.description);
  assert.equal(heading([...stages[2],use('c',6,'') ,ok('c',7)]).title,'运行了 3 条命令');
  const fold={id:'fold:a',seq:2,kind:'fold',role:'assistant',text:last.tool_input.description,
    fold:{tools:2,items:4,tool_names:['Bash'],through_seq:5,through_id:'b-r'}};
  for (const live of [true,false]) {
    assert.equal(heading(stages[2],live).title,last.tool_input.description);
    assert.equal(heading([fold],live).title,last.tool_input.description);
    assert.equal(heading([fold,use('c',6,'检查下一步'),ok('c',7)],live).title,'检查下一步');
  }
});

test('Claude next-step history retains the chosen reply after its question, including on reload', () => {
  const q={id:'q',seq:1,kind:'text',role:'user',text:'Review'};
  const use={id:'w',seq:2,kind:'tool_use',role:'assistant',tool_use_id:'w',tool_name:'mcp__ccd_turn__wrap_up',
    tool_input:{recap:'Tests passed.',ask:'Commit changes?',actions:[{action:'commit'},{label:'Not yet'}]}};
  const result={id:'ok',seq:3,kind:'tool_result',tool_use_id:'w',text:'Shown in the next-step box. Write your closing answer.'};
  const final={id:'done',seq:4,kind:'text',role:'assistant',text:'All checks passed.'};
  const rows=(a,live=false)=>projectTranscriptRows(a,live,[],'claude');
  assert.deepEqual(rows([q,use,result,final]).map(r=>r.kind),['input','reply','next_step']);
  assert.deepEqual(rows([q,use,result,final]).at(-1).proposal.choices,[{action:'commit',label:'Commit changes'},{label:'Not yet'}]);
  const next = {...q,id:'next',seq:5,text:'Not yet'}, follow = {...final,id:'follow',seq:6,text:'Understood.'};
  for (const live of [true,false]) {
    const history = rows([q,use,result,final,next,follow],live);
    const i = history.findIndex(r=>r.kind==='next_step');
    assert.ok(i>0); assert.equal(history[i-1].item.id,final.id); assert.equal(history[i+1].item.id,next.id);
    assert.equal(history[i].proposal.selected,'Not yet');
  }
  const translated = projectTranscriptRows([q,use,result,final,{...next,text:'提交更改'}],false,[],'claude',action=>action==='commit'?'提交更改':'');
  assert.equal(translated.find(row=>row.kind==='next_step').proposal.selected,'提交更改');
  assert.ok(!rows([q,use,result,final,{...next,text:'Another request'},follow,{...next,id:'later',seq:7}]).some(row=>row.kind==='next_step'));
  for(const items of [[q,use],[q,use,result],[q,use,{...result,is_error:true},final],
    [q,use,{...result,text:'Nothing is pending, so the app draws no box.'},final],
    [q,use,result,final,{...q,id:'next',seq:5}],
    [q,{...use,tool_input:{...use.tool_input,actions:[{label:'/release'}]}},result,final]]) {
    assert.ok(!rows(items).some(r=>r.kind==='next_step'));
  }
  assert.ok(!rows([q,use,result,final],true).some(r=>r.kind==='next_step'));
  assert.equal(rows([q,use,{...result,text:'Shown in the next-step box. The box ends your turn.'}]).at(-1).kind,'next_step');
});

test('live terminal action outranks a wrapper and an old phase, and returns to neutral on failure', () => {
  const q={id:'u',seq:1,role:'user',kind:'text',text:'Review'};
  const use={id:'label',seq:2,kind:'tool_use',tool_name:'mcp__codex_app__update_running_summary',tool_use_id:'label',tool_input:{labels:['Previous phase']}};
  const result={id:'r',seq:3,kind:'tool_result',tool_use_id:'label',outcome:{status:'ok'}};
  const base=[q,use,result];
  const h=(items,calls)=>activityHeading(projectTranscriptRows(items,true,calls,'codex').at(-1).work,t);
  for (const order of [[{id:'p',name:'Bash'},{id:'w',name:'exec'}],[{id:'w',name:'exec'},{id:'p',name:'Bash'}]]) {
    const head=h(base,order);
    assert.equal(head.title,'正在运行命令'); assert.equal(head.kind,'shell');
  }
  const command={id:'c',seq:4,kind:'tool_use',tool_use_id:'p',tool_name:'Bash',tool_input:{command:'python3 update-hub.py'}};
  assert.equal(h([...base,command],[{id:'p',name:'Bash'}]).title,'Bash · python3 update-hub.py');
  const done=h([...base,command,{id:'fail',seq:5,kind:'tool_result',tool_use_id:'p',outcome:{status:'failed'}}],[{id:'p',name:'Bash'}]);
  assert.equal(done.active,0); assert.equal(done.failures,1);
});

test('a refused Claude Bash retry can move an inline script to a file for the same described task', () => {
  const use=(id,seq,command)=>({id,seq,kind:'tool_use',tool_use_id:id,tool_name:'Bash',tool_input:{command,description:'比对返回的账户名称'}});
  const receipt=(id,seq,failed)=>({id:id+'r',seq,kind:'tool_result',tool_use_id:id,
    text:failed?'Claude Code did not run this command: this shell is too old':'ok',outcome:{status:failed?'failed':'ok'}});
  const items=[use('a',1,'S=/tmp/scratch python -'),receipt('a',2,true),
    use('b',3,'cd /tmp && python compare.py'),receipt('b',4,false)];
  const row=projectTranscriptRows(items,false,[],'claude')[0];
  assert.equal(activityHeading(row.work,t).failures,0);
  assert.equal(row.work.inspectionSteps[0].result.outcome.status,'failed');
});
