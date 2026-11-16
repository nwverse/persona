// Opt-in real-provider check. No user files or production hub are used; Grok keeps the temporary conversation in its own history.
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { mkdir, mkdtemp, rm, access } from 'node:fs/promises'
import { homedir, tmpdir } from 'node:os'
import { join } from 'node:path'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createGrokRuntime } from '../lib/runtime.js'

if (process.env.GROK_LIVE !== '1') throw new Error('Set GROK_LIVE=1 to make real Grok requests')
const home = await mkdtemp(join(tmpdir(), 'persona-grok-live-'))
const cwd = join(home, '.persona', 'assistant')
await mkdir(cwd, { recursive: true })
const nonce = randomUUID()
const node = await startFakeNode(undefined, { strict: true, tools: [{ name: 'integration_probe', description: 'Return the integration nonce supplied as value.', input_schema: { type: 'object', properties: { value: { type: 'string' } }, required: ['value'] } }] })
node.callTool = ({ args }) => ({ content: [{ type: 'text', text: `PROBE_OK:${args.value}` }] })
const options = { home, env: {}, socketPath: node.path, bin: process.env.GROK_BIN || 'grok',
  grokHome: process.env.GROK_HOME || join(homedir(), '.grok'), model: process.env.GROK_MODEL, effort: process.env.GROK_EFFORT,
  log: (msg) => process.stderr.write(`${msg}\n`) }
let runtime = createGrokRuntime(options)
let id
async function send(text, extra = {}) {
  await node.peer.call('session.send', { session_id: id, text, mode: 'bypassPermissions', ...extra })
  const s = runtime.sessions.get(id)
  await s.tail
  const result = await node.peer.call('session.items', { session_id: id })
  const last = result.items.filter((i) => i.role === 'assistant' && i.kind === 'text').at(-1)?.text || ''
  assert.ok(!last.startsWith('API Error:'), last)
  return { result, last }
}
try {
  runtime.start()
  await node.ready
  const start = await node.peer.call('session.start', { cwd, assistant: true, model: process.env.GROK_MODEL, effort: process.env.GROK_EFFORT, permission_mode: 'bypassPermissions' })
  id = start.session.session_id
  console.log(JSON.stringify({ stage: 'created', model: start.session.model, effort: start.session.effort }))
  assert.equal(node.hello.caps.rename, true)
  await node.peer.call('session.rename', { session_id: id, title: 'Initial integration title' })
  const title = 'Renamed integration conversation'
  await node.peer.call('session.rename', { session_id: id, title })
  assert.equal((await runtime.driver.listSessions()).find((s) => s.session_id === id)?.title, title)
  console.log(JSON.stringify({ stage: 'native_rename', success: true }))
  const first = await send('Call the Persona integration_probe tool once with the nonce from the private context. Discover it with search_tool if needed. Reply with exactly its result. Do not use shell or other tools.', { context: `The integration nonce is ${nonce}.` })
  assert.ok(node.toolCalls.some((c) => c.name === 'integration_probe' && c.args.value === nonce), 'Grok did not call the scoped Persona tool with private context')
  assert.ok(first.last.includes(`PROBE_OK:${nonce}`), first.last)
  assert.ok(!first.result.items.some((i) => i.role === 'user' && i.text?.includes('The integration nonce is')), 'Private context leaked into the rendered transcript')
  console.log(JSON.stringify({ stage: 'assistant_mcp_context', success: true }))
  const follow = await send('Without using any tools, repeat exactly the PROBE_OK result from your previous answer.')
  assert.ok(follow.last.includes(`PROBE_OK:${nonce}`), follow.last)
  console.log(JSON.stringify({ stage: 'continuation', success: true }))
  await runtime.stop()
  runtime = createGrokRuntime(options)
  runtime.start()
  await until('companion reconnect', () => node.hellos >= 2, 10_000)
  await until('session rediscovery', async () => (await runtime.driver.listSessions()).some((s) => s.session_id === id), 10_000)
  assert.equal((await runtime.driver.listSessions()).find((s) => s.session_id === id)?.title, title)
  const resumed = await send('Without using any tools, repeat exactly the PROBE_OK result from this conversation.')
  assert.ok(resumed.last.includes(`PROBE_OK:${nonce}`), resumed.last)
  assert.equal((await runtime.driver.listSessions()).find((s) => s.session_id === id)?.title, title)
  console.log(JSON.stringify({ stage: 'restart_resume', success: true }))
  const target = join(cwd, 'must-not-exist')
  const plan = await send(`Integration permission test. Attempt to create the file ${target} with the text forbidden using a write tool once. If the hook denies it, do not retry and reply DENIED.`, { mode: 'plan' })
  assert.ok(plan.last.includes('DENIED'), plan.last)
  await assert.rejects(access(target))
  console.log(JSON.stringify({ stage: 'readonly_hook', success: true }))
  await runtime.stop()
  runtime = createGrokRuntime({ ...options, mode: 'bypassPermissions' })
  runtime.start()
  await until('second companion reconnect', () => node.hellos >= 3, 10_000)
  const restored = await until('restored plan mode', async () => (await runtime.driver.listSessions()).find((s) => s.session_id === id), 10_000)
  assert.equal(restored.permission_mode, 'plan')
  console.log(JSON.stringify({ stage: 'restart_keeps_readonly', success: true }))
  const probeDir = join(home, 'launcher-check')
  await mkdir(probeDir)
  const checked = await node.peer.call('launchers.check', { cwd: probeDir, prompt: 'Reply exactly CHECK_OK. Do not use tools.' })
  assert.equal(checked.ok, true, checked.error)
  assert.equal(checked.model, process.env.GROK_MODEL || start.session.model)
  assert.equal(checked.answer, 'CHECK_OK')
  console.log(JSON.stringify({ stage: 'manual_launcher_check', success: true, model: checked.model }))
} finally {
  await runtime.stop()
  node.close()
  await rm(home, { recursive: true, force: true })
}
