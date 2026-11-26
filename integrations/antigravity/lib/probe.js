// Compatibility probe for the private Antigravity CLI (agy) surfaces this runtime reads (#16311).
//
// ACP is agy's public interface. The runtime also depends on three private surfaces, with no compatibility promise:
// the language server's HTTP Connect RPC port (announced in agy's log) and its trajectory methods, the
// conversation_summaries table, and the brain/<id>/.system_generated/logs/transcript(_full).jsonl exports. agy
// updates itself, so a change to any of them used to surface only as stalled or failed session reads.
//
// The probe runs once per agy version: a schema query, two bounded reads of one transcript export and two not-found
// RPCs. Reading sessions never waits for it. scripts/test/agy-live-smoke.mjs runs the same checks against a real agy.
import { closeSync, existsSync, openSync, readdirSync, readSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { RpcError } from '../../runtime-sdk/index.js'

export const INCOMPATIBLE = 'agy_incompatible'
// The newest agy release scripts/test/agy-live-smoke.mjs passed against. A newer one is only logged as untested.
export const VERIFIED_AGY = '1.5.18'
export const NATIVE_PORT_LINE = /random port at (\d+) for HTTP(?:\r?\n|$)/m
export const NATIVE_METHODS = Object.freeze(['GetCascadeTrajectory', 'GetCascadeTrajectorySteps'])
// The columns readConversationStates selects (runtime.js).
export const SUMMARY_COLUMNS = Object.freeze(['conversation_id', 'status', 'not_fully_idle', 'killed', 'step_count', 'title', 'workspace_uris', 'last_modified_time'])
// The fields every exported step carries and the mapping relies on (map.js).
export const STEP_FIELDS = Object.freeze({ step_index: 'number', type: 'string', source: 'string', status: 'string', created_at: 'string' })
// A conversation id agy cannot have, so trajectory reads answer not_found without loading anything.
export const PROBE_CASCADE = '00000000-0000-0000-0000-000000000000'

const TRANSCRIPT_WINDOW = 256 * 1024
const PROBLEMS = new Set(['incompatible', 'unreachable'])

const result = (surface, state, detail = '') => ({ surface, state, detail })

/** The port agy's language server announced in its log text, or 0. */
export function nativePortOf(text) {
  const port = Number(NATIVE_PORT_LINE.exec(text || '')?.[1])
  return port > 0 && port <= 65535 ? port : 0
}

/** The error a native request fails with when agy's language server no longer serves method. */
export function nativeMethodMissing(version, method, status) {
  return Object.assign(new Error(`agy ${version || '(unknown version)'} no longer serves ${method} on its language server (HTTP ${status})`), { code: INCOMPATIBLE })
}

/** Checks that conversation_summaries.db still has the table and columns the runtime selects. */
export function probeSummaries(agyDir) {
  const path = join(agyDir, 'conversation_summaries.db')
  if (!existsSync(path)) return result('summaries', 'absent', 'no conversation_summaries.db yet')
  let db
  try {
    db = new DatabaseSync(path, { readOnly: true })
    db.exec('PRAGMA query_only = 1; PRAGMA busy_timeout = 50')
    const columns = new Set(db.prepare('SELECT name FROM pragma_table_info(?)').all('conversation_summaries').map((row) => row.name))
    if (!columns.size) return result('summaries', 'incompatible', 'conversation_summaries.db has no conversation_summaries table')
    const missing = SUMMARY_COLUMNS.filter((column) => !columns.has(column))
    return missing.length
      ? result('summaries', 'incompatible', `conversation_summaries is missing ${missing.join(', ')}`)
      : result('summaries', 'ok')
  } catch (error) {
    // A busy or briefly locked database proves nothing either way.
    return result('summaries', 'unverified', `conversation_summaries.db unreadable: ${error.errstr || error.code || error.message}`)
  } finally {
    db?.close()
  }
}

/** The fields of one exported step that are missing or of another type. */
export function stepProblems(step) {
  if (!step || typeof step !== 'object' || Array.isArray(step)) return ['not an object']
  return Object.entries(STEP_FIELDS).filter(([field, type]) => typeof step[field] !== type).map(([field]) => field)
}

function readWindow(file, size, fromEnd) {
  const length = Math.min(size, TRANSCRIPT_WINDOW)
  const buf = Buffer.alloc(length)
  const fd = openSync(file, 'r')
  try {
    return buf.subarray(0, readSync(fd, buf, 0, length, fromEnd ? size - length : 0)).toString('utf8')
  } finally {
    closeSync(fd)
  }
}

/** The first and last complete lines within the file's first and last 256 KiB. */
function edgeLines(file) {
  const { size } = statSync(file)
  const head = readWindow(file, size, false)
  const tail = size > TRANSCRIPT_WINDOW ? readWindow(file, size, true) : head
  const lines = []
  const firstEnd = head.indexOf('\n')
  if (firstEnd > 0) lines.push(head.slice(0, firstEnd))
  // The tail window's first line may be cut off, and its last one may still be being written.
  const end = tail.lastIndexOf('\n')
  if (end > 0) {
    const complete = tail.slice(0, end)
    const lastStart = complete.lastIndexOf('\n')
    if (lastStart >= 0 || size <= TRANSCRIPT_WINDOW) lines.push(complete.slice(lastStart + 1))
  }
  return [...new Set(lines.filter((line) => line.trim()))]
}

/** The most recently written transcript export under agyDir/brain, or undefined. */
export function newestTranscript(agyDir) {
  const brain = join(agyDir, 'brain')
  let entries
  try {
    entries = readdirSync(brain, { withFileTypes: true })
  } catch {
    return undefined
  }
  let newest
  for (const entry of entries) {
    if (!entry.isDirectory()) continue
    for (const name of ['transcript_full.jsonl', 'transcript.jsonl']) {
      const file = join(brain, entry.name, '.system_generated', 'logs', name)
      let info
      try {
        info = statSync(file)
      } catch {
        continue
      }
      if (info.size > 0 && (!newest || info.mtimeMs > newest.mtimeMs)) newest = { file, mtimeMs: info.mtimeMs }
      break
    }
  }
  return newest?.file
}

/** Checks the step fields of one transcript export: by default the most recently written one. */
export function probeTranscripts(agyDir, file = newestTranscript(agyDir)) {
  if (!file) return result('transcripts', 'absent', 'no transcript exports yet')
  let lines
  try {
    lines = edgeLines(file)
  } catch (error) {
    return result('transcripts', 'unverified', `transcript export unreadable: ${error.code || error.message}`)
  }
  if (!lines.length) return result('transcripts', 'unverified', 'no complete step within 256 KiB of either end')
  for (const line of lines) {
    let step
    try {
      step = JSON.parse(line)
    } catch {
      return result('transcripts', 'incompatible', 'a transcript export line is not JSON')
    }
    const missing = stepProblems(step)
    if (missing.length) return result('transcripts', 'incompatible', `a transcript step lacks ${missing.join(', ')}`)
  }
  return result('transcripts', 'ok')
}

/**
 * Checks the language server's port line and its trajectory methods on acp's current process. A not_found answer
 * for PROBE_CASCADE shows that a method is still served. The ACP call that starts the language server is the only
 * one it makes.
 */
export async function probeNative(acp, { waitMs = 20e3, pollMs = 250, isStopped = () => false } = {}) {
  const deadline = Date.now() + waitMs
  let text = acp.nativeLog()
  if (!nativePortOf(text) && typeof acp.call === 'function') {
    // agy starts its language server lazily; session/list starts it, as nativeRequest does. Signed out, agy refuses
    // the list and still starts the server.
    await acp.call('session/list', {}, Math.max(1, waitMs)).catch(() => {})
    text = acp.nativeLog()
  }
  while (!nativePortOf(text) && Date.now() < deadline && !isStopped()) {
    await new Promise((resolve) => setTimeout(resolve, pollMs))
    text = acp.nativeLog()
  }
  if (!nativePortOf(text)) {
    if (isStopped()) return result('native', 'unverified', 'the agy process ended before it was probed')
    return text.trim()
      ? result('native', 'incompatible', 'agy\'s log no longer announces its language server port ("random port at N for HTTP")')
      : result('native', 'unverified', 'agy wrote nothing to its log file')
  }
  for (const method of NATIVE_METHODS) {
    try {
      await acp.nativeRequest(method, { cascadeId: PROBE_CASCADE, disableRehydration: true })
    } catch (error) {
      if (error?.code === 'not_found') continue
      if (error?.code === INCOMPATIBLE) return result('native', 'incompatible', error.message)
      if (isStopped()) return result('native', 'unverified', 'the agy process ended before it was probed')
      return result('native', 'unreachable', `${method}: ${error?.message || error}`)
    }
  }
  return result('native', 'ok')
}

function versionAbove(version, reference) {
  const parse = (v) => String(v).split(/[.-]/).slice(0, 3).map((part) => Number.parseInt(part, 10))
  const a = parse(version), b = parse(reference)
  if (a.some(Number.isNaN)) return false
  for (let i = 0; i < 3; i++) if ((a[i] || 0) !== (b[i] || 0)) return (a[i] || 0) > (b[i] || 0)
  return false
}

/** One verdict for an agy version: ok unless a surface is incompatible or unreachable, and its one log line. */
export function compatVerdict(version, results) {
  const name = `agy ${version || '(unknown version)'}`
  const problems = results.filter((entry) => PROBLEMS.has(entry.state))
  if (problems.length) {
    const what = problems.map((entry) => `${entry.surface}: ${entry.detail}`).join('; ')
    return { version, ok: false, problems, results, line: `${name} is not compatible with this Persona Antigravity runtime: ${what}. Update Persona, or keep the agy version that last passed scripts/test/agy-live-smoke.mjs.` }
  }
  const notes = results.filter((entry) => entry.state !== 'ok').map((entry) => `${entry.surface} ${entry.state}: ${entry.detail}`)
  if (versionAbove(version, VERIFIED_AGY)) notes.push(`newer than ${VERIFIED_AGY}, the last version scripts/test/agy-live-smoke.mjs passed`)
  return { version, ok: true, problems, results, line: `${name} compatibility: ${results.map((entry) => `${entry.surface} ${entry.state}`).join(', ')}${notes.length ? ` (${notes.join('; ')})` : ''}` }
}

/** The error an availability check fails with when the verdict is not ok. */
export function incompatibleError(verdict) {
  return new RpcError(verdict.line, INCOMPATIBLE, { version: verdict.version, surfaces: verdict.problems.map(({ surface, state, detail }) => ({ surface, state, detail })) })
}

/**
 * Probes agy once per version: observe(acp) after an ACP process starts (it waits for initialize), verdict(acp)
 * where a caller needs the answer. A verdict that could not check the native service is probed again on the next
 * process.
 */
export function createCompatProbe({ agyDir, log = () => {}, portWaitMs, pollMs }) {
  let probed, flight, stopped = false
  async function run(acp) {
    const version = acp.agentVersion || ''
    const files = [probeSummaries(agyDir), probeTranscripts(agyDir)]
    const proc = acp.proc
    const native = await probeNative(acp, { waitMs: portWaitMs, pollMs, isStopped: () => stopped || acp.proc !== proc })
    const verdict = compatVerdict(version, [native, ...files])
    if (!stopped) log(verdict.line)
    if (native.state === 'unverified') probed = undefined
    return verdict
  }
  function observe(acp) {
    if (stopped) return Promise.resolve(undefined)
    return Promise.resolve(acp.readyPromise).then(() => {
      if (stopped || !acp.proc) return undefined
      const version = acp.agentVersion || ''
      if (probed === version && flight) return flight
      probed = version
      flight = run(acp).catch((error) => {
        probed = undefined
        log(`agy compatibility probe failed: ${error?.message || error}`)
        return undefined
      })
      return flight
    }, () => undefined)
  }
  return {
    observe,
    async verdict(acp) {
      await acp.ensureStarted()
      return observe(acp)
    },
    stop() {
      stopped = true
    },
  }
}
