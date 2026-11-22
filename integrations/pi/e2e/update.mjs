// End-to-end check of pi's self-update (docs/integrations/pi.md "Automatic updates"): a real pi-web with this package installed from a store
// (settings.json packages: <store>/current/pi — the one-time switch), the companion run from there under a supervisor
// that starts it again when it exits (launchd's KeepAlive), DSH's llm-mock-server as the model, and a real
// `personad node --stdio` whose packages come from a directory (PERSONA_RUNTIME_PACKAGES), so it can carry another
// version than the one installed.
//
//   DSH_CHECKOUT=<dsh source checkout, built> PERSONAD=<personad binary> node integrations/pi/e2e/update.mjs
//
// 1. The node carries the installed version: nothing is offered; a session from the phone runs in pi-web.
// 2. A node carrying a newer version replaces it (a hub update does this): the extension in pi-web installs it and,
//    once idle, reloads its session onto it — same pi-web process, same session; the companion installs it, exits and
//    comes back on it. The session still answers from the phone, and the reload never reached the model.

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, readlinkSync, rmSync, writeFileSync } from 'node:fs'
import { createServer as netServer } from 'node:net'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'
import { installCurrent, PACKAGES, packageHash, readShipped } from '../../runtime-sdk/lib/package.js'

const here = dirname(fileURLToPath(import.meta.url))
const integrations = resolve(here, '..', '..')
const dshCheckout = process.env.DSH_CHECKOUT
const personad = process.env.PERSONAD
if (!dshCheckout || !personad) {
  console.error('set DSH_CHECKOUT (a built dsh source checkout, for its mock model server) and PERSONAD (a personad binary)')
  process.exit(2)
}
const NAME = 'persona-pi-runtime'
const root = mkdtempSync(join('/tmp', 'jpi-upd-'))
const agentDir = join(root, 'agent')
const work = join(root, 'work')
const home = join(root, 'home')
const pkgs = join(root, 'packages')
for (const d of [agentDir, work, home]) mkdirSync(d, { recursive: true })
const store = join(home, '.persona', 'runtime-packages', NAME)
const { check, summary } = checks()
const freePort = () => new Promise((r) => { const s = netServer().listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => r(p)) }) })

// ---- the installed version (the one-time switch) and what the node carries ----
const shippedFiles = readShipped(integrations, PACKAGES[NAME])
const shipped = packageHash(shippedFiles)
installCurrent(store, shipped, shippedFiles)
const shippedVersion = /RUNTIME_VERSION = '([^']+)'/.exec(readFileSync(join(integrations, 'pi', 'lib', 'pi.js'), 'utf8'))[1]
const nextVersion = `${shippedVersion}-e2e-next`
function carry(edit = (_p, t) => t) {
  rmSync(join(pkgs, NAME), { recursive: true, force: true })
  const files = shippedFiles.map((f) => ({ path: f.path, data: Buffer.from(edit(f.path, f.data.toString('utf8'))) }))
  for (const f of files) {
    const path = join(pkgs, NAME, ...f.path.split('/'))
    mkdirSync(dirname(path), { recursive: true })
    writeFileSync(path, f.data)
  }
  return packageHash(files)
}

// ---- the model and pi's config ----
const { startMockLlmServer } = await import(pathToFileURL(join(dshCheckout, 'packages/test-support/llm-mock-server/lib/index.js')).href)
const llm = await startMockLlmServer({ sequence: ['success'], repeatLast: true, successText: 'Hello from pi-web' })
writeFileSync(join(agentDir, 'models.json'), JSON.stringify({ providers: { mock: {
  baseUrl: llm.baseURL.replace(/\/$/, ''), api: 'anthropic-messages', apiKey: 'mock-key',
  models: [{ id: 'mock-model', name: 'Mock', input: ['text'], contextWindow: 200000, maxTokens: 8192, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
} } }))
writeFileSync(join(agentDir, 'settings.json'), JSON.stringify({ defaultProvider: 'mock', defaultModel: 'mock-model', quietStartup: true, packages: [join(store, 'current', 'pi')] }))
writeFileSync(join(agentDir, 'persona.json'), JSON.stringify({ askUser: false }))

// ---- the node, pi-web and the supervised companion ----
let node
const startTheNode = async () => { node = await startNode({ personad, keep: true, home, env: { PERSONA_RUNTIME_PACKAGES: pkgs } }) }
const nodeLog = () => node.log.join('')
carry()
await startTheNode()
const port = await freePort()
const env = { ...process.env, HOME: home, PI_CODING_AGENT_DIR: agentDir, PERSONA_RUNTIME_SOCKET: node.runtimeSocket, PI_OFFLINE: '1',
  PI_WEB_NO_OPEN: '1', PI_WEB_SKIP_VERSION_CHECK: '1', PI_WEB_IDLE_TIMEOUT_MS: '600000', PERSONA_PI_DEBUG: '1' }
const logs = { web: [], companion: [] }
const web = spawn('pi-web', ['--port', String(port)], { cwd: work, env, stdio: ['ignore', 'pipe', 'pipe'] })
web.stdout.on('data', (d) => logs.web.push(String(d)))
web.stderr.on('data', (d) => logs.web.push(String(d)))
let companion
let companionStarts = 0
let stopping = false
function superviseCompanion() { // what launchd's KeepAlive does
  companionStarts++
  companion = spawn(process.execPath, [join(store, 'current', 'pi', 'companion.mjs'), '--port', String(port), '--socket', node.runtimeSocket], { cwd: work, env, stdio: ['ignore', 'ignore', 'pipe'] })
  companion.stderr.on('data', (d) => logs.companion.push(String(d)))
  companion.once('exit', () => { if (!stopping) setTimeout(superviseCompanion, 300) })
}
superviseCompanion()

async function stop(child) {
  if (!child || child.exitCode !== null) return
  const exited = new Promise((r) => child.once('exit', r))
  child.kill('SIGTERM')
  await Promise.race([exited, new Promise((r) => setTimeout(r, 10_000))])
}
async function cleanup() {
  stopping = true
  await stop(companion)
  await stop(web)
  node?.stop()
  await llm.close?.()
  if (process.env.KEEP !== '1') rmSync(root, { recursive: true, force: true })
}
const connectedAs = (rt, version) => new RegExp(`runtime ${rt} ${version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} #\\d+ connected`)
const row = async (sid) => ((await node.call('sessions.list')).sessions ?? []).find((s) => s.session_id === sid)

try {
  // 1. The node carries the installed version.
  await until('pi-web up', async () => fetch(`http://127.0.0.1:${port}/api/agent/running`).then((r) => r.ok, () => false), 60_000)
  await until('the companion connected', () => connectedAs('pi-files', shippedVersion).test(nodeLog()), 60_000)
  const { session } = await node.call('session.start', { launcher: 'pi-web', cwd: work, async: true })
  const sid = session.session_id
  await until('served by the extension in pi-web', async () => (await row(sid))?.source_instance_id === 'pi-web', 60_000)
  await node.call('session.watch', { session_id: sid, since_seq: 0 })
  await node.call('session.send', { session_id: sid, text: 'hello from the phone', client_msg_id: randomUUID() })
  await until('the reply', () => node.items(sid).some((i) => i.text === 'Hello from pi-web'), 60_000)
  check('installed from the store, the extension in pi-web and the companion run the installed version', connectedAs('pi-web', shippedVersion).test(nodeLog()))
  check('the node carrying the same version offers nothing', !/offering package/.test(nodeLog()))
  check('pi loads the extension through <store>/current', readlinkSync(join(store, 'current')) === shipped)

  // 2. A node carrying a newer version replaces it.
  const next = carry((path, text) => (path === 'pi/lib/pi.js' ? text.replace(`RUNTIME_VERSION = '${shippedVersion}'`, `RUNTIME_VERSION = '${nextVersion}'`) : text))
  const webPid = web.pid
  const starts = companionStarts
  node.stop()
  await new Promise((r) => setTimeout(r, 500))
  await startTheNode()
  await until('the extension in pi-web on the new version', () => connectedAs('pi-web', nextVersion).test(nodeLog()), 120_000)
  check('the extension in pi-web switched in place: same pi-web process', web.exitCode === null && web.pid === webPid, nextVersion)
  await until('the companion on the new version', () => connectedAs('pi-files', nextVersion).test(nodeLog()), 120_000)
  check('the companion installed it, exited and came back on it', companionStarts > starts, `${companionStarts - starts} restart(s)`)
  check('current points at the node\'s version', readlinkSync(join(store, 'current')) === next)

  const served = await until('the session served by the new extension', async () => { const r = await row(sid); return r?.source_instance_id === 'pi-web' ? r : undefined }, 60_000)
  check('the session is still pi-web\'s, the same one', served.session_id === sid)
  await node.call('session.watch', { session_id: sid, since_seq: 0 })
  await node.call('session.send', { session_id: sid, text: 'still there?', client_msg_id: randomUUID() })
  await until('the reply after the switch', async () => (await node.call('session.items', { session_id: sid, turns: 1 })).items.some((i) => i.role === 'user' && i.text === 'still there?'), 60_000)
  await until('answered', async () => (await node.call('session.items', { session_id: sid, turns: 1 })).items.some((i) => i.text === 'Hello from pi-web'), 60_000)
  check('after the switch it answers from the phone', true)
  const all = await node.call('session.items', { session_id: sid, turns: 10 })
  check('the reload ran as a command, never as a message to the model', !all.items.some((i) => /persona-reload/.test(i.text ?? '')))
} catch (err) {
  check('run', false, err.message)
  console.log('--- node log ---\n' + nodeLog().split('\n').filter((l) => !/herdr/.test(l)).slice(-30).join('\n'))
  console.log('--- pi-web log ---\n' + logs.web.join('').split('\n').filter((l) => /persona|error/i.test(l)).slice(-30).join('\n'))
  console.log('--- companion log ---\n' + logs.companion.join('').slice(-1500))
}
await cleanup()
const failed = summary()
process.exit(failed ? 1 : 0)
