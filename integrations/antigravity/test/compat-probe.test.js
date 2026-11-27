// The agy compatibility probe (#16311): each private surface the runtime reads is checked once per agy version, and
// an incompatible one fails the availability check with a readable reason instead of surfacing as a 502 or a stall.
import assert from 'node:assert/strict'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, utimesSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { describe, it, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { AgyAcpClient } from '../lib/acp.js'
import {
  compatVerdict, createCompatProbe, INCOMPATIBLE, NATIVE_METHODS, nativePortOf, PROBE_CASCADE, probeNative, probeSummaries,
  probeTranscripts, stepProblems, SUMMARY_COLUMNS, VERIFIED_AGY,
} from '../lib/probe.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE = fileURLToPath(new URL('./fake-acp-compat.mjs', import.meta.url))
// agy 1.5.18's table, as created by agy (abridged to the columns that matter plus two the runtime ignores).
const SUMMARY_TABLE = 'CREATE TABLE `conversation_summaries` (`conversation_id` text,`title` text NOT NULL DEFAULT "",`preview` text NOT NULL DEFAULT "",`step_count` integer NOT NULL DEFAULT 0,`last_modified_time` datetime NOT NULL,`workspace_uris` text NOT NULL,`status` text NOT NULL DEFAULT "",`not_fully_idle` numeric NOT NULL DEFAULT false,`killed` numeric NOT NULL DEFAULT false,PRIMARY KEY (`conversation_id`))'
const STEP = { step_index: 0, source: 'USER_EXPLICIT', type: 'USER_INPUT', status: 'DONE', created_at: '2026-11-26T00:00:00Z', content: 'hi' }

function scratch(t, prefix = 'agy-compat-') {
  const dir = mkdtempSync(join(tmpdir(), prefix))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  return dir
}

function summaries(agyDir, sql = SUMMARY_TABLE) {
  mkdirSync(agyDir, { recursive: true })
  const db = new DatabaseSync(join(agyDir, 'conversation_summaries.db'))
  if (sql) db.exec(sql)
  db.close()
}

function transcript(agyDir, id, lines, name = 'transcript_full.jsonl') {
  const dir = join(agyDir, 'brain', id, '.system_generated', 'logs')
  mkdirSync(dir, { recursive: true })
  writeFileSync(join(dir, name), lines.map((line) => typeof line === 'string' ? line : JSON.stringify(line)).join('\n') + '\n')
  return join(dir, name)
}

describe('conversation_summaries.db', () => {
  it('passes the table agy 1.5.18 writes', (t) => {
    const agyDir = scratch(t)
    summaries(agyDir)
    assert.deepEqual(probeSummaries(agyDir), { surface: 'summaries', state: 'ok', detail: '' })
  })

  it('is absent, not incompatible, before agy has stored a conversation', (t) => {
    assert.equal(probeSummaries(scratch(t)).state, 'absent')
  })

  it('names a missing table', (t) => {
    const agyDir = scratch(t)
    summaries(agyDir, 'CREATE TABLE conversations (id text)')
    assert.deepEqual(probeSummaries(agyDir), { surface: 'summaries', state: 'incompatible', detail: 'conversation_summaries.db has no conversation_summaries table' })
  })

  it('names every missing column', (t) => {
    const agyDir = scratch(t)
    summaries(agyDir, SUMMARY_TABLE.replace('`not_fully_idle` numeric NOT NULL DEFAULT false,', '').replace('`step_count` integer NOT NULL DEFAULT 0,', ''))
    assert.deepEqual(probeSummaries(agyDir), { surface: 'summaries', state: 'incompatible', detail: 'conversation_summaries is missing not_fully_idle, step_count' })
  })

  it('is absent while SQLite has created the file and agy no table yet', (t) => {
    const agyDir = scratch(t)
    writeFileSync(join(agyDir, 'conversation_summaries.db'), '')
    assert.deepEqual(probeSummaries(agyDir), { surface: 'summaries', state: 'absent', detail: 'conversation_summaries.db has no tables yet' })
  })

  it('leaves an unreadable database unverified rather than incompatible', (t) => {
    const agyDir = scratch(t)
    writeFileSync(join(agyDir, 'conversation_summaries.db'), 'not sqlite')
    const result = probeSummaries(agyDir)
    assert.equal(result.state, 'unverified')
    assert.match(result.detail, /file is not a database/)
  })

  it('checks the columns the runtime selects', () => {
    assert.deepEqual([...SUMMARY_COLUMNS].sort(), ['conversation_id', 'killed', 'last_modified_time', 'not_fully_idle', 'status', 'step_count', 'title', 'workspace_uris'])
  })
})

describe('transcript exports', () => {
  it('passes steps with the fields the mapping relies on', (t) => {
    const agyDir = scratch(t)
    transcript(agyDir, 'a', [STEP, { ...STEP, step_index: 1, source: 'MODEL', type: 'PLANNER_RESPONSE' }])
    assert.equal(probeTranscripts(agyDir).state, 'ok')
  })

  it('is absent before agy has exported a conversation', (t) => {
    assert.equal(probeTranscripts(scratch(t)).state, 'absent')
  })

  it('names the fields a step lacks or carries with another type', (t) => {
    const agyDir = scratch(t)
    const { step_index: _index, ...renamed } = STEP
    transcript(agyDir, 'a', [STEP, { ...renamed, stepIndex: 1, created_at: 1 }])
    assert.deepEqual(probeTranscripts(agyDir), { surface: 'transcripts', state: 'incompatible', detail: 'a transcript step lacks step_index, created_at' })
    assert.deepEqual(stepProblems([]), ['not an object'])
  })

  it('flags an export that is no longer JSON lines', (t) => {
    const agyDir = scratch(t)
    transcript(agyDir, 'a', ['step 0: USER_INPUT hi'])
    assert.equal(probeTranscripts(agyDir).state, 'incompatible')
  })

  it('reads the most recently written export, and only its edges', (t) => {
    const agyDir = scratch(t)
    transcript(agyDir, 'old', ['not json'])
    const big = { ...STEP, content: 'x'.repeat(300 * 1024) }
    // The middle step is broken, outside both 256 KiB windows: the probe does not read whole exports.
    const file = transcript(agyDir, 'new', [STEP, big, { ...big, step_index: 'broken' }, big, { ...STEP, step_index: 4 }])
    const later = new Date(Date.now() + 5000)
    utimesSync(file, later, later)
    assert.equal(probeTranscripts(agyDir).state, 'ok')
    transcript(agyDir, 'new', [STEP, big, { ...STEP, step_index: 2, type: 7 }])
    utimesSync(file, later, later)
    assert.equal(probeTranscripts(agyDir).detail, 'a transcript step lacks type')
  })

  it('ignores a last line still being written', (t) => {
    const agyDir = scratch(t)
    const file = transcript(agyDir, 'a', [STEP])
    writeFileSync(file, `${JSON.stringify(STEP)}\n{"step_index":1,"sou`)
    assert.equal(probeTranscripts(agyDir).state, 'ok')
  })

  it('falls back to the compact export', (t) => {
    const agyDir = scratch(t)
    transcript(agyDir, 'a', [{ ...STEP, type: undefined }], 'transcript.jsonl')
    assert.equal(probeTranscripts(agyDir).detail, 'a transcript step lacks type')
  })
})

describe('native conversation service', () => {
  async function service(t, handler) {
    const calls = []
    const server = createServer((req, res) => {
      const chunks = []
      req.on('data', (chunk) => chunks.push(chunk)).on('end', () => {
        calls.push({ method: req.url.split('/').at(-1), params: JSON.parse(Buffer.concat(chunks).toString()) })
        handler(req, res)
      })
    })
    t.after(() => server.close())
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
    return { port: server.address().port, calls }
  }

  function client(t, text) {
    const dir = scratch(t, 'agy-compat-native-')
    const file = join(dir, 'native.log')
    writeFileSync(file, text)
    const acp = new AgyAcpClient()
    acp.ensureStarted = async () => {}
    acp.agentVersion = '9.0.0'
    acp.proc = {}
    acp.nativeLogs.set(acp.proc, { file })
    return { acp, file }
  }

  const line = (port) => `I1126 server.go:1351] Language server listening on random port at ${port} for HTTP\n`
  const notFound = (req, res) => res.writeHead(404).end('{"code":"not_found","message":"not_found: trajectory not found"}')

  it('reads the port agy announces and nothing else', () => {
    assert.equal(nativePortOf(line(34471)), 34471)
    assert.equal(nativePortOf('Language server listening on random port at 32479 for HTTPS (gRPC)\n'), 0)
    assert.equal(nativePortOf('Language server listening on port 34471\n'), 0)
    assert.equal(nativePortOf(line(70000)), 0)
  })

  it('passes when both trajectory methods answer not_found for the probe id', async (t) => {
    const { port, calls } = await service(t, notFound)
    const { acp } = client(t, line(port))
    assert.deepEqual(await probeNative(acp, { waitMs: 0 }), { surface: 'native', state: 'ok', detail: '' })
    assert.deepEqual(calls.map((c) => [c.method, c.params.cascadeId]), NATIVE_METHODS.map((method) => [method, PROBE_CASCADE]))
  })

  it('names a method agy no longer serves (a plain 404) and fails native requests with the same reason', async (t) => {
    const { port } = await service(t, (req, res) => res.writeHead(404, { 'Content-Type': 'text/plain' }).end('404 page not found\n'))
    const { acp } = client(t, line(port))
    const result = await probeNative(acp, { waitMs: 0 })
    assert.deepEqual(result, { surface: 'native', state: 'incompatible', detail: 'agy 9.0.0 no longer serves GetCascadeTrajectory on its language server (HTTP 404)' })
    await assert.rejects(acp.nativeRequest('GetCascadeTrajectorySteps', {}), (error) => error.code === INCOMPATIBLE && /no longer serves GetCascadeTrajectorySteps/.test(error.message))
  })

  it('treats a Connect unimplemented answer as a missing method', async (t) => {
    const { port } = await service(t, (req, res) => res.writeHead(404).end('{"code":"unimplemented","message":"unimplemented"}'))
    const { acp } = client(t, line(port))
    assert.equal((await probeNative(acp, { waitMs: 0 })).state, 'incompatible')
  })

  it('never takes a server error page for a missing method', async (t) => {
    for (const [status, body] of [[500, ''], [502, '<html>Bad Gateway</html>']]) {
      const { port } = await service(t, (req, res) => res.writeHead(status).end(body))
      const { acp } = client(t, line(port))
      await assert.rejects(acp.nativeRequest('GetCascadeTrajectory', {}), (error) => error.code === undefined && error.message === `Antigravity conversation service returned ${status}`)
      assert.equal((await probeNative(acp, { waitMs: 0 })).state, 'unreachable')
    }
  })

  it('keeps other native errors as they were', async (t) => {
    const { port } = await service(t, (req, res) => res.writeHead(500).end('{"code":"internal","message":"boom"}'))
    const { acp } = client(t, line(port))
    await assert.rejects(acp.nativeRequest('GetCascadeTrajectory', {}), (error) => error.code === 'internal' && error.message === 'boom')
  })

  it('reports a changed port line once agy has logged without it', async (t) => {
    const { acp } = client(t, 'Language server version: 9.0.0\nLanguage server listening on port 34471\n')
    const result = await probeNative(acp, { waitMs: 30, pollMs: 5 })
    assert.equal(result.state, 'incompatible')
    assert.match(result.detail, /no longer announces its language server port/)
  })

  it('waits for the port line agy writes after initialize', async (t) => {
    const { port } = await service(t, notFound)
    const { acp, file } = client(t, 'Language server version: 9.0.0\n')
    setTimeout(() => writeFileSync(file, `Language server version: 9.0.0\n${line(port)}`), 30)
    assert.equal((await probeNative(acp, { waitMs: 5000, pollMs: 5 })).state, 'ok')
  })

  it('starts agy\'s lazily started language server with the session/list native requests share', async (t) => {
    const { port, calls } = await service(t, notFound)
    const { acp, file } = client(t, 'Language server version: 9.0.0\n')
    const asked = []
    let started
    acp._rawCall = (method) => {
      asked.push(method)
      started ??= new Promise((resolve) => setTimeout(resolve, 50)).then(() => {
        writeFileSync(file, `Language server version: 9.0.0\n${line(port)}`)
        throw Object.assign(new Error('Authentication required'), { code: -32000 })
      })
      return started
    }
    const [probed, read] = await Promise.allSettled([probeNative(acp, { waitMs: 0 }), acp.nativeRequest('GetCascadeTrajectorySteps', { cascadeId: 'x' })])
    assert.equal(probed.value.state, 'ok')
    assert.equal(read.reason.message, 'Authentication required', 'the session read waited on the same call, not a second one')
    assert.deepEqual(asked, ['session/list'])
    assert.deepEqual(calls.map((c) => c.method), NATIVE_METHODS)
  })

  it('leaves an empty log unverified', async (t) => {
    const { acp } = client(t, '')
    assert.equal((await probeNative(acp, { waitMs: 20, pollMs: 5 })).state, 'unverified')
  })

  it('reports an announced port nothing answers on as unreachable', async (t) => {
    const { port } = await service(t, notFound)
    const closed = createServer()
    await new Promise((resolve) => closed.listen(0, '127.0.0.1', resolve))
    const free = closed.address().port
    await new Promise((resolve) => closed.close(resolve))
    assert.notEqual(free, port)
    const { acp } = client(t, line(free))
    const result = await probeNative(acp, { waitMs: 0 })
    assert.equal(result.state, 'unreachable')
    assert.match(result.detail, /^GetCascadeTrajectory: .*ECONNREFUSED/)
  })
})

describe('verdict', () => {
  const ok = (surface) => ({ surface, state: 'ok', detail: '' })

  it('is one line naming each surface', () => {
    const verdict = compatVerdict(VERIFIED_AGY, [ok('native'), ok('summaries'), { surface: 'transcripts', state: 'absent', detail: 'no transcript exports yet' }])
    assert.equal(verdict.ok, true)
    assert.equal(verdict.line, `agy ${VERIFIED_AGY} compatibility: native ok, summaries ok, transcripts absent (transcripts absent: no transcript exports yet)`)
  })

  it('notes a version newer than the last smoke-tested one without failing it', () => {
    const verdict = compatVerdict('1.6.0', [ok('native')])
    assert.equal(verdict.ok, true)
    assert.match(verdict.line, new RegExp(`newer than ${VERIFIED_AGY.replaceAll('.', '\\.')}`))
    assert.doesNotMatch(compatVerdict('1.5.17', [ok('native')]).line, /newer/)
  })

  it('fails on an incompatible or unreachable surface and says what to do', () => {
    const verdict = compatVerdict('1.6.0', [
      { surface: 'native', state: 'unreachable', detail: 'GetCascadeTrajectory: connect ECONNREFUSED' },
      { surface: 'summaries', state: 'incompatible', detail: 'conversation_summaries is missing step_count' },
      { surface: 'transcripts', state: 'unverified', detail: 'x' },
    ])
    assert.equal(verdict.ok, false)
    assert.deepEqual(verdict.problems.map((p) => p.surface), ['native', 'summaries'])
    assert.equal(verdict.line, 'agy 1.6.0 is not compatible with this Persona Antigravity runtime: native: GetCascadeTrajectory: connect ECONNREFUSED; summaries: conversation_summaries is missing step_count. Update Persona, or keep the agy version that last passed scripts/test/agy-live-smoke.mjs.')
  })
})

describe('once per agy version', () => {
  const fake = (text) => ({ agentVersion: '1.5.18', proc: {}, readyPromise: Promise.resolve(), ensureStarted: async () => {}, nativeLog: () => text })

  it('keeps a conclusive verdict for its version and probes a new version again', async (t) => {
    const agyDir = scratch(t)
    summaries(agyDir)
    transcript(agyDir, 'a', [STEP])
    const logs = []
    const probe = createCompatProbe({ agyDir, log: (line) => logs.push(line), portWaitMs: 0 })
    const acp = fake('Language server listening on port 1\n')
    const first = await probe.verdict(acp)
    assert.equal(first.ok, false)
    acp.proc = {}
    assert.equal(await probe.verdict(acp), first, 'the next process of the version reuses it')
    assert.equal(logs.length, 1)
    acp.agentVersion = '1.5.19'
    await probe.verdict(acp)
    assert.equal(logs.length, 2)
    probe.stop()
    acp.agentVersion = '1.5.20'
    assert.equal(await probe.verdict(acp), undefined)
    assert.equal(logs.length, 2)
  })

  it('probes again while a surface is absent or unverified, logging only a changed line', async (t) => {
    const agyDir = scratch(t)
    const logs = []
    const probe = createCompatProbe({ agyDir, log: (line) => logs.push(line), portWaitMs: 0 })
    const acp = fake('')
    const first = await probe.verdict(acp)
    assert.deepEqual(first.results.map((r) => r.state), ['unverified', 'absent', 'absent'])
    assert.equal((await probe.verdict(acp)).line, first.line)
    assert.equal(logs.length, 1, 'the same line is not logged twice')
    // agy creates its database and exports after the probe first ran: they are still checked.
    summaries(agyDir, SUMMARY_TABLE.replace('`killed` numeric NOT NULL DEFAULT false,', ''))
    transcript(agyDir, 'a', [{ ...STEP, type: 7 }])
    const later = await probe.verdict(acp)
    assert.deepEqual(later.problems.map((p) => p.detail), ['conversation_summaries is missing killed', 'a transcript step lacks type'])
    assert.equal(logs.length, 2)
  })

  it('does not keep a transient server error', async (t) => {
    const agyDir = scratch(t)
    summaries(agyDir)
    transcript(agyDir, 'a', [STEP])
    let failing = true
    const server = createServer((req, res) => {
      req.resume()
      req.on('end', () => failing ? res.writeHead(500).end('{"message":"transient"}') : res.writeHead(404).end('{"code":"not_found","message":"not found"}'))
    })
    t.after(() => server.close())
    await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
    const dir = scratch(t)
    const file = join(dir, 'native.log')
    writeFileSync(file, `Language server listening on random port at ${server.address().port} for HTTP\n`)
    const acp = new AgyAcpClient()
    acp.ensureStarted = async () => {}
    acp.agentVersion = '1.5.18'
    acp.proc = {}
    acp.nativeLogs.set(acp.proc, { file })
    const probe = createCompatProbe({ agyDir, portWaitMs: 0 })
    const first = await probe.verdict(acp)
    assert.equal(first.problems[0].state, 'unreachable')
    failing = false
    assert.equal((await probe.verdict(acp)).ok, true)
  })
})

async function runtimeFixture(t, { mode = 'served', version = '1.5.18', table = SUMMARY_TABLE, compatProbe = true, compatCheckWaitMs } = {}) {
  const home = scratch(t, 'agy-compat-runtime-')
  const agyDir = join(home, 'agy')
  summaries(agyDir, table)
  transcript(agyDir, 'a', [STEP])
  writeFileSync(join(home, 'native-mode'), mode)
  writeFileSync(join(home, 'version'), version)
  const binary = join(home, 'fake-agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${home}' "$@"\n`)
  chmodSync(binary, 0o700)
  const node = await startFakeNode(t)
  const logs = []
  const runtime = createAgyRuntime({ home, agyDir, agyBin: binary, personadBin: '/missing', socketPath: node.path, log: (line) => logs.push(line), compatProbe, compatPortWaitMs: 2000, compatPollMs: 10, compatCheckWaitMs, sharedAcpIdleMs: 50 })
  t.after(() => runtime.stop())
  runtime.start()
  await node.ready
  const lines = (name) => existsSync(join(home, name)) ? readFileSync(join(home, name), 'utf8').split('\n').filter(Boolean).map(JSON.parse) : []
  const check = () => node.peer.call('launchers.check', { cwd: home, prompt: 'Reply with OK' })
  return { home, node, logs, check, nativeCalls: () => lines('native-calls.jsonl'), starts: () => lines('acp-starts.jsonl') }
}

test('a compatible agy passes its check and is probed once for its version across processes', async (t) => {
  const f = await runtimeFixture(t)
  assert.equal((await f.check()).ok, true)
  await until('the idle process to be replaced', () => f.logs.some((line) => /exited/.test(line)))
  assert.equal((await f.check()).ok, true)
  assert.equal(f.starts().length, 2)
  assert.deepEqual(f.nativeCalls().map((c) => c.method), NATIVE_METHODS, 'two not-found reads for the version, none for the second process')
  assert.deepEqual(f.logs.filter((line) => /compatib/.test(line)), ['agy 1.5.18 compatibility: native ok, summaries ok, transcripts ok'])
})

for (const [name, options, reason] of [
  ['a missing summaries column', { table: SUMMARY_TABLE.replace('`step_count` integer NOT NULL DEFAULT 0,', '') }, 'summaries: conversation_summaries is missing step_count'],
  ['a missing native method', { mode: 'missing' }, 'native: agy 1.6.0 no longer serves GetCascadeTrajectory on its language server (HTTP 404)'],
  ['a changed port line', { mode: 'no-port' }, 'native: agy\'s log no longer announces its language server port'],
]) {
  test(`${name} fails the availability check with a readable reason before any prompt`, async (t) => {
    const f = await runtimeFixture(t, { version: '1.6.0', ...options })
    await assert.rejects(f.check(), (error) => error.code === INCOMPATIBLE && error.message.startsWith('agy 1.6.0 is not compatible') && error.message.includes(reason))
    assert.equal(f.logs.filter((line) => line.includes('is not compatible')).length, 1)
  })
}

test('an availability check waits for an unfinished probe only so long, then goes on', async (t) => {
  const f = await runtimeFixture(t, { mode: 'silent', compatCheckWaitMs: 100 })
  const begin = Date.now()
  assert.equal((await f.check()).ok, true)
  assert.ok(Date.now() - begin < 1500, `the check waited ${Date.now() - begin}ms for a probe still waiting for the port line`)
})

test('without the option the runtime never probes', async (t) => {
  const f = await runtimeFixture(t, { mode: 'missing', compatProbe: false })
  assert.equal((await f.check()).ok, true)
  assert.deepEqual(f.nativeCalls(), [])
  assert.deepEqual(f.logs.filter((line) => /compatib/.test(line)), [])
})
