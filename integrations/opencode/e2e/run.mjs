// End-to-end acceptance of the opencode plugin (docs/integrations/opencode.md "Verification"): a real `opencode serve` in a throwaway
// config / data / state directory with this plugin, a scripted model (mock-llm.mjs, no real model call), and a real
// `personad node` in an isolated HOME that plays the hub's side. Nothing outside the temp directory is touched; the
// user's own opencode server, config and database are never opened.
//
//   PERSONAD=<personad binary> [OPENCODE=<opencode binary>] [KEEP=1] node integrations/opencode/e2e/run.mjs

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'
import { install } from '../install.mjs'

const personad = process.env.PERSONAD
if (!personad) {
  console.error('set PERSONAD (a personad binary)')
  process.exit(2)
}
const opencode = process.env.OPENCODE || 'opencode'
const here = dirname(fileURLToPath(import.meta.url))
const plugin = join(here, '..', 'index.js')
const { check, summary } = checks()
const root = mkdtempSync('/tmp/ocrt-')
const [serverPort, mockPort] = [Number(process.env.OC_PORT) || 19026, Number(process.env.MOCK_PORT) || 19025]
for (const d of ['cfg/opencode/plugins', 'data', 'state', 'work']) mkdirSync(join(root, d), { recursive: true })
const children = []
const run = (cmd, args, env = {}) => {
  const c = spawn(cmd, args, { env: { PATH: process.env.PATH, HOME: root, ...env }, stdio: ['ignore', 'pipe', 'pipe'] })
  c.out = ''
  c.stdout.on('data', (d) => (c.out += d))
  c.stderr.on('data', (d) => (c.out += d))
  children.push(c)
  return c
}

const node = await startNode({ personad, keep: process.env.KEEP === '1' })
let server
try {
  writeFileSync(join(root, 'cfg/opencode/opencode.json'), JSON.stringify({
    model: 'mock/mock-1',
    permissions: [{ action: 'shell', resource: '*', effect: 'ask' }],
    providers: { mock: { name: 'Mock', package: '@opencode/ai/providers/openai-compatible', settings: { baseURL: `http://127.0.0.1:${mockPort}/v1`, apiKey: 'x' },
      models: { 'mock-1': { name: 'Mock 1', capabilities: { tools: true, input: ['text'], output: ['text'] }, limit: { context: 100000, output: 8000 } } } } },
  }))
  writeFileSync(join(root, 'cfg/opencode/persona.json'), JSON.stringify({ socket: node.runtimeSocket, answerFirst: 'phone' }))
  install({ configDir: join(root, 'cfg/opencode'), entry: plugin }) // the installer's own shim
  const mock = run('node', [join(here, 'mock-llm.mjs'), String(mockPort)])
  await until('the scripted model', () => mock.out.includes('listening'), 10_000)
  server = run(opencode, ['serve', '--hostname', '127.0.0.1', '--port', String(serverPort)], {
    XDG_CONFIG_HOME: join(root, 'cfg'), XDG_DATA_HOME: join(root, 'data'), XDG_STATE_HOME: join(root, 'state'),
  })
  await until('opencode listening', () => /server password \S+/.test(server.out), 30_000)
  const auth = 'Basic ' + Buffer.from(`opencode:${/server password (\S+)/.exec(server.out)[1]}`).toString('base64')
  const work = join(root, 'work')
  const api = async (path, init = {}) => {
    const res = await fetch(`http://127.0.0.1:${serverPort}${path}`, { ...init, headers: { authorization: auth, 'x-opencode-directory': work, 'content-type': 'application/json', ...init.headers } })
    return res.json().catch(() => ({}))
  }
  await api('/api/plugin') // opencode starts a location's plugins when a request for it comes
  const { data: created } = await api('/api/session', { method: 'POST', body: JSON.stringify({ location: { directory: work } }) })
  const sid = created.id
  const row = await until('the session listed by the plugin', async () => ((await node.call('sessions.list')).sessions ?? []).find((s) => s.session_id === sid), 20_000)
  check('the plugin connects and lists the session as a runtime source', row.agent === 'opencode' && row.source_kind === 'runtime' && row.space_kind === 'opencode' && !row.pane_id && row.cwd === work, JSON.stringify({ agent: row.agent, source: row.source_kind, cwd: row.cwd }))
  check('with the capabilities it declares', row.capabilities?.send === true && row.capabilities?.interrupt === true && !row.capabilities?.terminal, JSON.stringify(row.capabilities))
  await node.call('session.watch', { session_id: sid, since_seq: 0 })

  // A turn: the model answers, the conversation arrives, the state follows.
  const m1 = randomUUID()
  await node.call('session.send', { session_id: sid, text: 'hello there', client_msg_id: m1 })
  await node.call('session.send', { session_id: sid, text: 'hello there', client_msg_id: m1 }) // an app retry
  await until('the answer', () => node.items(sid).some((i) => i.text === 'pong: hello there'), 30_000)
  check('a message sent from the phone gets an answer in the conversation', node.items(sid).some((i) => i.role === 'user' && i.text === 'hello there'))
  check('a retried send is delivered once', node.items(sid).filter((i) => i.role === 'user' && i.text === 'hello there').length === 1)
  await until('done', () => node.status(sid) === 'done', 10_000).catch(() => {})
  check('the turn ends as done', node.status(sid) === 'done', String(node.status(sid)))
  check('pushed batches are continuous', node.gaps(sid).length === 0, `${node.batches(sid).length} batches`)

  // A permission the server asks: a card on the phone, the answer goes back to opencode.
  node.resolve = (ix) => (ix.kind === 'permission' ? { allow: true } : { dismiss: true })
  await node.call('session.send', { session_id: sid, text: 'run echo e2e-ok', client_msg_id: randomUUID() })
  await until('the tool result', () => node.items(sid).some((i) => i.kind === 'tool_result' && /e2e-ok/.test(i.text)), 30_000)
  const card = node.asked.find((i) => i.kind === 'permission' && i.session_id === sid)
  check('the permission arrives as the runtime\'s own card', card?.agent === 'opencode' && card?.tool_name === 'Bash' && card?.tool_input?.command === 'echo e2e-ok', JSON.stringify({ agent: card?.agent, tool: card?.tool_name, input: card?.tool_input }))
  check('allowing it runs the command', node.items(sid).some((i) => i.kind === 'tool_use' && i.tool_name === 'Bash' && i.tool_input.command === 'echo e2e-ok'))
  node.asked.length = 0
  node.resolve = (ix) => (ix.kind === 'permission' ? { allow: false, message: 'not now' } : { dismiss: true })
  await node.call('session.send', { session_id: sid, text: 'run echo denied-one', client_msg_id: randomUUID() })
  await until('the denied call', () => node.items(sid).some((i) => i.kind === 'tool_result' && i.is_error && /not now|reject|denied/i.test(i.text)), 30_000)
  check('denying it with a reason stops the command and says why', node.items(sid).some((i) => i.kind === 'tool_result' && i.is_error), 'the reason reached the model')
  await until('idle after the denial', () => node.status(sid) !== 'working', 20_000)

  // Interrupt in the middle of a slow answer.
  await node.call('session.send', { session_id: sid, text: 'slow please', client_msg_id: randomUUID() })
  await until('working', () => node.status(sid) === 'working', 10_000)
  await until('some slow text', () => node.deltas(sid).some((t) => t.startsWith('This is a slow')), 10_000)
  check('live text arrives as deltas that grow', node.deltas(sid).filter((t) => t.startsWith('This is a slow')).length >= 1, JSON.stringify(node.deltas(sid).filter(Boolean).slice(-2)))
  await node.call('session.interrupt', { session_id: sid })
  await until('stopped', () => node.status(sid) !== 'working', 15_000)
  check('interrupt stops the turn', node.status(sid) !== 'working', String(node.status(sid)))
  await until('the notice', () => node.items(sid).some((i) => i.kind === 'notice' && i.text === '已中断'), 8000).catch(() => {})
  check('and the conversation says so', node.items(sid).some((i) => i.kind === 'notice' && i.text === '已中断'), JSON.stringify(node.items(sid).slice(-5).map((i) => [i.id, i.kind, (i.text ?? '').slice(0, 30)])))

  // Rename and history.
  await node.call('session.rename', { session_id: sid, title: 'Renamed from the phone' })
  const info = (await api(`/api/session/${sid}`)).data
  check('rename reaches opencode', info?.title === 'Renamed from the phone', String(info?.title))
  await until('the new title is listed', async () => ((await node.call('sessions.list')).sessions ?? []).find((s) => s.session_id === sid)?.title === 'Renamed from the phone', 10_000)
  const page = await node.call('session.items', { session_id: sid, turns: 1 })
  check('history pages through the node', page.items.length > 0 && /^opencode:/.test(page.history) && page.to_seq > 0, `${page.items.length} items, to_seq ${page.to_seq}`)
  const keys = await node.call('session.keys', { session_id: sid, keys: ['enter'] }).then(() => 'ok', (e) => e.message)
  check('terminal calls are refused as unsupported', /^unsupported/.test(keys), keys)

  // The server going away takes its sessions with it.
  server.kill('SIGTERM')
  await until('its sessions leave with it', async () => !((await node.call('sessions.list')).sessions ?? []).some((s) => s.session_id === sid), 20_000)
  check('its sessions leave with the server', true)
} catch (err) {
  check('run', false, err.message)
  console.log('--- opencode ---\n' + (server?.out ?? '').replace(/server password \S+/, 'server password …').slice(-2500))
  console.log('--- node log ---\n' + node.log.join('').slice(-2000))
} finally {
  for (const c of children) {
    try {
      c.kill('SIGKILL')
    } catch {}
  }
  node.stop()
  if (process.env.KEEP !== '1') rmSync(root, { recursive: true, force: true })
}
process.exit(summary() ? 1 : 0)
