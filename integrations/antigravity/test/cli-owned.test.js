import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { transcriptState } from '../lib/map.js'
import { createAgyRuntime } from '../lib/runtime.js'

// A conversation the agy CLI (its terminal UI) has open is another agy process's (#13451): the CLI holds an exclusive
// flock on presence/<conversation>.lock while it has it open and leaves the empty file when it exits. The companion
// must not resume it into its own ACP, install a cut from its lagging summary, or send to it, and while agy's summary
// lags the CLI's writing its status comes from the transcript.

const RAW = '11111111-2222-4333-8444-555555555555'
const SESSION = `agy-${RAW}`
const FAKE = fileURLToPath(new URL('./fake-acp-rewind.mjs', import.meta.url))
const at = '2026-01-01T00:00:00Z'
const step = (index, type, fields = {}) => ({ step_index: index, type, status: 'DONE', created_at: at, ...fields })
const finished = [
  step(0, 'USER_INPUT', { content: 'Check the build' }),
  step(1, 'PLANNER_RESPONSE', { content: 'Running it', tool_calls: [{ name: 'run_command' }] }),
  step(2, 'GENERIC', { content: 'Build passed' }),
  step(3, 'PLANNER_RESPONSE', { content: 'The build passes.' }),
]
const running = finished.slice(0, 2)
const nativeRecord = (s) => ({
  type: `CORTEX_STEP_TYPE_${s.type}`,
  metadata: { source: s.type === 'USER_INPUT' ? 'CORTEX_STEP_SOURCE_USER_EXPLICIT' : 'MODEL', createdAt: s.created_at },
  ...(s.type === 'USER_INPUT' ? { userInput: { items: [{ text: s.content }] } } : {}),
  ...(s.type === 'PLANNER_RESPONSE' ? { plannerResponse: { response: s.content } } : {}),
})
const minutesAgo = (n) => new Date(Date.now() - n * 60_000)

// The /proc/locks line of an exclusive flock on file, as Linux writes it (major:minor in hex, inode in decimal).
function lockLine(file) {
  const { dev, ino } = statSync(file, { bigint: true })
  const major = dev >> 8n & 0xfffn | dev >> 32n & ~0xfffn, minor = dev & 0xffn | dev >> 12n & 0xffffff00n
  return `1: FLOCK  ADVISORY  WRITE 4242 ${major.toString(16).padStart(2, '0')}:${minor.toString(16).padStart(2, '0')}:${ino} 0 EOF\n`
}

// lock: 'held' or 'free' (a lock table that says so), 'unreadable' (none can be read: where the lock is not knowable),
// 'kernel' (the real /proc/locks), or 'none' (no presence file: never opened in the CLI).
async function fixture(t, { steps = finished, stepCount = 2, lock = 'held', mtime, snapshot = {} } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-cli-owned-'))
  const config = join(home, 'config')
  const logs = join(config, 'brain', RAW, '.system_generated/logs')
  mkdirSync(logs, { recursive: true })
  const transcript = join(logs, 'transcript_full.jsonl')
  const write = (values, when) => {
    writeFileSync(transcript, values.map((r) => JSON.stringify(r)).join('\n') + '\n')
    when && utimesSync(transcript, when, when)
  }
  write(steps, mtime)
  writeFileSync(join(home, 'calls.jsonl'), '')
  writeFileSync(join(home, 'trajectory.json'), JSON.stringify({
    status: 'CASCADE_RUN_STATUS_IDLE', trajectory: { cascadeId: RAW, steps: steps.map(nativeRecord) }, ...snapshot,
  }))
  const db = new DatabaseSync(join(config, 'conversation_summaries.db'))
  db.exec('CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, status TEXT, not_fully_idle INTEGER, killed INTEGER, step_count INTEGER, title TEXT, workspace_uris TEXT, last_modified_time TEXT)')
  db.prepare('INSERT INTO conversation_summaries VALUES (?, ?, 0, 0, ?, ?, ?, ?)')
    .run(RAW, 'CASCADE_RUN_STATUS_IDLE', stepCount, 'Build check', JSON.stringify([`file://${home}`]), new Date().toISOString())
  const presence = join(config, 'presence', `${RAW}.lock`)
  if (lock !== 'none') {
    mkdirSync(join(config, 'presence'))
    writeFileSync(presence, '')
  }
  const table = join(home, 'locks')
  const setLock = (held) => writeFileSync(table, held ? lockLine(presence) : '')
  let procLocks = table
  if (lock === 'unreadable') procLocks = join(home, 'no-lock-table')
  else if (lock === 'kernel') procLocks = undefined
  else setLock(lock === 'held')
  const binary = join(home, 'agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${home}' "$@"\n`)
  chmodSync(binary, 0o700)
  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({ home, agyDir: config, agyBin: binary, socketPath: node.path, log: () => {}, ...procLocks === undefined ? {} : { procLocks } })
  t.after(async () => { await runtime.stop(); db.close(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  await until('inventory', () => node.last('runtime.sessions')?.sessions.length)
  const calls = () => readFileSync(join(home, 'calls.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  const methods = () => calls().map((c) => c.method)
  const status = async () => {
    const before = node.all('runtime.sessions').length
    await node.peer.call('session.rename', { session_id: SESSION, title: `Build check ${before}` })
    await until('inventory refreshed', () => node.all('runtime.sessions').length > before)
    return node.last('runtime.sessions').sessions.find((s) => s.session_id === SESSION)?.status
  }
  const refused = async (method, params) => {
    const err = await node.peer.call(method, { session_id: SESSION, ...params }).then(() => null, (e) => e)
    assert.ok(err, `${method} was not refused`)
    assert.equal(err.code, 'open_elsewhere', `${method}: ${err.message}`)
    assert.deepEqual(err.data, { where: 'agy_cli', delivered: 'none' }, method)
  }
  return { home, node, presence, write, setLock, calls, methods, status, refused, db }
}

test('summary lag on a conversation open in the CLI is no rewind: no resume, no cut, status working', async (t) => {
  const { node, methods, status } = await fixture(t)
  const page = await node.peer.call('session.items', { session_id: SESSION })
  assert.deepEqual(page.items.map((i) => i.text).filter(Boolean).at(-1), 'The build passes.')
  assert.ok(!page.history.includes(':cut:'), page.history)
  assert.equal(await status(), 'working')
  const native = methods()
  assert.ok(!native.includes('session/resume') && !native.includes('session/load'), native.join(' '))
  assert.ok(!native.includes('GetCascadeTrajectory'), native.join(' '))
})

test('the CLI quiet on a complete reply reads done; an open tool call stays unknown', async (t) => {
  const { write, status, methods } = await fixture(t, { stepCount: 1, mtime: minutesAgo(5) })
  assert.equal(await status(), 'done')
  write(running, minutesAgo(5))
  assert.equal(await status(), 'unknown')
  write(running)
  assert.equal(await status(), 'working')
  assert.ok(!methods().includes('session/resume'))
})

test('send, rewind, fork and mode changes to a conversation open in the CLI are refused before agy sees them', async (t) => {
  const { refused, methods } = await fixture(t, { mtime: minutesAgo(5) })
  await refused('session.send', { text: 'Also run the tests', client_msg_id: 'm1' })
  await refused('session.rewind', { prompt: 'Check the build', occurrence: 1, prompts_after: 0, restore: 'conversation' })
  await refused('session.fork', { at_item_id: '' })
  await refused('session.modes', { permission_mode: 'plan' })
  const native = methods()
  for (const m of ['session/prompt', 'session/resume', 'session/load', 'session/set_mode']) assert.ok(!native.includes(m), `${m}: ${native.join(' ')}`)
})

test('after the CLI closes it, a finished reply reads done and a check resumes it only for the check', async (t) => {
  const { node, status, methods } = await fixture(t, { lock: 'free', mtime: minutesAgo(5) })
  assert.equal(await status(), 'done')
  const page = await node.peer.call('session.items', { session_id: SESSION })
  assert.ok(!page.history.includes(':cut:'), page.history)
  await until('the check closes what it resumed', () => methods().includes('session/close'))
  const native = methods()
  assert.ok(native.indexOf('session/resume') < native.indexOf('GetCascadeTrajectory'), native.join(' '))
  assert.ok(native.indexOf('GetCascadeTrajectory') < native.indexOf('session/close'), native.join(' '))
})

test('a CLI that closed the conversation moments ago still reads as its writer, but sends go through', async (t) => {
  const { node, status, methods, setLock } = await fixture(t, { lock: 'free', steps: running, stepCount: 1 })
  assert.equal(await status(), 'working')
  await node.peer.call('session.items', { session_id: SESSION })
  assert.ok(!methods().includes('session/resume'))
  setLock(false)
  await node.peer.call('session.send', { session_id: SESSION, text: 'Go on', client_msg_id: 'm1' })
  await until('the send reached agy', () => methods().includes('session/prompt'))
})

test('a held lock that is released lets the companion take the conversation again', async (t) => {
  const { node, refused, setLock, methods } = await fixture(t, { mtime: minutesAgo(5) })
  await refused('session.send', { text: 'First', client_msg_id: 'm1' })
  setLock(false)
  await delay(1100) // the lock table is read at most once a second
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second', client_msg_id: 'm2' })
  await until('the send reached agy', () => methods().includes('session/prompt'))
})

test('where the lock cannot be read, a presence conversation is never checked, and refused only while the CLI writes', async (t) => {
  const { node, write, refused, methods, status } = await fixture(t, { lock: 'unreadable' })
  assert.equal(await status(), 'working')
  await refused('session.send', { text: 'Now', client_msg_id: 'm1' })
  write(finished, minutesAgo(5))
  assert.equal(await status(), 'done')
  await node.peer.call('session.items', { session_id: SESSION })
  assert.ok(!methods().includes('GetCascadeTrajectory'))
  await node.peer.call('session.send', { session_id: SESSION, text: 'Later', client_msg_id: 'm2' })
  await until('the send reached agy', () => methods().includes('session/prompt'))
})

test("the companion's own turn and its export do not make the conversation look like the CLI's", async (t) => {
  const { node, methods } = await fixture(t, {
    lock: 'unreadable', stepCount: 10, mtime: minutesAgo(5),
    snapshot: { promptPlan: [{ reply: 'Done once.', export: true }, { reply: 'Done twice.', export: true }] },
  })
  const seen = node.all('runtime.sessions').length
  const since = () => node.all('runtime.sessions').slice(seen).map((e) => e.sessions.find((s) => s.session_id === SESSION)?.status)
  await node.peer.call('session.send', { session_id: SESSION, text: 'Once', client_msg_id: 'm1' })
  // Its export was just written: the next send comes while the transcript is fresh, after the turn is over.
  await until('first turn over', () => { const s = since(), w = s.indexOf('working'); return w >= 0 && s.slice(w + 1).includes('done') }, 8000)
  await node.peer.call('session.send', { session_id: SESSION, text: 'Twice', client_msg_id: 'm2' })
  await until('second turn', () => methods().filter((m) => m === 'session/prompt').length === 2)
})

test('a conversation never opened in the CLI keeps the summary rules', async (t) => {
  const { node, status, methods } = await fixture(t, { lock: 'none' })
  assert.equal(await status(), 'unknown')
  await node.peer.call('session.items', { session_id: SESSION })
  await until('the cut check ran', () => methods().includes('GetCascadeTrajectory'))
})

test('a real flock on the presence file reads as held', { skip: process.platform !== 'linux' || spawnSync('python3', ['-c', '']).status !== 0 }, async (t) => {
  const { refused, presence } = await fixture(t, { lock: 'kernel', mtime: minutesAgo(5) })
  const holder = spawn('python3', ['-c', 'import fcntl,sys,time\nf=open(sys.argv[1],"a")\nfcntl.flock(f,fcntl.LOCK_EX)\nprint("locked",flush=True)\ntime.sleep(60)', presence])
  t.after(() => holder.kill())
  await new Promise((resolve) => holder.stdout.once('data', resolve))
  await delay(1100)
  await refused('session.send', { text: 'Now', client_msg_id: 'm1' })
})

test('summary lag: CLI evidence decides working and done, never idle for new work', () => {
  const idle = { status: 'CASCADE_RUN_STATUS_IDLE', step_count: 1 }
  const reply = step(3, 'PLANNER_RESPONSE', { content: 'Finished' })
  const tool = step(3, 'PLANNER_RESPONSE', { content: 'Running', tool_calls: [{ name: 'run_command' }] })
  const input = step(3, 'USER_INPUT', { content: 'Next' })
  const cases = [
    ['no evidence keeps unknown', [reply], idle, undefined, { status: 'unknown', reason: 'summary_behind' }],
    ['the CLI writing', [tool], idle, { writing: true }, { status: 'working' }],
    ['the CLI writing a reply', [reply], idle, { writing: true }, { status: 'working' }],
    ['quiet on a complete reply', [reply], idle, { quiet: true }, { status: 'done' }],
    ['quiet on an open tool call', [tool], idle, { quiet: true }, { status: 'unknown', reason: 'summary_behind' }],
    ['quiet on new input', [input], idle, { quiet: true }, { status: 'unknown', reason: 'summary_behind' }],
    ['closed on a complete reply', [reply], idle, { closed: true, writing: true }, { status: 'done' }],
    ['closed mid-tool and quiet', [tool], idle, { closed: true, quiet: true }, { status: 'unknown', reason: 'summary_behind' }],
    ['no summary, the CLI writing', [input], undefined, { writing: true }, { status: 'working' }],
    ['no summary, quiet reply', [reply], undefined, { quiet: true }, { status: 'done' }],
    ['no summary, no evidence', [reply], undefined, undefined, { status: 'unknown', reason: 'no_summary' }],
    ['summary covers it', [reply], { ...idle, step_count: 4 }, { writing: true }, { status: 'done' }],
    ['an error ends it', [step(3, 'ERROR_MESSAGE')], idle, { writing: true }, { status: 'idle' }],
    ['native work wins', [reply], { status: 'CASCADE_RUN_STATUS_RUNNING' }, { quiet: true }, { status: 'working' }],
    ['no run state recorded, the CLI writing', [tool], { status: '', step_count: 1 }, { writing: true }, { status: 'working' }],
    ['no run state recorded, quiet reply', [reply], { status: '', step_count: 1 }, { quiet: true }, { status: 'done' }],
    ['no run state recorded, covered', [reply], { status: '', step_count: 4 }, undefined, { status: 'done' }],
    ['a set unknown run state stays unknown', [reply], { status: 'FUTURE_STATUS', step_count: 4 }, { quiet: true }, { status: 'unknown', reason: 'summary_not_idle' }],
  ]
  for (const [name, steps, summary, cli, expected] of cases) assert.deepEqual(transcriptState(steps, summary, cli), expected, name)
})
