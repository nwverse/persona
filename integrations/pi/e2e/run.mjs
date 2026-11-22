// End-to-end check of the pi runtime (docs/integrations/pi.md "Validation"): a real `pi --mode rpc` with this extension (isolated agent
// directory, the model played by DSH's llm-mock-server over Anthropic's API) and a real `personad node --stdio` started
// by the runtime SDK's harness, which plays the hub. It sends from the "phone", streams, answers an approval and an
// ask_user question there, and interrupts.
//
//   DSH_CHECKOUT=<dsh source checkout, built> PERSONAD=<personad binary> node integrations/pi/e2e/run.mjs
//
// (DSH_CHECKOUT only for its mock model server.) Nothing outside a fresh temp directory is touched.

import { spawn } from 'node:child_process'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { conformance } from '../../runtime-sdk/testing/conformance.mjs'
import { scriptedModel } from '../../runtime-sdk/testing/model.mjs'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'

const here = dirname(fileURLToPath(import.meta.url))
const dshCheckout = process.env.DSH_CHECKOUT
const personad = process.env.PERSONAD
const piBin = process.env.PI_BIN || 'pi'
if (!dshCheckout || !personad) {
  console.error('set DSH_CHECKOUT (a built dsh source checkout, for its mock model server) and PERSONAD (a personad binary)')
  process.exit(2)
}
const root = mkdtempSync(join('/tmp', 'jpi-e2e-'))
const agentDir = join(root, 'agent')
const work = join(root, 'work')
const home = join(root, 'home')
for (const d of [agentDir, work, home]) mkdirSync(d, { recursive: true })
const { check, summary } = checks()

// ---- the model (the SDK's scripted model) ----
const model = await scriptedModel(dshCheckout)
const modelURL = model.url

writeFileSync(join(agentDir, 'models.json'), JSON.stringify({ providers: { mock: {
  baseUrl: modelURL, api: 'anthropic-messages', apiKey: 'mock-key',
  models: [{ id: 'mock-model', name: 'Mock', reasoning: true, input: ['text', 'image'], contextWindow: 200000, maxTokens: 8192,
    cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
} } }))
writeFileSync(join(agentDir, 'settings.json'), JSON.stringify({ defaultProvider: 'mock', defaultModel: 'mock-model', quietStartup: true }))
writeFileSync(join(agentDir, 'persona.json'), JSON.stringify({ approve: ['bash'], askUser: true, answerFirst: 'phone' }))

// ---- the node (the harness plays the hub) and pi with the extension ----
const node = await startNode({ personad })
const piLog = []
const pi = spawn(piBin, ['--mode', 'rpc', '-e', join(here, '..', 'index.js'), '--no-skills', '--offline'], {
  cwd: work,
  env: { ...process.env, HOME: home, PI_CODING_AGENT_DIR: agentDir, PERSONA_RUNTIME_SOCKET: node.runtimeSocket, PI_OFFLINE: '1' },
  stdio: ['pipe', 'pipe', 'pipe'], // stdin stays open: pi's RPC mode runs until it closes
})
pi.stdout.on('data', (d) => piLog.push(String(d)))
pi.stderr.on('data', (d) => piLog.push(String(d)))

async function cleanup() {
  if (pi.exitCode === null) {
    const exited = new Promise((r) => pi.once('exit', r))
    pi.stdin.end()
    pi.kill('SIGTERM')
    await Promise.race([exited, new Promise((r) => setTimeout(r, 10_000))])
  }
  node.stop()
  await model.close()
  if (process.env.KEEP !== '1') rmSync(root, { recursive: true, force: true })
}

const list = async () => (await node.call('sessions.list')).sessions ?? []

try {
  const row = await until('the pi session listed', async () => (await list()).find((s) => s.agent === 'pi'), 60_000)
  const sid = row.session_id
  check('pi\'s session is listed as a runtime\'s, with its agent', row.source_kind === 'runtime' && row.source_instance_id === `pi-${pi.pid}` && row.cwd.endsWith('/work'), JSON.stringify({ src: row.source_instance_id, cwd: row.cwd }))
  check('it declares send and interrupt, no terminal', row.capabilities?.send === true && row.capabilities?.interrupt === true && !row.capabilities?.terminal)
  await node.call('session.watch', { session_id: sid, since_seq: 0 })

  // What every runtime does (the SDK's checks), then pi's own: its cards say pi.
  const { approval } = await conformance({
    node, check, sid, model,
    bashArgs: (command) => ({ command }),
    ask: { tool: 'ask_user', args: (question, options) => ({ question, options }) },
    history: (h) => h?.startsWith(`pi:${sid}:`),
  })
  check('the approval card is pi\'s', approval.agent === 'pi', approval.agent)
} catch (err) {
  check('run', false, err.message)
  console.log('--- node log ---\n' + node.log.join('').slice(-2000))
  console.log('--- pi log ---\n' + piLog.join('').slice(-3000))
}
const failed = summary()
await cleanup()
process.exit(failed ? 1 : 0)
