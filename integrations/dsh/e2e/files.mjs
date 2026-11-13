// End-to-end check of the DSH companion (docs/integrations/dsh.md "When DSH is not running"): a real `dsh web` with the plugin (isolated
// DSH_HOME, DSH's own mock model) runs a turn and stops; the companion then lists that session from the store as
// offline and pages it; a message from the "phone" wakes DSH through the configured command and is handed over to
// the plugin, which runs it and takes the session back. A real `personad node --stdio` sits in between (the harness
// plays the hub).
//
//   DSH_CHECKOUT=<dsh source checkout, built> PERSONAD=<personad binary> node integrations/dsh/e2e/files.mjs
//
// Nothing outside fresh temp directories is touched.

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { chmodSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { scriptedModel } from '../../runtime-sdk/testing/model.mjs'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const dshCheckout = process.env.DSH_CHECKOUT
const personad = process.env.PERSONAD
if (!dshCheckout || !personad) {
  console.error('set DSH_CHECKOUT (a built dsh source checkout) and PERSONAD (a personad binary)')
  process.exit(2)
}
const keep = process.env.KEEP === '1'
const root = mkdtempSync(join('/tmp', 'jdsh-files-'))
const dshHome = join(root, 'dsh')
const work = join(root, 'work')
for (const d of [dshHome, work]) mkdirSync(d, { recursive: true })
const { check, summary } = checks()
const pidFile = join(root, 'dsh.pid')
let node
let companion

/** Every file under dir with its size and modification time: what a read-only reader must leave as it was. */
function snapshot(dir) {
  const out = {}
  for (const e of readdirSync(dir, { withFileTypes: true, recursive: true })) {
    if (!e.isFile()) continue
    const path = join(e.parentPath ?? e.path, e.name)
    const st = statSync(path)
    out[path] = `${st.size}:${st.mtimeMs}`
  }
  return out
}

function dshPid() {
  try {
    return Number(readFileSync(pidFile, 'utf8').trim()) || undefined
  } catch {
    return undefined // not started (or its pid file is gone)
  }
}

function alive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

/** Stop the DSH the wake script started, and wait for it to exit (it writes its home while it shuts down). */
async function stopDsh() {
  const pid = dshPid()
  if (!pid || !alive(pid)) return
  process.kill(pid, 'SIGTERM')
  await until('DSH stopped', () => !alive(pid), 20_000).catch(() => process.kill(pid, 'SIGKILL'))
}

async function cleanup() {
  companion?.kill('SIGTERM')
  await stopDsh()
  node?.stop()
  if (!keep) rmSync(root, { recursive: true, force: true })
}
process.on('SIGINT', () => cleanup().finally(() => process.exit(130)))

const model = await scriptedModel(dshCheckout)
node = await startNode({ personad, keep })

// DSH with the plugin, started by the same script the companion runs to wake it.
const patch = join(root, 'patch.yml')
writeFileSync(patch, `- insert:
    - id: persona-dsh-runtime
      name: ${join(here, '..', 'index.js')}
      config:
        socket: ${node.runtimeSocket}
        interactions: phone
- id: permission
  config:
    presets:
      e2e:
        sandbox: danger-full-access
        approval: ask
    defaultPreset: e2e
`)
const wake = join(root, 'start-dsh.sh')
writeFileSync(wake, `#!/bin/sh
# Test fixture: how this "machine" starts DSH (a service manager would, on a real one). Returns at once.
cd ${JSON.stringify(work)} || exit 1
DSH_HOME=${JSON.stringify(dshHome)} DEEPSEEK_BASE_URL=${JSON.stringify(`${model.url}/v1`)} DEEPSEEK_API_KEY=mock-key \\
  nohup ${JSON.stringify(process.execPath)} ${JSON.stringify(join(dshCheckout, 'apps/cli/lib/bin.js'))} --profile web --patch ${JSON.stringify(patch)} --no-open --port 0 \\
  >>${JSON.stringify(join(root, 'dsh.log'))} 2>&1 &
echo $! > ${JSON.stringify(pidFile)}
`)
chmodSync(wake, 0o755)
const listed = async () => (await node.call('sessions.list')).sessions ?? []
const rowOf = async (sid) => (await listed()).find((s) => s.session_id === sid)
const texts = async (sid) => (await node.call('session.items', { session_id: sid, turns: 4 })).items.filter((i) => i.kind === 'text').map((i) => i.text)

try {
  // A session with one turn, then DSH stops.
  spawn('/bin/sh', [wake], { stdio: 'ignore' })
  await until('the plugin connected', async () => (await node.call('launchers.list')).launchers?.some((l) => l.name === 'dsh'), 120_000)
  const { session } = await node.call('session.start', { launcher: 'dsh', cwd: work, async: true })
  const sid = session.session_id
  await model.script({ sequence: ['success'], successText: 'First reply' })
  await node.call('session.send', { session_id: sid, text: 'first message', client_msg_id: randomUUID() })
  await until('the first reply', async () => (await texts(sid)).includes('First reply'), 60_000)
  await until('the turn ends', async () => (await rowOf(sid))?.status === 'done', 30_000)
  await stopDsh()
  await until('off the list with DSH', async () => !(await rowOf(sid)), 30_000)
  check('a session ran in DSH, and left the list when DSH stopped', true, sid)
  const before = snapshot(dshHome)

  // The companion lists it from the store, offline, and pages it.
  const companionLog = []
  companion = spawn(process.execPath, [join(here, '..', 'companion.mjs'), '--dsh', dshCheckout, '--home', dshHome, '--wake', wake, '--socket', node.runtimeSocket],
    { stdio: ['ignore', 'pipe', 'pipe'] })
  companion.stdout.on('data', (d) => companionLog.push(String(d)))
  companion.stderr.on('data', (d) => companionLog.push(String(d)))
  const offline = await until('listed by the companion', async () => {
    const r = await rowOf(sid)
    return r?.source_instance_id === 'dsh-files' ? r : undefined
  }, 60_000).catch((err) => { console.log(companionLog.join('')); throw err })
  check('the companion lists it offline, as DSH\'s', offline.status === 'offline' && offline.agent === 'dsh' && offline.agent_label === 'DSH' && offline.cwd === work, JSON.stringify({ status: offline.status, agent: offline.agent, label: offline.agent_label }))
  const page = await node.call('session.items', { session_id: sid, turns: 2 })
  check('it pages the session from the store', page.items.some((i) => i.text === 'first message') && page.items.some((i) => i.text === 'First reply') && page.history === `dsh:${sid}`, page.history)
  const after = snapshot(dshHome)
  const changed = [...new Set([...Object.keys(before), ...Object.keys(after)])].filter((f) => before[f] !== after[f])
  check('listing and paging write nothing in DSH\'s home', changed.length === 0 && !alive(dshPid()), changed.join(', '))

  // A message from the phone: the companion wakes DSH, the node hands the message to the plugin, the reply comes.
  await model.script({ sequence: ['success'], successText: 'Second reply' })
  const started = Date.now()
  await node.call('session.send', { session_id: sid, text: 'second message', client_msg_id: randomUUID() }, 90_000)
  check('the send wakes DSH and is handed over', alive(dshPid()), `${Math.round((Date.now() - started) / 1000)}s`)
  await until('the second reply', async () => (await texts(sid)).includes('Second reply'), 60_000)
  check('DSH runs the message and the reply comes back', (await texts(sid)).filter((t) => t === 'second message').length === 1)
  const back = await until('the plugin has it again', async () => {
    const r = await rowOf(sid)
    return r?.source_instance_id === 'dsh' ? r : undefined
  }, 30_000)
  check('the plugin takes the session back from the companion', back.status !== 'offline', back.status)
} catch (err) {
  check('run', false, err.message)
  console.log('--- node log ---\n' + node.log.join('').slice(-3000))
  try {
    console.log('--- dsh log ---\n' + readFileSync(join(root, 'dsh.log'), 'utf8').slice(-3000).replace(/token=[\w-]+/g, 'token=<redacted>'))
  } catch {
    // DSH never wrote a log
  }
} finally {
  await model.close()
}
const failed = summary()
await cleanup()
process.exit(failed ? 1 : 0)
