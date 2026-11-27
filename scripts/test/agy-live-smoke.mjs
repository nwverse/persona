#!/usr/bin/env node
// Opt-in live smoke for the Antigravity CLI (agy), not an offline test (#16311). Run it after agy updates itself and
// before relying on the new version:
//
//   node scripts/test/agy-live-smoke.mjs [--agy PATH] [--agy-dir DIR] [--sessions N] [--timeout SECONDS]
//
// It starts the installed agy under a disposable HOME, so agy cannot write to its real data: ANTIGRAVITY_CLI_CONFIG_DIR
// alone does not isolate agy 1.5.18, which still logs to and lists from ~/.gemini. It checks:
// - the ACP handshake and that every ACP method the runtime calls is still routed (signed out, agy answers
//   "Authentication required" for a known method and -32601 for an unknown one);
// - the language server's port line and trajectory methods, also against read-only copies of the longest and newest
//   local conversations;
// - the real conversation_summaries.db schema and those conversations' transcript exports, opened read-only and
//   parsed with the runtime's own mapping.
// It sends no prompt and spends no tokens. Progress goes to stderr; stdout gets one line: PASS, FAIL or TIMEOUT.
// Exit codes are 0, 1 and 2. Conversation ids are shortened and no titles or content are printed.
import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { parseArgs } from 'node:util'
import { AgyAcpClient } from '../../integrations/antigravity/lib/acp.js'
import { readTranscript, stepsToItems } from '../../integrations/antigravity/lib/map.js'
import { NATIVE_METHODS, probeNative, probeSummaries, probeTranscripts, stepProblems, VERIFIED_AGY } from '../../integrations/antigravity/lib/probe.js'

const { values } = parseArgs({
  options: { agy: { type: 'string' }, 'agy-dir': { type: 'string' }, sessions: { type: 'string', default: '2' }, timeout: { type: 'string', default: '180' }, help: { type: 'boolean' } },
})
if (values.help) {
  process.stderr.write('Usage: node scripts/test/agy-live-smoke.mjs [--agy PATH] [--agy-dir DIR] [--sessions N] [--timeout SECONDS]\n')
  process.exit(0)
}
const onPath = () => (process.env.PATH || '').split(delimiter).map((dir) => join(dir, 'agy')).find((file) => existsSync(file))
const agy = values.agy || onPath() || join(homedir(), '.local', 'bin', 'agy')
const agyDir = values['agy-dir'] || process.env.ANTIGRAVITY_CLI_CONFIG_DIR || join(homedir(), '.gemini', 'antigravity-cli')
const longest = Math.max(0, Number.parseInt(values.sessions, 10) || 0)
const timeoutMs = Math.max(10, Number.parseInt(values.timeout, 10) || 180) * 1000

// The ACP methods the runtime calls (lib/acp.js and lib/runtime.js) with harmless params; session/cancel is a
// notification and has no answer to check.
const NOWHERE = '00000000-0000-0000-0000-000000000000'
const ACP_METHODS = [
  ['session/new', (cwd) => ({ cwd, mcpServers: [] })],
  ['session/resume', (cwd) => ({ sessionId: NOWHERE, cwd, mcpServers: [] })],
  ['session/list', () => ({})],
  ['session/prompt', () => ({ sessionId: NOWHERE, prompt: [{ type: 'text', text: 'unused' }] })],
  ['session/set_mode', () => ({ sessionId: NOWHERE, modeId: 'default' })],
  ['session/set_model', () => ({ sessionId: NOWHERE, modelId: 'unused' })],
  ['session/set_config_option', () => ({ sessionId: NOWHERE, configId: 'unused', value: 'unused' })],
  ['session/close', () => ({ sessionId: NOWHERE })],
]
const METHOD_NOT_FOUND = -32601

const started = Date.now()
const checks = []
let version = ''
let step = 'starting'
let acp
let scratch

const short = (id) => String(id).slice(0, 8)
const seconds = (ms) => `${(ms / 1000).toFixed(1)}s`
function record(name, ok, detail = '') {
  checks.push({ name, ok, detail })
  process.stderr.write(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? `: ${detail}` : ''}\n`)
}
async function attempt(name, fn) {
  step = name
  try {
    const detail = await fn()
    record(name, true, detail || '')
  } catch (error) {
    record(name, false, error?.message || String(error))
  }
}
async function cleanup() {
  // Let agy exit before its disposable HOME, which holds the conversation copies, is removed.
  try { await Promise.race([acp?.stop(), new Promise((resolve) => setTimeout(resolve, 5000))]) } catch {}
  if (scratch) rmSync(scratch, { recursive: true, force: true })
}
let finishing = false
async function finish(verdict, summary, code) {
  if (finishing) return
  finishing = true
  clearTimeout(timer)
  await cleanup()
  process.stdout.write(`${verdict} agy-live-smoke agy ${version || '(unknown version)'}: ${summary}\n`)
  process.exit(code)
}
const timer = setTimeout(() => finish('TIMEOUT', `${step} did not finish within ${seconds(timeoutMs)} (${checks.filter((c) => c.ok).length} of the checks before it passed)`, 2), timeoutMs)
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => finish('FAIL', `interrupted during ${step}`, 1))

/** Conversations to spot-check: the longest ones by summary step count and the newest, which have a stored copy. */
function pickConversations() {
  const db = new DatabaseSync(join(agyDir, 'conversation_summaries.db'), { readOnly: true })
  try {
    db.exec('PRAGMA query_only = 1; PRAGMA busy_timeout = 1000')
    const rows = [
      ...db.prepare('SELECT conversation_id, step_count FROM conversation_summaries ORDER BY step_count DESC LIMIT ?').all(longest * 3 + 3),
      ...db.prepare('SELECT conversation_id, step_count FROM conversation_summaries ORDER BY last_modified_time DESC LIMIT 3').all(),
    ].filter((row) => existsSync(join(agyDir, 'conversations', `${row.conversation_id}.db`)) && existsSync(join(agyDir, 'brain', row.conversation_id)))
    const picked = new Map()
    for (const row of rows.slice(0, rows.length - 3).slice(0, longest)) picked.set(row.conversation_id, { ...row, role: 'longest' })
    const newest = rows.slice(-3).find((row) => !picked.has(row.conversation_id)) || rows.at(-1)
    if (newest && !picked.has(newest.conversation_id)) picked.set(newest.conversation_id, { ...newest, role: 'newest' })
    return [...picked.values()]
  } finally {
    db.close()
  }
}

/** A consistent read-only copy of one stored conversation in the disposable agy directory. */
function copyConversation(id, target) {
  const source = new DatabaseSync(join(agyDir, 'conversations', `${id}.db`), { readOnly: true })
  try {
    source.exec('PRAGMA busy_timeout = 1000')
    source.prepare('VACUUM INTO ?').run(join(target, 'conversations', `${id}.db`))
  } finally {
    source.close()
  }
}

try {
  if (!existsSync(agy)) await finish('FAIL', `no agy binary at ${agy === values.agy ? agy : 'agy on PATH or ~/.local/bin/agy'}`, 1)
  scratch = mkdtempSync(join(tmpdir(), 'persona-agy-smoke-'))
  const fakeHome = join(scratch, 'home')
  const fakeAgyDir = join(fakeHome, '.gemini', 'antigravity-cli')
  const workspace = join(scratch, 'workspace')
  mkdirSync(join(fakeAgyDir, 'conversations'), { recursive: true })
  mkdirSync(workspace)

  let picked = []
  await attempt('local conversations', () => {
    if (!existsSync(join(agyDir, 'conversation_summaries.db'))) return 'none yet, skipping the long-conversation checks'
    picked = pickConversations()
    for (const row of picked) copyConversation(row.conversation_id, fakeAgyDir)
    return picked.length ? picked.map((row) => `${row.role} ${short(row.conversation_id)} (${row.step_count} summarized steps)`).join(', ') : 'none with a stored copy'
  })

  acp = new AgyAcpClient({
    bin: agy,
    env: {
      HOME: fakeHome, ANTIGRAVITY_CLI_CONFIG_DIR: fakeAgyDir, XDG_CONFIG_HOME: join(fakeHome, '.config'),
      XDG_DATA_HOME: join(fakeHome, '.local', 'share'), XDG_CACHE_HOME: join(fakeHome, '.cache'), XDG_STATE_HOME: join(fakeHome, '.local', 'state'),
    },
    initTimeoutMs: 60e3,
  })

  let initialized = false
  await attempt('ACP initialize', async () => {
    const init = await acp.ensureStarted()
    version = init?.agentInfo?.version || ''
    initialized = true
    const caps = init?.agentCapabilities || {}
    const session = caps.sessionCapabilities || {}
    const missing = [
      init?.protocolVersion !== 1 && `protocolVersion ${init?.protocolVersion}`,
      !version && 'agentInfo.version',
      !caps.loadSession && 'loadSession',
      ...['resume', 'list', 'close'].filter((name) => !session[name]).map((name) => `sessionCapabilities.${name}`),
    ].filter(Boolean)
    if (missing.length) throw new Error(`initialize no longer reports ${missing.join(', ')}`)
    const newer = version && VERIFIED_AGY !== version ? `; the probe was last verified on ${VERIFIED_AGY}` : ''
    return `agy ${version}, protocol 1, resume/list/close/load advertised${newer}`
  })
  if (!initialized) await finish('FAIL', 'agy did not answer ACP initialize', 1)

  await attempt('ACP method routing', async () => {
    const control = await acp._rawCall('session/persona_smoke_missing', {}, 15e3).then(() => undefined, (error) => error)
    if (control?.code !== METHOD_NOT_FOUND) throw new Error(`an unknown method answered ${control ? control.code : 'with a result'}, so missing methods cannot be told apart`)
    const missing = []
    const answers = new Set()
    for (const [method, params] of ACP_METHODS) {
      const answer = await acp._rawCall(method, params(workspace), 15e3).then(() => 'result', (error) => error)
      if (answer?.code === METHOD_NOT_FOUND) missing.push(method)
      else answers.add(answer === 'result' ? 'result' : answer?.message || String(answer?.code))
    }
    if (missing.length) throw new Error(`agy no longer serves ${missing.join(', ')}`)
    return `${ACP_METHODS.length} methods routed (answers: ${[...answers].join(' | ')})`
  })

  await attempt('native service', async () => {
    const result = await probeNative(acp, { waitMs: 30e3 })
    if (result.state !== 'ok') throw new Error(`${result.state}: ${result.detail}`)
    return `port ${acp.nativePort()} serves ${NATIVE_METHODS.join(' and ')}`
  })

  await attempt('conversation_summaries.db schema', () => {
    const result = probeSummaries(agyDir)
    if (result.state === 'incompatible' || result.state === 'unverified') throw new Error(result.detail)
    return result.state
  })

  await attempt('newest transcript export', () => {
    const result = probeTranscripts(agyDir)
    if (result.state === 'incompatible' || result.state === 'unverified') throw new Error(result.detail)
    return result.state
  })

  let fullDone = false
  for (const row of picked) {
    const id = row.conversation_id
    await attempt(`transcript ${short(id)}`, () => {
      const begin = Date.now()
      const transcript = readTranscript(join(agyDir, 'brain', id), true)
      if (!transcript.steps.length) throw new Error('no exported steps')
      const bad = transcript.steps.map((s) => stepProblems(s)).find((problems) => problems.length)
      if (bad) throw new Error(`a step lacks ${bad.join(', ')}`)
      const { items } = stepsToItems(transcript.steps, { full: false })
      const lag = transcript.steps.length - row.step_count
      return `${transcript.steps.length} steps${transcript.compact ? ' (compact export)' : ''} -> ${items.length} items in ${seconds(Date.now() - begin)}; summary ${lag > 0 ? `${lag} step${lag === 1 ? '' : 's'} behind` : 'up to date'}`
    })
    await attempt(`native tail ${short(id)}`, async () => {
      const begin = Date.now()
      const offset = Math.max(0, row.step_count - 3)
      const { steps = [] } = await acp.nativeRequest('GetCascadeTrajectorySteps', { cascadeId: id, stepOffset: offset, disableRehydration: true })
      if (!steps.length) throw new Error(`no steps from offset ${offset}`)
      const odd = steps.find((s) => typeof s?.type !== 'string' || !s.type.startsWith('CORTEX_STEP_TYPE_') || typeof s.status !== 'string')
      if (odd) throw new Error(`a step has type ${JSON.stringify(odd?.type)} and status ${JSON.stringify(odd?.status)}`)
      return `${steps.length} steps from offset ${offset} in ${seconds(Date.now() - begin)}`
    })
    if (fullDone) continue
    fullDone = true
    await attempt(`native full trajectory ${short(id)}`, async () => {
      const begin = Date.now()
      const snapshot = await acp.nativeRequest('GetCascadeTrajectory', { cascadeId: id, disableRehydration: true })
      const steps = snapshot?.trajectory?.steps
      if (snapshot?.trajectory?.cascadeId !== id || !Array.isArray(steps)) throw new Error('the answer has no trajectory.cascadeId and trajectory.steps')
      return `${steps.length} steps (${(JSON.stringify(snapshot).length / 2 ** 20).toFixed(1)} MiB) in ${seconds(Date.now() - begin)}`
    })
  }

  step = 'isolation'
  const isolated = existsSync(join(fakeAgyDir, 'conversation_summaries.db')) && !existsSync(join(fakeAgyDir, 'antigravity-oauth-token'))
  record('isolation', isolated, isolated ? 'agy kept its state in the disposable HOME and ran signed out' : 'agy did not use the disposable HOME as expected')
} catch (error) {
  record(step, false, error?.message || String(error))
}

const failed = checks.filter((check) => !check.ok)
const elapsed = seconds(Date.now() - started)
if (failed.length) await finish('FAIL', `${failed[0].name}: ${failed[0].detail} (${failed.length}/${checks.length} checks failed, ${elapsed})`, 1)
await finish('PASS', `${checks.length}/${checks.length} checks in ${elapsed}`, 0)
