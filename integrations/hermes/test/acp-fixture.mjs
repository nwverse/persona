#!/usr/bin/env node
import { randomUUID } from 'node:crypto'
import { readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { createInterface } from 'node:readline'

const root = process.env.HERMES_TEST_STATE
const write = (message) => process.stdout.write(JSON.stringify({ jsonrpc: '2.0', ...message }) + '\n')
const reply = (id, result) => write({ id, result })
const update = (sessionId, update) => write({ method: 'session/update', params: { sessionId, update } })
const user = (text) => ({ sessionUpdate: 'user_message_chunk', content: { type: 'text', text } })
const agent = (text) => ({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } })
const tool = { toolCallId: 'tool-1', title: 'Echo', kind: 'execute', rawInput: { command: 'echo sample' } }
let session
let active
let permissionId = 0

const lines = createInterface({ input: process.stdin, crlfDelay: Infinity })
for await (const line of lines) {
  const { id, method, params, result } = JSON.parse(line)
  switch (method) {
    case 'initialize':
      reply(id, { protocolVersion: 1, agentInfo: { name: 'hermes-agent' },
        agentCapabilities: { loadSession: true, sessionCapabilities: { list: {} } } })
      break
    case 'session/list': {
      const files = (await readdir(root)).filter((file) => file.endsWith('.json')).sort()
      const next = params.cursor ? files.indexOf(params.cursor) + 1 : 0
      const selected = files[next]
      const sessions = selected ? [JSON.parse(await readFile(join(root, selected), 'utf-8')).info] : []
      reply(id, { sessions, ...(next + 1 < files.length ? { nextCursor: selected } : {}) })
      break
    }
    case 'session/new':
      if (process.cwd() !== params.cwd) throw new Error('wrong worker directory')
      session = { info: { sessionId: randomUUID(), cwd: params.cwd }, updates: [] }
      reply(id, { sessionId: session.info.sessionId })
      break
    case 'session/load':
      session = JSON.parse(await readFile(join(root, params.sessionId + '.json'), 'utf-8'))
      if (process.cwd() !== session.info.cwd) throw new Error('wrong restored worker directory')
      for (const event of session.updates) update(params.sessionId, event)
      reply(id, {})
      break
    case 'session/prompt': {
      if (active || params.sessionId !== session.info.sessionId) throw new Error('worker session overlap')
      const text = params.prompt[0].text
      active = { id, text }
      if (text === 'disconnect') process.exit(1)
      if (text === 'hold') {
        update(params.sessionId, agent('streaming'))
      } else if (text === 'permission') {
        active.permission = 'permission-' + ++permissionId
        write({ id: active.permission, method: 'session/request_permission', params: {
          sessionId: params.sessionId, toolCall: tool, options: [
            { kind: 'allow_always', optionId: 'always' }, { kind: 'allow_once', optionId: 'once' },
            { kind: 'reject_once', optionId: 'deny' },
          ],
        } })
      } else {
        await finish([agent('echo: '), agent(text)])
      }
      break
    }
    case 'session/cancel':
      if (params.sessionId !== session.info.sessionId || !active) throw new Error('unexpected cancellation')
      await finish([], 'cancelled')
      break
    default:
      if (active && id === active.permission) {
        const text = result?.outcome?.optionId ?? result?.outcome?.outcome
        await finish([
          { sessionUpdate: 'tool_call', ...tool },
          { sessionUpdate: 'tool_call_update', toolCallId: tool.toolCallId, status: 'completed', rawOutput: text },
          agent(text),
        ])
      }
  }
}

async function finish(events, reason = 'end_turn') {
  const { id, text } = active
  active = undefined
  session.info.updatedAt = new Date().toISOString()
  session.updates.push(user(text), ...events)
  await writeFile(join(root, session.info.sessionId + '.json'), JSON.stringify(session))
  for (const event of events) update(session.info.sessionId, event)
  reply(id, { stopReason: reason })
}
