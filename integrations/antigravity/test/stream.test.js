import assert from 'node:assert/strict'
import { appendFileSync, chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE = fileURLToPath(new URL('./fake-acp-stream.mjs', import.meta.url))
const RAW = '11111111-2222-4333-8444-555555555555'
const SESSION = `agy-${RAW}`

async function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), 'agy-stream-'))
  writeFileSync(join(directory, 'updates.jsonl'), '')
  writeFileSync(join(directory, 'calls.jsonl'), '')
  const binary = join(directory, 'agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${directory}' "$@"\n`)
  chmodSync(binary, 0o700)
  mkdirSync(join(directory, 'config/brain', RAW), { recursive: true })
  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({ agyBin: binary, agyDir: join(directory, 'config'), home: directory, socketPath: node.path, log: () => {} })
  t.after(async () => { await runtime.stop(); rmSync(directory, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  await node.peer.call('session.start', { cwd: directory })
  await node.peer.call('session.watch', { session_id: SESSION })
  const calls = () => readFileSync(join(directory, 'calls.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  const update = (value) => appendFileSync(join(directory, 'updates.jsonl'), `${JSON.stringify(value)}\n`)
  const chunk = async (text, expected = text) => {
    update({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } })
    await until(`live text ${expected}`, () => node.last('session.delta')?.text === expected, 15_000)
  }
  const promptCount = (count) => until(`${count} prompts started`, () => calls().filter((r) => r.method === 'session/prompt').length === count, 15_000)
  return { node, directory, calls, update, chunk, promptCount }
}

test('AGY streams one assistant message at a time across tool calls', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'Make the change.' })
  await promptCount(1)
  await chunk('Preparing ')
  await chunk('the change.', 'Preparing the change.')
  update({ sessionUpdate: 'tool_call', toolCallId: 'tool-1', title: 'Read' })
  update({ sessionUpdate: 'tool_call_update', toolCallId: 'tool-1', status: 'completed' })
  await chunk('The change is ')
  await chunk('complete.', 'The change is complete.')
  update({ end: true })
  await until('live text cleared at completion', () => node.last('session.delta')?.text === '', 15_000)
})

test('a queued prompt does not receive the executing prompt text and starts with an empty stream', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'First request.' })
  await promptCount(1)
  await chunk('First reply')
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second request.' })
  await chunk(' complete.', 'First reply complete.')
  update({ end: true })
  await promptCount(2)
  await until('queued prompt clears the previous text', () => node.last('session.delta')?.text === '', 15_000)
  await chunk('Second reply.')
  update({ end: true })
  await until('second prompt completes', () => node.last('session.delta')?.text === '', 15_000)
})

test('each prompt clears any live text before its first response', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'First request.' })
  await promptCount(1)
  await chunk('First reply.')
  update({ end: true })
  await until('first prompt finishes', () => node.last('session.delta')?.text === '', 15_000)
  const before = node.all('session.delta').length
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second request.' })
  await promptCount(2)
  await until('new prompt publishes its empty boundary', () => node.all('session.delta').length > before, 15_000)
  assert.equal(node.last('session.delta').text, '')
  await chunk('First reply.')
  assert.equal(node.last('session.delta').text, 'First reply.', 'a new turn may legitimately repeat earlier words')
  update({ end: true })
})

test('AGY completion retires the cached reply after the viewer leaves', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'First request.' })
  await promptCount(1)
  await chunk('Previous reply.')
  await node.peer.call('session.unwatch', { session_id: SESSION })
  update({ end: true })
  await until('unwatched completion clears the reply', () => node.last('session.delta')?.text === '', 2000)
  await node.peer.call('session.watch', { session_id: SESSION })
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second request.' })
  await promptCount(2)
  assert.equal(node.last('session.delta').text, '')
  await chunk('New reply.')
  update({ end: true })
})

function transcripts(directory, compact, complete) {
  const logs = join(directory, 'config/brain', RAW, '.system_generated/logs')
  mkdirSync(logs, { recursive: true })
  for (const [file, rows] of [['transcript.jsonl', compact], ['transcript_full.jsonl', complete]]) {
    writeFileSync(join(logs, file), rows.map((row) => JSON.stringify(row)).join('\n') + '\n')
  }
}

const userStep = (index, text) => ({ step_index: index, source: 'USER_EXPLICIT', type: 'USER_INPUT', status: 'DONE', content: text })
const replyStep = (index, text) => ({ step_index: index, source: 'MODEL', type: 'PLANNER_RESPONSE', status: 'DONE', content: text })

test('watched native turns push new content after an older quota error without ACP updates', async (t) => {
  const { node, directory, calls, update } = await fixture(t)
  const old = [userStep(0, 'First request.'), { step_index: 1, type: 'ERROR_MESSAGE', content: 'Quota exhausted.' }]
  transcripts(directory, old, old)
  update({ sessionUpdate: 'tool_call', toolCallId: 'initial', title: 'Read' })
  await until('old quota is pushed', () => node.last('session.items')?.to_seq === 2)
  const next = [...old, userStep(2, 'Second request.'), replyStep(3, 'Current progress.')]
  transcripts(directory, next, next)
  await until('external turn is pushed', () => node.last('session.items')?.to_seq === 4)
  assert.equal(node.last('session.items').from_seq, 2)
  assert.deepEqual(node.last('session.items').items.map((item) => item.text), ['Second request.', 'Current progress.'])
  assert.equal(calls().some((call) => call.method === 'session/prompt'), false)

  await node.peer.call('session.unwatch', { session_id: SESSION })
  const before = node.all('session.items').length
  const final = [...next, replyStep(4, 'Current result.')]
  transcripts(directory, final, final)
  await new Promise((resolve) => setTimeout(resolve, 600))
  assert.equal(node.all('session.items').length, before)
  await node.peer.call('session.watch', { session_id: SESSION, since_seq: 4 })
  await until('rewatching catches up', () => node.last('session.items')?.to_seq === 5)
  assert.equal(node.last('session.items').items.at(-1).text, 'Current result.')
})

test('a native planner completion at the same index replaces watched content', async (t) => {
  const { node, directory, update } = await fixture(t)
  const pending = { ...replyStep(1, ''), thinking: 'Reviewing the change.', created_at: '2026-01-02T12:00:01Z' }
  const old = [userStep(0, 'Review the change.'), pending]
  transcripts(directory, old, old)
  update({ sessionUpdate: 'tool_call', toolCallId: 'initial', title: 'Read' })
  await until('initial work is pushed', () => node.last('session.items')?.to_seq === 2)
  const history = node.last('session.items').history
  const complete = [old[0], { ...pending, content: 'Review complete.' }]
  transcripts(directory, complete, complete)
  await until('same-index completion is pushed', () => node.last('session.items')?.items.some((item) => item.text === 'Review complete.'))
  assert.equal(node.last('session.items').to_seq, 2)
  assert.notEqual(node.last('session.items').history, history)
})

test('normal AGY pages recover the complete native reply through the same source as full items', async (t) => {
  const { node, directory } = await fixture(t)
  const complete = '## Report\n' + 'A complete result with Unicode 字段.\n'.repeat(80) + 'End of report.'
  transcripts(directory,
    [userStep(0, 'Read the report.'), { ...replyStep(1, '## Report\n<truncated 4840 bytes>\nEnd of report.'), truncated_fields: ['content'] }],
    [userStep(0, 'Read the report.'), replyStep(1, complete)])
  const page = await node.peer.call('session.items', { session_id: SESSION })
  const reply = page.items.find((item) => item.role === 'assistant' && item.kind === 'text')
  assert.equal(reply.text, complete)
  assert.equal(reply.full_size, undefined)
  assert.equal(page.to_seq, 2)
  assert.notEqual(page.history, `agy:${RAW}`, 'compact transcript caches use a different history')
  const full = await node.peer.call('session.item', { session_id: SESSION, item_id: reply.id })
  assert.equal(full.text, complete)
})

test('live AGY items apply Persona page limits to the complete native reply', async (t) => {
  const { node, directory, update } = await fixture(t)
  const complete = '## Report\n' + 'A detailed report row.\n'.repeat(2000) + 'Literal example: <truncated 7 bytes>.'
  transcripts(directory,
    [userStep(0, 'Read the report.'), { ...replyStep(1, '## Report\n<truncated 40000 bytes>\nEnd.'), truncated_fields: ['content'] }],
    [userStep(0, 'Read the report.'), replyStep(1, complete)])
  update({ sessionUpdate: 'tool_call', toolCallId: 'read-report', title: 'Read' })
  await until('the reply is pushed', () => node.last('session.items')?.to_seq === 2)
  const reply = node.last('session.items').items.find((item) => item.role === 'assistant' && item.kind === 'text')
  assert.equal(reply.full_size, Buffer.byteLength(complete))
  assert.ok(reply.text.startsWith('## Report\nA detailed report row.'))
  const full = await node.peer.call('session.item', { session_id: SESSION, item_id: reply.id })
  assert.equal(full.text, complete)
  assert.ok(full.text.includes('Literal example: <truncated 7 bytes>.'))
})

test('AGY watch cursors follow the complete export while its compact export is ahead', async (t) => {
  const { node, directory, update } = await fixture(t)
  const old = [userStep(0, 'First request.'), replyStep(1, 'First reply.')]
  const nextUser = userStep(2, 'Second request.')
  transcripts(directory, [...old, nextUser], old)
  const page = await node.peer.call('session.items', { session_id: SESSION })
  assert.equal(page.to_seq, 2)
  update({ sessionUpdate: 'tool_call', toolCallId: 'read-first', title: 'Read' })
  await until('the initial complete export is pushed', () => node.last('session.items')?.to_seq === 2)
  await node.peer.call('session.unwatch', { session_id: SESSION })
  await node.peer.call('session.watch', { session_id: SESSION })
  const complete = [...old, nextUser, replyStep(3, 'Second reply.')]
  transcripts(directory, complete, complete)
  update({ sessionUpdate: 'tool_call', toolCallId: 'read-next', title: 'Read' })
  await until('the complete export catches up', () => node.last('session.items')?.to_seq === 4)
  assert.deepEqual(node.last('session.items').items.map((item) => item.seq), [3, 4])
})

test('owned ACP turns publish the user prompt, working state and live tool steps before native transcript export', { timeout: 20_000 }, async (t) => {
  const { node, directory, update, promptCount } = await fixture(t)
  const started = await node.peer.call('session.start', { cwd: directory, assistant: true })
  assert.equal(started.session.status, 'idle')

  await node.peer.call('session.send', { session_id: SESSION, text: 'Inspect the repository.', client_msg_id: 'msg-1', assistant: true })
  await promptCount(1)
  await until('working session published', () => node.last('runtime.sessions')?.sessions.find((s) => s.session_id === SESSION)?.status === 'working')

  const liveInitial = await node.peer.call('session.items', { session_id: SESSION })
  assert.equal(liveInitial.items.length, 1)
  assert.equal(liveInitial.items[0].role, 'user')
  assert.equal(liveInitial.items[0].text, 'Inspect the repository.')
  assert.equal(liveInitial.items[0].client_msg_id, 'msg-1')

  update({ sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: 'Checking files first.' } })
  update({ sessionUpdate: 'tool_call', toolCallId: 'tool-live-1', name: 'run_command', rawInput: { CommandLine: 'ls', toolSummary: 'List files' } })
  await until('live thought and tool call pushed', () => {
    const latest = node.last('session.items')
    return latest?.items?.some((it) => it.kind === 'thinking') && latest?.items?.some((it) => it.kind === 'tool_use' && it.tool_name === 'Bash')
  })

  const exported = [userStep(0, 'Inspect the repository.'), replyStep(1, 'Inspection complete.')]
  transcripts(directory, exported, exported)
  update({ end: true })
  await until('native export replaces live steps', () => node.last('session.items')?.items?.some((it) => it.role === 'assistant' && it.text === 'Inspection complete.'))
  const finalPage = await node.peer.call('session.items', { session_id: SESSION })
  assert.equal(finalPage.items[0].client_msg_id, 'msg-1')
  assert.equal(finalPage.items.at(-1).text, 'Inspection complete.')
})

test('live thinking chunks, parallel tool updates and settling keep history stable without rewriting prior turns (#11129)', { timeout: 25_000 }, async (t) => {
  const { node, directory, update, promptCount } = await fixture(t)
  const prior = [
    { ...userStep(0, 'Old prompt 1'), created_at: '2026-01-01T00:00:00Z' },
    { ...replyStep(1, 'Old answer 1'), created_at: '2026-01-01T00:00:01Z' },
    { ...userStep(2, 'Old prompt 2'), created_at: '2026-01-01T00:00:02Z' },
    { ...replyStep(3, 'Old answer 2'), created_at: '2026-01-01T00:00:03Z' },
  ]
  transcripts(directory, prior, prior)
  await node.peer.call('session.unwatch', { session_id: SESSION })
  await node.peer.call('session.watch', { session_id: SESSION })

  await node.peer.call('session.send', { session_id: SESSION, text: 'Investigate the jump.', client_msg_id: 'msg-jump' })
  await promptCount(1)
  await until('live turn user prompt pushed', () => node.last('session.items')?.to_seq === 5)

  const liveHistory = node.last('session.items').history
  const startBatchCount = node.all('session.items').length
  // Seed simulated client state from the initial live page before incremental updates begin.
  const mergedById = new Map((await node.peer.call('session.items', { session_id: SESSION })).items.map((it) => [it.id, it]))
  let covered = 5

  // 1. Multiple thinking chunks separated beyond the 150ms throttle update the same thinking item in place.
  update({ sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: 'Step 1: ' } })
  await until('first thinking chunk pushed', () => node.last('session.items')?.items?.some((it) => it.kind === 'thinking' && it.text === 'Step 1:'))
  const firstThought = node.last('session.items').items.find((it) => it.kind === 'thinking')
  assert.equal(node.last('session.items').history, liveHistory)

  update({ sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: 'reading runtime.js.' } })
  await until('second thinking chunk updates same item', () =>
    node.last('session.items')?.items?.some((it) => it.id === firstThought.id && it.text === 'Step 1: reading runtime.js.'),
  )
  const secondThoughtBatch = node.last('session.items')
  assert.equal(secondThoughtBatch.history, liveHistory)
  assert.equal(secondThoughtBatch.from_seq, firstThought.seq)
  assert.equal(secondThoughtBatch.to_seq, firstThought.seq)
  assert.deepEqual(secondThoughtBatch.items.map((it) => it.id), [firstThought.id])

  // 2. Parallel tool calls, an in-place argument update, and out-of-order completion stay incremental and paired.
  update({ sessionUpdate: 'tool_call', toolCallId: 'tc-1', name: 'read_file', rawInput: {} })
  update({ sessionUpdate: 'tool_call', toolCallId: 'tc-2', name: 'run_command', rawInput: { CommandLine: 'ls' } })
  await until('both parallel tool calls pushed', () => {
    const allLive = node.all('session.items').slice(startBatchCount).flatMap((b) => b.items)
    return allLive.some((it) => it.kind === 'tool_use' && it.tool_name === 'Read') &&
      allLive.some((it) => it.kind === 'tool_use' && it.tool_name === 'Bash')
  })

  update({ sessionUpdate: 'tool_call_update', toolCallId: 'tc-1', status: 'in_progress', rawInput: { Path: '/tmp/a.js' } })
  await until('tool call input updated in place', () =>
    node.all('session.items').slice(startBatchCount).flatMap((b) => b.items)
      .some((it) => it.kind === 'tool_use' && it.tool_name === 'Read' && it.tool_input?.file_path === '/tmp/a.js'),
  )

  update({ sessionUpdate: 'tool_call_update', toolCallId: 'tc-2', status: 'completed', rawOutput: 'ok-2' })
  update({ sessionUpdate: 'tool_call_update', toolCallId: 'tc-1', status: 'failed', rawOutput: 'err-1' })
  await until('both out-of-order tool results pushed', () => {
    const allLive = node.all('session.items').slice(startBatchCount).flatMap((b) => b.items)
    return allLive.some((it) => it.kind === 'tool_result' && it.text === 'ok-2') &&
      allLive.some((it) => it.kind === 'tool_result' && it.text === 'err-1' && it.is_error === true)
  })

  // 3. A second thinking phase after tools appends chronologically without mutating the first thinking item.
  update({ sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: 'Step 2: wrapping up.' } })
  await until('second thinking block pushed', () =>
    node.all('session.items').slice(startBatchCount).flatMap((b) => b.items)
      .some((it) => it.kind === 'thinking' && it.text === 'Step 2: wrapping up.'),
  )

  // 4. Streaming final text and ending the turn before disk export settles incrementally under the same history.
  update({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'Fixed cleanly.' } })
  await until('final reply streams', () => node.last('session.delta')?.text === 'Fixed cleanly.')
  update({ end: true })
  await until('settled reply pushed incrementally', () =>
    node.last('session.items')?.items?.some((it) => it.role === 'assistant' && it.kind === 'text' && it.text === 'Fixed cleanly.'),
  )

  const liveBatches = node.all('session.items').slice(startBatchCount)
  assert.ok(liveBatches.length >= 4, `expected multiple incremental batches, got ${liveBatches.length}`)
  for (const batch of liveBatches) {
    assert.equal(batch.history, liveHistory, 'live progress and settling must not mutate history')
    assert.ok(batch.from_seq > 0, `expected incremental continuation batch, got from_seq=${batch.from_seq}`)
    assert.ok(batch.from_seq <= covered, `batch must remain continuous (from_seq=${batch.from_seq} > covered=${covered})`)
    assert.equal(batch.items.some((it) => it.seq <= 4), false, 'prior turns must not be re-transmitted during live updates')
    for (const it of batch.items) mergedById.set(it.id, it)
    covered = Math.max(covered, batch.to_seq)
  }

  const merged = [...mergedById.values()].sort((a, b) => a.seq - b.seq)
  assert.deepEqual(merged.slice(0, 4).map((it) => it.text), ['Old prompt 1', 'Old answer 1', 'Old prompt 2', 'Old answer 2'])
  const thoughts = merged.filter((it) => it.kind === 'thinking')
  assert.deepEqual(thoughts.map((it) => it.text), ['Step 1: reading runtime.js.', 'Step 2: wrapping up.'])
  const readUse = merged.find((it) => it.kind === 'tool_use' && it.tool_name === 'Read')
  const bashUse = merged.find((it) => it.kind === 'tool_use' && it.tool_name === 'Bash')
  const resultsByCall = new Map(merged.filter((it) => it.kind === 'tool_result').map((it) => [it.tool_use_id, it]))
  assert.equal(readUse.tool_input.file_path, '/tmp/a.js')
  assert.equal(resultsByCall.get(readUse.tool_use_id)?.text, 'err-1')
  assert.equal(resultsByCall.get(readUse.tool_use_id)?.is_error, true)
  assert.equal(resultsByCall.get(bashUse.tool_use_id)?.text, 'ok-2')
  assert.equal(resultsByCall.get(bashUse.tool_use_id)?.is_error, false)
  assert.equal(merged.at(-1).text, 'Fixed cleanly.')

  // 5. Only once native transcript_full.jsonl actually flushes does history transition back to canonical disk history.
  const flushed = [
    ...prior,
    { ...userStep(4, 'Investigate the jump.'), created_at: '2026-01-01T00:00:04Z' },
    { ...replyStep(5, 'Fixed cleanly.'), thinking: 'Step 1: reading runtime.js.\nStep 2: wrapping up.', created_at: '2026-01-01T00:00:05Z' },
  ]
  transcripts(directory, flushed, flushed)
  await until('native disk export replaces synthetic settled history once', () => {
    const latest = node.last('session.items')
    return latest && latest.history !== liveHistory && !latest.history.includes(':settled:') && latest.to_seq === 6
  })
  assert.equal(node.last('session.items').from_seq, 0)
  assert.equal(node.last('session.items').items.at(-1).text, 'Fixed cleanly.')
})

test('trailing thinking chunks arriving right before turn completion are not dropped on settle (#11129)', { timeout: 20_000 }, async (t) => {
  const { node, update, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'Quick check.' })
  await promptCount(1)
  await until('user prompt pushed', () => node.last('session.items')?.to_seq === 1)
  const liveHistory = node.last('session.items').history

  update({ sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: 'First half' } })
  await until('first thinking chunk pushed', () => node.last('session.items')?.items?.some((it) => it.kind === 'thinking' && it.text === 'First half'))
  const thoughtItem = node.last('session.items').items.find((it) => it.kind === 'thinking')
  const afterFirstThought = node.all('session.items').length

  // Immediately append a second thinking chunk and end the turn before the 150ms thought throttle fires.
  update({ sessionUpdate: 'agent_thought_chunk', content: { type: 'text', text: ' + second half.' } })
  update({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'All good.' } })
  update({ end: true })

  await until('settled batch includes final reply', () =>
    node.last('session.items')?.items?.some((it) => it.role === 'assistant' && it.kind === 'text' && it.text === 'All good.'),
  )
  const followBatches = node.all('session.items').slice(afterFirstThought)
  for (const batch of followBatches) assert.equal(batch.history, liveHistory)
  const updatedThought = followBatches.flatMap((b) => b.items).findLast((it) => it.id === thoughtItem.id)
  assert.equal(updatedThought?.text, 'First half + second half.')
  const page = await node.peer.call('session.items', { session_id: SESSION })
  assert.equal(page.history, liveHistory)
  assert.equal(page.items.find((it) => it.id === thoughtItem.id)?.text, 'First half + second half.')
})
