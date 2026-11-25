#!/usr/bin/env node
/**
 * Standalone, zero-dependency XiaoYi OpenClaw Outbound WebSocket Bridge for Persona Hub.
 *
 * Allows personal Huawei developer accounts (with XiaoYi Open Platform OpenClaw mode + test whitelist)
 * to connect a Persona Hub to the phone's XiaoYi assistant immediately over outbound WSS.
 *
 * Configuration is supplied strictly via environment variables or a local git-ignored JSON file
 * (XIAOYI_CONFIG_FILE) — never commit credentials, AK/SK, agent IDs, or Hub tokens.
 *
 * Required config:
 *   XIAOYI_AK         XiaoYi Open Platform Access Key
 *   XIAOYI_SK         XiaoYi Open Platform Secret Key
 *   XIAOYI_AGENT_ID   XiaoYi OpenClaw Agent ID
 *   PERSONA_HUB_URL   Persona Hub HTTPS/HTTP base URL (e.g. https://hub.example.com)
 *   PERSONA_HUB_TOKEN Persona Hub device/API bearer token
 *
 * Optional config:
 *   XIAOYI_WS_URL     Default: wss://hag.cloud.huawei.com/openclaw/v1/ws/link
 *   PERSONA_MANAGER   Optional Persona manager ID header
 *   XIAOYI_HEARTBEAT_MS  Default: 25000
 *   XIAOYI_ASK_TIMEOUT_MS Default: 24000
 */

import { createHmac, randomBytes, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';

export const DEFAULT_OPENCLAW_WS_URL = 'wss://hag.cloud.huawei.com/openclaw/v1/ws/link';
export const DEFAULT_HEARTBEAT_MS = 25000;
export const DEFAULT_ASK_TIMEOUT_MS = 24000;
export const MAX_PROMPT_CHARS = 8000;

export function loadBridgeConfig(env = process.env) {
  let fileCfg = {};
  if (env.XIAOYI_CONFIG_FILE) {
    const raw = readFileSync(env.XIAOYI_CONFIG_FILE, 'utf8');
    fileCfg = JSON.parse(raw);
  }
  const pick = (envKey, fileKey, fallback = '') => {
    const val = env[envKey] !== undefined && env[envKey] !== '' ? env[envKey] : fileCfg[fileKey];
    return typeof val === 'string' ? val.trim() : fallback;
  };
  const cfg = {
    wsUrl: pick('XIAOYI_WS_URL', 'wsUrl', DEFAULT_OPENCLAW_WS_URL),
    ak: pick('XIAOYI_AK', 'ak'),
    sk: pick('XIAOYI_SK', 'sk'),
    agentId: pick('XIAOYI_AGENT_ID', 'agentId'),
    hubUrl: pick('PERSONA_HUB_URL', 'hubUrl').replace(/\/+$/, ''),
    hubToken: pick('PERSONA_HUB_TOKEN', 'hubToken'),
    manager: pick('PERSONA_MANAGER', 'manager'),
    heartbeatMs: Number(env.XIAOYI_HEARTBEAT_MS || fileCfg.heartbeatMs || DEFAULT_HEARTBEAT_MS),
    askTimeoutMs: Number(env.XIAOYI_ASK_TIMEOUT_MS || fileCfg.askTimeoutMs || DEFAULT_ASK_TIMEOUT_MS)
  };
  const missing = [];
  if (!cfg.ak) missing.push('XIAOYI_AK');
  if (!cfg.sk) missing.push('XIAOYI_SK');
  if (!cfg.agentId) missing.push('XIAOYI_AGENT_ID');
  if (!cfg.hubUrl) missing.push('PERSONA_HUB_URL');
  if (!cfg.hubToken) missing.push('PERSONA_HUB_TOKEN');
  if (missing.length > 0) {
    throw new Error(`Missing required configuration: ${missing.join(', ')}`);
  }
  if (!/^wss?:\/\//i.test(cfg.wsUrl)) {
    throw new Error('XIAOYI_WS_URL must use ws:// or wss://');
  }
  if (!/^https?:\/\//i.test(cfg.hubUrl)) {
    throw new Error('PERSONA_HUB_URL must use http:// or https://');
  }
  return cfg;
}

/** Build HMAC-SHA256 authentication headers for XiaoYi OpenClaw outbound WebSocket handshake. */
export function signOpenClawHeaders({ ak, sk, agentId, timestampMs = Date.now(), nonce = randomBytes(12).toString('hex') }) {
  const ts = String(Math.floor(timestampMs));
  const canonical = `${ak}\n${agentId}\n${ts}\n${nonce}`;
  const signature = createHmac('sha256', sk).update(canonical, 'utf8').digest('base64');
  return {
    'x-hag-ak': ak,
    'x-hag-agent-id': agentId,
    'x-hag-timestamp': ts,
    'x-hag-nonce': nonce,
    'x-hag-signature': signature
  };
}

/** Exponential backoff delay: 1s, 2s, 4s, 8s, 16s, capped at 30s. */
export function computeReconnectDelayMs(attempt) {
  const safe = Math.max(0, Math.floor(Number(attempt) || 0));
  return Math.min(30000, 1000 * Math.pow(2, Math.min(5, safe)));
}

function sanitizePrompt(raw) {
  const cleaned = String(raw || '').replace(/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/g, '').trim();
  const chars = Array.from(cleaned);
  return chars.length <= MAX_PROMPT_CHARS ? cleaned : chars.slice(0, MAX_PROMPT_CHARS).join('');
}

function extractMessageText(params) {
  const msg = params?.message || params;
  const parts = Array.isArray(msg?.parts) ? msg.parts : [];
  const chunks = [];
  for (const part of parts) {
    if (typeof part?.text === 'string' && part.text.trim()) chunks.push(part.text.trim());
    if (typeof part?.url === 'string' && /^https?:\/\//i.test(part.url.trim())) chunks.push(part.url.trim());
  }
  if (chunks.length === 0 && typeof params?.text === 'string') chunks.push(params.text);
  if (chunks.length === 0 && typeof params?.query === 'string') chunks.push(params.query);
  return sanitizePrompt(chunks.join('\n'));
}

function classifyRelayCommand(text) {
  // Security boundary: NEVER execute approvals silently over the cloud OpenClaw relay.
  if (/^(?:approve|allow\s+first|批准|通过待确认|审批|確認待ちを承認)/i.test(text)) {
    return { kind: 'refuse_approval', arg: '' };
  }
  const todoMatch = /^(?:add\s+(?:a\s+)?to-?do|new\s+to-?do|新建待办|记个待办|待办|ToDoを追加)\s*[:：]?\s*(.+)$/i.exec(text);
  if (todoMatch && todoMatch[1].trim()) {
    return { kind: 'add_todo', arg: todoMatch[1].trim() };
  }
  const kbMatch = /^(?:search\s+(?:persona\s+)?knowledge|搜索知识库|查知识库|ナレッジ検索)\s*[:：]?\s*(.+)$/i.exec(text);
  if (kbMatch && kbMatch[1].trim()) {
    return { kind: 'search_kb', arg: kbMatch[1].trim() };
  }
  if (/^(?:persona\s+status|check\s+status|status|查看状态|任务进度|待确认|状況確認)/i.test(text)) {
    return { kind: 'status', arg: '' };
  }
  return { kind: 'ask', arg: text };
}

export class PersonaHubClient {
  constructor(cfg, fetchImpl = globalThis.fetch) {
    this.hubUrl = cfg.hubUrl;
    this.hubToken = cfg.hubToken;
    this.manager = cfg.manager || '';
    this.askTimeoutMs = cfg.askTimeoutMs || DEFAULT_ASK_TIMEOUT_MS;
    this.fetch = fetchImpl;
  }

  async request(path, method = 'GET', body = undefined, opId = '') {
    const headers = {
      Authorization: `Bearer ${this.hubToken}`,
      'X-Persona-Client': 'harmony-xiaoyi-openclaw/1.0.0',
      'X-Persona-Source': 'xiaoyi-openclaw'
    };
    if (this.manager) headers['X-Persona-Manager'] = this.manager;
    if (opId) headers['X-Persona-Op-Id'] = opId;
    if (body !== undefined) headers['Content-Type'] = 'application/json';

    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 15000);
    try {
      const res = await this.fetch(`${this.hubUrl}${path}`, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal: controller.signal
      });
      const text = await res.text();
      if (!res.ok) {
        const err = new Error(`Hub HTTP ${res.status}`);
        err.status = res.status;
        err.body = text.slice(0, 512);
        throw err;
      }
      return text.trim() ? JSON.parse(text) : {};
    } finally {
      clearTimeout(timer);
    }
  }

  async checkStatus() {
    const [sessionsRes, interRes] = await Promise.all([
      this.request('/api/v1/sessions'),
      this.request('/api/v1/interactions')
    ]);
    const sessions = Array.isArray(sessionsRes.sessions) ? sessionsRes.sessions : [];
    const interactions = Array.isArray(interRes.interactions) ? interRes.interactions : [];
    const working = sessions.filter((s) => !s.assistant && (s.state === 'working' || s.status === 'working')).length;
    const open = interactions.filter((i) => {
      const inner = i.interaction || i;
      const st = String(i.status || inner.status || 'open').toLowerCase();
      return (st === 'open' || st === 'pending') && !inner.quiet && !inner.muted && !(inner.delegating && inner.kind !== 'question');
    });
    if (working === 0 && open.length === 0) {
      return 'All clear. Nothing is running or needs your attention right now.';
    }
    const parts = [];
    if (working > 0) parts.push(`${working} working`);
    if (open.length > 0) parts.push(`${open.length} need you`);
    const top = open[0] ? (open[0].interaction || open[0]) : null;
    const detail = top ? ` First pending: ${top.summary || top.tool_name || top.kind}. Confirm in Persona App.` : '';
    return `${parts.join(', ')}.${detail}`;
  }

  async addTodo(title) {
    const opId = randomUUID();
    const res = await this.request('/api/v1/todos', 'POST', { title }, opId);
    const savedTitle = res?.todo?.title || title;
    return `To-do saved: ${savedTitle}`;
  }

  async searchKnowledge(query) {
    const res = await this.request(`/api/v1/kb-search?q=${encodeURIComponent(query)}&messages=0`);
    const hits = Array.isArray(res.hits) ? res.hits : [];
    if (hits.length === 0) return `No knowledge matches found for "${query}".`;
    return hits.slice(0, 5).map((h) => `${h.path}${h.line ? `: ${h.line}` : ''}`).join('\n');
  }

  async interrupt(machineId, sessionId) {
    if (!machineId || !sessionId) return;
    try {
      await this.request(`/api/v1/sessions/${encodeURIComponent(machineId)}/${encodeURIComponent(sessionId)}/interrupt`, 'POST', {});
    } catch (_) {
      // Best-effort cancellation.
    }
  }

  async askPersona(question, { onChunk, isCancelled, onTarget } = {}) {
    const assistantRes = await this.request('/api/v1/assistant');
    let sessionId = assistantRes.main || '';
    let machineId = assistantRes.main_machine_id || assistantRes.machine_id || '';
    if (!machineId && sessionId && Array.isArray(assistantRes.conversations)) {
      const mainRow = assistantRes.conversations.find((c) => (c.id || c.session_id) === sessionId);
      if (mainRow) machineId = mainRow.machine_id || mainRow.machine || '';
    }
    if (!sessionId || !machineId) {
      const created = await this.request('/api/v1/assistant/conversations', 'POST', { main: true, prompt: '' }, randomUUID());
      sessionId = created?.session?.id || created?.session?.session_id || '';
      machineId = created?.session?.machine_id || created?.session?.machine || '';
    }
    if (!sessionId || !machineId) {
      throw new Error('No main Persona conversation available.');
    }
    if (onTarget) onTarget({ machineId, sessionId });

    const baseItems = `/api/v1/sessions/${encodeURIComponent(machineId)}/${encodeURIComponent(sessionId)}/items`;
    let baselineSeq = 0;
    try {
      const initial = await this.request(`${baseItems}?lines=20&fold_work=1&lazy_images=1`);
      for (const item of Array.isArray(initial.items) ? initial.items : []) {
        if (typeof item.seq === 'number' && item.seq > baselineSeq) baselineSeq = item.seq;
      }
    } catch (_) {
      // Fresh conversation.
    }

    const opId = randomUUID();
    await this.request(
      `/api/v1/sessions/${encodeURIComponent(machineId)}/${encodeURIComponent(sessionId)}/messages`,
      'POST',
      { text: question, client_msg_id: opId },
      opId
    );

    const deadline = Date.now() + this.askTimeoutMs;
    let emitted = '';
    while (Date.now() < deadline) {
      if (isCancelled && isCancelled()) {
        await this.interrupt(machineId, sessionId);
        return { text: '', canceled: true };
      }
      await new Promise((r) => setTimeout(r, 250));
      if (isCancelled && isCancelled()) {
        await this.interrupt(machineId, sessionId);
        return { text: '', canceled: true };
      }
      const polled = await this.request(`${baseItems}?after_seq=${baselineSeq}&lines=80&fold_work=1&lazy_images=1`);
      const items = Array.isArray(polled.items) ? polled.items : [];
      const texts = items
        .filter((i) => i.seq > baselineSeq && i.kind === 'text' && i.role === 'assistant' && typeof i.text === 'string' && i.text.trim())
        .map((i) => i.text.trim());
      const combined = texts.join('\n\n');
      if (combined.length > emitted.length) {
        const delta = combined.slice(emitted.length);
        emitted = combined;
        if (onChunk) onChunk(delta, false);
      }
      const sessionsRes = await this.request('/api/v1/sessions');
      const live = (Array.isArray(sessionsRes.sessions) ? sessionsRes.sessions : []).find(
        (s) => (s.machine_id || s.machine) === machineId && (s.session_id || s.id) === sessionId
      );
      const stillWorking = live ? (live.state === 'working' || live.status === 'working') : false;
      if (!stillWorking && emitted) {
        if (onChunk) onChunk('', true);
        return { text: emitted, canceled: false };
      }
    }
    return { text: emitted || 'Sent to Persona. It is still thinking — open Persona to view progress.', canceled: false };
  }
}

/** Handle one incoming JSON-RPC 2.0 frame from XiaoYi OpenClaw WebSocket. */
export async function handleOpenClawRpc(frame, hubClient, sendFrame, activeTasks = new Map()) {
  const id = frame?.id ?? null;
  const method = String(frame?.method || '');
  const params = frame?.params || {};

  if (method === 'ping' || method === 'heartbeat') {
    sendFrame({ jsonrpc: '2.0', id, result: { pong: true, timestamp: Date.now() } });
    return;
  }

  if (method === 'CancelTask' || method === 'tasks/cancel') {
    const taskId = String(params.taskId || params.id || '');
    const active = activeTasks.get(taskId);
    if (active) {
      active.cancelled = true;
      activeTasks.delete(taskId);
      if (active.machineId && active.sessionId) {
        await hubClient.interrupt(active.machineId, active.sessionId);
      }
    }
    sendFrame({ jsonrpc: '2.0', id, result: { id: taskId, status: { state: 'canceled' } } });
    return;
  }

  if (method !== 'SendMessage' && method !== 'SendStreamingMessage' && method !== 'message/send' && method !== 'message/stream') {
    sendFrame({ jsonrpc: '2.0', id, error: { code: -32601, message: `Unsupported method: ${method}` } });
    return;
  }

  const streaming = method === 'SendStreamingMessage' || method === 'message/stream';
  const taskId = String(params.taskId || params.id || randomUUID());
  const text = extractMessageText(params);
  if (!text) {
    sendFrame({ jsonrpc: '2.0', id, error: { code: -32602, message: 'Empty message content' } });
    return;
  }

  const stateHandle = { cancelled: false, machineId: '', sessionId: '' };
  activeTasks.set(taskId, stateHandle);

  if (streaming) {
    sendFrame({ jsonrpc: '2.0', method: 'TaskStatusUpdate', params: { taskId, status: { state: 'working' } } });
  }

  try {
    const cmd = classifyRelayCommand(text);
    let replyText = '';
    let finalState = 'completed';

    if (cmd.kind === 'refuse_approval') {
      replyText = 'For your security, approvals cannot be confirmed over cloud relay. Open the Persona app on your unlocked phone to review and approve.';
      finalState = 'input-required';
    } else if (cmd.kind === 'status') {
      replyText = await hubClient.checkStatus();
    } else if (cmd.kind === 'add_todo') {
      replyText = await hubClient.addTodo(cmd.arg);
    } else if (cmd.kind === 'search_kb') {
      replyText = await hubClient.searchKnowledge(cmd.arg);
    } else {
      let chunkIndex = 0;
      const res = await hubClient.askPersona(cmd.arg, {
        isCancelled: () => stateHandle.cancelled,
        onTarget: (target) => {
          stateHandle.machineId = target.machineId;
          stateHandle.sessionId = target.sessionId;
        },
        onChunk: (delta, lastChunk) => {
          if (!streaming || stateHandle.cancelled) return;
          sendFrame({
            jsonrpc: '2.0',
            method: 'TaskArtifactUpdate',
            params: {
              taskId,
              artifact: {
                artifactId: `${taskId}:answer`,
                parts: [{ text: delta, mediaType: 'text/plain' }],
                append: chunkIndex > 0,
                lastChunk
              }
            }
          });
          chunkIndex++;
        }
      });
      if (res.canceled || stateHandle.cancelled) return;
      replyText = res.text;
    }

    sendFrame({
      jsonrpc: '2.0',
      id,
      result: {
        id: taskId,
        status: {
          state: finalState,
          message: { role: 'agent', parts: [{ text: replyText, mediaType: 'text/plain' }] }
        },
        artifacts: [{ artifactId: `${taskId}:result`, parts: [{ text: replyText, mediaType: 'text/plain' }] }]
      }
    });
  } catch (error) {
    if (stateHandle.cancelled) return;
    sendFrame({
      jsonrpc: '2.0',
      id,
      error: { code: -32000, message: error?.message || 'Hub request failed' }
    });
  } finally {
    activeTasks.delete(taskId);
  }
}

export class OpenClawBridge {
  constructor(cfg, { WebSocketImpl = globalThis.WebSocket, fetchImpl = globalThis.fetch, logger = console } = {}) {
    this.cfg = cfg;
    this.WebSocketImpl = WebSocketImpl;
    this.hub = new PersonaHubClient(cfg, fetchImpl);
    this.logger = logger;
    this.ws = null;
    this.stopped = false;
    this.attempt = 0;
    this.reconnectTimer = null;
    this.heartbeatTimer = null;
    this.activeTasks = new Map();
  }

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    if (this.reconnectTimer) clearTimeout(this.reconnectTimer);
    if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
    this.reconnectTimer = null;
    this.heartbeatTimer = null;
    for (const handle of this.activeTasks.values()) handle.cancelled = true;
    this.activeTasks.clear();
    if (this.ws) {
      try { this.ws.close(); } catch (_) {}
      this.ws = null;
    }
  }

  connect() {
    if (this.stopped) return;
    const headers = signOpenClawHeaders(this.cfg);
    const wsUrl = new URL(this.cfg.wsUrl);
    wsUrl.searchParams.set('agentId', this.cfg.agentId);
    const ws = new this.WebSocketImpl(wsUrl.toString(), { headers });
    this.ws = ws;

    ws.addEventListener('open', () => {
      this.attempt = 0;
      this.logger.info('[xiaoyi-openclaw] connected to outbound gateway');
      ws.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'openclaw.hello',
        params: { agentId: this.cfg.agentId, auth: headers }
      }));
      if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = setInterval(() => {
        if (this.ws === ws && ws.readyState === 1) {
          ws.send(JSON.stringify({ jsonrpc: '2.0', method: 'heartbeat', params: { ts: Date.now() } }));
        }
      }, this.cfg.heartbeatMs);
    });

    ws.addEventListener('message', (event) => {
      let frame;
      try {
        frame = JSON.parse(String(event.data || ''));
      } catch (_) {
        return;
      }
      handleOpenClawRpc(frame, this.hub, (reply) => {
        if (this.ws === ws && ws.readyState === 1) {
          ws.send(JSON.stringify(reply));
        }
      }, this.activeTasks).catch((err) => {
        this.logger.error('[xiaoyi-openclaw] rpc error:', err?.message || err);
      });
    });

    ws.addEventListener('close', () => {
      if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
      if (this.stopped) return;
      const delay = computeReconnectDelayMs(this.attempt++);
      this.logger.warn(`[xiaoyi-openclaw] disconnected, reconnecting in ${delay}ms`);
      this.reconnectTimer = setTimeout(() => this.connect(), delay);
    });

    ws.addEventListener('error', (err) => {
      this.logger.error('[xiaoyi-openclaw] socket error:', err?.message || 'connection error');
    });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const cfg = loadBridgeConfig(process.env);
  const bridge = new OpenClawBridge(cfg);
  process.on('SIGINT', () => { bridge.stop(); process.exit(0); });
  process.on('SIGTERM', () => { bridge.stop(); process.exit(0); });
  bridge.start();
}
