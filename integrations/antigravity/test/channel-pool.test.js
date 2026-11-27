import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { toAgyId } from '../lib/map.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE_ACP_SOURCE = `
import { appendFileSync, existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { createInterface } from 'node:readline'

const home = process.argv[2]
const recordFile = join(home, 'events.jsonl')
const record = (ev) => appendFileSync(recordFile, JSON.stringify({ pid: process.pid, ...ev }) + '\\n')

record({
  kind: 'spawn',
  argv: process.argv.slice(3),
  key: process.env.GEMINI_API_KEY || '',
  base: process.env.GOOGLE_GEMINI_BASE_URL || '',
  provider: process.env.PERSONA_AGY_MODEL_PROVIDER || '',
})

if (process.argv.includes('models')) {
  const isProvider = process.argv.join(' ').includes('"modelProvider":"gemini"')
  if (isProvider) {
    console.log('gemini-4-neon-high\\tGemini 4 Neon High')
    console.log('gateway-special-high\\tGateway Special High')
  } else {
    console.log('native-oauth-model\\tNative OAuth Model')
  }
  process.exit(0)
}

const send = (obj) => process.stdout.write(JSON.stringify(obj) + '\\n')
let seq = 0
let authed = ''

process.on('SIGTERM', () => {
  record({ kind: 'exit', signal: 'SIGTERM' })
  process.exit(0)
})

createInterface({ input: process.stdin }).on('line', async (line) => {
  const msg = JSON.parse(line)
  record({ kind: 'rpc', method: msg.method, params: msg.params })
  if (msg.method === 'initialize') {
    send({
      jsonrpc: '2.0',
      id: msg.id,
      result: {
        protocolVersion: 1,
        agentInfo: { name: 'fake-pool', version: '1.0.0' },
        authMethods: [{ id: 'oauth-personal' }, { id: 'gemini-api-key' }],
      },
    })
    return
  }
  if (msg.method === 'authenticate') {
    authed = msg.params?.methodId || ''
    record({ kind: 'auth', methodId: authed })
    send({ jsonrpc: '2.0', id: msg.id, result: {} })
    return
  }
  if (msg.method === 'session/new') {
    seq++
    const id = '11111111-2222-3333-4444-' + String(process.pid % 100000).padStart(5, '0') + String(seq).padStart(7, '0')
    const brain = join(home, '.gemini', 'antigravity', 'brain', id)
    mkdirSync(brain, { recursive: true })
    writeFileSync(join(brain, 'transcript.json'), JSON.stringify({ steps: [] }))
    send({ jsonrpc: '2.0', id: msg.id, result: { sessionId: id } })
    return
  }
  if (msg.method === 'session/resume') {
    send({ jsonrpc: '2.0', id: msg.id, result: {} })
    return
  }
  if (msg.method === 'session/prompt') {
    const sid = msg.params?.sessionId
    const promptText = (msg.params?.prompt || []).map((b) => b.text || '').join(' ')
    // Support hold:<file> so tests can keep a turn active while a new channel_rev starts.
    const holdMatch = /hold:(\\S+)/.exec(promptText)
    if (holdMatch) {
      const gateFile = holdMatch[1]
      record({ kind: 'holding', sessionId: sid })
      while (!existsSync(gateFile)) {
        await new Promise((r) => setTimeout(r, 15))
      }
    }
    const reply = 'reply:' + (authed || 'oauth') + ':' + (process.env.GOOGLE_GEMINI_BASE_URL || 'native') + ':' + promptText
    if (process.env.GEMINI_API_KEY) {
      process.stderr.write('stderr diagnostic with secret ' + process.env.GEMINI_API_KEY + '\\n')
    }
    send({
      jsonrpc: '2.0',
      method: 'session/update',
      params: {
        sessionId: sid,
        update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: reply } },
      },
    })
    const logsDir = join(home, 'agy', 'brain', sid, '.system_generated', 'logs')
    mkdirSync(logsDir, { recursive: true })
    const rows = [
      { step_index: 0, source: 'USER_EXPLICIT', type: 'USER_INPUT', status: 'DONE', content: promptText },
      { step_index: 1, source: 'MODEL', type: 'PLANNER_RESPONSE', status: 'DONE', content: reply },
    ]
    const jsonl = rows.map((r) => JSON.stringify(r)).join('\\n') + '\\n'
    writeFileSync(join(logsDir, 'transcript.jsonl'), jsonl)
    writeFileSync(join(logsDir, 'transcript_full.jsonl'), jsonl)
    record({ kind: 'prompt_done', sessionId: sid, reply, authed, key: process.env.GEMINI_API_KEY || '' })
    send({ jsonrpc: '2.0', id: msg.id, result: { stopReason: 'end_turn' } })
    return
  }
  if (msg.id !== undefined) {
    send({ jsonrpc: '2.0', id: msg.id, result: {} })
  }
})
`

async function poolFixture(t, options = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-pool-'))
  const agyDir = join(home, 'agy')
  mkdirSync(agyDir, { recursive: true })
  const scriptPath = join(home, 'fake-acp.mjs')
  const bin = join(home, 'fake-agy')
  writeFileSync(scriptPath, FAKE_ACP_SOURCE)
  writeFileSync(join(home, 'events.jsonl'), '')
  writeFileSync(bin, `#!/bin/sh\nexec '${process.execPath}' '${scriptPath}' '${home}' "$@"\n`)
  chmodSync(bin, 0o700)
  const logs = []
  const node = await startFakeNode(t, { features: ['assistant_pids'] })
  const runtime = createAgyRuntime({
    home,
    agyDir,
    agyBin: bin,
    socketPath: node.path,
    sharedAcpIdleMs: options.sharedAcpIdleMs ?? 300_000,
    assistantAcpIdleMs: options.assistantAcpIdleMs ?? 300_000,
    exportPollMs: options.exportPollMs ?? 20,
    env: options.env ?? { ...process.env },
    log: (m) => logs.push(m),
  })
  t.after(async () => {
    await runtime.stop()
    rmSync(home, { recursive: true, force: true })
  })
  runtime.start()
  await node.ready
  const events = () => readFileSync(join(home, 'events.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  return { home, agyDir, bin, node, runtime, logs, events }
}

test('native OAuth and multiple managed API-key channels coexist concurrently in isolated pools', async (t) => {
  // Even if the host shell accidentally had GEMINI_API_KEY set, the native OAuth pool strips it.
  const { home, node, logs, events } = await poolFixture(t, {
    env: {
      ...process.env,
      GEMINI_API_KEY: 'ambient-leaked-key',
      GOOGLE_GEMINI_BASE_URL: 'https://ambient.example.com',
      PERSONA_AGY_MODEL_PROVIDER: '',
    },
  })

  const chAlpha = {
    id: 'gw-alpha',
    auth_mode: 'gemini-api-key',
    rev: 'r1',
    env: {
      GOOGLE_GEMINI_BASE_URL: 'https://alpha.example.com',
      GEMINI_API_KEY: 'secret-alpha-001',
      PERSONA_AGY_MODEL_PROVIDER: 'gemini',
    },
  }
  const chBeta = {
    id: 'gw-beta',
    auth_mode: 'gemini-api-key',
    rev: 'r1',
    env: {
      GOOGLE_GEMINI_BASE_URL: 'https://beta.example.com',
      GEMINI_API_KEY: 'secret-beta-002',
      PERSONA_AGY_MODEL_PROVIDER: 'gemini',
    },
  }

  const nativeStart = await node.peer.call('session.start', { cwd: home })
  const alphaStart = await node.peer.call('session.start', { cwd: home, channel: chAlpha })
  const betaStart = await node.peer.call('session.start', { cwd: home, channel: chBeta })

  await Promise.all([
    node.peer.call('session.send', { session_id: nativeStart.session.session_id, text: 'hello-native' }),
    node.peer.call('session.send', { session_id: alphaStart.session.session_id, text: 'hello-alpha', channel: chAlpha }),
    node.peer.call('session.send', { session_id: betaStart.session.session_id, text: 'hello-beta', channel: chBeta }),
  ])

  await until('all three turns completed', () => events().filter((e) => e.kind === 'prompt_done').length === 3)

  const all = events()
  const doneNative = all.find((e) => e.kind === 'prompt_done' && e.sessionId === toAgyId(nativeStart.session.session_id))
  const doneAlpha = all.find((e) => e.kind === 'prompt_done' && e.sessionId === toAgyId(alphaStart.session.session_id))
  const doneBeta = all.find((e) => e.kind === 'prompt_done' && e.sessionId === toAgyId(betaStart.session.session_id))

  // Three distinct ACP child processes (different PIDs).
  assert.equal(new Set([doneNative.pid, doneAlpha.pid, doneBeta.pid]).size, 3)

  // Native pool never authenticated with gemini-api-key and stripped ambient provider env vars.
  const nativeSpawn = all.find((e) => e.kind === 'spawn' && e.pid === doneNative.pid)
  assert.equal(nativeSpawn.key, '')
  assert.equal(nativeSpawn.base, '')
  assert.equal(nativeSpawn.provider, '')
  assert.equal(doneNative.authed, '')
  assert.match(doneNative.reply, /^reply:oauth:native:hello-native$/)

  // Each managed channel pool authenticated with gemini-api-key and received only its own env.
  assert.equal(doneAlpha.authed, 'gemini-api-key')
  assert.equal(doneAlpha.key, 'secret-alpha-001')
  assert.match(doneAlpha.reply, /^reply:gemini-api-key:https:\/\/alpha\.example\.com:hello-alpha$/)

  assert.equal(doneBeta.authed, 'gemini-api-key')
  assert.equal(doneBeta.key, 'secret-beta-002')
  assert.match(doneBeta.reply, /^reply:gemini-api-key:https:\/\/beta\.example\.com:hello-beta$/)

  // Channel secrets are redacted from all companion logs.
  const combinedLogs = logs.join('\n')
  assert.ok(!combinedLogs.includes('secret-alpha-001'), 'alpha secret leaked into logs')
  assert.ok(!combinedLogs.includes('secret-beta-002'), 'beta secret leaked into logs')
})

test('superseded channel_rev pool keeps running active turns and is reaped once idle', async (t) => {
  const { home, node, events } = await poolFixture(t, { sharedAcpIdleMs: 50 })
  const gateFile = join(home, 'release-r1')

  const chR1 = {
    id: 'gw-rotating',
    auth_mode: 'gemini-api-key',
    rev: 'r1',
    env: {
      GOOGLE_GEMINI_BASE_URL: 'https://r1.example.com',
      GEMINI_API_KEY: 'secret-r1',
      PERSONA_AGY_MODEL_PROVIDER: 'gemini',
    },
  }
  const chR2 = {
    id: 'gw-rotating',
    auth_mode: 'gemini-api-key',
    rev: 'r2',
    env: {
      GOOGLE_GEMINI_BASE_URL: 'https://r2.example.com',
      GEMINI_API_KEY: 'secret-r2',
      PERSONA_AGY_MODEL_PROVIDER: 'gemini',
    },
  }

  // Start a session on r1 and hold its turn in flight.
  const s1 = await node.peer.call('session.start', { cwd: home, channel: chR1 })
  await node.peer.call('session.send', { session_id: s1.session.session_id, text: `hold:${gateFile}`, channel: chR1 })
  await until('r1 turn is holding', () => events().some((e) => e.kind === 'holding' && e.sessionId === toAgyId(s1.session.session_id)))
  const r1Pid = events().find((e) => e.kind === 'holding').pid

  // Meanwhile, a new session starts on r2 (superseding r1) and finishes immediately.
  const s2 = await node.peer.call('session.start', { cwd: home, channel: chR2 })
  await node.peer.call('session.send', { session_id: s2.session.session_id, text: 'hello-r2', channel: chR2 })
  await until('r2 turn completed', () => events().some((e) => e.kind === 'prompt_done' && e.sessionId === toAgyId(s2.session.session_id)))
  const r2Pid = events().find((e) => e.kind === 'prompt_done' && e.sessionId === toAgyId(s2.session.session_id)).pid
  assert.notEqual(r1Pid, r2Pid, 'r2 must run in a new isolated pool')

  // Wait longer than the reaper interval: r1 is still active so it MUST NOT be killed.
  await delay(150)
  assert.equal(events().some((e) => e.kind === 'exit' && e.pid === r1Pid), false, 'active superseded pool was killed')

  // Release the held turn on r1 and verify it finishes cleanly.
  writeFileSync(gateFile, 'go')
  await until('r1 turn completed cleanly', () => events().some((e) => e.kind === 'prompt_done' && e.sessionId === toAgyId(s1.session.session_id)))

  // Once idle, the superseded r1 pool is automatically reaped.
  await until('superseded r1 pool reaped after idle', () => events().some((e) => e.kind === 'exit' && e.pid === r1Pid))
})

test('launchers.models isolates native OAuth catalog from managed channel catalog', async (t) => {
  const gwServer = createServer((req, res) => {
    if (req.headers['x-goog-api-key'] !== 'secret-models-key') {
      res.writeHead(401).end('{}')
      return
    }
    res.setHeader('content-type', 'application/json')
    res.end(JSON.stringify({
      models: [{ name: 'models/gateway-special', baseModelId: 'gateway-special', supportedGenerationMethods: ['generateContent'] }],
    }))
  })
  await new Promise((resolve) => gwServer.listen(0, '127.0.0.1', resolve))
  t.after(() => gwServer.close())
  const gwBase = `http://127.0.0.1:${gwServer.address().port}`

  const { node } = await poolFixture(t)
  const nativeRes = await node.peer.call('launchers.models', { launcher: 'agy', refresh: true })
  assert.deepEqual(nativeRes.models.map((m) => m.id), ['native-oauth-model'])

  const managedRes = await node.peer.call('launchers.models', {
    launcher: 'agy',
    refresh: true,
    channel: {
      id: 'gw-models',
      auth_mode: 'gemini-api-key',
      rev: 'r1',
      env: { GOOGLE_GEMINI_BASE_URL: gwBase, GEMINI_API_KEY: 'secret-models-key', PERSONA_AGY_MODEL_PROVIDER: 'gemini' },
    },
  })
  assert.deepEqual(managedRes.models.map((m) => m.id), ['gateway-special-high'])

  // Re-querying native models still returns the native catalog, completely unpolluted.
  const nativeAgain = await node.peer.call('launchers.models', { launcher: 'agy' })
  assert.deepEqual(nativeAgain.models.map((m) => m.id), ['native-oauth-model'])
})
