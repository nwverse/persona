import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { agentRowPreview, agentRowTone, closeoutPreview, failurePreview, plainPreview, runningSubagents, waitsInBackground, workPreview }
  from '../entry/src/main/ets/core/AgentRowPreview.ts';

const catalog = JSON.parse(readFileSync(new URL('../entry/src/main/resources/base/element/string.json', import.meta.url), 'utf8'));
const words = new Map(catalog.string.map((item) => [item.name, item.value]));
const tr = (key) => { assert.ok(words.has(key), 'missing string ' + key); return words.get(key); };
const now = Date.parse('2026-11-24T10:00:00Z');

test('the row line follows the iOS order, first match wins', () => {
  const all = { awaiting_first_message: true, attention_reason: 'Approve the plan', failure_reason: '⚠️ Build failed',
    awaits_reply: true, asks: 'Which branch?', work: { phase: 'done', line: 'Shipped' }, last_text: 'Last words' };
  assert.equal(agentRowPreview(all, 'idle', false, now, tr), 'Waiting for its first message');
  assert.equal(agentRowPreview(all, 'needs_you', false, now, tr), 'Approve the plan', 'Needs you skips the first-message line');
  const noFirst = { ...all, awaiting_first_message: false };
  assert.equal(agentRowPreview(noFirst, 'idle', false, now, tr), 'Build failed', 'the attention reason is only for Needs you');
  assert.equal(agentRowPreview({ ...noFirst, failure_reason: '' }, 'idle', false, now, tr), 'Which branch?');
  assert.equal(agentRowPreview({ ...noFirst, failure_reason: '' }, 'idle', true, now, tr), '✓ Shipped', 'a muted row does not ask');
  assert.equal(agentRowPreview({ last_text: '**Done.** See [the PR](https://example.com/pr)\n\n- tests pass' }, 'idle', false, now, tr),
    'Done. See the PR tests pass');
  assert.equal(agentRowPreview({ failure_reason: 'Crashed', last_text: 'still going' }, 'working', false, now, tr), 'still going',
    'a working row does not show an old failure');
});

test('an awaited reply prefers the triaged question to the last reply', () => {
  const data = { awaits_reply: true, asks: 'Ship it?', work: { phase: 'blocked', on: 'user', line: 'Needs a branch name' } };
  assert.equal(agentRowPreview(data, 'idle', false, now, tr), 'Needs a branch name');
  assert.equal(agentRowPreview({ ...data, work: { phase: 'blocked', on: 'ci', line: 'Waits for CI' } }, 'idle', false, now, tr), 'Ship it?');
});

test('work lines say when the hub acts next', () => {
  const at = (minutes) => new Date(now + minutes * 60_000).toISOString();
  assert.equal(workPreview({ work: { phase: 'blocked', on: 'time', line: 'Waits for the limit', wake_at: at(12) } }, 'idle', now, tr),
    'Waits for the limit (waking in 12 min)');
  assert.equal(workPreview({ work: { phase: 'blocked', on: 'transient', line: 'Service down', wake_at: at(125) } }, 'idle', now, tr),
    'Service down (retrying in 2 hr 5 min)');
  assert.equal(workPreview({ work: { phase: 'blocked', on: 'time', line: 'Soon', wake_at: at(0.5) } }, 'idle', now, tr), 'Soon (waking now)');
  assert.equal(workPreview({ work: { phase: 'blocked', line: 'Hourly', wake_at: at(60) } }, 'idle', now, tr), 'Hourly (waking in 1 hr)');
  assert.equal(workPreview({ work: { phase: 'working', line: 'Busy' } }, 'idle', now, tr), '');
  assert.equal(workPreview({ work: { phase: 'done', line: 'Done' } }, 'working', now, tr), '');
});

test('closeouts add where merged and deployed stand, marking a claim the hub found untrue', () => {
  const closeout = (fields, work = {}) => ({ work: { phase: 'done', ...work, closeout: { status: 'done', ...fields } } });
  assert.equal(closeoutPreview(closeout({ merged: 'yes', deployed: 'no' }), 'idle', now, tr), 'Reported done · merged · not deployed');
  assert.equal(closeoutPreview(closeout({ merged: 'yes', checked: { merged: 'yes' } }, { line: 'Fixed sign-in' }), 'idle', now, tr),
    '✓ Fixed sign-in · merged ✓');
  assert.equal(closeoutPreview(closeout({ merged: 'yes', checked: { merged: 'no' } }), 'idle', now, tr), "Reported done · ✗ says merged, isn't");
  assert.equal(closeoutPreview(closeout({ status: 'blocked', waiting: 'a review' }), 'idle', now, tr), 'Blocked on: a review');
  assert.equal(closeoutPreview(closeout({ status: 'not_done' }), 'idle', now, tr), 'Not done');
  assert.equal(closeoutPreview(closeout({ status: 'later' }), 'idle', now, tr), '', 'an unknown status shows no closeout');
  assert.equal(closeoutPreview(closeout({}), 'working', now, tr), '', 'a new turn has no closeout yet');
});

test('failure notices lose the warning sign and plain text loses Markdown', () => {
  assert.equal(failurePreview(' ⚠️  Out of credits '), 'Out of credits');
  assert.equal(plainPreview('# Title\n> quoted\n```\ncode\n```\n`npm test` passed'), 'Title quoted code npm test passed');
});

test('state marks: Needs you survives a mute, background waiting is its own mark', () => {
  assert.equal(agentRowTone({}, 'needs_you', true), 'needs_you');
  assert.equal(agentRowTone({}, 'unread', true), '');
  assert.equal(agentRowTone({}, 'unread', false), 'unread');
  assert.equal(agentRowTone({ background_waiting: true }, 'working', false), 'background');
  assert.equal(agentRowTone({ background_waiting: true, children: { working: 1 } }, 'working', false), 'working');
  assert.equal(waitsInBackground({ background_waiting: true, children: { needs_you: 1 } }, 'working'), false);
  assert.equal(agentRowTone({}, 'idle', false), '');
});

test('running subagents never count one twice', () => {
  assert.equal(runningSubagents({ session_id: 'p', subagents: [{ id: 'a', running: true }, { id: 'a', running: true }, { id: 'b', running: false }] }), 1);
  assert.equal(runningSubagents({ subagents: [{ id: 'a', running: true }], children: { working: 3, sides_working: 1 } }), 2);
  assert.equal(runningSubagents({ waiting: false, running_subagents: 4, subagents: [{ id: 'a', running: true }] }), 4,
    'a summary row trusts the hub count over a stale list');
  assert.equal(runningSubagents({}), 0);
});

test('every preview string ships in all four resource sets', () => {
  const keys = [...words.keys()].filter((key) => key.startsWith('agent_preview_') || key === 'agent_subagents_running');
  assert.ok(keys.length >= 23);
  for (const locale of ['en_US', 'zh_CN', 'ja_JP']) {
    const names = new Set(JSON.parse(readFileSync(new URL('../entry/src/main/resources/' + locale + '/element/string.json', import.meta.url), 'utf8'))
      .string.map((item) => item.name));
    for (const key of keys) assert.ok(names.has(key), locale + ' ' + key);
  }
});
