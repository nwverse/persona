import assert from 'node:assert/strict'
import { mkdir, mkdtemp, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createHermesRuntime } from '../lib/runtime.js'

async function setup(t) {
  const root = await mkdtemp(join(tmpdir(), 'hermes-test-'))
  const storage = join(root, 'profile')
  const cwd = join(root, 'workspace')
  await Promise.all([mkdir(storage), mkdir(cwd)])
  const node = await startFakeNode(t, { strict: true })
  let runtime
  t.after(async () => {
    await runtime?.stop()
    await rm(root, { recursive: true, force: true })
  })
  const restart = async () => {
    await runtime?.stop()
    const previous = node.hellos
    runtime = createHermesRuntime({
      bin: fileURLToPath(new URL('./acp-fixture.mjs', import.meta.url)), socketPath: node.path,
      env: { ...process.env, HERMES_HOME: storage, HERMES_TEST_STATE: storage }, retryMs: 20, scanMs: 25, idleMs: 140,
    })
    await runtime.start()
    await until('runtime hello', () => node.hellos > previous && node.peer)
  }
  await restart()
  return { node, cwd, restart, call: (method, params) => node.peer.call(method, params) }
}

test('launch, stream, page, deduplicate and recover across node reconnect and companion restart', async (t) => {
  const { node, cwd, restart, call } = await setup(t)
  assert.equal(node.hello.runtime, 'hermes')
  assert.equal(node.hello.launchers[0].name, 'hermes')
  const start = { cwd, session_id: 'persona-start' }
  const { session } = await call('session.start', start)
  const session_id = session.session_id
  assert.equal(session_id, start.session_id)
  assert.equal((await call('session.start', start)).session.session_id, session_id)
  await call('session.watch', { session_id, since_seq: 0 })
  const first = { session_id, text: 'first', client_msg_id: 'message-one' }
  await Promise.all([call('session.send', first), call('session.send', first)])
  const read = (p = {}) => call('session.items', { session_id, ...p })
  await until('first reply', async () => (await read()).items.some((i) => i.text === 'echo: first'))
  const saved = await read()
  assert.equal(saved.items.filter((i) => i.role === 'user').length, 1)
  assert.equal(saved.items[0].client_msg_id, first.client_msg_id)
  assert.deepEqual((await call('session.item', { session_id, item_id: saved.items[1].id })).text, 'echo: first')
  const old = node.peer
  old.close()
  await until('node reconnect', () => node.peer !== old)
  await call('session.send', first)
  assert.equal((await read()).to_seq, saved.to_seq)

  const { session: second } = await call('session.start', { cwd, prompt: 'another workspace session', client_msg_id: 'initial' })
  await until('second reply', async () =>
    (await call('session.items', { session_id: second.session_id })).items.length === 2)
  await restart()
  assert.equal((await call('session.start', start)).session.session_id, session_id)
  const history = await call('sessions.history', { since: new Date(0).toISOString() })
  assert.equal(history.rows.find((row) => row.session_id === session_id).started_by_persona, true)
  const restored = await read()
  assert.notEqual(restored.history, saved.history)
  assert.deepEqual(restored.items.map((i) => i.text), ['first', 'echo: first'])
  assert.deepEqual((await read({ after_seq: 1 })).items.map((i) => i.text), ['echo: first'])
  assert.deepEqual((await read({ limit: 1, before_seq: 2 })).items.map((i) => i.text), ['first'])
  assert.equal((await call('session.items', { session_id: second.session_id })).items[0].text, 'another workspace session')
  await assert.rejects(call('session.items', { session_id: 'hermes-unknown' }), { code: 'not_found' })
  await call('session.send', { session_id, text: 'continued' })
  await until('continued reply', async () => (await read()).items.some((i) => i.text === 'echo: continued'))
})

test('permissions and cancellation are isolated to the active session', async (t) => {
  const { node, cwd, call } = await setup(t)
  const start = async () => ({ session_id: (await call('session.start', { cwd })).session.session_id })
  const [one, two] = await Promise.all([start(), start()])
  node.resolve = (ix) => ({ allow: ix.session_id === one.session_id })
  await Promise.all([one, two].map((s) => call('session.send', { ...s, text: 'permission' })))
  const replyText = async (s) => (await call('session.items', s)).items.filter((i) => i.role === 'assistant' && i.kind === 'text').at(-1)?.text
  await until('allow and deny', async () => await replyText(one) === 'once' && await replyText(two) === 'deny')
  assert.equal(new Set(node.asked.map((i) => i.id)).size, 2)

  for (const s of [one, two]) {
    await call('session.watch', { ...s, since_seq: 0 })
    await call('session.send', { ...s, text: 'hold' })
  }
  await until('two independent live streams', () => node.all('session.delta').filter((p) => p.text === 'streaming').length === 2)
  await assert.rejects(call('session.send', { ...one, text: 'must not enqueue' }), { code: 'turn_running' })
  assert.equal((await call('session.interrupt', one)).settled, true)
  assert.equal((await call('session.items', one)).items.at(-1).text, '已中断')
  assert.equal((await call('session.items', two)).items.at(-1).text, 'hold')
  assert.equal((await call('session.interrupt', two)).settled, true)
  node.resolve = () => new Promise(() => {})
  await call('session.send', { ...one, text: 'permission' })
  await until('permission held by the hub', () => node.asked.length === 3)
  assert.equal((await call('session.interrupt', one)).settled, true)
  assert.equal((await call('session.items', one)).items.at(-1).text, '已中断')
})

test('a crashed worker only ends its own turn and saved sessions can continue', async (t) => {
  const { cwd, call } = await setup(t)
  const { session } = await call('session.start', { cwd, prompt: 'save first' })
  const session_id = session.session_id
  const read = () => call('session.items', { session_id })
  await until('saved reply', async () => (await read()).items.at(-1)?.text === 'echo: save first')
  await call('session.send', { session_id, text: 'disconnect' })
  await until('crash notice', async () => (await read()).items.at(-1)?.kind === 'notice')
  await call('session.send', { session_id, text: 'recovered' })
  await until('reply after recovery', async () => (await read()).items.at(-1)?.text === 'echo: recovered')
  assert.deepEqual((await read()).items.map((i) => i.text), ['save first', 'echo: save first', 'recovered', 'echo: recovered'])
})
