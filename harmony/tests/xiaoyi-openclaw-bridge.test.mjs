import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createHmac } from 'node:crypto';
import {
  DEFAULT_OPENCLAW_WS_URL,
  PersonaHubClient,
  computeReconnectDelayMs,
  handleOpenClawRpc,
  loadBridgeConfig,
  signOpenClawHeaders
} from '../scripts/xiaoyi-openclaw-bridge.mjs';

test('signOpenClawHeaders produces deterministic HMAC-SHA256 signatures with required headers', () => {
  const headers = signOpenClawHeaders({
    ak: 'test-ak',
    sk: 'test-sk-secret',
    agentId: 'agent-123',
    timestampMs: 1764100000000,
    nonce: 'deadbeef0102'
  });
  const expected = createHmac('sha256', 'test-sk-secret')
    .update('test-ak\nagent-123\n1764100000000\ndeadbeef0102', 'utf8')
    .digest('base64');
  assert.equal(headers['x-hag-ak'], 'test-ak');
  assert.equal(headers['x-hag-agent-id'], 'agent-123');
  assert.equal(headers['x-hag-timestamp'], '1764100000000');
  assert.equal(headers['x-hag-nonce'], 'deadbeef0102');
  assert.equal(headers['x-hag-signature'], expected);
});

test('computeReconnectDelayMs grows exponentially and caps at 30 seconds', () => {
  assert.equal(computeReconnectDelayMs(0), 1000);
  assert.equal(computeReconnectDelayMs(1), 2000);
  assert.equal(computeReconnectDelayMs(2), 4000);
  assert.equal(computeReconnectDelayMs(5), 30000);
  assert.equal(computeReconnectDelayMs(10), 30000);
});

test('loadBridgeConfig validates required env vars and URL schemes', () => {
  assert.throws(() => loadBridgeConfig({}), /Missing required configuration/);
  const cfg = loadBridgeConfig({
    XIAOYI_AK: 'ak',
    XIAOYI_SK: 'sk',
    XIAOYI_AGENT_ID: 'agent-1',
    PERSONA_HUB_URL: 'https://hub.example.com/',
    PERSONA_HUB_TOKEN: 'tok-1'
  });
  assert.equal(cfg.wsUrl, DEFAULT_OPENCLAW_WS_URL);
  assert.equal(cfg.hubUrl, 'https://hub.example.com');
  assert.throws(() => loadBridgeConfig({
    XIAOYI_AK: 'ak',
    XIAOYI_SK: 'sk',
    XIAOYI_AGENT_ID: 'agent-1',
    XIAOYI_WS_URL: 'http://bad',
    PERSONA_HUB_URL: 'https://hub.example.com',
    PERSONA_HUB_TOKEN: 'tok-1'
  }), /ws:\/\/ or wss:\/\//);
});

test('handleOpenClawRpc forwards status, todos, kb search, streaming ask, and refuses silent approvals over cloud relay', async () => {
  const requests = [];
  let messageSent = false;
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      requests.push({ method: req.method, url: req.url, auth: req.headers.authorization, body });
      res.setHeader('Content-Type', 'application/json');
      if (req.url === '/api/v1/sessions') {
        res.end(JSON.stringify({
          sessions: [
            { id: 'main-1', machine_id: 'm1', assistant: true, state: 'idle', status: 'idle' },
            { id: 's1', machine_id: 'm1', assistant: false, state: 'working', status: 'working' }
          ]
        }));
      } else if (req.url === '/api/v1/interactions') {
        res.end(JSON.stringify({
          interactions: [
            { id: 'dec-1', kind: 'permission', status: 'open', summary: 'Run deploy script' }
          ]
        }));
      } else if (req.url === '/api/v1/todos' && req.method === 'POST') {
        const parsed = JSON.parse(body);
        res.end(JSON.stringify({ todo: { id: 'todo-9', title: parsed.title } }));
      } else if (req.url.startsWith('/api/v1/kb-search')) {
        res.end(JSON.stringify({ hits: [{ path: 'docs/xiaoyi.md', line: 'OpenClaw setup' }] }));
      } else if (req.url === '/api/v1/assistant') {
        res.end(JSON.stringify({ main: 'main-1', main_machine_id: 'm1', conversations: [] }));
      } else if (req.url.includes('/messages') && req.method === 'POST') {
        messageSent = true;
        res.end(JSON.stringify({ ok: true }));
      } else if (req.url.includes('/items')) {
        res.end(JSON.stringify({
          items: messageSent
            ? [{ seq: 2, kind: 'text', role: 'assistant', text: 'Here is your Persona reply.' }]
            : [{ seq: 1, kind: 'text', role: 'assistant', text: 'Previous turn' }]
        }));
      } else {
        res.statusCode = 404;
        res.end(JSON.stringify({ error: 'not found' }));
      }
    });
  });

  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  const hubUrl = `http://127.0.0.1:${address.port}`;
  const hubClient = new PersonaHubClient({ hubUrl, hubToken: 'synthetic-token', askTimeoutMs: 4000 });

  try {
    // 1. Heartbeat ping
    const pingOut = [];
    await handleOpenClawRpc({ jsonrpc: '2.0', id: 'p1', method: 'ping' }, hubClient, (f) => pingOut.push(f));
    assert.equal(pingOut[0].result.pong, true);

    // 2. Refuse high-risk approval over cloud relay (ZERO POST /resolve calls!)
    const approvalOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'a1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Approve pending' }] } }
    }, hubClient, (f) => approvalOut.push(f));
    assert.equal(approvalOut[0].result.status.state, 'input-required');
    assert.ok(approvalOut[0].result.status.message.parts[0].text.includes('unlocked phone'));
    assert.ok(!requests.some((r) => r.url.includes('/resolve')));

    // 3. Status query
    const statusOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 's1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Check status' }] } }
    }, hubClient, (f) => statusOut.push(f));
    assert.ok(statusOut[0].result.status.message.parts[0].text.includes('1 working, 1 need you'));

    // 4. Add To-Do
    const todoOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 't1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: '新建待办：验证小艺通道' }] } }
    }, hubClient, (f) => todoOut.push(f));
    assert.ok(todoOut[0].result.status.message.parts[0].text.includes('验证小艺通道'));

    // 5. Knowledge search
    const kbOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'k1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Search knowledge: OpenClaw' }] } }
    }, hubClient, (f) => kbOut.push(f));
    assert.ok(kbOut[0].result.status.message.parts[0].text.includes('docs/xiaoyi.md'));

    // 6. Streaming Ask Persona
    const streamOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'm1',
      method: 'SendStreamingMessage',
      params: { taskId: 'task-stream-1', message: { parts: [{ text: 'Summarize my day' }] } }
    }, hubClient, (f) => streamOut.push(f));
    assert.ok(streamOut.some((f) => f.method === 'TaskStatusUpdate' && f.params.status.state === 'working'));
    assert.ok(streamOut.some((f) => f.method === 'TaskArtifactUpdate'));
    const finalFrame = streamOut.find((f) => f.id === 'm1');
    assert.equal(finalFrame.result.status.state, 'completed');
    assert.equal(finalFrame.result.status.message.parts[0].text, 'Here is your Persona reply.');
  } finally {
    server.close();
  }
});
