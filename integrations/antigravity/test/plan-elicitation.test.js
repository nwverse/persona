import assert from 'node:assert/strict'
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { mapElicitationAnswer, mapElicitationRequest, mapPermissionToolCall, mapToolName } from '../lib/map.js'
import { createAgyRuntime } from '../lib/runtime.js'

async function fixture(t, options = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'agy-plan-')), record = join(dir, 'record'), agyBin = join(dir, 'agy')
  const raw = '0c7d2c4e-5b1a-4f3e-9a6d-2f8b1c3d4e5f', agyDir = join(dir, 'config')
  const fake = fileURLToPath(new URL('./fake-acp-cancel.mjs', import.meta.url))
  writeFileSync(record, ''); writeFileSync(agyBin, `#!/bin/sh\nexec '${process.execPath}' '${fake}' --record '${record}' "$@"\n`)
  chmodSync(agyBin, 0o700); mkdirSync(join(agyDir, 'brain', raw), { recursive: true })
  const n = await startFakeNode(t), runtime = createAgyRuntime({ agyBin, agyDir, home: dir, socketPath: n.path, log: () => {}, ...options })
  t.after(async () => { await runtime.stop(); rmSync(dir, { recursive: true, force: true }) })
  runtime.start(); await n.ready
  const calls = () => readFileSync(record, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
  const start = prompt => n.peer.call('session.start', { cwd: dir, prompt, permission_mode: 'bypassPermissions' })
  return { n, calls, start, runtime }
}
const shapes = [
  { name: 'exit_plan_mode', rawInput: { plan: '# Plan\nRun checks' } },
  { name: 'write_file', rawInput: { Content: '# Plan\nRun checks', ArtifactMetadata: { RequestFeedback: true } } },
  { name: 'edit_file', rawInput: { ReplacementContent: '# Plan\nRun checks', ArtifactMetadata: { RequestFeedback: true } } },
]
test('plan mapping preserves full content and requires boolean RequestFeedback', () => {
  assert.equal(mapToolName('exit_plan_mode'), 'ExitPlanMode')
  for (const toolCall of shapes) {
    const mapped = mapPermissionToolCall(toolCall)
    assert.equal(mapped.isPlan, true); assert.equal(mapped.toolName, 'ExitPlanMode'); assert.equal(mapped.plan, '# Plan\nRun checks')
  }
  for (const value of [false, 'true', undefined]) assert.equal(mapPermissionToolCall({ name: 'write_file', rawInput: { ArtifactMetadata: { RequestFeedback: value } } }).isPlan, undefined)
})
for (const [i, toolCall] of shapes.entries()) test(`native plan approval: ${toolCall.name}`, { timeout: 15_000 }, async t => {
  const { n, calls, start } = await fixture(t)
  n.resolve = () => i % 2 ? { allow: true, always: true } : { approve: true }
  await start(`ask-full ${JSON.stringify({ toolCall, endOnAnswer: true, options: [{ optionId: 'once', kind: 'allow_once' }, { optionId: 'always', kind: 'allow_always' }] })}`)
  await until('plan answered', () => calls().some(m => m.id?.startsWith?.('perm-')), 10_000)
  assert.equal(n.asked[0].kind, 'plan'); assert.equal(n.asked[0].tool_name, 'ExitPlanMode'); assert.equal(n.asked[0].plan, '# Plan\nRun checks')
  assert.deepEqual(calls().find(m => m.id?.startsWith?.('perm-')).result, { outcome: { outcome: 'selected', optionId: 'once' } })
})
for (const field of ['feedback', 'message']) test(`plan refusal returns ${field} to the agent`, { timeout: 15_000 }, async t => {
  const { n, calls, start } = await fixture(t)
  n.resolve = () => ({ approve: false, [field]: '  Include validation  ' })
  await start(`ask-full ${JSON.stringify({ toolCall: shapes[0], endOnAnswer: true })}`)
  await until('feedback follow-up', () => calls().filter(m => m.method === 'session/prompt').length === 2, 10_000)
  assert.equal(calls().find(m => m.id?.startsWith?.('perm-')).result.outcome.optionId, 'reject')
  assert.deepEqual(calls().filter(m => m.method === 'session/prompt')[1].params.prompt, [{ type: 'text', text: '（没有执行）Include validation' }])
})

for (const action of ['accept', 'decline', 'cancel']) test(`URL elicitation: ${action}`, { timeout: 15_000 }, async t => {
  const params = { mode: 'url', url: 'https://example.com/confirm', message: 'Confirm access', elicitationId: 'request-url' }
  const mapped = mapElicitationRequest(params)
  assert.deepEqual(mapped, params)
  assert.deepEqual(mapElicitationAnswer(mapped, { action, content: { ignored: true } }), { action })
  assert.deepEqual(mapElicitationAnswer(mapped, { action: 'invalid' }), { action: 'cancel' })
  assert.deepEqual(mapElicitationAnswer(mapped, { action: 'accept', dismiss: true }), { action: 'cancel' })
  const { n, calls, start } = await fixture(t)
  n.resolve = () => ({ action })
  await start(`elicit ${JSON.stringify({ params })}`)
  await until('URL answered', () => calls().some(m => m.id?.startsWith?.('elicit-')), 10_000)
  assert.equal(n.asked[0].kind, 'elicitation')
  assert.deepEqual(n.asked[0].elicitation, { mode: 'url', url: params.url, message: params.message, elicitation_id: params.elicitationId })
  assert.deepEqual(calls().find(m => m.id?.startsWith?.('elicit-')).result, { action })
  assert.equal(calls().some(m => m.method === 'session/cancel'), false)
})

test('pending plan retains its ACP request and resumes the same card without cancelling', { timeout: 15_000 }, async t => {
  const { n, calls, start, runtime } = await fixture(t, { promptInactivityMs: 100 })
  const answer = Promise.withResolvers()
  n.resolve = () => n.asked.length === 1 ? { pending: true, dismiss: true } : answer.promise
  await start(`ask-full ${JSON.stringify({ toolCall: shapes[0], endOnAnswer: true })}`)
  await until('pending plan waits again', () => n.asked.length === 2, 10_000)
  assert.equal(n.asked[0].id, n.asked[1].id)
  assert.equal(calls().some(m => m.method === 'session/cancel' || m.id?.startsWith?.('perm-')), false)
  answer.resolve({ approve: true })
  await until('same ACP permission approved', () => calls().some(m => m.id?.startsWith?.('perm-')), 10_000)
  assert.equal(calls().find(m => m.id?.startsWith?.('perm-')).result.outcome.optionId, 'allow')
  await until('original turn settled', () => runtime.idle(), 10_000); assert.equal(calls().some(m => m.method === 'session/cancel'), false)
})
