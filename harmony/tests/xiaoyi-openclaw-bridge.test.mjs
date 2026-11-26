import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { createHash, createHmac } from 'node:crypto';
import {
  DEFAULT_OPENCLAW_WS_URL,
  OpenClawBridge,
  PersonaHubClient,
  computeReconnectDelayMs,
  formatPreflightReport,
  handleOpenClawRpc,
  isHubRecordingAllowed,
  loadBridgeConfig,
  parseCliArgs,
  runPreflightCheck,
  signOpenClawHeaders,
  unwrapOpenClawEnvelope
} from '../scripts/xiaoyi-openclaw-bridge.mjs';

/** Minimal zero-dependency RFC 6455 mock WebSocket server for end-to-end OpenClawBridge testing. */
function createMockWsServer() {
  const connections = [];
  const server = http.createServer((_req, res) => {
    res.statusCode = 426;
    res.end('Upgrade Required');
  });

  function encodeServerFrame(opcode, payloadBuf) {
    const len = payloadBuf.length;
    let header;
    if (len < 126) {
      header = Buffer.from([0x80 | opcode, len]);
    } else if (len < 65536) {
      header = Buffer.alloc(4);
      header[0] = 0x80 | opcode;
      header[1] = 126;
      header.writeUInt16BE(len, 2);
    } else {
      header = Buffer.alloc(10);
      header[0] = 0x80 | opcode;
      header[1] = 127;
      header.writeBigUInt64BE(BigInt(len), 2);
    }
    return Buffer.concat([header, payloadBuf]);
  }

  server.on('upgrade', (req, socket) => {
    const key = req.headers['sec-websocket-key'];
    const accept = createHash('sha1').update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`).digest('base64');
    socket.write(
      'HTTP/1.1 101 Switching Protocols\r\n' +
      'Upgrade: websocket\r\n' +
      'Connection: Upgrade\r\n' +
      `Sec-WebSocket-Accept: ${accept}\r\n\r\n`
    );

    const conn = {
      url: req.url,
      headers: req.headers,
      socket,
      received: [],
      waiters: [],
      sendJson(obj) {
        socket.write(encodeServerFrame(0x1, Buffer.from(JSON.stringify(obj), 'utf8')));
      },
      closeSocket() {
        try { socket.write(encodeServerFrame(0x8, Buffer.alloc(0))); } catch (_) {}
        socket.end();
      },
      waitForMessage(predicate, timeoutMs = 4000) {
        const existing = this.received.find(predicate);
        if (existing) return Promise.resolve(existing);
        return new Promise((resolve, reject) => {
          const timer = setTimeout(() => reject(new Error('Timed out waiting for WS message')), timeoutMs);
          this.waiters.push({
            predicate,
            resolve: (msg) => { clearTimeout(timer); resolve(msg); }
          });
        });
      }
    };

    let buf = Buffer.alloc(0);
    socket.on('data', (chunk) => {
      buf = Buffer.concat([buf, chunk]);
      while (buf.length >= 2) {
        const opcode = buf[0] & 0x0f;
        const masked = (buf[1] & 0x80) !== 0;
        let payloadLen = buf[1] & 0x7f;
        let offset = 2;
        if (payloadLen === 126) {
          if (buf.length < 4) return;
          payloadLen = buf.readUInt16BE(2);
          offset = 4;
        } else if (payloadLen === 127) {
          if (buf.length < 10) return;
          payloadLen = Number(buf.readBigUInt64BE(2));
          offset = 10;
        }
        const maskLen = masked ? 4 : 0;
        if (buf.length < offset + maskLen + payloadLen) return;
        const mask = masked ? buf.subarray(offset, offset + 4) : null;
        const raw = Buffer.from(buf.subarray(offset + maskLen, offset + maskLen + payloadLen));
        buf = buf.subarray(offset + maskLen + payloadLen);
        if (mask) {
          for (let i = 0; i < raw.length; i++) raw[i] ^= mask[i % 4];
        }
        if (opcode === 0x8) {
          if (socket.writable && !socket.writableEnded) {
            try { socket.write(encodeServerFrame(0x8, Buffer.alloc(0))); } catch (_) {}
            socket.end();
          }
          return;
        }
        if (opcode === 0x1) {
          const parsed = JSON.parse(raw.toString('utf8'));
          conn.received.push(parsed);
          for (let i = conn.waiters.length - 1; i >= 0; i--) {
            if (conn.waiters[i].predicate(parsed)) {
              const waiter = conn.waiters.splice(i, 1)[0];
              waiter.resolve(parsed);
            }
          }
        }
      }
    });

    connections.push(conn);
  });

  return {
    server,
    connections,
    async listen() {
      await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
      return `ws://127.0.0.1:${server.address().port}/openclaw/v1/ws/link`;
    },
    async waitForConnections(count, timeoutMs = 4000) {
      const deadline = Date.now() + timeoutMs;
      while (Date.now() < deadline) {
        if (connections.length >= count) return connections[count - 1];
        await new Promise((r) => setTimeout(r, 20));
      }
      throw new Error(`Timed out waiting for ${count} WS connections`);
    },
    async close() {
      for (const c of connections) {
        try { c.socket.destroy(); } catch (_) {}
      }
      await new Promise((resolve) => server.close(resolve));
    }
  };
}

function createMockHubServer({ recordingEnabled = true, rejectAuth = false, waterEnabled = true } = {}) {
  const requests = [];
  let messageSent = false;
  let holdWorking = false;
  let waterToday = 500;

  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      requests.push({
        method: req.method,
        url: req.url,
        auth: req.headers.authorization,
        manager: req.headers['x-persona-manager'] || '',
        body
      });
      res.setHeader('Content-Type', 'application/json');
      if (rejectAuth && req.url !== '/api/v1/health') {
        res.statusCode = 401;
        res.end(JSON.stringify({ error: 'unauthorized' }));
        return;
      }
      if (req.url === '/api/v1/health') {
        res.end(JSON.stringify({
          ok: true,
          hub_id: 'hub-test-1',
          hub_name: 'Studio Hub',
          version: '1.0.0',
          features: recordingEnabled ? ['modules', 'module.recording', 'module.voice'] : ['modules', 'module.voice']
        }));
      } else if (req.url === '/api/v1/machines') {
        res.end(JSON.stringify({
          machines: [
            { id: 'm1', name: 'Studio', online: true, trust: 'trusted' },
            { id: 'm2', name: 'Laptop', online: false, trust: 'trusted' }
          ]
        }));
      } else if (req.url === '/api/v1/sessions') {
        res.end(JSON.stringify({
          sessions: [
            { id: 'main-1', machine_id: 'm1', assistant: true, state: holdWorking ? 'working' : 'idle', status: holdWorking ? 'working' : 'idle' },
            { id: 's-ro', machine_id: 'm1', title: 'ReadOnly', assistant: false, read_only: true, state: 'idle', status: 'idle', updated_at: '2026-11-27T09:00:00Z' },
            { id: 's1', machine_id: 'm1', title: 'RepoWorker', assistant: false, state: 'working', status: 'working', updated_at: '2026-11-27T08:00:00Z' }
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
      } else if (req.url === '/api/v1/assistant/health/intake?space=health' && req.method === 'GET') {
        res.end(JSON.stringify({
          items: waterEnabled ? ['water', 'protein'] : ['protein'],
          water: { today: waterToday, goal: 2000 }
        }));
      } else if (req.url === '/api/v1/assistant/health/intake?space=health' && req.method === 'POST') {
        const parsed = JSON.parse(body);
        const amt = Number(parsed.amount || 250);
        waterToday += amt;
        res.end(JSON.stringify({
          entry: { id: parsed.id, kind: 'water', amount: amt },
          today: { water: waterToday, protein: 0 },
          zone: 'Asia/Shanghai'
        }));
      } else if (req.url.startsWith('/api/v1/kb-search')) {
        res.end(JSON.stringify({ hits: [{ path: 'docs/xiaoyi.md', line: 'OpenClaw setup' }] }));
      } else if (req.url === '/api/v1/assistant') {
        res.end(JSON.stringify({ main: 'main-1', main_machine_id: 'm1', conversations: [] }));
      } else if (req.url.includes('/interrupt') && req.method === 'POST') {
        holdWorking = false;
        res.end(JSON.stringify({ ok: true }));
      } else if (req.url.includes('/main-1/messages') && req.method === 'POST') {
        messageSent = true;
        res.end(JSON.stringify({ ok: true }));
      } else if (req.url.includes('/messages') && req.method === 'POST') {
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

  return {
    server,
    requests,
    setHoldWorking(v) { holdWorking = v; },
    resetMessageSent() { messageSent = false; },
    async listen() {
      await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
      return `http://127.0.0.1:${server.address().port}`;
    },
    async close() {
      await new Promise((resolve) => server.close(resolve));
    }
  };
}

test('signOpenClawHeaders produces real Huawei OpenClaw headers by default and supports legacy hag mode', () => {
  const huawei = signOpenClawHeaders({
    ak: 'test-ak',
    sk: 'test-sk-secret',
    agentId: 'agent-123',
    timestampMs: 1764100000000,
    traceId: 'trace-abc'
  });
  const expectedHuaweiSign = createHmac('sha256', 'test-sk-secret')
    .update('1764100000000', 'utf8')
    .digest('base64');
  assert.equal(huawei['x-access-key'], 'test-ak');
  assert.equal(huawei['x-ts'], '1764100000000');
  assert.equal(huawei['x-sign'], expectedHuaweiSign);
  assert.equal(huawei['x-agent-id'], 'agent-123');
  assert.equal(huawei['x-hag-trace-id'], 'trace-abc');

  const hag = signOpenClawHeaders({
    ak: 'test-ak',
    sk: 'test-sk-secret',
    agentId: 'agent-123',
    authMode: 'hag',
    timestampMs: 1764100000000,
    nonce: 'deadbeef0102'
  });
  const expectedHagSign = createHmac('sha256', 'test-sk-secret')
    .update('test-ak\nagent-123\n1764100000000\ndeadbeef0102', 'utf8')
    .digest('base64');
  assert.equal(hag['x-hag-ak'], 'test-ak');
  assert.equal(hag['x-hag-signature'], expectedHagSign);
});

test('computeReconnectDelayMs, parseCliArgs, isHubRecordingAllowed, and loadBridgeConfig validate cleanly', () => {
  assert.equal(computeReconnectDelayMs(0), 1000);
  assert.equal(computeReconnectDelayMs(2), 4000);
  assert.equal(computeReconnectDelayMs(10), 30000);

  assert.deepEqual(parseCliArgs(['--check', '--check-ws', '--config', '/tmp/cfg.json']), {
    check: true,
    checkWs: true,
    help: false,
    configFile: '/tmp/cfg.json'
  });
  assert.equal(isHubRecordingAllowed(['modules', 'module.recording']), true);
  assert.equal(isHubRecordingAllowed(['modules', 'module.voice']), false);
  assert.equal(isHubRecordingAllowed(['file_upload']), true);

  assert.throws(() => loadBridgeConfig({}), /Missing required configuration/);
  const cfg = loadBridgeConfig({
    XIAOYI_AK: 'ak',
    XIAOYI_SK: 'sk',
    XIAOYI_AGENT_ID: 'agent-1',
    PERSONA_HUB_URL: 'https://hub.example.com/',
    PERSONA_HUB_TOKEN: 'tok-1'
  });
  assert.equal(cfg.wsUrl, DEFAULT_OPENCLAW_WS_URL);
  assert.equal(cfg.authMode, 'huawei');
  assert.equal(cfg.hubUrl, 'https://hub.example.com');
  assert.throws(() => loadBridgeConfig({
    XIAOYI_AK: 'ak',
    XIAOYI_SK: 'sk',
    XIAOYI_AGENT_ID: 'agent-1',
    XIAOYI_WS_URL: 'http://bad',
    PERSONA_HUB_URL: 'https://hub.example.com',
    PERSONA_HUB_TOKEN: 'tok-1'
  }), /ws:\/\/ or wss:\/\//);

  const ignored = unwrapOpenClawEnvelope({ msgType: 'heartbeat', timestamp: 1 });
  assert.equal(ignored.ignore, true);
});

test('handleOpenClawRpc handles bare JSON-RPC status, todos, water, agent task, kb search, streaming ask, approval refusal, and recording module off', async () => {
  const hub = createMockHubServer({ recordingEnabled: false });
  const hubUrl = await hub.listen();
  const hubClient = new PersonaHubClient({ hubUrl, hubToken: 'synthetic-token', manager: 'work', askTimeoutMs: 4000 });

  try {
    const pingOut = [];
    await handleOpenClawRpc({ jsonrpc: '2.0', id: 'p1', method: 'ping' }, hubClient, (f) => pingOut.push(f));
    assert.equal(pingOut[0].result.pong, true);

    const approvalOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'a1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Approve pending' }] } }
    }, hubClient, (f) => approvalOut.push(f));
    assert.equal(approvalOut[0].result.status.state, 'input-required');
    assert.ok(approvalOut[0].result.status.message.parts[0].text.includes('unlocked phone'));
    assert.ok(!hub.requests.some((r) => r.url.includes('/resolve')));

    const recOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'r1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Start meeting recording' }] } }
    }, hubClient, (f) => recOut.push(f));
    assert.equal(recOut[0].result.status.state, 'rejected');
    assert.equal(recOut[0].result.status.message.parts[0].text, 'Meeting recording is off on this hub.');

    const statusOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 's1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Check status' }] } }
    }, hubClient, (f) => statusOut.push(f));
    assert.ok(statusOut[0].result.status.message.parts[0].text.includes('1 working, 1 need you'));

    const todoOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 't1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: '新建待办：验证小艺通道' }] } }
    }, hubClient, (f) => todoOut.push(f));
    assert.ok(todoOut[0].result.status.message.parts[0].text.includes('验证小艺通道'));

    // Log water (always omits X-Persona-Manager, targeting default manager)
    const waterOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'w1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Log water 350 ml' }] } }
    }, hubClient, (f) => waterOut.push(f));
    assert.equal(waterOut[0].result.status.message.parts[0].text, 'Logged 350 ml. Today: 850 / 2000 ml.');
    const waterPost = hub.requests.find((r) => r.url.includes('/health/intake') && r.method === 'POST');
    assert.equal(waterPost.manager, '');

    // Send agent task (skips read-only session `s-ro` and dispatches to live `s1`)
    const agentOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'ag1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Send to agent: on Studio: run all tests' }] } }
    }, hubClient, (f) => agentOut.push(f));
    assert.equal(agentOut[0].result.status.message.parts[0].text, 'Sent to RepoWorker (Studio).');
    assert.ok(hub.requests.some((r) => r.url === '/api/v1/sessions/m1/s1/messages' && r.method === 'POST'));
    assert.ok(!hub.requests.some((r) => r.url.includes('/s-ro/')));

    const kbOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'k1',
      method: 'SendMessage',
      params: { message: { parts: [{ text: 'Search knowledge: OpenClaw' }] } }
    }, hubClient, (f) => kbOut.push(f));
    assert.ok(kbOut[0].result.status.message.parts[0].text.includes('docs/xiaoyi.md'));

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
    await hub.close();
  }

  // Verify water-not-enabled refusal cleanly surfaces guidance error
  const noWaterHub = createMockHubServer({ waterEnabled: false });
  const noWaterUrl = await noWaterHub.listen();
  try {
    const noWaterClient = new PersonaHubClient({ hubUrl: noWaterUrl, hubToken: 'tok', askTimeoutMs: 2000 });
    const errOut = [];
    await handleOpenClawRpc({
      jsonrpc: '2.0',
      id: 'w-off',
      method: 'SendMessage',
      params: { message: { parts: [{ text: '喝水' }] } }
    }, noWaterClient, (f) => errOut.push(f));
    assert.ok(errOut[0].error.message.includes('Water tracking'));
  } finally {
    await noWaterHub.close();
  }
});

test('OpenClawBridge completes real local WebSocket round-trip (auth headers, clawd_bot_init, heartbeat, enveloped streaming, clearContext, cancel, reconnect)', async () => {
  const hub = createMockHubServer({ recordingEnabled: false });
  const wsServer = createMockWsServer();
  const hubUrl = await hub.listen();
  const wsUrl = await wsServer.listen();

  const cfg = {
    wsUrl,
    authMode: 'huawei',
    ak: 'demo-ak-value',
    sk: 'demo-sk-secret-never-logged',
    agentId: 'agent-e2e-1',
    hubUrl,
    hubToken: 'demo-hub-token-never-logged',
    manager: '',
    heartbeatMs: 1200,
    askTimeoutMs: 4000
  };

  const silentLogger = { info() {}, warn() {}, error() {} };
  const bridge = new OpenClawBridge(cfg, { logger: silentLogger });
  bridge.start();

  try {
    // 1. Verify real HTTP Upgrade auth headers & clawd_bot_init
    const conn1 = await wsServer.waitForConnections(1);
    assert.equal(conn1.headers['x-access-key'], 'demo-ak-value');
    assert.equal(conn1.headers['x-agent-id'], 'agent-e2e-1');
    assert.ok(conn1.headers['x-ts']);
    const expectedSign = createHmac('sha256', cfg.sk).update(conn1.headers['x-ts'], 'utf8').digest('base64');
    assert.equal(conn1.headers['x-sign'], expectedSign);
    assert.ok(conn1.headers['x-hag-trace-id']);

    const initMsg = await conn1.waitForMessage((m) => m.msgType === 'clawd_bot_init');
    assert.equal(initMsg.agentId, 'agent-e2e-1');

    // 2. Verify enveloped Huawei OpenClaw streaming request (`message/stream`)
    conn1.sendJson({
      msgType: 'agent_request',
      agentId: 'agent-e2e-1',
      sessionId: 'sess-hw-1',
      taskId: 'task-hw-1',
      msgDetail: JSON.stringify({
        jsonrpc: '2.0',
        id: 'rpc-1',
        method: 'message/stream',
        params: { id: 'task-hw-1', sessionId: 'sess-hw-1', message: { parts: [{ text: 'Summarize my notes' }] } }
      })
    });

    const finalStreamEnvelope = await conn1.waitForMessage((m) => {
      if (m.msgType !== 'agent_response' || m.taskId !== 'task-hw-1') return false;
      const detail = JSON.parse(m.msgDetail);
      return detail.result?.kind === 'status-update' && detail.result?.final === true;
    });
    assert.equal(finalStreamEnvelope.sessionId, 'sess-hw-1');
    const finalDetail = JSON.parse(finalStreamEnvelope.msgDetail);
    assert.equal(finalDetail.result.status.state, 'completed');
    assert.equal(finalDetail.result.status.message.parts[0].text, 'Here is your Persona reply.');
    assert.ok(conn1.received.some((m) => m.msgType === 'agent_response' && JSON.parse(m.msgDetail).result?.kind === 'artifact-update'));

    // 3. Verify `clearContext` clears the cached session mapping
    assert.ok(bridge.sessionContexts.has('sess-hw-1'));
    conn1.sendJson({
      msgType: 'agent_request',
      agentId: 'agent-e2e-1',
      sessionId: 'sess-hw-1',
      taskId: 'task-clear',
      msgDetail: JSON.stringify({ jsonrpc: '2.0', id: 'rpc-clear', method: 'clearContext', params: {} })
    });
    const clearedEnv = await conn1.waitForMessage((m) => m.msgType === 'agent_response' && JSON.parse(m.msgDetail).id === 'rpc-clear');
    assert.equal(JSON.parse(clearedEnv.msgDetail).result.cleared, true);
    assert.equal(bridge.sessionContexts.has('sess-hw-1'), false);

    // 4. Verify `tasks/cancel` interrupts an active streaming turn on the Hub
    hub.resetMessageSent();
    hub.setHoldWorking(true);
    conn1.sendJson({
      msgType: 'agent_request',
      agentId: 'agent-e2e-1',
      sessionId: 'sess-hw-2',
      taskId: 'task-cancel-1',
      msgDetail: JSON.stringify({
        jsonrpc: '2.0',
        id: 'rpc-run',
        method: 'message/stream',
        params: { id: 'task-cancel-1', message: { parts: [{ text: 'Long running task' }] } }
      })
    });
    await conn1.waitForMessage((m) => m.msgType === 'agent_response' && m.taskId === 'task-cancel-1');
    await new Promise((r) => setTimeout(r, 40));
    conn1.sendJson({
      msgType: 'agent_request',
      agentId: 'agent-e2e-1',
      sessionId: 'sess-hw-2',
      taskId: 'task-cancel-1',
      msgDetail: JSON.stringify({ jsonrpc: '2.0', id: 'rpc-cancel', method: 'tasks/cancel', params: { id: 'task-cancel-1' } })
    });
    const cancelEnv = await conn1.waitForMessage((m) => m.msgType === 'agent_response' && JSON.parse(m.msgDetail).id === 'rpc-cancel');
    assert.equal(JSON.parse(cancelEnv.msgDetail).result.status.state, 'canceled');
    assert.ok(hub.requests.some((r) => r.url.includes('/interrupt')));

    // 5. Verify periodic heartbeat frame arrives
    const hb = await conn1.waitForMessage((m) => m.msgType === 'heartbeat', 2500);
    assert.equal(hb.agentId, 'agent-e2e-1');

    // 6. Verify automatic reconnect when server drops the socket
    conn1.closeSocket();
    const conn2 = await wsServer.waitForConnections(2, 3000);
    const initMsg2 = await conn2.waitForMessage((m) => m.msgType === 'clawd_bot_init');
    assert.equal(initMsg2.agentId, 'agent-e2e-1');
  } finally {
    bridge.stop();
    await wsServer.close();
    await hub.close();
  }
});

test('runPreflightCheck validates Hub + live WS handshake, detects auth failures, and never leaks secrets', async () => {
  const hub = createMockHubServer({ recordingEnabled: false });
  const wsServer = createMockWsServer();
  const hubUrl = await hub.listen();
  const wsUrl = await wsServer.listen();

  const cfg = {
    wsUrl,
    authMode: 'huawei',
    ak: 'ak-preflight',
    sk: 'super-secret-sk-must-not-print',
    agentId: 'agent-preflight',
    hubUrl,
    hubToken: 'super-secret-hub-token-must-not-print',
    manager: ''
  };

  try {
    const okReport = await runPreflightCheck(cfg, { checkWs: true });
    assert.equal(okReport.ok, true);
    assert.equal(okReport.checks.hubHealth.ok, true);
    assert.equal(okReport.checks.hubAuth.ok, true);
    assert.equal(okReport.checks.recordingModule.allowed, false);
    assert.equal(okReport.checks.wsHandshake.ok, true);

    const formatted = formatPreflightReport(okReport);
    assert.ok(formatted.includes('PASS'));
    assert.ok(!formatted.includes(cfg.sk), 'preflight report must never contain SK');
    assert.ok(!formatted.includes(cfg.hubToken), 'preflight report must never contain hub token');
  } finally {
    await wsServer.close();
    await hub.close();
  }

  const badAuthHub = createMockHubServer({ rejectAuth: true });
  const badUrl = await badAuthHub.listen();
  try {
    const failReport = await runPreflightCheck({ ...cfg, hubUrl: badUrl }, { checkWs: false });
    assert.equal(failReport.ok, false);
    assert.equal(failReport.checks.hubAuth.ok, false);
    assert.ok(failReport.checks.hubAuth.detail.includes('401'));
  } finally {
    await badAuthHub.close();
  }
});
