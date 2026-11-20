import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { test } from 'node:test'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const source = '11111111-2222-4333-8444-555555555555'
const branch = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
const sessionId = `agy-${source}`
const branched = `agy-${branch}`
const fake = fileURLToPath(new URL('./fake-acp-rewind.mjs', import.meta.url))
const records = [
  { type: 'USER_INPUT', content: 'First question' },
  { type: 'PLANNER_RESPONSE', thinking: 'Reasoning', content: 'First draft', tool_calls: [{ name: 'view_file' }] },
  { type: 'GENERIC', content: 'Tool result' },
  { type: 'PLANNER_RESPONSE', content: 'First answer' },
  { type: 'USER_INPUT', content: 'Second question' },
  { type: 'PLANNER_RESPONSE', content: 'Second answer' },
].map((step, step_index) => ({ ...step, step_index, status: 'DONE', created_at: '2026-01-01T00:00:00Z' }))

async function fixture(t, { assistant = false, status = 'IDLE' } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-fork-'))
  const cwd = join(home, assistant ? '.persona/assistant' : 'Example workspace')
  const config = join(home, 'config')
  const logs = join(config, 'brain', source, '.system_generated/logs')
  mkdirSync(logs, { recursive: true })
  mkdirSync(cwd, { recursive: true })
  const transcript = records.map((record) => JSON.stringify(record)).join('\n') + '\n'
  const transcriptPath = join(logs, 'transcript_full.jsonl')
  writeFileSync(transcriptPath, transcript)
  writeFileSync(join(home, 'calls.jsonl'), '')
  writeFileSync(join(home, 'trajectory.json'), JSON.stringify({
    forkId: branch, delayForkExport: assistant, status: `CASCADE_RUN_STATUS_${status}`,
    trajectory: { cascadeId: source, steps: records.map((record) => ({ type: `CORTEX_STEP_TYPE_${record.type}` })) },
  }))
  const db = new DatabaseSync(join(config, 'conversation_summaries.db'))
  db.exec('CREATE TABLE conversation_summaries (conversation_id TEXT, status TEXT, not_fully_idle INTEGER, killed INTEGER, step_count INTEGER, title TEXT, workspace_uris TEXT, last_modified_time TEXT)')
  db.prepare('INSERT INTO conversation_summaries VALUES (?, ?, 0, 0, ?, ?, ?, ?)').run(
    source, 'CASCADE_RUN_STATUS_IDLE', records.length, 'Example chat', JSON.stringify([pathToFileURL(cwd).href]), new Date().toISOString())
  const binary = join(home, 'agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${fake}' '${home}' "$@"\n`)
  chmodSync(binary, 0o700)
  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({ home, agyDir: config, agyBin: binary, socketPath: node.path, log: () => {} })
  t.after(async () => { await runtime.stop(); db.close(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  await until('source listed', () => node.last('runtime.sessions')?.sessions.some((row) => row.session_id === sessionId))
  const fork = (at_item_id, session_id = sessionId) => node.peer.call('session.fork', { session_id, at_item_id })
  const items = async (id = sessionId) => (await node.peer.call('session.items', { session_id: id })).items
  const calls = () => readFileSync(join(home, 'calls.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
  return { node, cwd, fork, items, calls, checkSource: () => assert.equal(readFileSync(transcriptPath, 'utf8'), transcript) }
}

test('native fork keeps the entire conversation in the same workspace', async (t) => {
  const { node, cwd, fork, items, calls, checkSource } = await fixture(t)
  assert.equal(node.hello.caps.fork, true)
  assert.equal((await fork()).session_id, branched)
  assert.deepEqual(calls().find((call) => call.method === 'ForkConversation')?.params, {
    sourceCascadeId: source, forkAtStepIndex: -1,
    targetForkWorkspace: 'FORK_TARGET_CURRENT_WORKSPACE', sourceWorkspaceUris: [pathToFileURL(cwd).href],
  })
  assert.deepEqual(await items(branched), await items())
  checkSource()
})

test('forking at a reply keeps its tool results and stops before the next user message', async (t) => {
  const { fork, items, calls, checkSource } = await fixture(t)
  const reply = (await items()).find((item) => item.text === 'First draft')
  assert.match(reply.id, /^2@[\da-f]+#1$/)
  assert.equal((await fork(reply.id)).session_id, branched)
  assert.equal(calls().find((call) => call.method === 'ForkConversation')?.params.forkAtStepIndex, 3)
  assert.deepEqual((await items(branched)).filter((item) => item.kind === 'text').map((item) => item.text),
    ['First question', 'First draft', 'First answer'])
  checkSource()
})

test('an assistant fork can be opened and continued before the native export arrives', async (t) => {
  const { node, cwd, fork, items, calls } = await fixture(t, { assistant: true })
  const result = await fork('4#0')
  assert.equal(result.session_id, branched)
  assert.equal(result.session.cwd, cwd)
  assert.equal(calls().find((call) => call.method === 'ForkConversation')?.params.forkAtStepIndex, 3)
  const listed = await until('assistant fork listed', () => node.last('runtime.sessions')?.sessions.find((row) => row.session_id === branched))
  assert.equal(listed.cwd, cwd)
  assert.equal(listed.status, 'idle')
  assert.equal((await items(branched)).at(-1).text, 'First answer')
  await node.peer.call('session.send', { session_id: branched, assistant: true, text: 'Continue the branch' })
  const sent = await until('branch continued', () => calls().find((call) => call.method === 'session/prompt'))
  assert.equal(sent.params.sessionId, branch)
  assert.equal(calls().find((call) => call.method === 'session/resume')?.params.cwd, cwd)
})

test('a native running turn refuses to fork', async (t) => {
  const { fork, calls } = await fixture(t, { status: 'RUNNING' })
  await assert.rejects(fork(), (error) => error.code === 'fork_turn_running')
  assert.ok(!calls().some((call) => call.method === 'ForkConversation'))
})

test('unknown, archived and stale targets do not fork', async (t) => {
  const { node, fork, calls } = await fixture(t)
  await assert.rejects(fork('', 'agy-11111111-eeee-4333-8444-555555555555'), (error) => error.code === 'not_found')
  await assert.rejects(fork('4@old#0'), (error) => error.code === 'fork_mismatch')
  await assert.rejects(fork('4#9'), (error) => error.code === 'fork_mismatch')
  await node.peer.call('session.close', { session_id: sessionId })
  await assert.rejects(fork(), (error) => error.code === 'not_found')
  assert.ok(!calls().some((call) => call.method === 'ForkConversation'))
})
