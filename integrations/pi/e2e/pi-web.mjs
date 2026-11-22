// End-to-end check of pi-web in Persona (docs/integrations/pi.md "Validation"): a real pi-web on a spare port with this package installed
// the way a user installs it (settings.json packages), the companion (companion.mjs), a real `personad node --stdio`
// played as the hub by the runtime SDK's harness, and DSH's llm-mock-server as the model. pi-web's idle reaping is
// cut to a few seconds so the run sees a session go back to disk and wake up again on the next send.
//
//   DSH_CHECKOUT=<dsh source checkout, built> PERSONAD=<personad binary> node integrations/pi/e2e/pi-web.mjs

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { createServer as netServer } from 'node:net'
import { request as httpRequest, createServer } from 'node:http'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const pkg = resolve(here, '..')
const dshCheckout = process.env.DSH_CHECKOUT
const personad = process.env.PERSONAD
if (!dshCheckout || !personad) {
  console.error('set DSH_CHECKOUT (a built dsh source checkout, for its mock model server) and PERSONAD (a personad binary)')
  process.exit(2)
}
const root = mkdtempSync(join('/tmp', 'jpiweb-e2e-'))
const agentDir = join(root, 'agent')
const work = join(root, 'work')
const home = join(root, 'home')
for (const d of [agentDir, work, home]) mkdirSync(d, { recursive: true })
const { check, summary } = checks()
const freePort = () => new Promise((r) => { const s = netServer().listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => r(p)) }) })

// ---- the model ----
const { startMockLlmServer } = await import(pathToFileURL(join(dshCheckout, 'packages/test-support/llm-mock-server/lib/index.js')).href)
const llm = await startMockLlmServer({ sequence: ['success'], repeatLast: true, successText: 'Hello from pi-web' })
writeFileSync(join(agentDir, 'models.json'), JSON.stringify({ providers: { mock: {
  baseUrl: llm.baseURL.replace(/\/$/, ''), api: 'anthropic-messages', apiKey: 'mock-key',
  models: [{ id: 'mock-model', name: 'Mock', input: ['text'], contextWindow: 200000, maxTokens: 8192, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
} } }))
// The package as a user installs it (pi install <dir> writes the same line).
writeFileSync(join(agentDir, 'settings.json'), JSON.stringify({ defaultProvider: 'mock', defaultModel: 'mock-model', quietStartup: true, packages: [pkg] }))
writeFileSync(join(agentDir, 'persona.json'), JSON.stringify({ askUser: false }))

// ---- the node, pi-web and the companion ----
const node = await startNode({ personad })
const port = await freePort()
const env = { ...process.env, HOME: home, PI_CODING_AGENT_DIR: agentDir, PERSONA_RUNTIME_SOCKET: node.runtimeSocket, PI_OFFLINE: '1',
  PI_WEB_NO_OPEN: '1', PI_WEB_SKIP_VERSION_CHECK: '1', PI_WEB_IDLE_TIMEOUT_MS: '6000' }
const logs = { web: [], companion: [] }
const web = spawn('pi-web', ['--port', String(port)], { cwd: work, env, stdio: ['ignore', 'pipe', 'pipe'] })
web.stdout.on('data', (d) => logs.web.push(String(d)))
web.stderr.on('data', (d) => logs.web.push(String(d)))
const companion = spawn(process.execPath, [join(pkg, 'companion.mjs'), '--port', String(port), '--socket', node.runtimeSocket], { cwd: work, env, stdio: ['ignore', 'ignore', 'pipe'] })
companion.stderr.on('data', (d) => logs.companion.push(String(d)))

async function stop(child) {
  if (child.exitCode !== null) return
  const exited = new Promise((r) => child.once('exit', r))
  child.kill('SIGTERM')
  await Promise.race([exited, new Promise((r) => setTimeout(r, 10_000))])
}
async function cleanup() {
  await stop(companion)
  await stop(web)
  node.stop()
  await llm.close?.()
  if (process.env.KEEP !== '1') rmSync(root, { recursive: true, force: true })
}

const rows = async () => (await node.call('sessions.list')).sessions ?? []
const row = async (sid) => (await rows()).find((s) => s.session_id === sid)
const replies = (sid) => node.items(sid).filter((i) => i.role === 'assistant' && i.kind === 'text').length

try {
  await until('pi-web up', async () => fetch(`http://127.0.0.1:${port}/api/agent/running`).then((r) => r.ok, () => false), 60_000)
  const launchers = await until('the pi-web launcher', async () => {
    const { launchers } = await node.call('launchers.list')
    return launchers.find((l) => l.name === 'pi-web') ? launchers : undefined
  }, 30_000)
  check('before any session is open, the companion offers pi-web as a launcher', launchers.some((l) => l.name === 'pi-web' && l.source === 'pi-files' && l.agent === 'pi'))

  // A session started from the phone runs in pi-web; the extension there serves it.
  const { session } = await node.call('session.start', { launcher: 'pi-web', cwd: work, async: true })
  const sid = session.session_id
  check('a session starts from the phone in pi-web', typeof sid === 'string' && sid.length > 8, sid)
  const live = await until('served by the extension in pi-web', async () => { const r = await row(sid); return r?.source_instance_id === 'pi-web' ? r : undefined }, 60_000)
  check('while it runs in pi-web the extension there has it', live.agent === 'pi' && live.capabilities?.send === true)
  await node.call('session.watch', { session_id: sid, since_seq: 0 })
  await node.call('session.send', { session_id: sid, text: 'hello from the phone', client_msg_id: randomUUID() })
  await until('the reply', () => replies(sid) >= 1, 60_000)
  check('a send is answered through pi-web', node.items(sid).some((i) => i.text === 'Hello from pi-web') && node.items(sid).some((i) => i.role === 'user' && i.text === 'hello from the phone'))

  // pi-web reaps the idle session: it goes back to disk, the companion lists it.
  const off = await until('reaped back to disk', async () => { const r = await row(sid); return r?.source_instance_id === 'pi-files' ? r : undefined }, 60_000)
  check('once pi-web lets it go, the companion lists it from disk', off.status === 'offline', off.status)
  const p = await node.call('session.items', { session_id: sid, turns: 2 })
  check('its history pages from disk', p.items.some((i) => i.text === 'Hello from pi-web'))

  // The next send wakes it up in pi-web.
  const before = replies(sid)
  const sent = await node.call('session.send', { session_id: sid, text: 'are you there?', client_msg_id: randomUUID() }).then(() => 'sent', (e) => e.message)
  check('a send to it from the phone goes through', sent === 'sent', sent)
  await until('the reply after waking', async () => (await node.call('session.items', { session_id: sid, turns: 1 })).items.some((i) => i.role === 'user' && i.text === 'are you there?') && (await node.call('session.items', { session_id: sid, turns: 1 })).items.some((i) => i.text === 'Hello from pi-web'), 60_000).catch(() => {})
  const after = await node.call('session.items', { session_id: sid, turns: 1 })
  check('pi-web opens it again and answers', after.items.some((i) => i.role === 'user' && i.text === 'are you there?') && after.items.some((i) => i.text === 'Hello from pi-web'))
  await until('the extension has it again', async () => (await row(sid))?.source_instance_id === 'pi-web', 30_000).catch(() => {})
  check('the extension in pi-web takes it back while it runs', (await row(sid))?.source_instance_id === 'pi-web')
  check('items pushed while it was watched kept coming', replies(sid) >= before)

  // A session made in pi-web's own UI shows up too.
  const res = await fetch(`http://127.0.0.1:${port}/api/agent/new`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ cwd: work, type: 'prompt', message: 'from the browser' }) }).then((r) => r.json())
  await until('the browser\'s session listed', async () => !!(await row(res.sessionId)), 30_000).catch(() => {})
  check('a session started in pi-web\'s browser UI is listed', !!(await row(res.sessionId)))
} catch (err) {
  check('run', false, err.message)
  console.log('--- sessions ---\n' + JSON.stringify((await rows().catch(() => [])).map((r) => ({ id: r.session_id, status: r.status, src: r.source_instance_id, agent: r.agent }))))
  console.log('--- node log ---\n' + node.log.join('').slice(-1500))
  console.log('--- pi-web log ---\n' + logs.web.join('').slice(-2000))
  console.log('--- companion log ---\n' + logs.companion.join('').slice(-1500))
}
const failed = summary()
await cleanup()
process.exit(failed ? 1 : 0)
