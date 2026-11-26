#!/usr/bin/env node
/**
 * Standalone, zero-dependency XiaoYi OpenClaw Outbound WebSocket Bridge for Persona Hub.
 *
 * Allows personal Huawei developer accounts (with XiaoYi Open Platform OpenClaw mode + test whitelist)
 * to connect a Persona Hub to the phone's XiaoYi assistant immediately over outbound WSS.
 *
 * Configuration is supplied strictly via environment variables or a local git-ignored JSON file
 * (--config <path> or XIAOYI_CONFIG_FILE) — never commit credentials, AK/SK, agent IDs, or Hub tokens.
 *
 * Usage:
 *   node harmony/scripts/xiaoyi-openclaw-bridge.mjs [--config /path/to/config.json]
 *   node harmony/scripts/xiaoyi-openclaw-bridge.mjs --check [--check-ws] [--config /path/to/config.json]
 *
 * Required config:
 *   XIAOYI_AK         XiaoYi Open Platform Access Key
 *   XIAOYI_SK         XiaoYi Open Platform Secret Key
 *   XIAOYI_AGENT_ID   XiaoYi OpenClaw Agent ID
 *   PERSONA_HUB_URL   Persona Hub HTTPS/HTTP base URL (e.g. https://hub.example.com)
 *   PERSONA_HUB_TOKEN Persona Hub device/API bearer token
 *
 * Optional config:
 *   XIAOYI_WS_URL        Default: wss://hag.cloud.huawei.com/openclaw/v1/ws/link
 *   XIAOYI_AUTH_MODE     "huawei" (default: x-access-key/x-ts/x-sign/x-agent-id) or "hag"
 *   PERSONA_MANAGER      Optional Persona manager ID header
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
const MAX_SESSION_CONTEXTS = 64;
const RECORDING_OFF_MESSAGE = 'Meeting recording is off on this hub.';

export function parseCliArgs(argv = process.argv.slice(2)) {
  const opts = { check: false, checkWs: false, help: false, configFile: '' };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--check') opts.check = true;
    else if (arg === '--check-ws') { opts.check = true; opts.checkWs = true; }
    else if (arg === '--help' || arg === '-h') opts.help = true;
    else if (arg === '--config' && i + 1 < argv.length) { opts.configFile = argv[++i]; }
    else if (arg.startsWith('--config=')) { opts.configFile = arg.slice('--config='.length); }
  }
  return opts;
}

export function loadBridgeConfig(env = process.env, configFileOverride = '') {
  let fileCfg = {};
  const configPath = configFileOverride || env.XIAOYI_CONFIG_FILE || '';
  if (configPath) {
    const raw = readFileSync(configPath, 'utf8');
    fileCfg = JSON.parse(raw);
  }
  const pick = (envKey, fileKey, fallback = '') => {
    const val = env[envKey] !== undefined && env[envKey] !== '' ? env[envKey] : fileCfg[fileKey];
    return typeof val === 'string' ? val.trim() : fallback;
  };
  const authMode = pick('XIAOYI_AUTH_MODE', 'authMode', 'huawei').toLowerCase();
  const cfg = {
    wsUrl: pick('XIAOYI_WS_URL', 'wsUrl', DEFAULT_OPENCLAW_WS_URL),
    authMode: authMode === 'hag' ? 'hag' : 'huawei',
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
  if (!Number.isFinite(cfg.heartbeatMs) || cfg.heartbeatMs < 1000) {
    cfg.heartbeatMs = DEFAULT_HEARTBEAT_MS;
  }
  if (!Number.isFinite(cfg.askTimeoutMs) || cfg.askTimeoutMs < 1000) {
    cfg.askTimeoutMs = DEFAULT_ASK_TIMEOUT_MS;
  }
  return cfg;
}

/**
 * Build HMAC-SHA256 authentication headers for XiaoYi OpenClaw outbound WebSocket handshake.
 * - "huawei" (default): official Huawei OpenClaw headers (`x-access-key`, `x-ts`, `x-sign`, `x-agent-id`, `x-hag-trace-id`).
 * - "hag": legacy custom gateway headers (`x-hag-ak`, `x-hag-agent-id`, `x-hag-timestamp`, `x-hag-nonce`, `x-hag-signature`).
 */
export function signOpenClawHeaders({
  ak,
  sk,
  agentId,
  authMode = 'huawei',
  timestampMs = Date.now(),
  nonce = randomBytes(12).toString('hex'),
  traceId = randomUUID()
}) {
  const ts = String(Math.floor(timestampMs));
  if (authMode === 'hag') {
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
  const sign = createHmac('sha256', sk).update(ts, 'utf8').digest('base64');
  return {
    'x-access-key': ak,
    'x-ts': ts,
    'x-sign': sign,
    'x-agent-id': agentId,
    'x-hag-trace-id': traceId
  };
}

/** Exponential backoff delay: 1s, 2s, 4s, 8s, 16s, capped at 30s. */
export function computeReconnectDelayMs(attempt) {
  const safe = Math.max(0, Math.floor(Number(attempt) || 0));
  return Math.min(30000, 1000 * Math.pow(2, Math.min(5, safe)));
}

/** Follow upstream HubModules (#15978 / #15985): off only when `modules` is announced without `module.recording`. */
export function isHubRecordingAllowed(features) {
  if (!Array.isArray(features) || features.length === 0) return true;
  if (!features.includes('modules')) return true;
  return features.includes('module.recording');
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
  // Recording boundary: microphone capture is on-device and gated by Hub `module.recording` (#15985).
  if (/^(?:start\s+(?:a\s+)?(?:meeting\s+)?recording|record\s+(?:a\s+)?meeting|开始(?:会议)?录音|用\s*persona\s*录音|会議を録音|録音を開始)/i.test(text)) {
    return { kind: 'start_recording', arg: '' };
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

  async getHealth() {
    return this.request('/api/v1/health');
  }

  async isRecordingAllowed() {
    try {
      const health = await this.getHealth();
      return isHubRecordingAllowed(health?.features);
    } catch (_) {
      return true;
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

  async resolveMainConversation() {
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
    return { machineId, sessionId };
  }

  async askPersona(question, { onChunk, isCancelled, onTarget, conversationId = '', machineId: existingMachineId = '' } = {}) {
    let machineId = existingMachineId;
    let sessionId = conversationId;
    const usedCached = Boolean(machineId && sessionId);
    if (!machineId || !sessionId) {
      ({ machineId, sessionId } = await this.resolveMainConversation());
    }
    if (onTarget) onTarget({ machineId, sessionId });

    const readBaseline = async (m, s) => {
      const baseItems = `/api/v1/sessions/${encodeURIComponent(m)}/${encodeURIComponent(s)}/items`;
      let seq = 0;
      try {
        const initial = await this.request(`${baseItems}?lines=20&fold_work=1&lazy_images=1`);
        for (const item of Array.isArray(initial.items) ? initial.items : []) {
          if (typeof item.seq === 'number' && item.seq > seq) seq = item.seq;
        }
      } catch (_) {
        // Fresh conversation.
      }
      return { baseItems, baselineSeq: seq };
    };

    let { baseItems, baselineSeq } = await readBaseline(machineId, sessionId);
    const opId = randomUUID();
    try {
      await this.request(
        `/api/v1/sessions/${encodeURIComponent(machineId)}/${encodeURIComponent(sessionId)}/messages`,
        'POST',
        { text: question, client_msg_id: opId },
        opId
      );
    } catch (err) {
      if (usedCached && err?.status === 404) {
        ({ machineId, sessionId } = await this.resolveMainConversation());
        if (onTarget) onTarget({ machineId, sessionId });
        ({ baseItems, baselineSeq } = await readBaseline(machineId, sessionId));
        const retryOpId = randomUUID();
        await this.request(
          `/api/v1/sessions/${encodeURIComponent(machineId)}/${encodeURIComponent(sessionId)}/messages`,
          'POST',
          { text: question, client_msg_id: retryOpId },
          retryOpId
        );
      } else {
        throw err;
      }
    }

    const deadline = Date.now() + this.askTimeoutMs;
    let emitted = '';
    while (Date.now() < deadline) {
      if (isCancelled && isCancelled()) {
        await this.interrupt(machineId, sessionId);
        return { text: '', canceled: true, machineId, sessionId };
      }
      await new Promise((r) => setTimeout(r, 250));
      if (isCancelled && isCancelled()) {
        await this.interrupt(machineId, sessionId);
        return { text: '', canceled: true, machineId, sessionId };
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
        return { text: emitted, canceled: false, machineId, sessionId };
      }
    }
    return {
      text: emitted || 'Sent to Persona. It is still thinking — open Persona to view progress.',
      canceled: false,
      machineId,
      sessionId
    };
  }
}

function rememberSessionContext(sessionContexts, sessionKey, binding) {
  if (!sessionKey || !binding?.machineId || !binding?.sessionId) return;
  sessionContexts.delete(sessionKey);
  sessionContexts.set(sessionKey, binding);
  while (sessionContexts.size > MAX_SESSION_CONTEXTS) {
    const oldest = sessionContexts.keys().next().value;
    sessionContexts.delete(oldest);
  }
}

/**
 * Unwrap an incoming WebSocket message that may be either:
 * 1. Huawei XiaoYi OpenClaw envelope (`{ msgType, agentId, sessionId, taskId, msgDetail }`), or
 * 2. Bare JSON-RPC 2.0 frame (`{ jsonrpc: "2.0", id, method, params }`).
 */
export function unwrapOpenClawEnvelope(rawFrame, defaultAgentId = '') {
  if (!rawFrame || typeof rawFrame !== 'object') return { ignore: true };
  if (typeof rawFrame.msgType === 'string') {
    const type = rawFrame.msgType;
    if (type === 'heartbeat' || type === 'pong' || type === 'clawd_bot_init_ack' || type === 'init_ack') {
      return { ignore: true, msgType: type };
    }
    let detail = rawFrame.msgDetail;
    if (typeof detail === 'string') {
      try { detail = JSON.parse(detail); } catch (_) { return { ignore: true }; }
    }
    if (!detail || typeof detail !== 'object') return { ignore: true };
    return {
      ignore: false,
      enveloped: true,
      envelope: {
        agentId: String(rawFrame.agentId || defaultAgentId || ''),
        sessionId: String(rawFrame.sessionId || detail?.params?.sessionId || ''),
        taskId: String(rawFrame.taskId || detail?.params?.id || detail?.params?.taskId || '')
      },
      rpcFrame: detail
    };
  }
  return { ignore: false, enveloped: false, envelope: null, rpcFrame: rawFrame };
}

/** Handle one incoming JSON-RPC 2.0 frame (bare or unwrapped from Huawei OpenClaw envelope). */
export async function handleOpenClawRpc(
  frame,
  hubClient,
  sendFrame,
  activeTasks = new Map(),
  { enveloped = false, envelope = null, sessionContexts = new Map() } = {}
) {
  const id = frame?.id ?? null;
  const method = String(frame?.method || '');
  const params = frame?.params || {};
  const sessionKey = String(envelope?.sessionId || params.sessionId || params.contextId || '');

  const emit = (rpcPayload, explicitTaskId = '') => {
    if (!enveloped) {
      sendFrame(rpcPayload);
      return;
    }
    sendFrame({
      msgType: 'agent_response',
      agentId: envelope?.agentId || '',
      sessionId: sessionKey,
      taskId: explicitTaskId || envelope?.taskId || '',
      msgDetail: JSON.stringify(rpcPayload)
    });
  };

  if (method === 'ping' || method === 'heartbeat') {
    emit({ jsonrpc: '2.0', id, result: { pong: true, timestamp: Date.now() } });
    return;
  }

  if (method === 'clearContext' || method === 'context/clear') {
    if (sessionKey) sessionContexts.delete(sessionKey);
    emit({ jsonrpc: '2.0', id, result: { cleared: true, sessionId: sessionKey } });
    return;
  }

  if (method === 'CancelTask' || method === 'tasks/cancel') {
    const taskId = String(params.taskId || params.id || envelope?.taskId || '');
    const active = activeTasks.get(taskId);
    if (active) {
      active.cancelled = true;
      activeTasks.delete(taskId);
      if (active.machineId && active.sessionId) {
        await hubClient.interrupt(active.machineId, active.sessionId);
      }
    }
    emit({ jsonrpc: '2.0', id, result: { id: taskId, status: { state: 'canceled' } } }, taskId);
    return;
  }

  const supportedSend = [
    'SendMessage',
    'SendStreamingMessage',
    'message/send',
    'message/stream',
    'tasks/send',
    'tasks/sendSubscribe'
  ];
  if (!supportedSend.includes(method)) {
    emit({ jsonrpc: '2.0', id, error: { code: -32601, message: `Unsupported method: ${method}` } });
    return;
  }

  const streaming = method === 'SendStreamingMessage' || method === 'message/stream' || method === 'tasks/sendSubscribe';
  const taskId = String(params.taskId || params.id || envelope?.taskId || randomUUID());
  const text = extractMessageText(params);
  if (!text) {
    emit({ jsonrpc: '2.0', id, error: { code: -32602, message: 'Empty message content' } }, taskId);
    return;
  }

  const stateHandle = { cancelled: false, machineId: '', sessionId: '' };
  activeTasks.set(taskId, stateHandle);

  if (streaming) {
    if (enveloped) {
      emit({
        jsonrpc: '2.0',
        id,
        result: { taskId, contextId: sessionKey, kind: 'status-update', final: false, status: { state: 'working' } }
      }, taskId);
    } else {
      emit({ jsonrpc: '2.0', method: 'TaskStatusUpdate', params: { taskId, status: { state: 'working' } } }, taskId);
    }
  }

  try {
    const cmd = classifyRelayCommand(text);
    let replyText = '';
    let finalState = 'completed';

    if (cmd.kind === 'refuse_approval') {
      replyText = 'For your security, approvals cannot be confirmed over cloud relay. Open the Persona app on your unlocked phone to review and approve.';
      finalState = 'input-required';
    } else if (cmd.kind === 'start_recording') {
      const allowed = await hubClient.isRecordingAllowed();
      if (!allowed) {
        replyText = RECORDING_OFF_MESSAGE;
        finalState = 'rejected';
      } else {
        replyText = 'Open Persona on your phone to start Meeting Recording (microphone capture stays on-device).';
        finalState = 'input-required';
      }
    } else if (cmd.kind === 'status') {
      replyText = await hubClient.checkStatus();
    } else if (cmd.kind === 'add_todo') {
      replyText = await hubClient.addTodo(cmd.arg);
    } else if (cmd.kind === 'search_kb') {
      replyText = await hubClient.searchKnowledge(cmd.arg);
    } else {
      const existing = sessionKey ? sessionContexts.get(sessionKey) : undefined;
      let chunkIndex = 0;
      const res = await hubClient.askPersona(cmd.arg, {
        conversationId: existing?.sessionId || '',
        machineId: existing?.machineId || '',
        isCancelled: () => stateHandle.cancelled,
        onTarget: (target) => {
          stateHandle.machineId = target.machineId;
          stateHandle.sessionId = target.sessionId;
        },
        onChunk: (delta, lastChunk) => {
          if (!streaming || stateHandle.cancelled) return;
          const artifact = {
            artifactId: `${taskId}:answer`,
            parts: [{ text: delta, mediaType: 'text/plain' }],
            append: chunkIndex > 0,
            lastChunk
          };
          if (enveloped) {
            emit({
              jsonrpc: '2.0',
              id,
              result: { taskId, contextId: sessionKey, kind: 'artifact-update', append: artifact.append, lastChunk, artifact }
            }, taskId);
          } else {
            emit({ jsonrpc: '2.0', method: 'TaskArtifactUpdate', params: { taskId, artifact } }, taskId);
          }
          chunkIndex++;
        }
      });
      if (res.canceled || stateHandle.cancelled) return;
      if (sessionKey && res.machineId && res.sessionId) {
        rememberSessionContext(sessionContexts, sessionKey, { machineId: res.machineId, sessionId: res.sessionId });
      }
      replyText = res.text;
    }

    if (enveloped && streaming) {
      emit({
        jsonrpc: '2.0',
        id,
        result: {
          taskId,
          contextId: sessionKey,
          kind: 'status-update',
          final: true,
          status: {
            state: finalState,
            message: { role: 'agent', parts: [{ text: replyText, mediaType: 'text/plain' }] }
          }
        }
      }, taskId);
    } else {
      emit({
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
      }, taskId);
    }
  } catch (error) {
    if (stateHandle.cancelled) return;
    emit({
      jsonrpc: '2.0',
      id,
      error: { code: -32000, message: error?.message || 'Hub request failed' }
    }, taskId);
  } finally {
    if (activeTasks.get(taskId) === stateHandle) {
      activeTasks.delete(taskId);
    }
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
    this.sessionContexts = new Map();
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
    if (!wsUrl.searchParams.has('agentId')) {
      wsUrl.searchParams.set('agentId', this.cfg.agentId);
    }
    const ws = new this.WebSocketImpl(wsUrl.toString(), { headers });
    this.ws = ws;

    ws.addEventListener('open', () => {
      if (this.ws !== ws) return;
      this.attempt = 0;
      this.logger.info('[xiaoyi-openclaw] connected to outbound gateway');
      ws.send(JSON.stringify({ msgType: 'clawd_bot_init', agentId: this.cfg.agentId }));
      if (this.heartbeatTimer) clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = setInterval(() => {
        if (this.ws === ws && ws.readyState === 1) {
          ws.send(JSON.stringify({ msgType: 'heartbeat', agentId: this.cfg.agentId, timestamp: Date.now() }));
        }
      }, this.cfg.heartbeatMs);
    });

    ws.addEventListener('message', (event) => {
      if (this.ws !== ws) return;
      let rawFrame;
      try {
        rawFrame = JSON.parse(String(event.data || ''));
      } catch (_) {
        return;
      }
      const unwrapped = unwrapOpenClawEnvelope(rawFrame, this.cfg.agentId);
      if (unwrapped.ignore) return;
      handleOpenClawRpc(
        unwrapped.rpcFrame,
        this.hub,
        (reply) => {
          if (this.ws === ws && ws.readyState === 1) {
            ws.send(JSON.stringify(reply));
          }
        },
        this.activeTasks,
        {
          enveloped: unwrapped.enveloped,
          envelope: unwrapped.envelope,
          sessionContexts: this.sessionContexts
        }
      ).catch((err) => {
        this.logger.error('[xiaoyi-openclaw] rpc error:', err?.message || err);
      });
    });

    ws.addEventListener('close', () => {
      if (this.ws !== ws) return;
      this.ws = null;
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

/**
 * Preflight check (`--check` / `--check-ws`) so a developer can validate Hub reachability, token auth,
 * recording module state, and optional live OpenClaw WebSocket handshake in under 60 seconds without printing secrets.
 */
export async function runPreflightCheck(
  cfg,
  { checkWs = false, fetchImpl = globalThis.fetch, WebSocketImpl = globalThis.WebSocket, wsTimeoutMs = 8000 } = {}
) {
  const hub = new PersonaHubClient(cfg, fetchImpl);
  const report = {
    ok: false,
    hubUrl: cfg.hubUrl,
    wsUrl: cfg.wsUrl,
    authMode: cfg.authMode,
    agentId: cfg.agentId,
    checks: {
      config: { ok: true, detail: `authMode=${cfg.authMode}` },
      hubHealth: { ok: false, detail: '' },
      hubAuth: { ok: false, detail: '' },
      assistant: { ok: false, detail: '' },
      recordingModule: { ok: true, allowed: true, detail: 'on' },
      wsSigning: { ok: false, detail: '' },
      wsHandshake: checkWs ? { ok: false, detail: '' } : { ok: true, skipped: true, detail: 'skipped (pass --check-ws to test live socket)' }
    }
  };

  try {
    const health = await hub.getHealth();
    const recordingAllowed = isHubRecordingAllowed(health?.features);
    report.checks.hubHealth = {
      ok: true,
      detail: `hub=${health?.hub_name || health?.hub_id || 'ok'}${health?.version ? ` v${health.version}` : ''}`
    };
    report.checks.recordingModule = {
      ok: true,
      allowed: recordingAllowed,
      detail: recordingAllowed ? 'on' : 'off (recording requests will fail closed)'
    };
  } catch (err) {
    report.checks.hubHealth = { ok: false, detail: err?.message || 'unreachable' };
  }

  try {
    const machinesRes = await hub.request('/api/v1/machines');
    const machines = Array.isArray(machinesRes?.machines) ? machinesRes.machines : [];
    const online = machines.filter((m) => m.online !== false && m.trust !== 'pending').length;
    report.checks.hubAuth = { ok: true, detail: `authorized (${online}/${machines.length} machines online)` };
  } catch (err) {
    report.checks.hubAuth = {
      ok: false,
      detail: err?.status === 401 || err?.status === 403 ? 'token rejected (HTTP 401/403)' : (err?.message || 'failed')
    };
  }

  try {
    const assistantRes = await hub.request('/api/v1/assistant');
    report.checks.assistant = {
      ok: true,
      detail: assistantRes?.main ? 'main conversation ready' : 'ready (will create main on first message)'
    };
  } catch (err) {
    report.checks.assistant = { ok: false, detail: err?.message || 'unavailable' };
  }

  try {
    const headers = signOpenClawHeaders(cfg);
    const headerNames = Object.keys(headers).join(', ');
    report.checks.wsSigning = { ok: true, detail: `signed headers ready (${headerNames})` };
  } catch (err) {
    report.checks.wsSigning = { ok: false, detail: err?.message || 'signing failed' };
  }

  if (checkWs) {
    report.checks.wsHandshake = await new Promise((resolve) => {
      let settled = false;
      const done = (result) => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        resolve(result);
      };
      const timer = setTimeout(() => {
        try { ws.close(); } catch (_) {}
        done({ ok: false, detail: `timed out after ${wsTimeoutMs}ms` });
      }, wsTimeoutMs);

      let ws;
      try {
        const headers = signOpenClawHeaders(cfg);
        const wsUrl = new URL(cfg.wsUrl);
        if (!wsUrl.searchParams.has('agentId')) wsUrl.searchParams.set('agentId', cfg.agentId);
        ws = new WebSocketImpl(wsUrl.toString(), { headers });
      } catch (err) {
        done({ ok: false, detail: err?.message || 'invalid WebSocket config' });
        return;
      }

      ws.addEventListener('open', () => {
        try {
          ws.send(JSON.stringify({ msgType: 'clawd_bot_init', agentId: cfg.agentId }));
          ws.close(1000, 'preflight-ok');
        } catch (_) {}
        done({ ok: true, detail: 'connected and sent clawd_bot_init' });
      });
      ws.addEventListener('error', (err) => {
        done({ ok: false, detail: err?.message || 'WebSocket connection failed' });
      });
      ws.addEventListener('close', (ev) => {
        if (!settled) {
          done({ ok: false, detail: `closed before open (code ${ev?.code || 0})` });
        }
      });
    });
  }

  report.ok = Object.values(report.checks).every((c) => c.ok);
  return report;
}

export function formatPreflightReport(report) {
  const lines = [
    `Persona XiaoYi OpenClaw Preflight (${report.ok ? 'PASS' : 'FAIL'})`,
    `  Hub: ${report.hubUrl}`,
    `  WS:  ${report.wsUrl} (agentId=${report.agentId}, authMode=${report.authMode})`
  ];
  for (const [name, entry] of Object.entries(report.checks)) {
    const badge = entry.skipped ? 'SKIP' : entry.ok ? 'PASS' : 'FAIL';
    lines.push(`  [${badge}] ${name}: ${entry.detail}`);
  }
  return lines.join('\n');
}

function printUsage() {
  console.log([
    'Persona XiaoYi OpenClaw Outbound WebSocket Bridge (Node 24, zero dependencies)',
    '',
    'Usage:',
    '  node harmony/scripts/xiaoyi-openclaw-bridge.mjs [--config /path/to/config.json]',
    '  node harmony/scripts/xiaoyi-openclaw-bridge.mjs --check [--check-ws] [--config /path/to/config.json]',
    '',
    'Required env vars (or JSON keys):',
    '  XIAOYI_AK / ak, XIAOYI_SK / sk, XIAOYI_AGENT_ID / agentId,',
    '  PERSONA_HUB_URL / hubUrl, PERSONA_HUB_TOKEN / hubToken',
    '',
    'Optional env vars:',
    `  XIAOYI_WS_URL (default: ${DEFAULT_OPENCLAW_WS_URL})`,
    '  XIAOYI_AUTH_MODE ("huawei" default or "hag"), PERSONA_MANAGER, XIAOYI_CONFIG_FILE'
  ].join('\n'));
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const cli = parseCliArgs(process.argv.slice(2));
  if (cli.help) {
    printUsage();
    process.exit(0);
  }
  const cfg = loadBridgeConfig(process.env, cli.configFile);
  if (cli.check) {
    const report = await runPreflightCheck(cfg, { checkWs: cli.checkWs });
    console.log(formatPreflightReport(report));
    process.exit(report.ok ? 0 : 1);
  }
  const bridge = new OpenClawBridge(cfg);
  process.on('SIGINT', () => { bridge.stop(); process.exit(0); });
  process.on('SIGTERM', () => { bridge.stop(); process.exit(0); });
  bridge.start();
}
