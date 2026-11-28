import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { messageSentTime, sessionContextUsage, pendingDesktopProposal, waitsOnClaudeDesktop } from '../entry/src/main/ets/core/ConversationMetadata.ts';
import { projectTranscriptRows } from '../entry/src/main/ets/core/Domain.ts';

test('only a waiting desktop session without a native approval or terminal needs the screen handoff', () => {
  const row = {source_kind:'claude_desktop',status:'blocked',state:'needs_you',capabilities:{read:true,send:true}};
  assert.equal(waitsOnClaudeDesktop(row),true);
  for (const fields of [{status:'busy'}, {status:'done'}, {state:'offline'}, {deleted:true}, {source_kind:'claude_cli'},
    {capabilities:{approve:true}}, {capabilities:{terminal:true}}]) {
    assert.equal(waitsOnClaudeDesktop({...row,...fields}),false);
  }
  assert.equal(waitsOnClaudeDesktop(undefined),false);
});

test('the handoff describes only the latest unresolved desktop start call, and drops it after its receipt', () => {
  const start = {kind:'tool_use',role:'assistant',id:'start',seq:1,tool_use_id:'s',tool_name:'mcp__ccd_session__start_session',
    tool_input:{title:'Tell the owner when a run waits',use_worktree:true,source_branch:'main',branch_name:'start-wait-notice'}};
  const receipt = {kind:'tool_result',role:'user',id:'result',seq:2,tool_use_id:'s',text:'Proposed'};
  const work = items => projectTranscriptRows(items,true,[],'claude').findLast(row=>row.kind==='work')?.work;
  assert.deepEqual(pendingDesktopProposal(work([start])),{title:start.tool_input.title,branch:'main → start-wait-notice'});
  assert.equal(pendingDesktopProposal(work([start,receipt])),undefined);
  assert.equal(pendingDesktopProposal(work([start,{...start,id:'new',seq:2,tool_use_id:'n',tool_name:'Read'}])),undefined);
  assert.equal(pendingDesktopProposal({...work([start]),latestExchange:false}),undefined);
});

test('context is the latest input divided by this model window and can fall after compaction', () => {
  const reported = { context_tokens:335405, context_window:500000, output_tokens:9200000 };
  assert.deepEqual(sessionContextUsage(reported), { tokens:335405, capacity:500000, percent:67 });
  assert.equal(sessionContextUsage({ ...reported, context_tokens:24000 }).percent, 5);
  assert.equal(sessionContextUsage({ ...reported, context_window:200000 }).percent, 100);
  assert.equal(sessionContextUsage({ ...reported, context_tokens:1 }).percent, 0);
});

test('missing, cleared, unknown and malformed window reports never imply zero usage', () => {
  for (const session of [undefined, {}, { context_window:500000 }, { context_tokens:1200 },
    { context_tokens:0, context_window:500000 }, { context_tokens:100, context_window:0 },
    { context_tokens:-1, context_window:500000 }, { context_tokens:100, context_window:Infinity },
    { context_tokens:'1000', context_window:2000 }, { context_tokens:1, context_window:5, usage_unknown:true }]) {
    assert.equal(sessionContextUsage(session), undefined);
  }
});

test('message time uses the recorded instant, local date and chosen display language', () => {
  const at = new Date(2026, 10, 23, 11, 39).getTime();
  for (const language of ['zh-CN', 'en-US', 'ja-JP', undefined]) {
    const shown = messageSentTime(at, language);
    assert.match(shown, /2026/);
    assert.match(shown, /11:39/);
  }
  assert.notEqual(messageSentTime(at, 'zh-CN'), messageSentTime(at, 'en-US'));
  for (const missing of [0, -1, NaN, Infinity, 9e15]) assert.equal(messageSentTime(missing, 'zh-CN'), '');
});

test('selectable message text receives its own tap while links, quote controls and attachments keep their actions', () => {
  const component = name => readFileSync(new URL('../entry/src/main/ets/components/' + name + '.ets', import.meta.url), 'utf8');
  const chat = component('Conversation'), markdown = component('MarkdownText');
  assert.match(chat, /onBodyTap:entry\.item\.message!\.role === 'user' && entry\.item\.message!\.at > 0/);
  assert.match(markdown, /copyOption\(CopyOptions\.LocalDevice\)\.onClick\(this\.onBodyTap\)/);
  assert.match(markdown, /onClick\(\(\) => this\.onLink\(url\)\)/);
  assert.match(markdown, /id\('chat\.passage\.toggle\./);
  assert.match(chat, /else if \(!message\.text\) \{\s*Button[\s\S]*?'chat\.attachmentTime\./);
});
