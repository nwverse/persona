import { assistantSearchPaths } from './compat.js'
// Persistent stdio JSON-RPC client for `agy acp`.
import { spawn } from 'node:child_process'
import { EventEmitter } from 'node:events'
import { createInterface } from 'node:readline'

export class AgyAcpClient extends EventEmitter {
  constructor(options = {}) {
    super()
    this.bin = options.bin || 'agy'
    this.args = options.args || ['acp']
    this.log = options.log || (() => {})
    this.onPermissionRequest = options.onPermissionRequest || null
    this.proc = null
    this.readyPromise = null
    this.nextId = 1
    this.pending = new Map()
    this.loadedSessions = new Map() // sessionId -> boolean (hasMcp)
    this.availableModels = null
    this.agentVersion = ''
    this.closed = false
  }

  async ensureStarted() {
    if (this.proc && this.readyPromise) return this.readyPromise
    this.readyPromise = this._start()
    return this.readyPromise
  }

  async _start() {
    const home = process.env.HOME || '/root'
    const extraPath = assistantSearchPaths(home).join(':')
    const child = spawn(this.bin, this.args, {
      stdio: ['pipe', 'pipe', 'pipe'],
      env: { ...process.env, PATH: `${extraPath}:${process.env.PATH || ''}` },
    })
    this.proc = child
    this.loadedSessions.clear()

    const rl = createInterface({ input: child.stdout, crlfDelay: Infinity })
    rl.on('line', (line) => {
      const trimmed = line.trim()
      if (!trimmed) return
      let msg
      try {
        msg = JSON.parse(trimmed)
      } catch {
        return
      }
      this._onMessage(msg)
    })

    child.stderr.on('data', (chunk) => {
      const s = String(chunk).trim()
      if (s) this.log(`agy acp stderr: ${s}`)
    })

    child.on('exit', (code, sig) => {
      this.log(`agy acp exited (code=${code}, sig=${sig})`)
      this.proc = null
      this.readyPromise = null
      this.loadedSessions.clear()
      const err = new Error(`agy acp exited (${code ?? sig})`)
      for (const [, p] of this.pending) p.reject(err)
      this.pending.clear()
    })

    const init = await this._rawCall('initialize', {
      protocolVersion: 1,
      clientCapabilities: {},
      clientInfo: { name: 'persona', version: '0.1.0' },
    })
    this.agentVersion = init?.agentInfo?.version || ''
    return init
  }

  _send(obj) {
    if (!this.proc || !this.proc.stdin.writable) {
      throw new Error('agy acp process is not writable')
    }
    this.proc.stdin.write(`${JSON.stringify(obj)}\n`)
  }

  _rawCall(method, params, timeoutMs = 60_000) {
    const id = this.nextId++
    return new Promise((resolve, reject) => {
      const timer = timeoutMs > 0 ? setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`agy acp ${method} timed out after ${timeoutMs}ms`))
      }, timeoutMs) : null
      this.pending.set(id, {
        resolve: (res) => { clearTimeout(timer); resolve(res) },
        reject: (err) => { clearTimeout(timer); reject(err) },
      })
      try {
        this._send({ jsonrpc: '2.0', id, method, params })
      } catch (err) {
        clearTimeout(timer)
        this.pending.delete(id)
        reject(err)
      }
    })
  }

  /** Send a JSON-RPC notification (no id, no answer). False when no agy process is running to take it. */
  notify(method, params) {
    if (!this.proc || !this.proc.stdin.writable) return false
    try {
      this._send({ jsonrpc: '2.0', method, params })
      return true
    } catch (err) {
      this.log(`agy acp: could not send ${method}: ${err.message}`)
      return false
    }
  }

  async call(method, params, timeoutMs = 60_000) {
    await this.ensureStarted()
    return this._rawCall(method, params, timeoutMs)
  }

  _onMessage(msg) {
    if (msg.id !== undefined && (msg.result !== undefined || msg.error !== undefined)) {
      const entry = this.pending.get(msg.id)
      if (!entry) return
      this.pending.delete(msg.id)
      if (msg.error) {
        const err = new Error(msg.error.message || `ACP error ${msg.error.code}`)
        err.code = msg.error.code
        entry.reject(err)
      } else {
        entry.resolve(msg.result)
      }
      return
    }

    if (msg.id !== undefined && msg.method) {
      void this._onServerRequest(msg.id, msg.method, msg.params)
      return
    }

    if (msg.method === 'session/update' && msg.params) {
      this.emit('update', msg.params)
    }
  }

  async _onServerRequest(id, method, params) {
    try {
      if (method === 'session/request_permission' && this.onPermissionRequest) {
        const result = await this.onPermissionRequest(params)
        this._send({ jsonrpc: '2.0', id, result })
        return
      }
      // Default fallback for unhandled permission requests: select allow_once if available
      if (method === 'session/request_permission') {
        const opts = Array.isArray(params?.options) ? params.options : []
        const allow = opts.find((o) => o.kind === 'allow_once' || o.kind === 'allow_always') || opts[0]
        this._send({
          jsonrpc: '2.0',
          id,
          result: allow ? { outcome: { outcome: 'selected', optionId: allow.optionId } } : { outcome: { outcome: 'cancelled' } },
        })
        return
      }
      this._send({ jsonrpc: '2.0', id, error: { code: -32601, message: `Method not found: ${method}` } })
    } catch (err) {
      this._send({ jsonrpc: '2.0', id, error: { code: -32000, message: err.message || 'Internal error' } })
    }
  }

  async listSessions() {
    const res = await this.call('session/list', {}, 20_000)
    return Array.isArray(res?.sessions) ? res.sessions : []
  }

  async newSession({ cwd, mcpServers = [] }) {
    const res = await this.call('session/new', { cwd, mcpServers }, 30_000)
    if (res?.sessionId) {
      this.loadedSessions.set(res.sessionId, mcpServers.length > 0)
      if (res.models?.availableModels) this.availableModels = res.models.availableModels
    }
    return res
  }

  async ensureSessionLoaded(sessionId, cwd, mcpServers = []) {
    await this.ensureStarted()
    if (this.loadedSessions.has(sessionId) && (mcpServers.length === 0 || this.loadedSessions.get(sessionId))) {
      return
    }
    const res = await this.call('session/resume', { sessionId, cwd: cwd || process.env.HOME || '/tmp', mcpServers }, 30_000)
    this.loadedSessions.set(sessionId, mcpServers.length > 0)
    if (res?.models?.availableModels) this.availableModels = res.models.availableModels
    return res
  }

  async setModel(sessionId, modelId) {
    if (!modelId) return
    await this.call('session/set_model', { sessionId, modelId }, 15_000)
  }

  async setMode(sessionId, modeId) {
    if (!modeId) return
    await this.call('session/set_mode', { sessionId, modeId }, 15_000)
  }

  async setConfigOption(sessionId, configId, value) {
    if (!configId || value === undefined || value === '') return
    await this.call('session/set_config_option', { sessionId, configId, value }, 15_000)
  }

  async prompt(sessionId, promptBlocks) {
    // Turn execution can take minutes
    return this.call('session/prompt', { sessionId, prompt: promptBlocks }, 0)
  }

  // ACP's session/cancel is a notification: the agent ends the running session/prompt with stopReason 'cancelled' and
  // answers nothing else (sent as a request, an agent that follows the spec rejects it and keeps running). With no agy
  // process there is no turn to cancel.
  async cancel(sessionId) {
    this.notify('session/cancel', { sessionId })
  }

  async closeSession(sessionId) {
    this.loadedSessions.delete(sessionId)
    try {
      return await this.call('session/close', { sessionId }, 10_000)
    } catch {
      return {}
    }
  }

  stop() {
    this.closed = true
    if (this.proc) {
      try {
        this.proc.kill('SIGTERM')
      } catch {}
      this.proc = null
    }
  }
}
