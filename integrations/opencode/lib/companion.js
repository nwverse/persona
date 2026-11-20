// Standalone ACP companion runtime for opencode (docs/integrations/opencode.md).
// Connects to ~/.persona/runtime.sock without requiring a resident `opencode serve` process:
//  - Discovers available models from `opencode models --verbose` (and merges ACP session configOptions)
//  - Starts new Agent sessions (`session.start`) and Persona Assistant conversations (`assistant: true`) via `opencode acp`
//  - Loads `AGENTS.md` natively from the requested working directory (`~/.persona/assistant` for Assistant chats)
//  - Exposes Persona MCP tools per session through a private loopback HTTP MCP proxy (`lib/mcp.js`), with strict
//    assistant vs. ordinary Agent tool scoping and command-gate enforcement
//  - Reads existing sessions from `opencode.db` (both 1.x `session`/`message`/`part` and 2.x `session_v2`/`session_message`),
//    listing sessions not owned by this companion as `offline` so a live `opencode serve` plugin wins (`prefer_running`)
//    and herdr TUI rows continue merging cleanly via `mergeOpencodeRow`.
//
// Safety boundary (#11220): `opencode acp` advertises `{ close, fork, list, resume }` without `additionalDirectories`,
// so this companion deliberately omits `caps.supervision_policy` and fails closed (`assistant_supervision_runtime`)
// if any caller supplies non-empty `supervision_instructions`.

import { createHash } from 'node:crypto'
import { homedir } from 'node:os'
import { basename, isAbsolute, relative, resolve } from 'node:path'
import { createRuntime, interactionId, light, page, RpcError, snippet } from '../../runtime-sdk/index.js'
import { isAssistantDir } from '../../runtime-sdk/lib/identity.js'
import { OpencodeAcpClient } from './acp.js'
import { defaultDbPath, openReadOnly } from './db.js'
import {
  acpPermissionCard,
  contextTokens,
  fromRow,
  imageOf,
  lastText,
  messageItems,
  promptWithContext,
  toolInput,
  toolName,
  v1ImageOf,
  v1SessionItems,
  visibleUserText,
} from './map.js'
import { createMcpProxy, extractShellGates, matchShellGate } from './mcp.js'
import {
  buildLauncher,
  discoverCliModels,
  normalizeModels,
  parseAcpModelOptions,
  readConfiguredModel,
  resolveModel,
} from './models.js'

export const COMPANION_VERSION = '0.2.0'
const ROOTS = 200
const LIST_CACHE = 3000
const MODELS_EVERY = 5 * 60_000
const VALID_MODES = new Set(['default', 'acceptEdits', 'auto', 'plan', 'bypassPermissions'])
const SUPERVISION_ERROR_CODE = 'assistant_supervision_runtime'
const INTERRUPT_SETTLE_MS = 10_000

/**
 * @param {{
 *   bin?: string,
 *   args?: string[],
 *   dbPath?: string,
 *   openDb?: () => Promise<{ all(sql: string, ...params: any[]): any[], close(): void }>,
 *   configDir?: string,
 *   home?: string,
 *   env?: NodeJS.ProcessEnv,
 *   socketPath?: string,
 *   connect?: (path: string) => import('node:net').Socket,
 *   runtime?: string,
 *   name?: string,
 *   launcher?: string,
 *   displayName?: string,
 *   model?: string,
 *   assistant?: boolean,
 *   listDays?: number,
 *   maxSessions?: number,
 *   retryMs?: number,
 *   pushDelayMs?: number,
 *   refreshMs?: number,
 *   instance?: string,
 *   log?: (msg: string) => void,
 *   now?: () => number,
 *   acp?: OpencodeAcpClient,
 *   discoverModels?: (opts?: { refresh?: boolean }) => Promise<any[]>,
 * }} [options]
 */
export function createOpencodeCompanion(options = {}) {
  const home = options.home || homedir()
  const env = options.env || process.env
  const bin = options.bin || 'opencode'
  const log = options.log || (() => {})
  const now = options.now || Date.now
  const listDays = options.listDays ?? 7
  const maxSessions = options.maxSessions ?? 80
  const runtimeId = options.runtime || 'opencode'
  const label = options.name || 'opencode'
  const launcherName = options.launcher === undefined ? 'opencode' : String(options.launcher).trim()
  const displayName = typeof options.displayName === 'string' && options.displayName.trim() ? options.displayName.trim() : label
  const assistantEnabled = options.assistant !== false
  const openDb = options.openDb || (() => openReadOnly(options.dbPath || defaultDbPath(env, home)))
  const discoverModelsFn = options.discoverModels || (() => discoverCliModels({ bin, env }))

  let configuredDefaultModel = typeof options.model === 'string' && options.model.trim()
    ? options.model.trim()
    : readConfiguredModel({ configDir: options.configDir, env, home })

  let catalog = []
  let catalogAt = 0
  let catalogFlight
  const windows = new Map()
  let gateShellRules = []

  // Sessions created or actively driven by this companion over ACP.
  const live = new Map()
  const closedSessions = new Set()

  let db
  let dbFailedAt = 0
  let schemaKind = '' // 'v2' | 'v1' | ''
  let listCache = { at: 0, rows: [] }
  let stopped = false

  const acp = options.acp || new OpencodeAcpClient({
    bin,
    args: options.args,
    cwd: home,
    env,
    log,
    onPermissionRequest: (params) => handlePermissionRequest(params),
  })
  acp.onPermissionRequest = (params) => handlePermissionRequest(params)
  acp.on('update', (params) => onAcpUpdate(params))

  const mcpProxy = createMcpProxy({
    tools: async (binding) => {
      const asst = binding?.assistant === true
      const list = await rt.tools({ assistant: asst })
      if (asst) gateShellRules = extractShellGates(list)
      return list
    },
    call: async (binding, name, args) => {
      const s = binding?.session
      const sid = s?.id || binding?.sessionId || ''
      if (!sid || closedSessions.has(sid)) {
        return { content: [{ type: 'text', text: 'Conversation is not available' }], is_error: true }
      }
      const asst = Boolean(s?.assistant ?? binding?.assistant)
      return rt.callTool({ name, args, session_id: sid, assistant: asst }, { signal: s?.abort?.signal })
    },
    log,
  })

  const initialLaunchers = launcherName
    ? [buildLauncher({ name: launcherName, displayName, models: catalog, defaultModel: configuredDefaultModel })]
    : undefined

  const rt = createRuntime({
    listSessions,
    items,
    cursor,
    item,
    image,
    start,
    send,
    interrupt,
    rename,
    close,
    models,
    modes,
    hasLocalClient: () => false,
  }, {
    runtime: runtimeId,
    name: label,
    agent: 'opencode',
    version: COMPANION_VERSION,
    ...(launcherName ? { launcher: launcherName, launcherModel: configuredDefaultModel || undefined, launchers: initialLaunchers } : {}),
    assistant: assistantEnabled,
    readonly: true,
    modelSwitch: true,
    // Security boundary (#11220): intentionally DO NOT set supervisionPolicy: true.
    requires: ['prefer_running'],
    socketPath: options.socketPath,
    connect: options.connect,
    instance: options.instance,
    retryMs: options.retryMs,
    pushDelayMs: options.pushDelayMs ?? 50,
    refreshMs: options.refreshMs ?? 15_000,
    log,
    onHub: (connected) => {
      if (connected && assistantEnabled) void refreshGateRules()
    },
  })

  function rejectSupervisionPolicy(params) {
    if (typeof params?.supervision_instructions === 'string' && params.supervision_instructions.trim() !== '') {
      throw new RpcError('opencode does not support isolated supervision policy channels', SUPERVISION_ERROR_CODE)
    }
  }

  async function refreshGateRules() {
    try {
      const list = await rt.tools({ assistant: true })
      gateShellRules = extractShellGates(list)
    } catch (err) {
      log(`persona: opencode assistant tools: ${err.message}`)
    }
  }

  // ---- models ----

  function publishCatalog(rawList, fallbackDefault = '') {
    const def = configuredDefaultModel || fallbackDefault
    const normalized = normalizeModels(rawList, def)
    for (const [k, w] of normalized.windows) windows.set(k, w)
    if (normalized.models.length > 0) {
      catalog = normalized.models
      if (!configuredDefaultModel && normalized.defaultModel) configuredDefaultModel = normalized.defaultModel
    }
    if (launcherName) {
      rt.launchersChanged([buildLauncher({ name: launcherName, displayName, models: catalog, defaultModel: configuredDefaultModel })])
    }
    return catalog
  }

  async function models(params = {}) {
    if (!params.refresh && catalog.length > 0 && now() - catalogAt < MODELS_EVERY) {
      return { agent: 'opencode', models: catalog }
    }
    if (!catalogFlight) {
      catalogFlight = Promise.resolve()
        .then(() => discoverModelsFn({ refresh: params.refresh === true }))
        .then((raw) => {
          catalogAt = now()
          publishCatalog(raw)
          return { agent: 'opencode', models: catalog }
        })
        .finally(() => {
          catalogFlight = undefined
        })
    }
    return catalogFlight
  }

  function applyConfigOptions(s, configOptions) {
    if (!Array.isArray(configOptions)) return
    const { models: acpModels, currentModel } = parseAcpModelOptions(configOptions)
    if (currentModel && !s.model) s.model = currentModel
    else if (currentModel) s.model = currentModel
    const modeOpt = configOptions.find((c) => c && (c.id === 'mode' || c.category === 'mode'))
    if (modeOpt?.currentValue === 'plan') s.mode = 'plan'
    else if (modeOpt?.currentValue && s.mode === 'plan') s.mode = 'default'
    if (acpModels.length > 0 && catalog.length === 0) {
      publishCatalog(acpModels, currentModel)
    }
  }

  // ---- read-only database (supports both 1.x session/message/part and 2.x session_v2/session_message) ----

  async function database() {
    if (db) return db
    if (dbFailedAt && now() - dbFailedAt < 60_000) return null
    try {
      db = await openDb()
      dbFailedAt = 0
      schemaKind = ''
    } catch (err) {
      dbFailedAt = now()
      log(`persona: opencode db: ${err.message}`)
    }
    return db ?? null
  }

  async function queryRows(sql, ...params) {
    const d = await database()
    if (!d) return []
    try {
      return d.all(sql, ...params)
    } catch (err) {
      log(`persona: opencode query: ${err.message}`)
      if (/not an? (database|open)|closed/i.test(String(err.message))) {
        db = undefined
        schemaKind = ''
      }
      return []
    }
  }

  async function detectSchema() {
    if (schemaKind) return schemaKind
    const tables = new Set((await queryRows("select name from sqlite_master where type = 'table'")).map((r) => r.name))
    if (tables.has('session_v2') && tables.has('session_message')) schemaKind = 'v2'
    else if (tables.has('session') && tables.has('message') && tables.has('part')) schemaKind = 'v1'
    return schemaKind
  }

  async function readDbSessionMeta(id) {
    const kind = await detectSchema()
    if (kind === 'v2') {
      const [r] = await queryRows('select id, title, directory, model, revert, time_updated from session_v2 where id = ? and parent_id is null', id)
      return r ? { ...r, _schema: 'v2' } : null
    }
    if (kind === 'v1') {
      const [r] = await queryRows('select id, title, directory, model, revert, time_updated from session where id = ? and parent_id is null', id)
      return r ? { ...r, _schema: 'v1' } : null
    }
    return null
  }

  function parseSessionModelJson(raw) {
    if (!raw) return ''
    try {
      const parsed = JSON.parse(raw)
      if (parsed?.providerID && parsed?.id) return `${parsed.providerID}/${parsed.id}`
      if (parsed?.providerID && parsed?.modelID) return `${parsed.providerID}/${parsed.modelID}`
      return parsed?.id || parsed?.modelID || ''
    } catch {
      return ''
    }
  }

  async function readDbItems(id, clientMsgIdFor) {
    const meta = await readDbSessionMeta(id)
    if (!meta) return null
    if (meta._schema === 'v2') {
      const rows = await queryRows('select id, seq, type, data, time_updated from session_message where session_id = ? order by seq', id)
      const all = rows.flatMap((r) => messageItems(fromRow(r), r.seq))
      const toSeq = rows.at(-1)?.seq ?? 0
      const msgs = rows.map((r) => ({ ...fromRow(r), seq: r.seq }))
      const lastAssistant = [...msgs].reverse().find((m) => m.type === 'assistant')
      const revertHash = meta.revert ? `:r${createHash('sha256').update(String(meta.revert)).digest('hex').slice(0, 8)}` : ''
      return {
        meta,
        items: all,
        toSeq,
        history: `opencode:${id}${revertHash}`,
        lastText: lastText(msgs),
        model: lastAssistant?.model?.providerID && lastAssistant?.model?.id
          ? `${lastAssistant.model.providerID}/${lastAssistant.model.id}`
          : (lastAssistant?.model?.id || parseSessionModelJson(meta.model)),
        tokens: contextTokens(lastAssistant),
        hasAssistant: !!lastAssistant,
      }
    }
    const messages = await queryRows('select id, time_created, time_updated, data from message where session_id = ? order by time_created asc, rowid asc', id)
    const parts = await queryRows('select id, message_id, time_created, time_updated, data from part where session_id = ? order by time_created asc, rowid asc', id)
    const byMsg = new Map()
    for (const p of parts) {
      let arr = byMsg.get(p.message_id)
      if (!arr) { arr = []; byMsg.set(p.message_id, arr) }
      arr.push(p)
    }
    const mapped = v1SessionItems(messages, byMsg, { clientMsgIdFor })
    const revertHash = meta.revert ? `:r${createHash('sha256').update(String(meta.revert)).digest('hex').slice(0, 8)}` : ''
    const qualifiedModel = mapped.provider && mapped.model
      ? `${mapped.provider}/${mapped.model}`
      : (mapped.model || parseSessionModelJson(meta.model))
    return {
      meta,
      items: mapped.items,
      toSeq: mapped.toSeq,
      history: `opencode:${id}${revertHash}`,
      lastText: mapped.lastText,
      model: qualifiedModel,
      tokens: mapped.tokens,
      hasAssistant: mapped.hasAssistant,
      messages,
      partsByMessage: byMsg,
    }
  }

  // ---- live session state ----

  function createLiveState(id, cwd, { assistant = false, title = '', model = '', mode = 'default', startedByPersona = false } = {}) {
    const s = {
      id,
      cwd,
      title: title || basename(cwd) || 'opencode',
      model,
      mode: mode || 'default',
      assistant,
      memoryDir: '',
      startedByPersona,
      loadedGeneration: 0,
      running: false,
      queued: 0,
      tail: Promise.resolve(),
      abort: null,
      mcpRoute: null,
      liveItems: [],
      liveSeq: 0,
      pushedSeq: 0,
      activeText: '',
      activeThought: '',
      activeTools: new Map(), // toolCallId -> index in liveItems
      lastText: '',
      contextTokens: 0,
      contextWindow: 0,
      updatedAt: now(),
      history: `opencode:${id}`,
      asks: new Set(),
      sentClientMsgIds: new Map(), // userOrdinal -> { text, id }
      userInputCount: 0,
      hasAssistant: false,
      activeTurnDone: null,
    }
    live.set(id, s)
    return s
  }

  function clientMsgIdLookup(s) {
    if (!s) return undefined
    return (ordinal, text) => {
      const rec = s.sentClientMsgIds.get(ordinal)
      if (rec && (!rec.text || rec.text === text)) return rec.id
      return undefined
    }
  }

  function appendLiveItem(s, item) {
    const seq = ++s.liveSeq
    const full = { id: `${seq}#0`, seq, ts: new Date(now()).toISOString(), ...item }
    s.liveItems.push(full)
    s.updatedAt = now()
    return full
  }

  function flushLiveText(s) {
    if (s.activeThought) {
      appendLiveItem(s, { role: 'assistant', kind: 'thinking', text: s.activeThought })
      s.activeThought = ''
    }
    if (s.activeText) {
      appendLiveItem(s, { role: 'assistant', kind: 'text', text: s.activeText })
      s.lastText = snippet(s.activeText)
      s.hasAssistant = true
      s.activeText = ''
    }
  }

  function onAcpUpdate(params) {
    const id = params?.sessionId
    const upd = params?.update
    if (!id || !upd) return
    const s = live.get(id)
    if (!s || closedSessions.has(id)) return
    s.updatedAt = now()
    switch (upd.sessionUpdate) {
      case 'config_option_update':
        applyConfigOptions(s, upd.configOptions)
        rt.sessionsChanged()
        break
      case 'session_info_update':
        if (typeof upd.title === 'string' && upd.title.trim()) {
          s.title = upd.title.trim()
          rt.sessionsChanged()
        }
        break
      case 'agent_thought_chunk': {
        const txt = upd.content?.text
        if (typeof txt === 'string' && txt) s.activeThought += txt
        break
      }
      case 'agent_message_chunk': {
        const txt = upd.content?.text
        if (typeof txt === 'string' && txt) {
          if (s.activeThought) {
            appendLiveItem(s, { role: 'assistant', kind: 'thinking', text: s.activeThought })
            s.activeThought = ''
          }
          s.activeText += txt
          s.lastText = snippet(s.activeText)
          s.hasAssistant = true
          rt.stream(id, s.activeText)
        }
        break
      }
      case 'tool_call': {
        rt.streamEnd(id)
        flushLiveText(s)
        const callId = upd.toolCallId || `call_${now()}`
        const rawName = String(upd.kind === 'execute' ? 'bash' : (upd.title || upd.kind || 'tool')).split(/\s+/)[0]
        const item = appendLiveItem(s, {
          role: 'assistant',
          kind: 'tool_use',
          tool_name: toolName(rawName),
          tool_use_id: callId,
          tool_input: toolInput(rawName, upd.rawInput),
        })
        s.activeTools.set(callId, item)
        pushLiveSnapshot(s)
        break
      }
      case 'tool_call_update': {
        const callId = upd.toolCallId
        if (upd.status === 'completed' || upd.status === 'failed' || upd.status === 'error') {
          let resultStr = ''
          if (Array.isArray(upd.content)) {
            resultStr = upd.content
              .map((c) => c?.content?.text ?? c?.text ?? '')
              .filter(Boolean)
              .join('\n')
          } else if (typeof upd.rawOutput?.output === 'string') {
            resultStr = upd.rawOutput.output
          } else if (typeof upd.rawOutput === 'string') {
            resultStr = upd.rawOutput
          }
          appendLiveItem(s, {
            role: 'user',
            kind: 'tool_result',
            tool_use_id: callId || `call_${now()}`,
            is_error: upd.status === 'failed' || upd.status === 'error',
            text: resultStr,
          })
          pushLiveSnapshot(s)
        }
        break
      }
      case 'usage_update':
        if (typeof upd.used === 'number' && upd.used > 0) s.contextTokens = upd.used
        if (typeof upd.size === 'number' && upd.size > 0) s.contextWindow = upd.size
        break
    }
  }

  function pushLiveSnapshot(s) {
    if (!rt.watching(s.id)) return
    if (s.liveSeq <= s.pushedSeq) return
    const fresh = s.liveItems
      .filter((it) => it.seq > s.pushedSeq)
      .map((it) => (it.image ? { ...it, image: { mime: it.image.mime, data: '', lazy: true } } : light(it)))
    const fromSeq = s.pushedSeq
    s.pushedSeq = s.liveSeq
    rt.append(s.id, { items: fresh, fromSeq, toSeq: s.liveSeq, history: `${s.history}:live` })
  }

  // ---- session listing and reading ----

  function liveRow(s) {
    const status = s.asks.size > 0
      ? 'blocked'
      : (s.running || s.queued > 0 ? 'working' : (s.hasAssistant || s.liveItems.length > 0 ? 'done' : 'idle'))
    const row = {
      session_id: s.id,
      title: s.title || basename(s.cwd) || 'opencode',
      cwd: s.cwd,
      status,
      updated_at: new Date(s.updatedAt).toISOString(),
      rev: Math.max(s.liveSeq, s.liveItems.length),
      space_id: s.cwd,
      space_name: basename(s.cwd),
      space_kind: 'opencode',
      ...(s.lastText ? { last_text: s.lastText } : {}),
      ...(s.model ? { model: s.model } : {}),
      ...(s.startedByPersona ? { started_by_persona: true } : {}),
      ...(launcherName ? { launcher: launcherName } : {}),
      ...(s.mode && s.mode !== 'default' ? { permission_mode: s.mode } : {}),
    }
    if (s.contextTokens > 0) {
      row.context_tokens = s.contextTokens
      const w = s.contextWindow || windows.get(s.model)
      if (w > 0) row.context_window = w
    }
    return row
  }

  async function dbRows() {
    if (now() - listCache.at < LIST_CACHE) return listCache.rows
    const kind = await detectSchema()
    let rows = []
    if (kind === 'v2') {
      rows = await queryRows('select id, title, directory, time_updated, model from session_v2 where parent_id is null and time_archived is null order by time_updated desc limit ?', ROOTS)
    } else if (kind === 'v1') {
      rows = await queryRows('select id, title, directory, time_updated, model from session where parent_id is null and time_archived is null order by time_updated desc limit ?', ROOTS)
    }
    listCache = { at: now(), rows }
    return rows
  }

  async function listSessions() {
    const rows = []
    const seenIds = new Set()
    for (const [id, s] of live) {
      if (closedSessions.has(id)) continue
      seenIds.add(id)
      rows.push(liveRow(s))
    }
    for (const r of await dbRows()) {
      if (seenIds.has(r.id) || closedSessions.has(r.id)) continue
      seenIds.add(r.id)
      const directory = r.directory || ''
      const model = parseSessionModelJson(r.model)
      // Sessions only on disk (not actively run by this companion) are reported as `offline` so a running
      // `opencode serve` plugin takes ownership via `prefer_running`.
      rows.push({
        session_id: r.id,
        title: (typeof r.title === 'string' && r.title.trim()) || basename(directory) || 'opencode',
        cwd: directory,
        status: 'offline',
        updated_at: new Date(r.time_updated ?? now()).toISOString(),
        rev: 0,
        space_id: directory,
        space_name: basename(directory),
        space_kind: 'opencode',
        ...(model ? { model } : {}),
      })
    }
    return rt.recent(rows, {
      days: listDays,
      max: maxSessions,
      pinned: (r) => r.status === 'working' || r.status === 'blocked',
      id: (r) => r.session_id,
      updatedAt: (r) => Date.parse(r.updated_at) || 0,
    })
  }

  async function sessionContent(id) {
    const s = live.get(id)
    const dbData = await readDbItems(id, clientMsgIdLookup(s))
    if (s && (s.running || !dbData || dbData.items.length < s.liveItems.length)) {
      const placeholderItems = s.liveItems.map((it) => (
        it.image ? { ...it, image: { mime: it.image.mime, data: '', lazy: true } } : it
      ))
      return {
        items: placeholderItems,
        fullItems: s.liveItems,
        toSeq: s.liveSeq,
        history: `${s.history}:live`,
        dbData,
      }
    }
    if (dbData) {
      return {
        items: dbData.items,
        fullItems: dbData.items,
        toSeq: dbData.toSeq,
        history: dbData.history,
        dbData,
      }
    }
    if (s) {
      return { items: [], fullItems: [], toSeq: 0, history: s.history, dbData: null }
    }
    throw new RpcError(`opencode 里没有这个会话（${id}）`, 'not_found')
  }

  async function items(params) {
    const c = await sessionContent(params.session_id)
    return page(c.items, params, { history: c.history, toSeq: c.toSeq })
  }

  async function cursor(sessionId) {
    const c = await sessionContent(sessionId)
    return { to_seq: c.toSeq, history: c.history }
  }

  async function item(params) {
    const c = await sessionContent(params.session_id)
    const found = c.fullItems.find((it) => it.id === params.item_id)
    if (!found) throw new RpcError(`no item ${params.item_id}`, 'not_found')
    return found
  }

  async function image(params) {
    const c = await sessionContent(params.session_id)
    const found = c.fullItems.find((it) => it.id === params.item_id)
    if (found?.image?.data) return { mime: found.image.mime, data: found.image.data }
    const [seqText, idxText] = String(params.item_id).split('#')
    const seq = Number(seqText)
    const idx = Number(idxText)
    if (c.dbData?.meta?._schema === 'v1' && Number.isSafeInteger(seq) && seq >= 1) {
      const msg = c.dbData.messages?.[seq - 1]
      const rawParts = msg ? (c.dbData.partsByMessage.get(msg.id) ?? []).map((p) => {
        try { return JSON.parse(p.data) } catch { return {} }
      }) : []
      const img = v1ImageOf(rawParts, idx)
      if (img) return img
    } else if (c.dbData?.meta?._schema === 'v2' && Number.isSafeInteger(seq) && seq >= 1) {
      const [r] = await queryRows('select id, seq, type, data from session_message where session_id = ? and seq = ?', params.session_id, seq)
      const img = r && imageOf(fromRow(r), idx)
      if (img) return img
    }
    throw new RpcError(`no image ${params.item_id}`, 'not_found')
  }

  // ---- permissions ----

  function isMemoryWrite(s, rawName, input) {
    if (!s?.assistant || !s.memoryDir || s.mode === 'plan') return false
    if (rawName !== 'write' && rawName !== 'edit') return false
    const file = typeof input?.file_path === 'string' ? input.file_path : ''
    if (!file) return false
    const rel = relative(s.memoryDir, resolve(s.memoryDir, file))
    return rel !== '' && !rel.startsWith('..') && !isAbsolute(rel)
  }

  function pickOption(options, kind) {
    const list = Array.isArray(options) ? options : []
    return list.find((o) => o?.kind === kind)
  }

  async function handlePermissionRequest(params) {
    const id = params?.sessionId
    const s = live.get(id)
    if (!s || closedSessions.has(id)) return { outcome: { outcome: 'cancelled' } }
    const options = Array.isArray(params?.options) ? params.options : []
    const allowOnce = pickOption(options, 'allow_once') ?? options[0]
    const allowAlways = pickOption(options, 'allow_always') ?? allowOnce
    const rejectOnce = pickOption(options, 'reject_once') ?? pickOption(options, 'reject_always')
    const card = acpPermissionCard(params)

    // 1. Assistant command gates are never auto-approved, even in bypassPermissions.
    if (s.assistant && (card.raw_name === 'bash' || card.raw_name === 'shell')) {
      const gate = matchShellGate(card.tool_input?.command || '', gateShellRules)
      if (gate) {
        return rejectOnce
          ? { outcome: { outcome: 'selected', optionId: rejectOnce.optionId } }
          : { outcome: { outcome: 'cancelled' } }
      }
    }

    // 2. Plan mode rejects writes and shell execution.
    if (s.mode === 'plan' && ['write', 'edit', 'bash', 'shell'].includes(card.raw_name)) {
      return rejectOnce
        ? { outcome: { outcome: 'selected', optionId: rejectOnce.optionId } }
        : { outcome: { outcome: 'cancelled' } }
    }

    // 3. Assistant memory writes outside plan mode and bypassPermissions auto-approve.
    if (isMemoryWrite(s, card.raw_name, card.tool_input) || s.mode === 'bypassPermissions') {
      return allowOnce
        ? { outcome: { outcome: 'selected', optionId: allowOnce.optionId } }
        : { outcome: { outcome: 'cancelled' } }
    }

    // 4. Ask the phone.
    if (!rt.hubConnected) return { outcome: { outcome: 'cancelled' } }
    const ac = new AbortController()
    s.asks.add(ac)
    rt.sessionsChanged(0)
    try {
      const res = await rt.ask({
        id: interactionId(runtimeId, id, 'permission', params?.toolCall?.toolCallId || `${now()}`),
        kind: 'permission',
        session_id: id,
        tool_use_id: params?.toolCall?.toolCallId,
        tool_name: card.tool_name,
        tool_input: card.tool_input,
        summary: card.summary,
        ...(card.always_label ? { always_label: card.always_label } : {}),
      }, { signal: ac.signal })
      if (res?.allow === true) {
        const picked = res.always === true ? allowAlways : allowOnce
        return picked
          ? { outcome: { outcome: 'selected', optionId: picked.optionId } }
          : { outcome: { outcome: 'cancelled' } }
      }
      if (res?.allow === false) {
        return rejectOnce
          ? { outcome: { outcome: 'selected', optionId: rejectOnce.optionId } }
          : { outcome: { outcome: 'cancelled' } }
      }
      return { outcome: { outcome: 'cancelled' } }
    } catch {
      return { outcome: { outcome: 'cancelled' } }
    } finally {
      s.asks.delete(ac)
      rt.sessionsChanged(0)
    }
  }

  // ---- session lifecycle & control ----

  async function ensureLoaded(s) {
    await acp.ensureStarted()
    if (s.loadedGeneration === acp.generation && acp.loadedSessions.has(s.id)) return
    if (!s.mcpRoute) s.mcpRoute = await mcpProxy.forSession(s)
    const res = await acp.loadSession({
      sessionId: s.id,
      cwd: s.cwd,
      mcpServers: [s.mcpRoute.acpServer],
    })
    s.loadedGeneration = acp.generation
    applyConfigOptions(s, res?.configOptions)
  }

  async function applySessionConfig(s, { model, permission_mode }) {
    if (permission_mode) {
      if (!VALID_MODES.has(permission_mode)) {
        throw new RpcError(`不支持的权限模式 "${permission_mode}"`, 'bad_request')
      }
      const targetNative = permission_mode === 'plan' ? 'plan' : 'build'
      const prevNative = s.mode === 'plan' ? 'plan' : 'build'
      if (targetNative !== prevNative || permission_mode === 'plan') {
        try {
          const res = await acp.setConfigOption(s.id, 'mode', targetNative)
          applyConfigOptions(s, res?.configOptions)
        } catch (err) {
          if (permission_mode === 'plan') {
            throw new RpcError(`opencode 无法切换到只读模式（plan）：${err.message}`, 'readonly_unsupported')
          }
          throw new RpcError(`opencode 模式切换失败：${err.message}`, 'opencode')
        }
      }
      s.mode = permission_mode
    }
    if (model) {
      if (catalog.length === 0) {
        try { await models() } catch {}
      }
      const resolved = resolveModel(model, catalog)
      if (resolved && resolved.id !== s.model) {
        try {
          const res = await acp.setConfigOption(s.id, 'model', resolved.id)
          applyConfigOptions(s, res?.configOptions)
          s.model = resolved.id
        } catch (err) {
          throw new RpcError(`opencode 切换模型失败（${resolved.id}）：${err.message}`, 'opencode')
        }
      }
    }
  }

  async function start(params = {}, onSessionId) {
    rejectSupervisionPolicy(params)
    const cwd = typeof params.cwd === 'string' && params.cwd.trim() ? resolve(params.cwd.trim()) : ''
    if (!cwd) throw new RpcError('需要选择一个目录', 'bad_request')

    const reqMode = typeof params.permission_mode === 'string' && params.permission_mode
      ? params.permission_mode
      : (typeof params.mode === 'string' ? params.mode : '')
    if (reqMode && !VALID_MODES.has(reqMode)) {
      throw new RpcError(`不支持的权限模式 "${reqMode}"`, 'bad_request')
    }

    let chosenModel = null
    if (typeof params.model === 'string' && params.model.trim()) {
      if (catalog.length === 0) {
        try { await models() } catch {}
      }
      chosenModel = resolveModel(params.model, catalog)
    }

    const isAsst = assistantEnabled && (params.assistant === true || isAssistantDir(cwd, home))
    const route = await mcpProxy.allocate({ assistant: isAsst, mode: reqMode || 'default' })
    let sessionId = ''
    try {
      await acp.ensureStarted()
      const created = await acp.newSession({ cwd, mcpServers: [route.acpServer] })
      sessionId = typeof created?.sessionId === 'string' ? created.sessionId.trim() : ''
      if (!sessionId) throw new RpcError('opencode ACP 没有返回新会话 ID', 'opencode')

      const initialTitle = typeof params.prompt === 'string' && params.prompt.trim()
        ? snippet(visibleUserText(params.prompt))
        : ''
      const s = createLiveState(sessionId, cwd, {
        assistant: isAsst,
        title: initialTitle,
        model: chosenModel?.id || '',
        mode: reqMode || 'default',
        startedByPersona: true,
      })
      s.loadedGeneration = acp.generation
      s.mcpRoute = route
      route.bind(s)
      onSessionId?.(sessionId)
      applyConfigOptions(s, created.configOptions)

      // Strictly apply plan mode (readonly) before any prompt runs.
      if (reqMode === 'plan') {
        await applySessionConfig(s, { permission_mode: 'plan' })
      }
      const targetModel = chosenModel?.id || (configuredDefaultModel && configuredDefaultModel !== s.model ? configuredDefaultModel : '')
      if (targetModel) {
        await applySessionConfig(s, { model: targetModel })
      }
      if (isAsst) void refreshGateRules()

      const hasInput = Boolean(
        (typeof params.prompt === 'string' && params.prompt.trim()) ||
        (Array.isArray(params.images) && params.images.some((i) => i?.data)),
      )
      if (hasInput) {
        await send({
          session_id: sessionId,
          text: params.prompt ?? '',
          images: params.images,
          client_msg_id: params.client_msg_id,
          assistant: isAsst,
          mode: reqMode || undefined,
        })
      }

      listCache.at = 0
      rt.sessionsChanged(0)
      return { session: liveRow(s) }
    } catch (err) {
      route.release()
      if (sessionId) {
        live.delete(sessionId)
        await acp.closeSession(sessionId).catch(() => {})
      }
      if (err instanceof RpcError) throw err
      throw new RpcError(`opencode：${err?.message ?? err}`, 'opencode')
    }
  }

  async function resolveSession(id) {
    let s = live.get(id)
    if (s) return s
    if (closedSessions.has(id)) throw new RpcError('opencode 会话已关闭', 'closed')
    const dbData = await readDbItems(id)
    if (!dbData) throw new RpcError(`opencode 里没有这个会话（${id}）`, 'not_found')
    const cwd = dbData.meta.directory || home
    s = createLiveState(id, cwd, {
      assistant: assistantEnabled && isAssistantDir(cwd, home),
      title: dbData.meta.title || basename(cwd) || 'opencode',
      model: dbData.model || '',
      mode: 'default',
    })
    s.liveItems = [...dbData.items]
    s.liveSeq = dbData.toSeq
    s.pushedSeq = dbData.toSeq
    s.lastText = dbData.lastText
    s.hasAssistant = dbData.hasAssistant
    s.history = dbData.history
    return s
  }

  async function send(params) {
    rejectSupervisionPolicy(params)
    const id = params.session_id
    const s = await resolveSession(id)
    const rawText = typeof params.text === 'string' ? params.text : ''
    const images = (Array.isArray(params.images) ? params.images : []).filter((i) => i?.data)
    if (!rawText.trim() && images.length === 0) {
      throw new RpcError('消息是空的', 'bad_request')
    }

    const isAsst = assistantEnabled && (params.assistant === true || s.assistant)
    s.assistant = isAsst
    if (typeof params.memory_dir === 'string' && params.memory_dir) s.memoryDir = params.memory_dir
    const reqMode = params.mode || params.permission_mode || ''
    const wasBusy = s.running || s.queued > 0
    s.queued++

    const promptText = isAsst && typeof params.context === 'string' && params.context.trim()
      ? promptWithContext(rawText, params.context)
      : rawText

    // Validate model/mode upfront when idle so invalid requests fail immediately instead of asynchronously.
    if (!wasBusy) {
      if (reqMode && !VALID_MODES.has(reqMode)) {
        s.queued--
        throw new RpcError(`不支持的权限模式 "${reqMode}"`, 'bad_request')
      }
      if (params.model) {
        try {
          if (catalog.length === 0) await models().catch(() => {})
          resolveModel(params.model, catalog)
        } catch (err) {
          s.queued--
          throw err
        }
      }
    }

    const turnDone = s.tail.catch(() => {}).then(async () => {
      s.queued = Math.max(0, s.queued - 1)
      if (stopped || closedSessions.has(id)) return
      s.running = true
      s.abort = new AbortController()
      s.activeText = ''
      s.activeThought = ''
      s.activeTools.clear()

      const shown = visibleUserText(rawText)
      if (shown) {
        s.userInputCount++
        if (params.client_msg_id) {
          s.sentClientMsgIds.set(s.userInputCount, { text: shown, id: params.client_msg_id })
        }
        appendLiveItem(s, {
          role: 'user',
          kind: 'text',
          text: shown,
          ...(params.client_msg_id ? { client_msg_id: params.client_msg_id } : {}),
        })
      }
      for (const img of images) {
        appendLiveItem(s, {
          role: 'user',
          kind: 'image',
          image: { mime: img.mime || img.media_type || 'image/png', data: img.data },
        })
      }
      pushLiveSnapshot(s)
      rt.sessionsChanged(0)

      try {
        await ensureLoaded(s)
        await applySessionConfig(s, { model: params.model, permission_mode: reqMode })
        const promptBlocks = []
        if (promptText) promptBlocks.push({ type: 'text', text: promptText })
        for (const img of images) {
          promptBlocks.push({
            type: 'image',
            data: img.data,
            mimeType: img.mime || img.media_type || 'image/png',
          })
        }
        const res = await acp.prompt(id, promptBlocks, 0)
        flushLiveText(s)
        if (res?.stopReason === 'cancelled') {
          appendLiveItem(s, { role: 'system', kind: 'notice', text: '已中断' })
        }
      } catch (err) {
        flushLiveText(s)
        if (s.abort?.signal.aborted) {
          appendLiveItem(s, { role: 'system', kind: 'notice', text: '已中断' })
        } else {
          appendLiveItem(s, { role: 'system', kind: 'notice', text: `出错了：${snippet(err?.message ?? String(err))}` })
        }
      } finally {
        rt.streamEnd(id)
        for (const ac of s.asks) ac.abort()
        s.asks.clear()
        s.running = false
        s.abort = null
        s.updatedAt = now()
        listCache.at = 0
        pushLiveSnapshot(s)
        rt.sessionsChanged(0)
      }
    })
    s.tail = turnDone
    s.activeTurnDone = turnDone
    return wasBusy ? { queued: true } : {}
  }

  async function interrupt(params) {
    const id = params.session_id
    const s = live.get(id)
    if (!s) return { settled: true }
    s.abort?.abort()
    for (const ac of s.asks) ac.abort()
    s.asks.clear()
    acp.cancel(id)
    if (!s.running) return { settled: true }
    const settled = await Promise.race([
      s.tail.then(() => true, () => true),
      new Promise((r) => {
        const t = setTimeout(() => r(false), INTERRUPT_SETTLE_MS)
        t.unref?.()
      }),
    ])
    return settled ? { settled: true } : {}
  }

  async function modes(params) {
    const s = await resolveSession(params.session_id)
    await ensureLoaded(s)
    await applySessionConfig(s, { model: params.model, permission_mode: params.permission_mode })
    listCache.at = 0
    rt.sessionsChanged(0)
  }

  async function rename(params) {
    const title = typeof params.title === 'string' ? params.title.trim() : ''
    if (!title) throw new RpcError('名字是空的', 'bad_request')
    const s = await resolveSession(params.session_id)
    s.title = title
    s.updatedAt = now()
    rt.sessionsChanged(0)
  }

  async function close(params) {
    const id = params.session_id
    const s = live.get(id)
    closedSessions.add(id)
    if (s) {
      s.abort?.abort()
      for (const ac of s.asks) ac.abort()
      s.asks.clear()
      acp.cancel(id)
      await acp.closeSession(id)
      s.mcpRoute?.release()
      s.mcpRoute = null
      live.delete(id)
    }
    listCache.at = 0
    rt.streamEnd(id)
    rt.sessionsChanged(0)
  }

  async function startCompanion() {
    stopped = false
    rt.start()
    void models().catch((err) => log(`persona: opencode initial models: ${err.message}`))
  }

  async function stopCompanion() {
    stopped = true
    for (const s of live.values()) {
      s.abort?.abort()
      for (const ac of s.asks) ac.abort()
      s.asks.clear()
      s.mcpRoute?.release()
    }
    live.clear()
    mcpProxy.stop()
    rt.stop()
    await acp.stop()
    try { db?.close() } catch {}
    db = undefined
  }

  return {
    start: startCompanion,
    stop: stopCompanion,
    runtime: rt,
    acp,
    live,
    models,
  }
}
