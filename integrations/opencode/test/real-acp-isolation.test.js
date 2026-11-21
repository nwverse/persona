// Opt-in pinned native-engine contract. The CI wrapper verifies the binary;
// all model traffic goes to this test's local deterministic HTTP server.
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'node:http'
import { mkdtempSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { performance } from 'node:perf_hooks'
import { OpencodeAcpClient } from '../lib/acp.js'
import { createMcpProxy } from '../lib/mcp.js'
import { createOpencodeCompanion } from '../lib/companion.js'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'

const bin = process.env.OPENCODE_CONTRACT_BIN
const probe = { name: 'probe', description: 'Return the test conversation identity', input_schema: { type: 'object', properties: {} } }

async function fakeModel(t) {
  const requests = []
  const server = createServer(async (req, res) => {
    try {
      assert.equal(req.url, '/v1/chat/completions')
      let text = ''
      for await (const chunk of req) text += chunk
      const body = JSON.parse(text)
      requests.push(body)
      const lastUser = body.messages.findLastIndex((m) => m.role === 'user')
      const user = JSON.stringify(body.messages[lastUser]?.content ?? '')
      const alreadyCalled = body.messages.slice(lastUser + 1).some((m) => m.role === 'tool')
      const name = body.tools?.find((tool) => /probe$/.test(tool.function?.name))?.function.name
      // Native title/metadata requests may repeat the user text without any
      // tools. Only a main tool-enabled request can select probe; the outer
      // identity assertions still require every requested MCP call to happen.
      const call = /PROBE_IDENTITY/.test(user) && !alreadyCalled && Boolean(name)
      if (!body.stream) {
        res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({ id: 'fake', object: 'chat.completion', model: 'test-model', choices: [{ index: 0, message: { role: 'assistant', content: 'Test conversation' }, finish_reason: 'stop' }], usage: { prompt_tokens: 10, completion_tokens: 2, total_tokens: 12 } }))
        return
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      const event = (delta, finish_reason = null) => res.write('data: ' + JSON.stringify({ id: 'fake', object: 'chat.completion.chunk', model: 'test-model', choices: [{ index: 0, delta, finish_reason }] }) + '\n\n')
      event({ role: 'assistant' })
      if (call) event({ tool_calls: [{ index: 0, id: 'probe-call', type: 'function', function: { name, arguments: '{}' } }] })
      else event({ content: 'contract complete' })
      event({}, call ? 'tool_calls' : 'stop')
      res.end('data: [DONE]\n\n')
    } catch (error) {
      requests.push({ error: error.message })
      res.writeHead(500).end(JSON.stringify({ error: { message: error.message } }))
    }
  })
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  return { url: `http://127.0.0.1:${server.address().port}/v1`, requests }
}

function environment(home, modelUrl) {
  return {
    PATH: process.env.PATH, LANG: 'C.UTF-8', HOME: home, OPENCODE_TEST_HOME: home,
    XDG_CONFIG_HOME: join(home, 'config'), XDG_DATA_HOME: join(home, 'data'),
    XDG_STATE_HOME: join(home, 'state'), XDG_CACHE_HOME: join(home, 'cache'),
    OPENCODE_DISABLE_PROJECT_CONFIG: '1', OPENCODE_PURE: '1', OPENCODE_DISABLE_AUTOUPDATE: '1',
    OPENCODE_DISABLE_AUTOCOMPACT: '1', OPENCODE_DISABLE_MODELS_FETCH: '1', OPENCODE_AUTH_CONTENT: '{}',
    OPENCODE_CONFIG_CONTENT: JSON.stringify({
      model: 'test/test-model', small_model: 'test/test-model', formatter: false, lsp: false,
      enabled_providers: ['test'], share: 'disabled', permission: { '*': 'allow' },
      provider: { test: { name: 'Test', id: 'test', env: [], npm: '@ai-sdk/openai-compatible',
        options: { apiKey: 'fake-contract-key', baseURL: modelUrl },
        models: { 'test-model': { id: 'test-model', name: 'Test Model', attachment: false, reasoning: false,
          temperature: false, tool_call: true, limit: { context: 100_000, output: 10_000 }, cost: { input: 0, output: 0 } } },
      } },
    }),
  }
}

test('pinned OpenCode reproduces shared MCP replacement and isolates same-directory assistant identities', { skip: !bin, timeout: 210_000 }, async (t) => {
  const home = mkdtempSync(join(tmpdir(), 'opencode-real-contract-'))
  const cwd = join(home, 'assistant')
  mkdirSync(cwd)
  t.after(() => rmSync(home, { recursive: true, force: true }))
  const model = await fakeModel(t)
  const env = environment(home, model.url)
  const logs = []
  const sharedCalls = []
  const proxy = createMcpProxy({ tools: () => [probe], call: (binding) => {
    sharedCalls.push(binding.sessionId)
    return { content: [{ type: 'text', text: 'fake identity' }] }
  } })
  const shared = new OpencodeAcpClient({ bin, cwd: home, env, processGroup: true, log: (line) => logs.push(line),
    onPermissionRequest: ({ options }) => ({ outcome: { outcome: 'selected', optionId: options.find((option) => option.kind === 'allow_once').optionId } }),
  })
  t.after(async () => { await shared.stop(); proxy.stop() })
  try {
    const routeA = await proxy.allocate({ sessionId: 'a', assistant: true })
    const routeB = await proxy.allocate({ sessionId: 'b', assistant: true })
    const initialized = await shared.ensureStarted()
    assert.equal(initialized.agentInfo?.version, '1.19.6')
    const first = await shared.newSession({ cwd, mcpServers: [routeA.acpServer] })
    await shared.newSession({ cwd, mcpServers: [routeB.acpServer] })
    await shared.prompt(first.sessionId, [{ type: 'text', text: 'PROBE_IDENTITY shared' }], 30_000)
    assert.deepEqual(sharedCalls, ['b'], 'pinned engine replaces a same-name MCP route within a directory')
    await shared.stop()

    const node = await startFakeNode(t, { tools: [probe], features: ['assistant_pids', 'prefer_running'] })
    node.resolve = () => ({ allow: true })
    const privateEnv = { ...env, OPENCODE_CONFIG_CONTENT: JSON.stringify({ ...JSON.parse(env.OPENCODE_CONFIG_CONTENT), server: { port: Number(new URL(model.url).port), hostname: '0.0.0.0', mdns: true } }) }
    const firstTokens = new Map()
    const companion = createOpencodeCompanion({ home, bin, env: privateEnv, socketPath: node.path, retryMs: 20, pushDelayMs: 5,
      assistantAcpFactory: (options) => {
        const client = new OpencodeAcpClient(options)
        client.on('update', (params) => {
          if (params.update?.sessionUpdate === 'agent_message_chunk' && firstTokens.get(params.sessionId) === null) {
            firstTokens.set(params.sessionId, performance.now())
          }
        })
        return client
      },
      log: (line) => logs.push(line), openDb: async () => ({ all: () => [], close() {} }),
      discoverModels: async () => [{ providerID: 'test', id: 'test-model', name: 'Test Model' }],
    })
    t.after(() => companion.stop())
    await companion.start()
    await node.ready
    const start = async () => (await node.peer.call('session.start', { cwd, assistant: true })).session.session_id
    const a = await start(), b = await start()
    const pids = node.last('runtime.assistant_pids').pids
    assert.notEqual(pids[a], pids[b])
    assert.equal(node.hello.caps.supervision_policy, undefined)
    const prompt = async (id, text) => {
      await node.peer.call('session.send', { session_id: id, text })
      await until('real worker turn settled', () => !companion.live.get(id).running && !companion.live.get(id).queued, 30_000)
      assert.equal(companion.live.get(id).turnError, '')
    }
    await Promise.all([prompt(a, 'PROBE_IDENTITY isolated A'), prompt(b, 'PROBE_IDENTITY isolated B')])
    assert.deepEqual(node.toolCalls.map((call) => call.session_id).sort(), [a, b].sort())

    // T12 observations never set a performance threshold. Five process-cold
    // samples and five warm turns; OS page-cache state is uncontrolled.
    const cold = [], warm = [], rss = []
    const firstToken = async (id, began) => {
      firstTokens.set(id, null)
      await prompt(id, 'PERFORMANCE_TEXT')
      const timestamp = firstTokens.get(id)
      assert.equal(typeof timestamp, 'number', 'the engine must produce a first token')
      firstTokens.delete(id)
      return timestamp - began
    }
    for (let sample = 0; sample < 5; sample++) {
      const id = await start()
      await companion.assistantWorkers.discard(companion.live.get(id).worker)
      // This send starts a new process, initializes, then session/resume's the
      // existing native conversation. It does not time session/new instead.
      cold.push(await firstToken(id, performance.now()))
      warm.push(await firstToken(id, performance.now()))
      const pid = companion.live.get(id).worker.acp.pid
      rss.push(Number(readFileSync(`/proc/${pid}/status`, 'utf8').match(/^VmRSS:\s+(\d+)/m)?.[1]))
      assert.ok(Number.isFinite(rss.at(-1)) && rss.at(-1) > 0, 'RSS must be a valid observation')
      await node.peer.call('session.close', { session_id: id })
    }
    const quantile = (list, q) => [...list].sort((a, b) => a - b)[Math.ceil(list.length * q) - 1]
    t.diagnostic(JSON.stringify({ version: '1.19.6', samples: 5, cache: 'uncontrolled OS cache; fresh worker versus existing worker',
      cold_ms: { p50: quantile(cold, .5), p95: quantile(cold, .95) }, warm_ms: { p50: quantile(warm, .5), p95: quantile(warm, .95) }, rss_kib: rss }))
    assert.deepEqual(model.requests.filter((r) => r.error), [], 'fake model accepts all native request shapes')
  } catch (error) {
    t.diagnostic(logs.slice(-20).join('\n'))
    throw error
  }
})
