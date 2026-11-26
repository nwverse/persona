import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const CARBON = 'gemini-4-carbon-high'
const CARBON_MEDIUM = 'gemini-4-carbon-medium'
const NEON = 'gemini-4-neon-high'
const NEON_XHIGH = 'gemini-4-neon-xhigh'
const models = [
  { modelId: CARBON, name: 'Gemini 4 Carbon (High)' },
  { modelId: CARBON_MEDIUM, name: 'Gemini 4 Carbon (Medium)' },
  { modelId: NEON, name: 'Gemini 4 Neon (High)' },
  { modelId: NEON_XHIGH, name: 'Gemini 4 Neon (Extra High)' },
]
const rawId = '11111111-2222-3333-4444-555555555555'
const sessionId = `agy-${rawId}`
const fake = fileURLToPath(new URL('./fake-acp-models.mjs', import.meta.url))

async function fixture(t, { configured = CARBON, assistantAcpIdleMs, transcriptModel, ...fakeOptions } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-resume-model-'))
  const agyDir = join(home, 'settings')
  mkdirSync(agyDir)
  writeFileSync(join(agyDir, 'settings.json'), JSON.stringify({ model: configured }))
  writeFileSync(join(home, 'catalog.json'), JSON.stringify({ models, resumeModel: NEON, ...fakeOptions }))
  writeFileSync(join(home, 'calls.jsonl'), '')
  const agyBin = join(home, 'agy')
  writeFileSync(agyBin, `#!/bin/sh\nexec '${process.execPath}' '${fake}' '${home}' "$@"\n`, { mode: 0o700 })
  if (transcriptModel) {
    const logs = join(agyDir, 'brain', rawId, '.system_generated', 'logs')
    mkdirSync(logs, { recursive: true })
    const steps = [CARBON, transcriptModel, '<synthetic>'].map((model, step_index) => ({
      type: 'PLANNER_RESPONSE', step_index, model, input_tokens: 100, content: 'Reply',
    }))
    writeFileSync(join(logs, 'transcript_full.jsonl'), steps.map(step => JSON.stringify(step)).join('\n') + '\n')
  }
  const n = await startFakeNode(t, { features: ['assistant_pids'] })
  const runtime = createAgyRuntime({ home, agyDir, agyBin, assistantAcpIdleMs, personadBin: join(home, 'missing'), socketPath: n.path, log: () => {} })
  t.after(async () => { await runtime.stop(); rmSync(home, { recursive: true, force: true }) })
  runtime.start()
  await n.ready
  const calls = () => readFileSync(join(home, 'calls.jsonl'), 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse)
  const withSession = (method, params) => n.peer.call(`session.${method}`, { session_id: sessionId, ...params })
  const promptModel = async (number = 1) => (await until('native prompt model', () => calls().filter(call => call.method === 'fake/prompt_model')[number - 1])).model
  const setModels = () => calls().filter(call => call.method === 'session/set_model').map(call => call.params.modelId)
  const start = params => n.peer.call('session.start', { cwd: home, assistant: true, ...params })
  return { n, calls, withSession, promptModel, setModels, start }
}

test('an assistant restores its in-memory model after its idle ACP worker is reaped', async (t) => {
  const { n, calls, withSession, promptModel, setModels, start } = await fixture(t, { assistantAcpIdleMs: 250 })
  await start({ prompt: 'First turn' })
  assert.equal(await promptModel(), CARBON)
  const pids = () => n.last('runtime.assistant_pids')?.pids
  const firstWorker = await until('original ACP worker reported', () => pids()?.[sessionId])
  await until('idle ACP worker reaped', () => !pids()?.[sessionId])
  await withSession('send', { text: 'Next turn' })
  assert.equal(await promptModel(2), CARBON)
  assert.notEqual(await until('replacement ACP worker reported', () => pids()?.[sessionId]), firstWorker)
  assert.equal(calls().filter(call => call.method === 'session/resume').length, 1)
  assert.deepEqual(setModels(), [CARBON, CARBON])
})

for (const model of [CARBON, NEON, CARBON_MEDIUM]) {
  test(`a fresh runtime keeps the last exported model (${model})`, async (t) => {
    const { withSession, promptModel, setModels } = await fixture(t, { transcriptModel: model.toUpperCase() })
    await withSession('send', { text: 'Continue the exported session', assistant: true })
    assert.equal(await promptModel(), model)
    assert.deepEqual(setModels(), model === NEON ? [] : [model])
  })
}

test('an explicit turn model overrides both the transcript and configured default', async (t) => {
  const { withSession, promptModel, setModels } = await fixture(t, { transcriptModel: NEON })
  await withSession('send', { text: 'Use the picked model', model: CARBON_MEDIUM })
  assert.equal(await promptModel(), CARBON_MEDIUM)
  assert.deepEqual(setModels(), [CARBON_MEDIUM])
})

test('the first resume resolves a settings display name using the catalog in its reply', async (t) => {
  const { n, withSession, promptModel, setModels } = await fixture(t, {
    configured: 'Gemini 4 Carbon (High)', error: 'Discovery unavailable',
  })
  assert.deepEqual(n.last('runtime.launchers')?.launchers[0]?.models ?? [], [])
  await withSession('send', { text: 'Resume without an exported model' })
  assert.equal(await promptModel(), CARBON)
  assert.deepEqual(setModels(), [CARBON])
})

test('legacy effort-only modes select the matching model in the saved family after resume', async (t) => {
  const { calls, withSession, promptModel, setModels } = await fixture(t, { transcriptModel: CARBON })
  await withSession('modes', { effort: 'medium' })
  await withSession('send', { text: 'Use the new effort' })
  assert.equal(await promptModel(), CARBON_MEDIUM)
  assert.deepEqual(setModels(), [CARBON_MEDIUM])
  assert.ok(!calls().some(call => call.method === 'session/set_config_option'))
})

test('an unlisted effort keeps its legacy request but a native family switch is reverted', async (t) => {
  const { calls, withSession, promptModel, setModels } = await fixture(t, {
    transcriptModel: CARBON, effortFallback: { xhigh: NEON_XHIGH },
  })
  await withSession('modes', { effort: 'xhigh' })
  assert.deepEqual(calls().filter(call => call.method === 'session/set_config_option').map(call => call.params.value), ['xhigh'])
  assert.deepEqual(setModels(), [CARBON, CARBON])
  await withSession('send', { text: 'Keep the original family' })
  assert.equal(await promptModel(), CARBON)
})
