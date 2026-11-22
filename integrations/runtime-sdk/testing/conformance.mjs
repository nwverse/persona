// The checks every runtime's end-to-end run makes once a session is open on the "phone" (docs/integrations/runtimes.md "Acceptance"):
// a text turn (the prompt, thinking, the reply, the turn's end, paging), an approval allowed and one refused with
// words, a question answered, live text and an interrupt, continuous pushes. A runtime's run sets up its host, the
// node (node-harness.mjs) and the scripted model (model.mjs), opens a session, calls this, then checks what is
// particular to it (its row, its cards' labels) on what this returns.

import { randomUUID } from 'node:crypto'
import { until } from './fake-node.js'

/**
 * @param {object} t
 * @param {object} t.node - startNode()'s handle, the session watched from since_seq 0.
 * @param {(name: string, ok: boolean, detail?: string) => void} t.check
 * @param {string} t.sid - the session.
 * @param {{ script(opts: object): Promise<void> }} t.model - scriptedModel().
 * @param {(command: string) => object} t.bashArgs - the runtime's bash tool arguments for a command (the run makes
 *   commands containing PERSONA_E2E_ASK need approval).
 * @param {{ tool: string, args(question: string, options: string[]): object }} t.ask - its question tool.
 * @param {(history: string) => boolean} [t.history] - what a page's history should look like.
 * @param {(status: string) => boolean} [t.ended] - a status that means the turn ended (default: done).
 * @param {(sid: string) => Promise<string>} [t.status] - the session's status (default: the node's pushes).
 * @returns {Promise<{ approval: object, question: object }>} the cards the phone got.
 */
export async function conformance(t) {
  const { node, check, sid, model } = t
  const ended = t.ended ?? ((s) => s === 'done')
  const status = t.status ?? (async (id) => node.status(id))
  const userText = (text) => node.items(sid).some((i) => i.role === 'user' && i.kind === 'text' && i.text === text)
  const assistantText = (text) => node.items(sid).some((i) => i.role === 'assistant' && i.kind === 'text' && i.text === text)
  const send = (text) => node.call('session.send', { session_id: sid, text, client_msg_id: randomUUID() })

  // A text turn: thinking and the reply come back, the turn ends.
  await model.script({ sequence: ['reasoning_success'], reasoningText: 'thinking it over', successText: 'Hello from the runtime', chunkSize: 3 })
  await send('hello from the phone')
  await until('the reply', () => assistantText('Hello from the runtime'), 60_000)
  check('the prompt comes back as a user item', userText('hello from the phone'))
  check('thinking comes through', node.items(sid).some((i) => i.kind === 'thinking' && i.text.includes('thinking it over')))
  await until('the turn ends', async () => ended(await status(sid)), 20_000).catch(() => {})
  const end = await status(sid)
  check('the turn ends', ended(end), String(end))
  const page = await node.call('session.items', { session_id: sid, turns: 2, fold_work: true, lazy_images: true })
  check('history pages from the end', page.items.some((i) => i.text === 'Hello from the runtime') && page.to_seq > 0 && (t.history ? t.history(page.history) : true), String(page.history))

  // An approval allowed on the phone: the command runs and the turn goes on.
  node.resolve = (ix) => (ix.kind === 'permission' ? { allow: true } : { dismiss: true })
  await model.script({ sequence: ['tool_call_success', 'success'], toolName: 'bash', toolArguments: JSON.stringify(t.bashArgs('echo PERSONA_E2E_ASK ran')), successText: 'The command ran' })
  await send('run the command')
  const approval = await until('the approval', () => node.asked.find((i) => i.kind === 'permission'), 60_000)
  check('the approval reaches the phone with the command', approval.tool_name === 'Bash' && approval.tool_input?.command === 'echo PERSONA_E2E_ASK ran', JSON.stringify({ tool: approval.tool_name, input: approval.tool_input }))
  await until('the tool result', () => node.items(sid).some((i) => i.kind === 'tool_result' && /PERSONA_E2E_ASK ran/.test(i.text ?? '')), 60_000)
  check('the allowed command runs and its output comes back', true)
  await until('the follow-up reply', () => assistantText('The command ran'), 60_000)
  check('the turn goes on after the approval', true)

  // A refusal with words: the command does not run, the words reach the agent.
  node.resolve = (x) => (x.kind === 'permission' ? { allow: false, message: 'not this one, PERSONA_E2E_WHY' } : { dismiss: true })
  const asked = node.asked.length
  await model.script({ sequence: ['tool_call_success', 'success'], toolName: 'bash', toolArguments: JSON.stringify(t.bashArgs('echo PERSONA_E2E_ASK again')), successText: 'Understood' })
  await send('run it again')
  await until('the second approval', () => node.asked.length > asked, 60_000)
  await until('the words in the conversation', () => node.items(sid).some((i) => /PERSONA_E2E_WHY/.test(i.text ?? '')), 60_000)
  check('a refusal with words reaches the agent, and the command does not run', !node.items(sid).some((i) => i.kind === 'tool_result' && !i.is_error && /PERSONA_E2E_ASK again/.test(i.text ?? '')))
  await until('the turn after the refusal ends', async () => ended(await status(sid)), 60_000).catch(() => {})

  // A question answered on the phone: the agent gets the answer.
  node.resolve = (q) => (q.kind === 'question' ? { answers: { 'Which database?': 'sqlite' } } : { dismiss: true })
  await model.script({ sequence: ['tool_call_success', 'success'], toolName: t.ask.tool, toolArguments: JSON.stringify(t.ask.args('Which database?', ['postgres', 'sqlite'])), successText: 'Using sqlite' })
  await send('ask me something')
  const question = await until('the question', () => node.asked.find((x) => x.kind === 'question'), 60_000)
  check('the question reaches the phone with its options', question.questions?.[0]?.question === 'Which database?' && question.questions[0].options.length === 2)
  await until('the answer as a tool result', () => node.items(sid).some((i) => i.kind === 'tool_result' && /sqlite/.test(i.text ?? '')), 60_000)
  check('the answer goes back to the agent', true)
  await until('the reply after the answer', () => assistantText('Using sqlite'), 60_000)

  // A slow reply: live text streams; an interrupt from the phone stops it.
  await model.script({ sequence: ['slow_success'], successText: 'a long slow reply '.repeat(30), chunkSize: 2, chunkDelayMs: 100 })
  await send('say something long')
  await until('working', async () => (await status(sid)) === 'working', 20_000)
  await until('live text', () => node.deltas(sid).some((x) => x.includes('a long slow')), 20_000)
  check('live text arrives as deltas while the reply streams', true)
  await node.call('session.interrupt', { session_id: sid })
  await until('the interrupt notice', () => node.items(sid).some((i) => i.kind === 'notice' && i.text === '已中断'), 30_000)
  check('an interrupt from the phone stops the turn', true)

  check('pushed batches are continuous', node.gaps(sid).length === 0, `${node.batches(sid).length} batches`)
  return { approval, question }
}
