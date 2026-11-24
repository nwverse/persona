import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, rmSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import { test } from 'node:test'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const PARENT = '11111111-2222-3333-4444-555555555555'
const CHILD = '22222222-3333-4444-5555-666666666666'
const GRANDCHILD = '33333333-4444-5555-6666-777777777777'
const DONE_SUB = '44444444-5555-6666-7777-888888888888'
const INTERNAL_HELPER = '55555555-6666-7777-8888-999999999999'
const running = 'CASCADE_RUN_STATUS_RUNNING'
const idle = 'CASCADE_RUN_STATUS_IDLE'

// agy records which conversation started a subagent (invoke_subagent, a Swarm's workers) in parent_conversation_id
// (#14817): the companion lists both running and finished subagents as their parent's children without taking root
// maxSessions slots, titles untitled ones from subagents/<id>.json or agent_name, keeps every ancestor of a running
// descendant listed however old or idle it is, and still skips non-subagent internal helper conversations.
test('subagent conversations name their parent, root, and role while keeping ancestors listed', { timeout: 15_000 }, async (t) => {
  const home = mkdtempSync(join(tmpdir(), 'persona-agy-subagents-'))
  const agyDir = join(home, 'agy')
  const old = new Date(Date.now() - 30 * 86400_000)
  const now = new Date()
  const conversations = [
    [PARENT, '', idle, '', old],
    [CHILD, PARENT, idle, 'self', old],
    [GRANDCHILD, CHILD, running, 'visual-auditor', now],
    [DONE_SUB, PARENT, idle, 'self', now],
    [INTERNAL_HELPER, '', idle, '', now],
  ]
  for (const [id, , , , stamp] of conversations) {
    const logs = join(agyDir, 'brain', id, '.system_generated', 'logs')
    mkdirSync(logs, { recursive: true })
    const transcript = join(logs, 'transcript.jsonl')
    writeFileSync(transcript, JSON.stringify({ step_index: 0, type: 'PLANNER_RESPONSE', status: 'DONE', created_at: stamp.toISOString(), content: 'Started' }) + '\n')
    utimesSync(transcript, stamp, stamp)
  }
  const subDir = join(agyDir, 'brain', PARENT, '.system_generated', 'subagents')
  mkdirSync(subDir, { recursive: true })
  writeFileSync(join(subDir, `${CHILD}.json`), JSON.stringify({ conversationId: CHILD, subagentDescriptor: { typeName: 'self', role: 'Code reviewer' } }))
  writeFileSync(join(subDir, `${DONE_SUB}.json`), JSON.stringify({ conversationId: DONE_SUB, subagentDescriptor: { typeName: 'worker', role: 'Swarm Worker (api)' } }))

  mkdirSync(join(agyDir, 'cache'), { recursive: true })
  writeFileSync(join(agyDir, 'cache', 'conversation_metadata.json'), JSON.stringify({
    conversations: {
      [CHILD]: { is_internal: true },
      [GRANDCHILD]: { is_internal: true },
      [DONE_SUB]: { is_internal: true },
      [INTERNAL_HELPER]: { is_internal: true },
    },
  }))
  const db = new DatabaseSync(join(agyDir, 'conversation_summaries.db'))
  db.exec('CREATE TABLE conversation_summaries (conversation_id TEXT PRIMARY KEY, status TEXT, not_fully_idle NUMERIC, killed NUMERIC, step_count INTEGER, title TEXT, workspace_uris TEXT, last_modified_time TEXT, parent_conversation_id TEXT NOT NULL DEFAULT "", agent_name TEXT NOT NULL DEFAULT "")')
  for (const [id, parent, status, agentName, stamp] of conversations) {
    db.prepare('INSERT INTO conversation_summaries VALUES (?, ?, 0, 0, 1, ?, ?, ?, ?, ?)').run(id, status, id === PARENT ? 'Parent task' : '', '[]', stamp.toISOString(), parent, agentName)
  }
  db.close()
  const binary = join(home, 'fake-agy.mjs')
  writeFileSync(binary, `#!/usr/bin/env node
if (process.argv.includes('models')) { console.log('test-model\\tTest Model'); process.exit(0) }
import { createInterface } from 'node:readline'
createInterface({ input: process.stdin }).on('line', (line) => {
  const request = JSON.parse(line)
  if (request.id === undefined || request.method === 'session/prompt') return
  process.stdout.write(JSON.stringify({ jsonrpc: '2.0', id: request.id, result: {} }) + '\\n')
})
`, { mode: 0o700 })
  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({ home, agyDir, agyBin: binary, socketPath: node.path, listDays: 1, maxSessions: 1, log: () => {} })
  t.after(async () => {
    await runtime.stop()
    rmSync(home, { recursive: true, force: true })
  })
  runtime.start()
  await node.ready
  await until('parent and all three subagents listed', () => node.last('runtime.sessions')?.sessions.length === 4)
  const rows = Object.fromEntries(node.last('runtime.sessions').sessions.map((row) => [row.session_id, row]))
  assert.equal(rows[`agy-${INTERNAL_HELPER}`], undefined, 'idle non-subagent internal conversation stays hidden')
  const view = (id) => {
    const { title, is_subagent, parent_session_id, root_session_id } = rows[`agy-${id}`]
    return { title, is_subagent, parent_session_id, root_session_id }
  }
  assert.deepEqual(view(PARENT), { title: 'Parent task', is_subagent: undefined, parent_session_id: undefined, root_session_id: undefined })
  assert.deepEqual(view(CHILD), { title: 'Code reviewer', is_subagent: true, parent_session_id: `agy-${PARENT}`, root_session_id: `agy-${PARENT}` })
  assert.deepEqual(view(GRANDCHILD), { title: 'visual-auditor', is_subagent: true, parent_session_id: `agy-${CHILD}`, root_session_id: `agy-${PARENT}` })
  assert.deepEqual(view(DONE_SUB), { title: 'Swarm Worker (api)', is_subagent: true, parent_session_id: `agy-${PARENT}`, root_session_id: `agy-${PARENT}` })
})
