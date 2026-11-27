// A fake agy for the compatibility probe (#16311). Files in the test's directory choose what it reports:
// version (agentInfo.version), native-mode (served | missing | unimplemented | no-port | silent) and records
// native-calls.jsonl / acp-starts.jsonl.
import { randomUUID } from 'node:crypto'
import { appendFileSync, existsSync, readFileSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:http'
import { createInterface } from 'node:readline'

if (process.argv.includes('models')) { console.log('test-model\tTest Model'); process.exit(0) }

const directory = process.argv[2]
const read = (name, fallback) => existsSync(`${directory}/${name}`) ? readFileSync(`${directory}/${name}`, 'utf8').trim() : fallback
const mode = read('native-mode', 'served')
const log = process.argv[process.argv.indexOf('--log-file') + 1]
appendFileSync(`${directory}/acp-starts.jsonl`, `${JSON.stringify({ pid: process.pid })}\n`)
const server = createServer(async (req, res) => {
  // Port discovery elsewhere may probe listeners with GET /; only POSTs are native calls.
  if (req.method !== 'POST') return res.writeHead(404).end()
  const chunks = []
  for await (const chunk of req) chunks.push(chunk)
  const method = req.url.split('/').at(-1)
  appendFileSync(`${directory}/native-calls.jsonl`, `${JSON.stringify({ method, params: JSON.parse(Buffer.concat(chunks).toString()) })}\n`)
  if (mode === 'missing') return res.writeHead(404, { 'Content-Type': 'text/plain' }).end('404 page not found\n')
  if (mode === 'unimplemented') return res.writeHead(404).end(JSON.stringify({ code: 'unimplemented', message: `${method} is not implemented` }))
  res.writeHead(404).end(JSON.stringify({ code: 'not_found', message: 'not_found: trajectory not found' }))
})
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
if (mode === 'no-port') writeFileSync(log, `Language server version: ${read('version', '1.5.18')}\nLanguage server listening on port ${server.address().port}\n`)
else if (mode !== 'silent') writeFileSync(log, `Language server version: ${read('version', '1.5.18')}\nLanguage server listening on random port at ${server.address().port} for HTTP\n`)

const send = (message) => process.stdout.write(`${JSON.stringify({ jsonrpc: '2.0', ...message })}\n`)
createInterface({ input: process.stdin }).on('line', (line) => {
  if (!line.trim()) return
  const req = JSON.parse(line)
  if (req.id === undefined) return
  if (req.method === 'initialize') return send({ id: req.id, result: { protocolVersion: 1, agentInfo: { name: 'fake-agy', version: read('version', '1.5.18') } } })
  if (req.method === 'session/new') return send({ id: req.id, result: { sessionId: randomUUID(), models: { currentModelId: 'test-model', availableModels: [{ modelId: 'test-model', name: 'Test Model' }] } } })
  if (req.method === 'session/prompt') {
    send({ method: 'session/update', params: { sessionId: req.params?.sessionId, update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: 'OK' } } } })
    return send({ id: req.id, result: { stopReason: 'end_turn' } })
  }
  send({ id: req.id, result: {} })
})
process.stdin.on('end', () => server.close(() => process.exit(0)))
