// A fake `agy` whose `acp` process keeps the account it started with, as agy acp does after the plan lapses (#10368).
// Usage: node fake-acp-stale.mjs <directory> models | acp [...]
//   <directory>/account.json  the CLI's account now: {models: [{modelId, name}], rejects?: [modelId]} or {error}
//   <directory>/hold          while it exists, session/prompt does not end
//   <directory>/calls.jsonl   every request an acp process receives, with its pid
// `agy models` reads the account each time. An acp process reads it once when it starts, refuses models it did not
// have then (and the IDs in `rejects`) as unknown, and stays signed out from the first request that finds the CLI so.
import { appendFileSync, existsSync, readFileSync } from 'node:fs'
import { randomUUID } from 'node:crypto'
import { join } from 'node:path'
import { createInterface } from 'node:readline'

const [directory, command] = process.argv.slice(2)
const account = () => JSON.parse(readFileSync(join(directory, 'account.json'), 'utf8'))

if (command === 'models') {
  const now = account()
  if (now.error) {
    console.error(now.error)
    process.exit(1)
  }
  console.error('Fetching available models...')
  for (const model of now.models) console.log(`${model.modelId}\t${model.name}`)
  process.exit(0)
}

const started = account()
const models = started.models || []
let signedOut = Boolean(started.error)
const send = (value) => process.stdout.write(`${JSON.stringify(value)}\n`)
const answer = (id, result) => send({ jsonrpc: '2.0', id, result })
const fail = (id, message) => send({ jsonrpc: '2.0', id, error: { code: -32000, message } })

createInterface({ input: process.stdin }).on('line', (line) => {
  const request = JSON.parse(line)
  const { id, method, params } = request
  if (!method) return
  appendFileSync(join(directory, 'calls.jsonl'), `${JSON.stringify({ pid: process.pid, ...request })}\n`)
  if (account().error) signedOut = true
  if (id === undefined) return
  if (method === 'initialize') return answer(id, { protocolVersion: 1, agentInfo: { version: 'test' } })
  if (method === 'session/close') return answer(id, {})
  if (signedOut) return fail(id, 'Please sign in')
  switch (method) {
    case 'session/new':
    case 'session/resume':
      return answer(id, { sessionId: params.sessionId || randomUUID(), models: { availableModels: models, currentModelId: models[0]?.modelId } })
    case 'session/set_model':
      if (!models.some((model) => model.modelId === params.modelId) || started.rejects?.includes(params.modelId)) {
        return send({ jsonrpc: '2.0', id, error: { code: -32602, message: `Unknown model ID: ${params.modelId}` } })
      }
      return answer(id, {})
    case 'session/set_mode':
    case 'session/set_config_option':
      return answer(id, {})
    case 'session/prompt': {
      const end = () => {
        send({ jsonrpc: '2.0', method: 'session/update', params: { sessionId: params.sessionId, update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'OK' } } } })
        answer(id, { stopReason: 'end_turn' })
      }
      if (!existsSync(join(directory, 'hold'))) return end()
      const wait = setInterval(() => {
        if (existsSync(join(directory, 'hold'))) return
        clearInterval(wait)
        end()
      }, 20)
      return
    }
    default:
      return send({ jsonrpc: '2.0', id, error: { code: -32601, message: `Method not found: ${method}` } })
  }
})
