// End-to-end check of the DSH runtime (docs/integrations/dsh.md "Verification", the acceptance list of docs/integrations/runtimes.md): a real `dsh web`
// (isolated DSH_HOME, the model played by DSH's own llm-mock-server) with this plugin loaded through --patch, a real
// `personad node --stdio` started by the runtime SDK's harness, which plays the hub. It starts a session from the
// "phone", sends to it, and answers an approval and a question.
//
//   DSH_CHECKOUT=<dsh source checkout, built> PERSONAD=<personad binary> node integrations/dsh/e2e/run.mjs
//
// With PERSONA_E2E_DAEMON=1 the plugin is a daemon instead (docs/integrations/daemons.md): it serves a WebSocket endpoint and the
// node dials it from daemons.json, as for a DSH that cannot reach runtime.sock; the same checks run.
//
// Nothing outside fresh temp directories is touched: DSH_HOME, the node's HOME and the workspace all live there.

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { conformance } from '../../runtime-sdk/testing/conformance.mjs'
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
const root = mkdtempSync(join('/tmp', 'jdsh-e2e-'))
const dshHome = join(root, 'dsh')
const work = join(root, 'work')
for (const d of [dshHome, work]) mkdirSync(d, { recursive: true })
const { check, summary } = checks()
let dsh
let node

// DSH keeps writing to its home while it shuts down: wait for it to exit before removing the directory.
async function cleanup() {
  if (dsh && dsh.exitCode === null) {
    const exited = new Promise((r) => dsh.once('exit', r))
    dsh.kill('SIGTERM')
    await Promise.race([exited, new Promise((r) => setTimeout(r, 10_000))])
  }
  node?.stop()
  if (!keep) rmSync(root, { recursive: true, force: true })
}
process.on('SIGINT', () => cleanup().finally(() => process.exit(130)))

// ---- the model (the SDK's scripted model: DSH's mock server behind a switch) ----

const model = await scriptedModel(dshCheckout)
const modelURL = `${model.url}/v1`

// ---- the node (the harness plays the hub) and DSH with the plugin ----

// Daemon mode: the node's HOME carries daemons.json (the same names as the plugin's, so the checks below hold; the
// session ids get the daemon's prefix), the plugin listens on a free port with the token from a file.
const daemon = process.env.PERSONA_E2E_DAEMON === '1'
let pluginConn = '' // how the plugin reaches the node: its socket, or listening for it
let home
if (daemon) {
  const port = await new Promise((res) => {
    const s = createServer().listen(0, '127.0.0.1', () => {
      const p = s.address().port
      s.close(() => res(p))
    })
  })
  const tokenFile = join(root, 'token')
  writeFileSync(tokenFile, randomUUID(), { mode: 0o600 })
  home = join(root, 'home')
  mkdirSync(join(home, '.persona'), { recursive: true })
  writeFileSync(join(home, '.persona', 'daemons.json'), JSON.stringify({ daemons: [{ id: 'dsh', name: 'DSH', launcher: 'dsh',
    url: `ws://127.0.0.1:${port}/persona/runtime`, token_file: tokenFile }] }), { mode: 0o600 })
  pluginConn = `listen:\n          port: ${port}\n          token_file: ${tokenFile}`
}
node = await startNode({ personad, keep, home })
check('the node starts without herdr', typeof node.info.version === 'string')
if (!daemon) pluginConn = `socket: ${node.runtimeSocket}`
// The hub's tools (the phone's): DSH's agents get them through the plugin (docs/integrations/dsh.md "Phone tools").
node.tools = [{ name: 'phone_echo', description: 'Echo a text on the phone (e2e)', input_schema: { type: 'object', properties: { text: { type: 'string' } }, required: ['text'] } }]
node.callTool = (params) => ({ content: [{ type: 'text', text: `the phone echoes ${params.args?.text}` }], is_error: false })

const patch = join(root, 'patch.yml')
const askPlugin = join(root, 'e2e-ask.js')
writeFileSync(askPlugin, `export const name = 'e2e-ask'
export function apply(ctx) {
  // Test fixture: a command containing PERSONA_E2E_ASK needs approval, so the run exercises the approval chain.
  ctx.on('tools/pre-execute', (exec, next) => {
    const cmd = exec?.arguments?.command
    if (exec?.name === 'bash' && typeof cmd === 'string' && cmd.includes('PERSONA_E2E_ASK')) return { kind: 'ask', reason: 'e2e approval' }
    return next()
  })
}
`)
writeFileSync(patch, `- insert:
    - id: persona-dsh-runtime
      name: ${join(here, '..', 'index.js')}
      config:
        ${pluginConn}
        interactions: phone
    - id: e2e-ask
      name: ${askPlugin}
# Commands run unconfined but still ask: a sandbox cannot start inside another sandbox (an agent's shell running this).
- id: permission
  config:
    presets:
      e2e:
        sandbox: danger-full-access
        approval: ask
    defaultPreset: e2e
`)
const dshLog = []
dsh = spawn(process.execPath, [join(dshCheckout, 'apps/cli/lib/bin.js'), '--profile', 'web', '--patch', patch, '--no-open', '--port', '0'], {
  cwd: work,
  env: { ...process.env, DSH_HOME: dshHome, DEEPSEEK_BASE_URL: modelURL, DEEPSEEK_API_KEY: 'mock-key' },
  stdio: ['ignore', 'pipe', 'pipe'],
})
dsh.stdout.on('data', (d) => dshLog.push(String(d)))
dsh.stderr.on('data', (d) => dshLog.push(String(d)))

try {
  const launchers = await until('the dsh launcher (the plugin connected)', async () => {
    const { launchers } = await node.call('launchers.list')
    return launchers.find((l) => l.name === 'dsh') ? launchers : undefined
  }, 120_000)
  check('the runtime connects and offers its launcher', launchers.some((l) => l.name === 'dsh' && l.source === 'dsh'))

  const { session } = await node.call('session.start', { launcher: 'dsh', cwd: work, async: true })
  const sid = session.session_id
  check('a session starts from the phone', typeof sid === 'string' && sid !== '' && (!daemon || sid.startsWith('dsh.session-')), sid)
  const dshSid = daemon ? sid.slice('dsh.'.length) : sid // DSH's own id (a daemon's sessions carry the node's prefix)
  await until('the new session listed', async () => ((await node.call('sessions.list')).sessions ?? []).some((s) => s.session_id === sid))
  const row = ((await node.call('sessions.list')).sessions ?? []).find((s) => s.session_id === sid)
  check('the session carries the runtime\'s agent and label', row.agent === 'dsh' && row.agent_label === 'DSH' && row.space_kind === 'dsh')
  check('it is a runtime source that can send, interrupt and answer, without a terminal', row.source_kind === 'runtime' && row.capabilities?.send === true && row.capabilities?.interrupt === true && row.capabilities?.approve === true && !row.capabilities?.terminal, JSON.stringify(row.capabilities))
  await node.call('session.watch', { session_id: sid, since_seq: 0 })

  // What every runtime does (the SDK's checks), then what is DSH's own: its cards' labels and reasons.
  const { approval } = await conformance({
    node, check, sid, model,
    bashArgs: (command) => ({ command, description: 'e2e' }),
    ask: { tool: 'ask_user_question', args: (question, options) => ({ questions: [{ id: 'db', question, options: options.map((label) => ({ label })) }] }) },
    history: (h) => h === `dsh:${dshSid}`,
  })
  check('the card is DSH\'s, with DSH\'s reason beside the command', approval.agent === 'dsh' && approval.agent_label === 'DSH' && approval.reason === 'e2e approval' && approval.summary === 'echo PERSONA_E2E_ASK ran', JSON.stringify({ agent: approval.agent, label: approval.agent_label, reason: approval.reason, summary: approval.summary }))

  // A phone tool, called by DSH's agent: through the node to the hub, with the session, and its result back.
  await until('the turn after the interrupt ends', async () => node.status(sid) === 'done', 30_000).catch(() => {})
  await model.script({ sequence: ['tool_call_success', 'success'], toolName: 'mcp__persona__phone_echo', toolArguments: JSON.stringify({ text: 'PERSONA_E2E_PHONE' }), successText: 'The phone answered' })
  await node.call('session.send', { session_id: sid, text: 'ask the phone', client_msg_id: randomUUID() })
  if (daemon) {
    // A daemon gets no phone tools (docs/integrations/daemons.md "Isolation"): the agent's call never reaches the hub.
    await until('the reply after the tool call', () => node.items(sid).some((i) => i.role === 'assistant' && i.text === 'The phone answered'), 60_000)
    check('a daemon\'s agent gets no phone tools: nothing reaches the hub', !node.toolCalls.some((c) => c.name === 'phone_echo'), JSON.stringify(node.toolCalls))
  } else {
    await until('the phone tool result', () => node.items(sid).some((i) => i.kind === 'tool_result' && /the phone echoes PERSONA_E2E_PHONE/.test(i.text ?? '')), 60_000)
    const call = node.toolCalls.find((c) => c.name === 'phone_echo')
    check('a phone tool reaches the hub with the calling session, and its result reaches the agent', call?.session_id === sid && call?.args?.text === 'PERSONA_E2E_PHONE', JSON.stringify(call))
    await until('the reply after the phone tool', () => node.items(sid).some((i) => i.role === 'assistant' && i.text === 'The phone answered'), 60_000)
  }

  // A background subagent (docs/integrations/dsh.md "Subagents and background tasks"): the parent's turn ends while it runs; the parent's row
  // lists it running (the apps' "Waiting"), the phone reads and watches it as "<parent>@<child>", and when it finishes the
  // parent is woken. (DSH pins a delegated child's approvals to never: it runs within the scope it was given and asks
  // nobody, so no card comes from it here; the unit tests cover the cards of a child whose policy was switched back.)
  await until('idle before the subagent', async () => node.status(sid) === 'done', 30_000).catch(() => {})
  const rowNow = async () => ((await node.call('sessions.list')).sessions ?? []).find((s) => s.session_id === sid)
  node.resolve = (ix) => (ix.kind === 'permission' ? { allow: true } : { dismiss: true })
  await model.scriptWhen('PERSONA_E2E_CHILD', { sequence: ['tool_call_success', 'success'], toolName: 'bash',
    toolArguments: JSON.stringify({ command: 'sleep 8; echo E2E_SUBAGENT_OUTPUT', description: 'e2e' }), successText: 'The child mapped it' })
  await model.script({ sequence: ['tool_call_success', 'success'], toolName: 'subagent',
    toolArguments: JSON.stringify({ description: 'E2E child', prompt: 'PERSONA_E2E_CHILD map the workspace' }), successText: 'The child is on it' })
  await node.call('session.send', { session_id: sid, text: 'hand it to a subagent', client_msg_id: randomUUID() })
  const waiting = await until('the parent done while its subagent runs', async () => {
    const r = await rowNow()
    return r?.status === 'done' && r.subagents?.some((a) => a.running) ? r : undefined
  }, 90_000)
  await model.script({ sequence: ['success'], successText: 'The parent woke up' })
  const sub = waiting.subagents.find((a) => a.running)
  check('a background subagent: the parent\'s turn ends, its row lists the subagent running', sub.description === 'E2E child' && sub.session_id === `${sid}@${sub.id}`, JSON.stringify(sub))
  check('the subagent answers the parent\'s tool call (the chat opens it from there)', node.items(sid).some((i) => i.kind === 'tool_use' && i.tool_use_id === sub.tool_use_id), sub.tool_use_id)
  const listedNow = (await node.call('sessions.list')).sessions ?? []
  check('the subagent is not a session of its own in the list', !listedNow.some((s) => s.session_id.endsWith(sub.id)))
  const childPage = await node.call('session.items', { session_id: sub.session_id, turns: 2 })
  check('the phone reads the subagent as "<parent>@<child>"', childPage.items.some((i) => i.role === 'user' && /PERSONA_E2E_CHILD/.test(i.text ?? '')), JSON.stringify(childPage.items.map((i) => i.kind)))
  await node.call('session.watch', { session_id: sub.session_id, since_seq: childPage.to_seq })
  await until('the subagent\'s command output, pushed live', () => node.items(sub.session_id).some((i) => i.kind === 'tool_result' && /E2E_SUBAGENT_OUTPUT/.test(i.text ?? '')), 60_000)
  check('its new content is pushed live under its own id', true)
  await until('the subagent finished', async () => (await rowNow())?.subagents?.every((a) => !a.running), 60_000)
  check('when it finishes the row says so', true)
  await until('the parent woken', () => node.items(sid).some((i) => i.role === 'assistant' && i.text === 'The parent woke up'), 60_000)
  check('and the parent is woken with its result', node.items(sid).some((i) => i.kind === 'notice' && /子 Agent 完成/.test(i.text ?? '')))
  check('pushed batches stay continuous', node.gaps(sid).length === 0 && node.gaps(sub.session_id).length === 0)

  // A shell run in the background: the turn ends, the row lists it until it exits.
  await until('idle before the background shell', async () => node.status(sid) === 'done', 30_000).catch(() => {})
  await model.script({ sequence: ['tool_call_success', 'success'], toolName: 'bash',
    toolArguments: JSON.stringify({ command: 'sleep 6', description: 'e2e background', run_in_background: true }), successText: 'Started it' })
  await node.call('session.send', { session_id: sid, text: 'run it in the background', client_msg_id: randomUUID() })
  const bg = await until('the turn done, the shell still running', async () => {
    const r = await rowNow()
    return r?.status === 'done' && r.background?.length ? r.background : undefined
  }, 60_000)
  check('a background shell is listed while the turn is done', bg[0].kind === 'shell' && bg[0].command === 'sleep 6', JSON.stringify(bg))
  await until('the shell exited', async () => !(await rowNow())?.background?.length, 60_000)
  check('and leaves the row when it exits', true)

  // Closing from the phone archives the session in DSH: it leaves the list, and DSH's archive set has it.
  await until('idle before closing', async () => node.status(sid) === 'done', 30_000).catch(() => {})
  await node.call('session.close', { session_id: sid })
  await until('gone from the list', async () => !((await node.call('sessions.list')).sessions ?? []).some((s) => s.session_id === sid), 30_000)
  const workspace = JSON.parse(readFileSync(join(dshHome, 'storages', 'workspace.json'), 'utf8'))
  check('closing from the phone archives it in DSH and takes it off the list', workspace.global?.archivedSessionIds?.includes(dshSid), JSON.stringify(workspace.global?.archivedSessionIds))
} catch (err) {
  check('run', false, err.message)
  console.log('--- node log ---\n' + node.log.join('').slice(-3000))
  console.log('--- dsh log ---\n' + dshLog.join('').slice(-3000).replace(/token=[\w-]+/g, 'token=<redacted>'))
} finally {
  await model.close()
}
const failed = summary()
await cleanup()
process.exit(failed ? 1 : 0)
