import assert from 'node:assert/strict'
import { appendFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { toAgyId } from '../lib/map.js'
import { createAgyRuntime } from '../lib/runtime.js'

const policyFake = fileURLToPath(new URL('./fake-acp-policy.mjs', import.meta.url))
const SLOW = 15_000

function isAlive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch (error) {
    assert.equal(error.code, 'ESRCH')
    return false
  }
}

function writeTranscriptSteps(agyDir, rawId, steps, { append = false } = {}) {
  const logs = join(agyDir, 'brain', rawId, '.system_generated', 'logs')
  mkdirSync(logs, { recursive: true })
  const file = join(logs, 'transcript.jsonl')
  const payload = steps.map((step) => JSON.stringify(step)).join('\n') + '\n'
  if (append) appendFileSync(file, payload)
  else writeFileSync(file, payload)
}

function runningTaskSteps(rawId, tasks, startStepIndex = 0) {
  const planner = {
    step_index: startStepIndex,
    type: 'PLANNER_RESPONSE',
    status: 'DONE',
    created_at: tasks[0].startedAt,
    tool_calls: tasks.map((task) => ({
      name: task.tool || 'run_command',
      args: task.args || { CommandLine: JSON.stringify(task.command || 'sleep 60') },
    })),
  }
  const results = tasks.map((task, idx) => ({
    step_index: startStepIndex + 1 + idx,
    type: 'GENERIC',
    status: 'RUNNING',
    created_at: task.startedAt,
    content: `Created At: ${task.startedAt}\nTool is running as a background task with task id: ${rawId}/task-${task.num}\nTask Description: ${task.description || `Task ${task.num}`}`,
  }))
  return [planner, ...results]
}

function upsertSummary(agyDir, rawId, cwd, { status = 'CASCADE_RUN_STATUS_IDLE', killed = 0, stepCount = 2 } = {}) {
  const db = new DatabaseSync(join(agyDir, 'conversation_summaries.db'))
  try {
    db.exec('CREATE TABLE IF NOT EXISTS conversation_summaries (conversation_id TEXT PRIMARY KEY, status TEXT, not_fully_idle INTEGER, killed INTEGER, step_count INTEGER, title TEXT, workspace_uris TEXT, last_modified_time TEXT)')
    db.prepare('INSERT OR REPLACE INTO conversation_summaries VALUES (?, ?, 0, ?, ?, ?, ?, ?)')
      .run(rawId, status, killed, stepCount, 'Lifecycle test', JSON.stringify([pathToFileURL(cwd).href]), new Date().toISOString())
  } finally {
    db.close()
  }
}

async function createFixture(t, options = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-bg-lifecycle-'))
  const agyDir = join(home, 'config')
  mkdirSync(agyDir, { recursive: true })
  const binary = join(home, 'agy')
  const record = join(home, 'requests.jsonl')
  writeFileSync(record, '')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${policyFake}' --record '${record}' "$@"\n`, { mode: 0o700 })
  const node = await startFakeNode(t, { features: ['assistant_pids'] })
  const runtime = createAgyRuntime({
    home,
    agyDir,
    agyBin: binary,
    personadBin: '/missing',
    socketPath: node.path,
    log: () => {},
    ...options,
  })
  t.after(async () => {
    await runtime.stop()
    rmSync(home, { recursive: true, force: true })
  })
  await runtime.start()
  await node.ready
  const calls = () => readFileSync(record, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
  const startedPids = () => calls().filter((entry) => entry.started).map((entry) => entry.started)
  const rowOf = (sid) => node.last('runtime.sessions')?.sessions.find((entry) => entry.session_id === sid)
  const start = async (params = {}) => (await node.peer.call('session.start', { cwd: home, ...params })).session.session_id
  const call = (method, session_id, params = {}) => node.peer.call(`session.${method}`, { session_id, ...params })
  const request = (method, session) => calls().findLast((entry) => entry.method === method && entry.params?.sessionId === toAgyId(session))
  return { home, agyDir, node, runtime, calls, startedPids, rowOf, start, call, request }
}

test('resuming a session after sharedAcp restart does not resurrect pre-restart background tasks or lock idle reaping', { timeout: 20_000 }, async (t) => {
  const { home, agyDir, startedPids, rowOf, start, call, request } = await createFixture(t, {
    sharedAcpIdleMs: 250,
    promptInactivityMs: 0,
  })

  const session = await start()
  const rawId = toAgyId(session)
  const firstPid = await until('initial sharedAcp worker started', () => startedPids()[0], SLOW)

  // Write a pre-restart unclosed background task dated well before the replacement ACP process.
  const staleStartedAt = new Date(Date.now() - 60_000).toISOString()
  writeTranscriptSteps(agyDir, rawId, runningTaskSteps(rawId, [{ num: 1, startedAt: staleStartedAt, description: 'Stale pre-restart task' }]))
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 2 })

  // Kill the first worker abruptly (simulating a crash or companion restart without a transcript stop notice).
  process.kill(firstPid, 'SIGKILL')
  await until('first sharedAcp worker exited', () => !isAlive(firstPid), SLOW)

  // Resume the session on a fresh sharedAcp worker via session.send (triggers session/resume).
  await call('send', session, { text: 'follow up after restart' })
  const resumed = await until('fresh sharedAcp resumed and prompted session', () => {
    const entry = request('session/prompt', session)
    return entry && entry.worker !== firstPid ? entry : null
  }, SLOW)
  const secondPid = resumed.worker
  assert.equal(request('session/resume', session)?.worker, secondPid)

  // Refresh listed rows while secondPid is still alive and verify the pre-restart task does NOT resurrect.
  await call('rename', session, { title: 'Post-restart check' })
  const listed = await until('post-restart row published', () => {
    const row = rowOf(session)
    return row?.title === 'Post-restart check' ? row : null
  }, SLOW)
  assert.equal(listed.background, undefined, 'pre-restart background task must not resurrect on session/resume')
  assert.deepEqual(listed.agy_pane_background?.map((task) => task.id), [`${rawId}/task-1`])

  // Because the stale task is ignored by ACP lifecycle alignment, sharedAcp must still retire after idle timeout.
  await until('replacement sharedAcp retires cleanly instead of self-locking', () => !isAlive(secondPid), SLOW)
})

test('post-restart live background tasks report on row.background, exclude co-existing stale tasks, and protect sharedAcp idle reaping until finished', { timeout: 20_000 }, async (t) => {
  const { home, agyDir, startedPids, rowOf, start, call, request } = await createFixture(t, {
    sharedAcpIdleMs: 250,
    promptInactivityMs: 0,
  })

  const session = await start()
  const rawId = toAgyId(session)
  const firstPid = await until('first sharedAcp worker started', () => startedPids()[0], SLOW)

  const staleStartedAt = new Date(Date.now() - 60_000).toISOString()
  writeTranscriptSteps(agyDir, rawId, runningTaskSteps(rawId, [{ num: 1, startedAt: staleStartedAt, description: 'Old stale task' }]))
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 2 })

  process.kill(firstPid, 'SIGKILL')
  await until('first sharedAcp exited', () => !isAlive(firstPid), SLOW)

  await call('send', session, { text: 'start fresh background work' })
  const resumed = await until('fresh sharedAcp ran prompt', () => {
    const entry = request('session/prompt', session)
    return entry && entry.worker !== firstPid ? entry : null
  }, SLOW)
  const secondPid = resumed.worker

  // Append two genuine tasks started inside the new ACP process, including one with whole-second RFC3339 precision.
  const freshMs = new Date().toISOString()
  const freshWholeSecond = new Date().toISOString().replace(/\.\d{3}Z$/, 'Z')
  writeTranscriptSteps(agyDir, rawId, runningTaskSteps(rawId, [
    { num: 2, startedAt: freshMs, description: 'Fresh live task (ms)' },
    { num: 3, startedAt: freshWholeSecond, description: 'Fresh live task (whole seconds)' },
  ], 2), { append: true })
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 5 })

  await call('rename', session, { title: 'Fresh tasks active' })
  const activeRow = await until('only post-restart live tasks appear in background', () => {
    const row = rowOf(session)
    return row?.title === 'Fresh tasks active' && row?.background?.length === 2 ? row : null
  }, SLOW)
  assert.deepEqual(activeRow.background.map((task) => task.id), [`${rawId}/task-2`, `${rawId}/task-3`])
  assert.equal(activeRow.agy_pane_background, undefined)

  // Genuine running tasks must protect sharedAcp from idle reaping well past sharedAcpIdleMs.
  await delay(500)
  assert.equal(isAlive(secondPid), true, 'sharedAcp must stay alive while genuine post-restart tasks are running')

  // Complete both genuine tasks (leaving only stale task-1 unclosed in the transcript).
  const endedAt = new Date(Date.now() + 1_000).toISOString()
  writeTranscriptSteps(agyDir, rawId, [
    { step_index: 5, type: 'SYSTEM_MESSAGE', created_at: endedAt, content: `Task id "${rawId}/task-2" finished with result: ok` },
    { step_index: 6, type: 'SYSTEM_MESSAGE', created_at: endedAt, content: `Task id "${rawId}/task-3" finished with result: ok` },
  ], { append: true })
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 7 })

  await call('rename', session, { title: 'Fresh tasks done' })
  const settledRow = await until('background clears once genuine tasks finish', () => {
    const row = rowOf(session)
    return row?.title === 'Fresh tasks done' && !row?.background ? row : null
  }, SLOW)
  assert.deepEqual(settledRow.background_ended?.map((task) => task.id), [`${rawId}/task-2`, `${rawId}/task-3`])

  // Now that only stale pre-restart task-1 remains unclosed, sharedAcp retires cleanly.
  await until('sharedAcp retires after genuine tasks end', () => !isAlive(secondPid), SLOW)
})

test('resuming an Assistant session on a replacement worker does not resurrect stale tasks or block Assistant idle reaping', { timeout: 20_000 }, async (t) => {
  const { home, agyDir, node, rowOf, start, call } = await createFixture(t, {
    assistantAcpIdleMs: 250,
    promptInactivityMs: 0,
  })
  const pids = () => node.last('runtime.assistant_pids')?.pids ?? {}

  const assistant = await start({ assistant: true })
  const rawId = toAgyId(assistant)
  const firstPid = await until('initial Assistant worker started', () => pids()[assistant], SLOW)

  const staleStartedAt = new Date(Date.now() - 60_000).toISOString()
  writeTranscriptSteps(agyDir, rawId, runningTaskSteps(rawId, [{ num: 1, startedAt: staleStartedAt, description: 'Stale assistant task' }]))
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 2 })

  process.kill(firstPid, 'SIGKILL')
  await until('first Assistant worker cleared', () => !pids()[assistant], SLOW)

  await call('send', assistant, { assistant: true, text: 'resume assistant after crash' })
  const secondPid = await until('replacement Assistant worker started', () => {
    const pid = pids()[assistant]
    return pid && pid !== firstPid ? pid : null
  }, SLOW)

  await call('rename', assistant, { title: 'Assistant post-restart' })
  const listed = await until('Assistant row published', () => {
    const row = rowOf(assistant)
    return row?.title === 'Assistant post-restart' ? row : null
  }, SLOW)
  assert.equal(listed.background, undefined)

  await until('replacement Assistant worker retires after idle timeout', () => !pids()[assistant] && !isAlive(secondPid), SLOW)
})

test('same-process re-resume for supervision policy updates preserves genuine running background tasks', { timeout: 20_000 }, async (t) => {
  const { home, agyDir, node, calls, rowOf, start, call } = await createFixture(t, {
    assistantAcpIdleMs: 350,
    promptInactivityMs: 0,
  })
  const pids = () => node.last('runtime.assistant_pids')?.pids ?? {}

  const assistant = await start({ assistant: true })
  const rawId = toAgyId(assistant)
  const workerPid = await until('Assistant worker started', () => pids()[assistant], SLOW)

  const liveStartedAt = new Date().toISOString()
  writeTranscriptSteps(agyDir, rawId, runningTaskSteps(rawId, [{ num: 1, startedAt: liveStartedAt, description: 'Active task across policy reload' }]))
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 2 })

  await call('rename', assistant, { title: 'Before policy reload' })
  await until('live task reported before re-resume', () => rowOf(assistant)?.background?.length === 1, SLOW)

  const resumesBefore = calls().filter((entry) => entry.method === 'session/resume' && entry.params?.sessionId === rawId).length
  await call('send', assistant, {
    assistant: true,
    text: 'continue with policy',
    supervision_instructions: 'Keep replies concise.',
  })
  await until('same-process session/resume executed', () =>
    calls().filter((entry) => entry.method === 'session/resume' && entry.params?.sessionId === rawId).length > resumesBefore, SLOW)

  assert.equal(pids()[assistant], workerPid, 'policy update must reuse the same Assistant worker')
  await delay(500)
  assert.equal(isAlive(workerPid), true, 'live task must still protect worker after same-process re-resume')
  assert.deepEqual(rowOf(assistant)?.background?.map((task) => task.id), [`${rawId}/task-1`])
})

test('native terminal pane projection stays intact in agy_pane_background and respects native.killed', { timeout: 20_000 }, async (t) => {
  const home = mkdtempSync(join(tmpdir(), 'agy-pane-bg-'))
  const agyDir = join(home, 'config')
  mkdirSync(agyDir, { recursive: true })
  const binary = join(home, 'agy')
  const record = join(home, 'requests.jsonl')
  writeFileSync(record, '')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${policyFake}' --record '${record}' "$@"\n`, { mode: 0o700 })

  // Pre-seed a standalone on-disk conversation representing an independent terminal pane session.
  const paneRawId = '33333333-4444-4555-8666-777777777777'
  const paneSid = `agy-${paneRawId}`
  const paneStartedAt = new Date(Date.now() - 60_000).toISOString()
  writeTranscriptSteps(agyDir, paneRawId, runningTaskSteps(paneRawId, [{ num: 1, startedAt: paneStartedAt, description: 'Terminal pane background task' }]))
  upsertSummary(agyDir, paneRawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', killed: 0, stepCount: 2 })

  const node = await startFakeNode(t, { features: ['assistant_pids'] })
  const runtime = createAgyRuntime({
    home,
    agyDir,
    agyBin: binary,
    personadBin: '/missing',
    socketPath: node.path,
    sharedAcpIdleMs: 250,
    promptInactivityMs: 0,
    log: () => {},
  })
  t.after(async () => {
    await runtime.stop()
    rmSync(home, { recursive: true, force: true })
  })
  await runtime.start()
  await node.ready

  const rowOf = (sid) => node.last('runtime.sessions')?.sessions.find((entry) => entry.session_id === sid)
  const calls = () => readFileSync(record, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)

  // Unloaded disk session exposes agy_pane_background for a live terminal pane without claiming ACP background ownership.
  const unloadedRow = await until('standalone pane session listed', () => rowOf(paneSid), SLOW)
  assert.equal(unloadedRow.background, undefined)
  assert.deepEqual(unloadedRow.agy_pane_background?.map((task) => task.id), [`${paneRawId}/task-1`])

  // Even if sharedAcp loads the conversation (for example via session.modes), the older pane task stays in
  // agy_pane_background rather than resurrecting as ACP background or locking sharedAcp idle retirement.
  await node.peer.call('session.modes', { session_id: paneSid, model: 'test-model' })
  const sharedPid = await until('sharedAcp resumed standalone session', () =>
    calls().findLast((entry) => entry.method === 'session/resume' && entry.params?.sessionId === paneRawId)?.worker, SLOW)

  await node.peer.call('session.rename', { session_id: paneSid, title: 'Pane inspected by ACP' })
  const inspectedRow = await until('inspected pane row published', () => {
    const row = rowOf(paneSid)
    return row?.title === 'Pane inspected by ACP' ? row : null
  }, SLOW)
  assert.equal(inspectedRow.background, undefined)
  assert.deepEqual(inspectedRow.agy_pane_background?.map((task) => task.id), [`${paneRawId}/task-1`])
  await until('sharedAcp retires cleanly after inspecting standalone pane session', () => !isAlive(sharedPid), SLOW)

  // When native summary marks the session killed, even a fresh-timestamp task is not reported as live ACP background.
  const liveSession = (await node.peer.call('session.start', { cwd: home })).session.session_id
  const liveRawId = toAgyId(liveSession)
  const liveStartedAt = new Date().toISOString()
  writeTranscriptSteps(agyDir, liveRawId, runningTaskSteps(liveRawId, [{ num: 1, startedAt: liveStartedAt, description: 'Killed session task' }]))
  upsertSummary(agyDir, liveRawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', killed: 1, stepCount: 2 })
  await node.peer.call('session.rename', { session_id: liveSession, title: 'Native killed session' })
  const killedRow = await until('killed session row published', () => {
    const row = rowOf(liveSession)
    return row?.title === 'Native killed session' ? row : null
  }, SLOW)
  assert.equal(killedRow.background, undefined)
})

test('tasks without a parseable started_at remain live while loaded, while tasks just outside the clock skew margin are filtered out', { timeout: 20_000 }, async (t) => {
  const { home, agyDir, rowOf, start, call } = await createFixture(t, {
    sharedAcpIdleMs: 5_000,
    promptInactivityMs: 0,
  })

  const session = await start()
  const rawId = toAgyId(session)
  const beforeCutoff = new Date(Date.now() - 5_000).toISOString()

  writeTranscriptSteps(agyDir, rawId, [
    {
      step_index: 0,
      type: 'PLANNER_RESPONSE',
      status: 'DONE',
      created_at: beforeCutoff,
      tool_calls: [
        { name: 'run_command', args: { CommandLine: JSON.stringify('echo old') } },
        { name: 'run_command', args: { CommandLine: JSON.stringify('echo untimestamped') } },
      ],
    },
    {
      step_index: 1,
      type: 'GENERIC',
      status: 'RUNNING',
      created_at: beforeCutoff,
      content: `Created At: ${beforeCutoff}\nTool is running as a background task with task id: ${rawId}/task-1\nTask Description: Outside skew window`,
    },
    {
      step_index: 2,
      type: 'GENERIC',
      status: 'RUNNING',
      content: `Tool is running as a background task with task id: ${rawId}/task-2\nTask Description: Missing timestamp`,
    },
  ])
  upsertSummary(agyDir, rawId, home, { status: 'CASCADE_RUN_STATUS_IDLE', stepCount: 3 })

  await call('rename', session, { title: 'Skew and missing timestamp check' })
  const listed = await until('only missing-timestamp task stays live', () => {
    const row = rowOf(session)
    return row?.title === 'Skew and missing timestamp check' && row?.background?.length === 1 ? row : null
  }, SLOW)
  assert.deepEqual(listed.background.map((task) => task.id), [`${rawId}/task-2`])
})
