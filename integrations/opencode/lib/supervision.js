// Authority is supplied through an isolated worker's configuration or ACP
// systemPrompt, never through user text or shared workspace instructions.
import { execFile } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { mkdir, mkdtemp, writeFile, readFile, realpath, rm } from 'node:fs/promises'
import { join, relative, resolve, isAbsolute, dirname, basename } from 'node:path'
import { promisify } from 'node:util'
import { RpcError } from '../../runtime-sdk/index.js'
import { OpencodeAcpClient, privateAcpArgs } from './acp.js'
import { stripJsonc } from './models.js'

export const VERIFIED_POLICY_VERSIONS = new Set(['1.19.6'])
const runFile = promisify(execFile)
const MAX_POLICY_BYTES = 128 * 1024
const refusal = () => new RpcError('OpenCode could not apply isolated follow-up instructions. Nothing was sent.', 'assistant_supervision_runtime')
const isObject = (value) => value !== null && typeof value === 'object' && !Array.isArray(value)
const contained = (parent, path) => { const rel = relative(parent, path); return rel === '' || !rel.startsWith('..') && !isAbsolute(rel) }

export function supportsSystemPrompt(capabilities) {
  // ACP: {} advertises append; mode only lists additional delivery modes.
  return isObject(capabilities?.sessionCapabilities?.systemPrompt)
}

export function policyEnvironment(env, file, route) {
  const content = env.OPENCODE_CONFIG_CONTENT
  const config = content ? JSON.parse(stripJsonc(content)) : {}
  if (!isObject(config) || config.instructions !== undefined && (!Array.isArray(config.instructions) || config.instructions.some((p) => typeof p !== 'string'))) throw refusal()
  if (file) config.instructions = [...new Set([...(config.instructions ?? []), file])]
  if (route) {
    if (config.mcp !== undefined && !isObject(config.mcp)) throw refusal()
    config.mcp = { ...config.mcp, persona: route.remoteConfig }
  }
  return { ...env, OPENCODE_CONFIG_CONTENT: JSON.stringify(config) }
}

export function createSupervisionStore({ home, bin, env, directory, run = runFile, acpFactory = (options) => new OpencodeAcpClient({ ...options, args: privateAcpArgs() }) }) {
  const root = resolve(directory || join(home, '.local', 'state', 'persona', 'opencode-supervision'))
  let ready = null
  let rootReady
  const owned = new Set()
  async function ensureRoot() {
    rootReady ??= (async () => {
      const persona = resolve(home, '.persona')
      if (contained(persona, root)) throw refusal()
      const actualPersona = await realpath(persona).catch(() => persona)
      // Resolve existing ancestors before creating anything: a symlink from
      // the configured root must not create a directory in shared Persona data.
      let ancestor = root
      const missing = []
      for (;;) {
        try {
          const actual = await realpath(ancestor)
          if (contained(actualPersona, resolve(actual, ...missing))) throw refusal()
          break
        } catch (error) {
          if (error.code !== 'ENOENT') throw error
          missing.unshift(basename(ancestor)); ancestor = dirname(ancestor)
        }
      }
      await mkdir(root, { recursive: true, mode: 0o700 })
      const actualRoot = await realpath(root)
      if (contained(actualPersona, actualRoot)) throw refusal()
    })()
    return rootReady
  }
  async function create(text) {
    if (typeof text !== 'string' || Buffer.byteLength(text) > MAX_POLICY_BYTES) throw refusal()
    if (!text) return null
    await ensureRoot()
    const dir = await mkdtemp(join(root, 'session-'))
    owned.add(dir)
    const file = join(dir, 'policy.md')
    try {
      await writeFile(file, text, { mode: 0o600, flag: 'wx' })
      return { dir, file, text }
    } catch (error) { await remove({ dir }); throw error }
  }
  async function remove(record) {
    if (!record || !owned.has(record.dir)) return
    await rm(record.dir, { recursive: true, force: true })
    owned.delete(record.dir)
  }
  async function verify(record, route) {
    if (!record || await readFile(record.file, 'utf8') !== record.text) throw refusal()
    const workerEnv = policyEnvironment(env, record.file, route)
    const { stdout } = await run(bin, ['debug', 'config'], { env: workerEnv, cwd: home, timeout: 15_000, maxBuffer: 4 * 1024 * 1024 })
    const config = JSON.parse(stdout)
    if (!Array.isArray(config.instructions) || !config.instructions.includes(record.file)) throw refusal()
    const prior = env.OPENCODE_CONFIG_CONTENT ? JSON.parse(stripJsonc(env.OPENCODE_CONFIG_CONTENT)).instructions ?? [] : []
    if (prior.some((file) => !config.instructions.includes(file))) throw refusal()
    if (route && (config.mcp?.persona?.url !== route.remoteConfig.url)) throw refusal()
    return workerEnv
  }
  return {
    get ready() { return ready },
    create,
    remove,
    verify,
    async check(record) {
      if (record && await readFile(record.file, 'utf8') !== record.text) throw refusal()
    },
    async probe() {
      ready = null
      let record, client
      try {
        const { stdout } = await run(bin, ['--version'], { env, cwd: home, timeout: 10_000, maxBuffer: 4096 })
        const version = stdout.trim()
        if (!VERIFIED_POLICY_VERSIONS.has(version)) return null
        record = await create(`Persona isolated instruction probe ${randomUUID()}`)
        const probeEnv = await verify(record)
        client = acpFactory({ bin, cwd: home, env: probeEnv, processGroup: true })
        const initialized = await client.ensureStarted(15_000)
        if (initialized.agentInfo?.version !== version) throw refusal()
        ready = { version, channel: supportsSystemPrompt(initialized.capabilities) ? 'append' : 'config' }
        return ready
      } catch {
        // Never log config stdout/stderr: it can contain user provider settings.
        ready = null
        return null
      } finally {
        await client?.stop().catch(() => {})
        await remove(record)
      }
    },
    async stop() {
      ready = null
      await Promise.all([...owned].map((dir) => remove({ dir })))
    },
  }
}
