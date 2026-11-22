// End-to-end check of the DSH plugin's self-update (docs/integrations/dsh.md "Automatic updates", docs/integrations/runtimes.md "Auto-update"): a real
// `dsh web` with the plugin's loader (index.js) and a real `personad node --stdio` whose packages come from a directory
// (PERSONA_RUNTIME_PACKAGES), so it can carry another version of the plugin than DSH runs without another build.
//
//   DSH_CHECKOUT=<dsh source checkout, built> PERSONAD=<personad binary> node integrations/dsh/e2e/update.mjs
//
// 1. The node carries what the plugin ships: nothing is offered, the shipped version runs from the store.
// 2. The node is replaced by one carrying a newer plugin (a hub update does this): the plugin is offered it, fetches
//    it, switches without DSH's plugin manager, and works — a session started and answered from the "phone".
// 3. DSH restarts: the plugin starts the version the node sent, with nothing fetched again.

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { scriptedModel } from '../../runtime-sdk/testing/model.mjs'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'
import { PACKAGES, packageHash, readShipped } from '../../runtime-sdk/lib/package.js'

const here = dirname(fileURLToPath(import.meta.url))
const integrations = join(here, '..', '..')
const dshCheckout = process.env.DSH_CHECKOUT
const personad = process.env.PERSONAD
if (!dshCheckout || !personad) {
  console.error('set DSH_CHECKOUT (a built dsh source checkout) and PERSONAD (a personad binary)')
  process.exit(2)
}
const NAME = 'persona-dsh-runtime'
const keep = process.env.KEEP === '1'
const root = mkdtempSync(join('/tmp', 'jdsh-upd-'))
const dshHome = join(root, 'dsh')
const work = join(root, 'work')
const home = join(root, 'home')
const pkgs = join(root, 'packages')
for (const d of [dshHome, work, home]) mkdirSync(d, { recursive: true })
const store = join(home, '.persona', 'runtime-packages', NAME)
const { check, summary } = checks()
let dsh
let node

async function stopDsh() {
  if (dsh && dsh.exitCode === null) {
    const exited = new Promise((r) => dsh.once('exit', r))
    dsh.kill('SIGTERM')
    await Promise.race([exited, new Promise((r) => setTimeout(r, 10_000))])
  }
}
async function cleanup() {
  await stopDsh()
  node?.stop()
  if (!keep) rmSync(root, { recursive: true, force: true })
}
process.on('SIGINT', () => cleanup().finally(() => process.exit(130)))

/** The node's package directory: the plugin's files as shipped, with edit(path, text) applied. */
function carry(edit = (_p, t) => t) {
  rmSync(join(pkgs, NAME), { recursive: true, force: true })
  const files = readShipped(integrations, PACKAGES[NAME]).map((f) => ({ path: f.path, data: Buffer.from(edit(f.path, f.data.toString('utf8'))) }))
  for (const f of files) {
    const path = join(pkgs, NAME, ...f.path.split('/'))
    mkdirSync(dirname(path), { recursive: true })
    writeFileSync(path, f.data)
  }
  return packageHash(files)
}

const nodeLog = () => node.log.join('')
async function startTheNode() {
  node = await startNode({ personad, keep: true, home, env: { PERSONA_RUNTIME_PACKAGES: pkgs } })
}

const model = await scriptedModel(dshCheckout)
const patch = join(root, 'patch.yml')
writeFileSync(patch, `- insert:
    - id: persona-dsh-runtime
      name: ${join(integrations, 'dsh', 'index.js')}
      config:
        socket: ${join(home, '.persona', 'runtime.sock')}
        interactions: phone
`)
const dshLog = []
function startDsh() {
  dsh = spawn(process.execPath, [join(dshCheckout, 'apps/cli/lib/bin.js'), '--profile', 'web', '--patch', patch, '--no-open', '--port', '0'], {
    cwd: work,
    env: { ...process.env, DSH_HOME: dshHome, DEEPSEEK_BASE_URL: `${model.url}/v1`, DEEPSEEK_API_KEY: 'mock-key' },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  dsh.stdout.on('data', (d) => dshLog.push(String(d)))
  dsh.stderr.on('data', (d) => dshLog.push(String(d)))
}
const connectedAs = (version) => new RegExp(`runtime dsh ${version.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')} #\\d+ connected`)
const shippedVersion = /RUNTIME_VERSION = '([^']+)'/.exec(readFileSync(join(integrations, 'dsh', 'lib', 'dsh.js'), 'utf8'))[1]
const nextVersion = `${shippedVersion}-e2e-next`

try {
  // 1. The node carries what DSH runs.
  const shipped = carry()
  await startTheNode()
  startDsh()
  await until('the plugin connected', () => connectedAs(shippedVersion).test(nodeLog()), 120_000)
  await new Promise((r) => setTimeout(r, 1500))
  check('the node carrying the same package offers nothing', !/offering package/.test(nodeLog()))
  check('the shipped version runs from the store, under its hash', existsSync(join(store, shipped, 'dsh', 'lib', 'plugin.js')) && !existsSync(join(store, 'current.json')), shipped.slice(0, 12))

  // 2. A node with a newer plugin replaces it.
  const next = carry((path, text) => (path === 'dsh/lib/dsh.js' ? text.replace(`RUNTIME_VERSION = '${shippedVersion}'`, `RUNTIME_VERSION = '${nextVersion}'`) : text))
  node.stop()
  await new Promise((r) => setTimeout(r, 500))
  await startTheNode()
  await until('the plugin running the node\'s version', () => connectedAs(nextVersion).test(nodeLog()), 120_000)
  check('the new node offers its package and sends it', /offering package persona-dsh-runtime/.test(nodeLog()) && /sent package persona-dsh-runtime/.test(nodeLog()))
  check('the plugin switched without DSH\'s plugin manager, and says the new version', true, nextVersion)
  const current = JSON.parse(readFileSync(join(store, 'current.json'), 'utf8'))
  check('the store says which version runs, and keeps it under its hash', current.hash === next && current.shipped === shipped && existsSync(join(store, next, 'dsh', 'lib', 'dsh.js')))

  await until('the dsh launcher on the new node', async () => ((await node.call('launchers.list')).launchers ?? []).some((l) => l.name === 'dsh'), 60_000)
  const { session } = await node.call('session.start', { launcher: 'dsh', cwd: work, async: true })
  const sid = session.session_id
  await until('the new session listed', async () => ((await node.call('sessions.list')).sessions ?? []).some((s) => s.session_id === sid), 30_000)
  await node.call('session.watch', { session_id: sid, since_seq: 0 })
  await model.script({ sequence: ['success'], successText: 'Answered by the updated plugin' })
  await node.call('session.send', { session_id: sid, text: 'are you there?', client_msg_id: randomUUID() })
  await until('the reply', () => node.items(sid).some((i) => i.role === 'assistant' && i.text === 'Answered by the updated plugin'), 60_000)
  check('the switched plugin works: a session started and answered from the phone', true)

  // 3. DSH restarts: the version the node sent, at once.
  const offered = (nodeLog().match(/offering package/g) ?? []).length
  const sent = (nodeLog().match(/sent package/g) ?? []).length
  const runs = (nodeLog().match(connectedAs(nextVersion)) ?? []).length
  await stopDsh()
  startDsh()
  await until('the plugin back after DSH restarted', () => (nodeLog().match(new RegExp(connectedAs(nextVersion).source, 'g')) ?? []).length > runs, 120_000)
  check('after a DSH restart the stored version runs, nothing offered or fetched again',
    (nodeLog().match(/offering package/g) ?? []).length === offered && (nodeLog().match(/sent package/g) ?? []).length === sent)
} catch (err) {
  check('run', false, err.message)
  console.log('--- node log ---\n' + node?.log.join('').split('\n').filter((l) => !/herdr/.test(l)).slice(-30).join('\n'))
  console.log('--- dsh log ---\n' + dshLog.join('').split('\n').filter((l) => /persona/i.test(l)).slice(-30).join('\n'))
} finally {
  await model.close()
  await cleanup()
}
const failed = summary()
process.exit(failed ? 1 : 0)
