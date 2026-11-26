import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { test } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { cleanUserText, stepsToItems } from '../lib/map.js'
import { createAgyRuntime } from '../lib/runtime.js'

const RAW = '11111111-2222-4333-8444-555555555555'
const SESSION = `agy-${RAW}`
const FAKE = fileURLToPath(new URL('./fake-acp-rewind.mjs', import.meta.url))
const AT = '2026-01-02T12:00:00Z'

const initialSteps = [
  { step_index: 0, type: 'USER_INPUT', source: 'USER_EXPLICIT', status: 'DONE', created_at: AT, content: 'Inspect context' },
  {
    step_index: 1, type: 'PLANNER_RESPONSE', source: 'MODEL', status: 'DONE', created_at: AT,
    model: 'gemini-4-neon-high', content: 'Done.',
    input_tokens: 120000, cache_read_tokens: 210000, output_tokens: 450, context_window: 1048576,
  },
]

async function fixture(t, promptPlan) {
  const home = mkdtempSync(join(tmpdir(), 'agy-compact-'))
  const config = join(home, 'config')
  const assistantDir = join(home, '.persona', 'assistant')
  const logs = join(config, 'brain', RAW, '.system_generated', 'logs')
  mkdirSync(assistantDir, { recursive: true })
  mkdirSync(logs, { recursive: true })
  const transcript = join(logs, 'transcript_full.jsonl')
  writeFileSync(transcript, initialSteps.map((s) => JSON.stringify(s)).join('\n') + '\n')
  writeFileSync(join(home, 'calls.jsonl'), '')
  writeFileSync(join(home, 'trajectory.json'), JSON.stringify({
    lazyService: true,
    status: 'CASCADE_RUN_STATUS_IDLE',
    trajectory: { cascadeId: RAW, steps: [] },
    promptPlan,
  }))
  const db = new DatabaseSync(join(config, 'conversation_summaries.db'))
  db.exec('CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, status TEXT, not_fully_idle INTEGER, killed INTEGER, step_count INTEGER, title TEXT, workspace_uris TEXT, last_modified_time TEXT)')
  db.prepare('INSERT INTO conversation_summaries VALUES (?, ?, 0, 0, ?, ?, ?, ?)')
    .run(RAW, 'CASCADE_RUN_STATUS_IDLE', initialSteps.length, 'Main', JSON.stringify([pathToFileURL(assistantDir).href]), new Date().toISOString())
  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({
    home, agyDir: config, agyBin: process.execPath, agyArgs: [FAKE, home, 'acp'],
    personadBin: process.execPath, socketPath: node.path, log: () => {},
    transient401RetryDelayMs: 10, exportPollMs: 20,
  })
  t.after(async () => { await runtime.stop(); db.close(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  const row = () => node.last('runtime.sessions')?.sessions.find((entry) => entry.session_id === SESSION)
  await until('session listed', () => row()?.context_tokens === 330000)
  const calls = () => readFileSync(join(home, 'calls.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  const items = () => node.peer.call('session.items', { session_id: SESSION })
  const appendCheckpoint = () => {
    const step = { step_index: 2, type: 'CHECKPOINT', source: 'SYSTEM', status: 'DONE', created_at: new Date().toISOString() }
    writeFileSync(transcript, [...initialSteps, step].map((s) => JSON.stringify(s)).join('\n') + '\n')
    db.prepare('UPDATE conversation_summaries SET step_count = ?, last_modified_time = ? WHERE conversation_id = ?')
      .run(3, new Date().toISOString(), RAW)
  }
  return { node, row, calls, items, appendCheckpoint, idle: () => runtime.idle() }
}

test('/compact with CHECKPOINT export does not retry as an empty response and resets context_tokens', async (t) => {
  const { node, row, calls, items, idle } = await fixture(t, [{ export: true, skipInput: true, checkpoint: true }])
  await node.peer.call('session.send', { session_id: SESSION, text: '/compact', assistant: true })
  await until('compaction settled', () => idle() && row()?.context_tokens === undefined)
  const prompts = calls().filter((call) => call.method === 'session/prompt')
  assert.equal(prompts.length, 1)
  assert.deepEqual(prompts[0].params.prompt, [{ type: 'text', text: '/compact' }])
  assert.equal(row().turn_error, undefined)
  const page = await items()
  assert.deepEqual(page.items.at(-1), {
    ...page.items.at(-1), kind: 'notice', role: 'system', text: '上下文已压缩',
  })
  assert.equal(page.items.some((item) => /no response returned/i.test(item.text || '')), false)
})

test('silent /compact does not retry, emits no empty-response error, and picks up a delayed CHECKPOINT', async (t) => {
  const { node, row, calls, items, appendCheckpoint, idle } = await fixture(t, [{}])
  await node.peer.call('session.send', { session_id: SESSION, text: '/compact', assistant: true })
  await until('silent /compact settled', idle)
  assert.equal(calls().filter((call) => call.method === 'session/prompt').length, 1)
  assert.equal(row().turn_error, undefined)
  // No phantom unexported /compact user bubble when agy has not exported anything yet.
  assert.equal((await items()).items.some((item) => item.text === '/compact'), false)
  // A delayed CHECKPOINT written after prompt return is picked up by export polling and clears context_tokens.
  appendCheckpoint()
  await until('delayed checkpoint observed', () => row()?.context_tokens === undefined)
  const page = await items()
  assert.equal(page.items.at(-1).kind, 'notice')
  assert.equal(page.items.at(-1).text, '上下文已压缩')
})

test('a genuine ACP error during /compact is still published as turn_error', async (t) => {
  const { node, row, calls, idle } = await fixture(t, [{ error: 'Compaction failed upstream' }])
  await node.peer.call('session.send', { session_id: SESSION, text: '/compact', assistant: true })
  await until('compact error published', () => idle() && row()?.turn_error === 'Compaction failed upstream')
  assert.equal(calls().filter((call) => call.method === 'session/prompt').length, 1)
})

test('bare /compact USER_INPUT maps to system notice instead of user bubble and preserves lastText and client_msg_id', () => {
  const steps = [
    { step_index: 0, type: 'USER_INPUT', source: 'USER_EXPLICIT', status: 'DONE', created_at: AT, content: '<USER_REQUEST>\nHello\n</USER_REQUEST>' },
    { step_index: 1, type: 'PLANNER_RESPONSE', source: 'MODEL', status: 'DONE', created_at: AT, content: 'Previous reply' },
    { step_index: 2, type: 'USER_INPUT', source: 'USER_EXPLICIT', status: 'DONE', created_at: AT, content: '<USER_REQUEST>\n/compact\n</USER_REQUEST>' },
    { step_index: 3, type: 'CHECKPOINT', source: 'SYSTEM', status: 'DONE', created_at: AT, content: '# Resuming from a compaction' },
    { step_index: 4, type: 'USER_INPUT', source: 'USER_EXPLICIT', status: 'DONE', created_at: AT, content: '<USER_REQUEST>\nNext message\n</USER_REQUEST>' },
  ]
  const ordinals = []
  const full = stepsToItems(steps, {
    full: false,
    clientMsgIdFor: (_idx, text, ord) => {
      ordinals.push([ord, text])
      if (ord === 2) return 'manual-compact-1'
      if (ord === 3) return 'next-msg-id'
      return undefined
    },
  })
  assert.equal(full.items.some((item) => item.role === 'user' && item.text === '/compact'), false)
  const notices = full.items.filter((item) => item.kind === 'notice')
  assert.deepEqual(notices.map((n) => ({ role: n.role, text: n.text, client_msg_id: n.client_msg_id })), [
    { role: 'system', text: '压缩上下文', client_msg_id: 'manual-compact-1' },
    { role: 'system', text: '上下文已压缩', client_msg_id: undefined },
  ])
  assert.deepEqual(ordinals, [[1, 'Hello'], [2, '/compact'], [3, 'Next message']])
  assert.equal(full.items.at(-1).client_msg_id, 'next-msg-id')

  const fullBeforeNext = stepsToItems(steps.slice(0, 4), { full: false })
  const summaryBeforeNext = stepsToItems(steps.slice(0, 4), { full: false, summaryOnly: true })
  assert.equal(fullBeforeNext.lastText, 'Previous reply')
  assert.equal(summaryBeforeNext.lastText, 'Previous reply')
})

test('live and exported /compact with USER_INPUT never emits a /compact user bubble', async (t) => {
  const { node, row, items, idle } = await fixture(t, [{ export: true, checkpoint: true }])
  await node.peer.call('session.send', { session_id: SESSION, text: '/compact', assistant: true })
  await until('compaction settled', () => idle() && row()?.context_tokens === undefined)
  const page = await items()
  assert.equal(page.items.some((item) => item.role === 'user' && item.text === '/compact'), false)
  const notices = page.items.filter((item) => item.kind === 'notice').map((item) => item.text)
  assert.deepEqual(notices, ['压缩上下文', '上下文已压缩'])
})

test('cleanUserText strips phantom trailing /compact appended by agy CLI even when middle text is truncated', () => {
  const fullMention = '<USER_REQUEST>\nFix agy /compact handling in PR #14755.\nStatus: CLEAN\n/compact\n</USER_REQUEST>'
  assert.equal(cleanUserText(fullMention), 'Fix agy /compact handling in PR #14755.\nStatus: CLEAN')

  const truncatedMention = '<USER_REQUEST>\nContent of persona-context:///snapshot.txt:\n<persona-context>\nsnapshot\n</persona-context>\nHeader\n<truncated 12684 bytes>\nStatus: CLEAN\n/compact\n</USER_REQUEST>'
  assert.equal(cleanUserText(truncatedMention), 'Header\n<truncated 12684 bytes>\nStatus: CLEAN')

  const attachedImg = '<USER_REQUEST>\nFix /compact\n<attached_images>\n/tmp/a.png\n</attached_images>/compact\n</USER_REQUEST>'
  assert.equal(cleanUserText(attachedImg), 'Fix /compact\n<attached_images>\n/tmp/a.png\n</attached_images>')

  const bareCompact = '<USER_REQUEST>\n/compact\n</USER_REQUEST>'
  assert.equal(cleanUserText(bareCompact), '/compact')

  const genuineEnding = '<USER_REQUEST>\nPlease run this next\n/compact\n</USER_REQUEST>'
  assert.equal(cleanUserText(genuineEnding), 'Please run this next\n/compact')
})
