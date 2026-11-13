// The DSH runtime's implementation (docs/integrations/dsh.md): what index.js — the loader DSH keeps — imports from the version it
// runs, the shipped one or one the node sent (docs/integrations/runtimes.md "Auto-update"). It wires DSH's Cordis services into
// createDshRuntime; everything here may change from one version to the next, the loader contract (api, start) may not.

import { readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { createDshRuntime } from './dsh.js'

/** The loader contract this implementation speaks (runtime-sdk/lib/loader.js LOADER_API). */
export const api = 1

/** Read an optional service without declaring it (absent in profiles without it). */
function optional(ctx, key) {
  try {
    return typeof ctx.get === 'function' ? ctx.get(key) : undefined
  } catch {
    return undefined
  }
}

// A runtime id, as the node takes it ([A-Za-z0-9][A-Za-z0-9_.-]*, up to 128); launcher names follow the same rule.
const ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/

/** A configured id, or the default; a bad one fails the plugin's load (a typo must not join as `dsh` silently). */
function idOf(config, key, fallback) {
  const v = config[key]
  if (v === undefined || v === null || v === '') return fallback
  if (typeof v !== 'string' || !ID.test(v)) throw new Error(`persona-dsh-runtime: config.${key} must match ${ID}, got ${JSON.stringify(v)}`)
  return v
}

const MODES = ['default', 'acceptEdits', 'auto', 'plan', 'bypassPermissions']

/** config.presets: Persona permission mode -> DSH preset name; anything else fails the load. */
function presetsOf(p) {
  if (p === undefined || p === null) return undefined
  if (typeof p !== 'object' || Array.isArray(p)) throw new Error('persona-dsh-runtime: config.presets must map Persona permission modes to DSH presets')
  for (const [mode, name] of Object.entries(p)) {
    if (!MODES.includes(mode) || typeof name !== 'string' || name === '') {
      throw new Error(`persona-dsh-runtime: config.presets.${mode}: a mode of ${MODES.join(' / ')} to a DSH preset name`)
    }
  }
  return p
}

/**
 * listen in the config: be a daemon the node dials (docs/integrations/daemons.md) — {port, host?, path?, token | token_file} — for a
 * DSH that cannot reach the node's runtime.sock (another system user, a container, another machine). The token file
 * is read at every connection.
 */
function listenOf(l) {
  if (!l || typeof l !== 'object' || !Number.isInteger(l.port) || l.port < 0 || l.port > 65535) return undefined
  const file = typeof l.token_file === 'string' && l.token_file !== '' ? l.token_file.replace(/^~(?=\/)/, homedir()) : undefined
  const token = typeof l.token === 'string' && l.token !== '' ? l.token : file ? () => readFileSync(file, 'utf8').trim() : undefined
  if (!token) return undefined
  const out = { port: l.port, token }
  if (typeof l.host === 'string' && l.host !== '') out.host = l.host
  if (typeof l.path === 'string' && l.path.startsWith('/')) out.path = l.path
  return out
}

/**
 * Start the runtime for one DSH scope.
 * @param {{ ctx: import('@deepseek-ai/cordis').Context, config?: object, package?: {name: string, hash: string},
 *   onUpdate?: Function, log?: (msg: string) => void }} host - from the loader: ctx is the scope that has DSH's
 *   sessionController; config is the plugin's config: { socket?, interactions?: 'auto'|'phone'|'browser', listDays?,
 *   maxSessions?, runtime?, name?, launcher?, phoneTools?, assistant?, presets?, listen?, autoUpdate? }.
 *   runtime / name / launcher: how this DSH appears in Persona (default dsh / DSH / dsh); a second DSH on the same
 *   machine needs its own. assistant (default true): its launcher may run the Persona assistant (docs/integrations/dsh.md "Assistant");
 *   presets: the DSH permission preset per Persona permission mode there. listen: be a daemon the node dials instead
 *   (listenOf). autoUpdate is the loader's.
 * @returns {{ stop(): void, busy(): boolean, fetchPackage(name: string): Promise<object> }}
 */
export function start(host) {
  const scope = host.ctx
  const config = host.config ?? {}
  const runtimeID = idOf(config, 'runtime', 'dsh')
  const launcher = idOf(config, 'launcher', runtimeID === 'dsh' ? 'dsh' : runtimeID)
  const label = typeof config.name === 'string' && config.name.trim() !== '' ? config.name.trim() : 'DSH'
  const runtime = createDshRuntime({
    controller: scope.sessionController,
    on: (event, listener, options) => scope.on(event, listener, options),
    userQuestions: optional(scope, 'userQuestions'),
    workspace: optional(scope, 'workspaceRegistry'),
    models: optional(scope, 'modelRegistry') ?? optional(scope, 'models') ?? optional(scope, 'providerRegistry'),
    registerTool: config.phoneTools === false ? undefined : (definition, opts) => {
      const tools = optional(scope, 'tools')
      return typeof tools?.register === 'function' ? tools.register(definition, opts) : undefined
    },
    agents: optional(scope, 'agents'),
    permissionPresets: optional(scope, 'permissionPresets'),
    jobs: optional(scope, 'jobs'),
    hasLiveClient: () => {
      const gateway = optional(scope, 'typertGateway')
      return typeof gateway?.hasLiveClient === 'function' ? gateway.hasLiveClient() : true
    },
    log: host.log ?? ((message) => scope.logger?.info?.(message)),
  }, {
    runtime: runtimeID,
    name: label,
    launcher,
    socketPath: typeof config.socket === 'string' && config.socket !== '' ? config.socket : undefined,
    interactions: ['auto', 'phone', 'browser'].includes(config.interactions) ? config.interactions : 'auto',
    listDays: Number.isFinite(config.listDays) && config.listDays > 0 ? config.listDays : 7,
    maxSessions: Number.isFinite(config.maxSessions) && config.maxSessions > 0 ? config.maxSessions : 80,
    assistant: config.assistant !== false,
    presets: presetsOf(config.presets),
    listen: listenOf(config.listen),
    package: host.package,
    onUpdate: host.onUpdate,
  })
  runtime.start()
  return {
    stop: () => runtime.stop(),
    busy: () => runtime.busy(),
    fetchPackage: (name) => runtime.fetchPackage(name),
  }
}
