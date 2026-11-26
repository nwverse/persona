import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, rmSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE = fileURLToPath(new URL('./fake-acp-stream.mjs', import.meta.url))
const AGED_OUT_RAW = '11111111-2222-4333-8444-555555555555'
const NEWER_RAW = '22222222-3333-4444-8555-666666666666'
const DB_ONLY_RAW = '33333333-4444-4555-8666-777777777777'
const MISSING_RAW = '99999999-8888-4777-8666-555555555555'

function writeTranscript(configDir, rawId, text, mtimeSec) {
  const logs = join(configDir, 'brain', rawId, '.system_generated/logs')
  mkdirSync(logs, { recursive: true })
  const file = join(logs, 'transcript.jsonl')
  writeFileSync(file, [
    { step_index: 0, source: 'USER_EXPLICIT', type: 'USER_INPUT', status: 'DONE', content: text },
    { step_index: 1, source: 'MODEL', type: 'PLANNER_RESPONSE', status: 'DONE', content: 'Done.' },
  ].map((row) => JSON.stringify(row)).join('\n') + '\n')
  if (mtimeSec !== undefined) {
    utimesSync(file, mtimeSec, mtimeSec)
  }
}

test('AGY session.items returns not_found for nonexistent UUIDs while preserving aged-out and db-only sessions', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'agy-missing-session-'))
  writeFileSync(join(directory, 'updates.jsonl'), '')
  writeFileSync(join(directory, 'calls.jsonl'), '')
  const binary = join(directory, 'agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${directory}' "$@"\n`)
  chmodSync(binary, 0o700)

  const configDir = join(directory, 'config')
  const nowSec = Math.floor(Date.now() / 1000)
  writeTranscript(configDir, AGED_OUT_RAW, 'Older review report.', nowSec - 3600)
  writeTranscript(configDir, NEWER_RAW, 'Newer session.', nowSec - 10)
  mkdirSync(join(configDir, 'conversations'), { recursive: true })
  writeFileSync(join(configDir, 'conversations', `${DB_ONLY_RAW}.db`), '')

  const node = await startFakeNode(t)
  // maxSessions=1 evicts AGED_OUT_RAW from runtime.sessions even though it still exists on disk.
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir: configDir,
    home: directory,
    socketPath: node.path,
    maxSessions: 1,
    log: () => {},
  })
  t.after(async () => { await runtime.stop(); rmSync(directory, { recursive: true, force: true }) })
  runtime.start()
  await node.ready

  // Truly nonexistent UUID must return typed not_found instead of an empty page.
  await assert.rejects(
    node.peer.call('session.items', { session_id: `agy-${MISSING_RAW}`, limit: 1 }),
    (err) => err?.code === 'not_found',
    'nonexistent agy UUID must return not_found',
  )

  // Aged-out session still on disk must stay readable (never misclassified as nonexistent).
  const aged = await node.peer.call('session.items', { session_id: `agy-${AGED_OUT_RAW}`, limit: 1 })
  assert.equal(aged.items.length, 1)
  assert.equal(aged.to_seq, 2)

  // Session with a conversation DB file on disk must also not be reported not_found.
  const dbOnly = await node.peer.call('session.items', { session_id: `agy-${DB_ONLY_RAW}`, limit: 1 })
  assert.deepEqual(dbOnly.items, [])
})

test('AGY session.items fails closed when conversation_summaries.db is unreadable', async (t) => {
  const directory = mkdtempSync(join(tmpdir(), 'agy-missing-unreadable-'))
  writeFileSync(join(directory, 'updates.jsonl'), '')
  writeFileSync(join(directory, 'calls.jsonl'), '')
  const binary = join(directory, 'agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${directory}' "$@"\n`)
  chmodSync(binary, 0o700)

  const configDir = join(directory, 'config')
  mkdirSync(configDir, { recursive: true })
  // A corrupt summaries DB triggers summaryReadFailure, so unknown IDs must fail closed (not not_found).
  writeFileSync(join(configDir, 'conversation_summaries.db'), 'not-a-sqlite-database')

  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({
    agyBin: binary,
    agyDir: configDir,
    home: directory,
    socketPath: node.path,
    log: () => {},
  })
  t.after(async () => { await runtime.stop(); rmSync(directory, { recursive: true, force: true }) })
  runtime.start()
  await node.ready

  const page = await node.peer.call('session.items', { session_id: `agy-${MISSING_RAW}`, limit: 1 })
  assert.deepEqual(page.items, [])
})
