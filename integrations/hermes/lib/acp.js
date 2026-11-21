import { spawn } from 'node:child_process'
import { createInterface } from 'node:readline'
import { setTimeout as delay } from 'node:timers/promises'

const RPC_TIMEOUT = 20_000

export class HermesAcp {
  constructor({ bin, cwd, env, onUpdate, onPermission, onExit }) {
    this.processGroup = process.platform !== 'win32'
    const child = spawn(bin, ['acp'], { cwd, env, detached: this.processGroup, stdio: ['pipe', 'pipe', 'pipe'] })
    this.child = child
    this.pending = new Map()
    this.nextId = 0
    this.closed = false
    // Provider logs can contain prompts and credentials.
    child.stderr.resume()
    child.once('exit', () => {
      disconnect()
      if (this.processGroup) this.signal('SIGKILL')
    })
    const lines = createInterface({ input: child.stdout, crlfDelay: Infinity })
    const disconnect = () => {
      if (this.closed) return
      this.closed = true
      lines.close()
      for (const { fail } of this.pending.values()) fail(new Error('Hermes ACP disconnected'))
      onExit?.()
    }
    this.exited = new Promise((resolve) => {
      child.once('close', () => { disconnect(); resolve() })
    })
    child.once('error', disconnect)
    child.stdin.once('error', () => {
      disconnect()
      void this.stop()
    })
    lines.on('line', (line) => {
      let msg
      try { msg = JSON.parse(line) } catch { return }
      if (this.closed || !msg || typeof msg !== 'object') return
      if (msg.method === 'session/update' && msg.id == null) onUpdate?.(msg.params)
      else if (msg.method && msg.id != null) void this.answer(msg, onPermission)
      else if (msg.id != null) this.pending.get(msg.id)?.reply(msg)
    })
  }

  async initialize() {
    const hello = await this.call('initialize', {
      protocolVersion: 1, clientCapabilities: {},
      clientInfo: { name: 'persona', version: '0.1.0' },
    })
    if (hello?.protocolVersion !== 1 || hello.agentInfo?.name !== 'hermes-agent' ||
        !hello.agentCapabilities?.loadSession || !hello.agentCapabilities?.sessionCapabilities?.list) {
      throw new Error('Hermes ACP must support protocol version 1 and session loading and listing')
    }
    return hello.agentCapabilities
  }

  async answer(msg, onPermission) {
    if (msg.method !== 'session/request_permission') {
      this.write({ id: msg.id, error: { code: -32601, message: 'Method not supported' } })
      return
    }
    let outcome
    try { outcome = await onPermission?.(msg.params) } catch {}
    this.write({ id: msg.id, result: { outcome: outcome ?? { outcome: 'cancelled' } } })
  }

  write(message) {
    if (this.closed || !this.child.stdin.writable) return false
    this.child.stdin.write(JSON.stringify({ jsonrpc: '2.0', ...message }) + '\n')
    return true
  }

  notify(method, params) {
    this.write({ method, params })
  }

  call(method, params, timeout = RPC_TIMEOUT) {
    const id = ++this.nextId
    return new Promise((resolve, reject) => {
      const finish = (fn, value) => {
        clearTimeout(timer)
        this.pending.delete(id)
        fn(value)
      }
      const fail = (error) => finish(reject, error)
      const timer = timeout > 0 ? setTimeout(() => fail(new Error(`Hermes ACP ${method} timed out`)), timeout) : null
      this.pending.set(id, {
        fail,
        reply: (msg) => msg.error
          ? fail(new Error(`Hermes ACP ${method} failed (${Number(msg.error.code) || 'unknown'})`))
          : finish(resolve, msg.result),
      })
      if (!this.write({ id, method, params })) fail(new Error('Hermes ACP disconnected'))
    })
  }

  stop() {
    return this.stopping ??= this.terminate()
  }

  signal(signal) {
    const pid = this.child.pid
    if (!pid) return
    try {
      if (this.processGroup) process.kill(-pid, signal)
      else this.child.kill(signal)
    } catch (error) {
      if (error.code !== 'ESRCH') throw error
    }
  }

  async terminate() {
    this.child.stdin.end()
    const timer = new AbortController()
    const exited = (ms) => Promise.race([
      this.exited.then(() => true),
      delay(ms, false, { signal: timer.signal, ref: false }),
    ])
    try {
      if (await exited(1_000)) return
      this.signal('SIGTERM')
      if (await exited(2_000)) return
      this.signal('SIGKILL')
      await this.exited
    } finally {
      timer.abort()
    }
  }
}
