import { lstatSync, statSync } from 'node:fs'
import { join } from 'node:path'

export const LEGACY_SUBPROTOCOL = 'jarvis-runtime.v1'
export const LEGACY_LISTEN_PATH = '/jarvis/runtime'
const LEGACY_ROOT = '.jarvis'
const LEGACY_PREFIX = 'JARVIS_'
const PACKAGES = {
  'persona-dsh-runtime': 'jarvis-dsh-runtime',
  'persona-pi-runtime': 'jarvis-pi-runtime',
  'persona-paseo-runtime': 'jarvis-paseo-runtime',
  'persona-agent-sdk-runtime': 'jarvis-agent-sdk-runtime',
}

export function legacyEnvName(name) { return LEGACY_PREFIX + name.slice('PERSONA_'.length) }
export function wirePackageName(name) { return PACKAGES[name] ?? name }
export function addPackageAliases(packages) {
  for (const [canonical, legacy] of Object.entries(PACKAGES)) packages[legacy] = packages[canonical]
  return packages
}
export function defaultStateRoot(home) {
  const canonical = join(home, '.persona'), legacy = join(home, LEGACY_ROOT)
  const present = (p) => {
    try { lstatSync(p); return true } catch (err) { if (err.code === 'ENOENT') return false; throw err }
  }
  const hasCanonical = present(canonical), hasLegacy = present(legacy)
  if (hasCanonical && hasLegacy) {
    const a = statSync(canonical), b = statSync(legacy)
    if (a.dev !== b.dev || a.ino !== b.ino) throw new Error('independent state directories exist; set PERSONA_HOME explicitly')
  }
  return !hasCanonical && hasLegacy ? legacy : canonical
}
export function assistantRootName(name) { return name === '.persona' || name === LEGACY_ROOT }
export function runtimeStorePath(root, name) {
  const canonical = join(root, 'runtime-packages', name), legacy = join(root, 'runtime-packages', wirePackageName(name))
  const present = (p) => { try { lstatSync(p); return true } catch { return false } }
  return !present(canonical) && present(legacy) ? legacy : canonical
}
export function defaultListenPathMatches(actual, configured, canonical) {
  return actual === configured || (configured === canonical && actual === LEGACY_LISTEN_PATH)
}
export const MCP_SERVER_NAMES = ['persona', 'jarvis']
// The snapshot Persona starts an assistant message with, and the view feedback the node may put after it (#4335):
// both for the model, not the user's words.
export const SNAPSHOT_RE = /<(persona|jarvis)-context>[\s\S]*?<\/\1-context>\s*|<persona-view-errors\b[^>]*>[\s\S]*?<\/persona-view-errors>\s*/g
export const SNAPSHOT_END_RE = /^\s*<\/(?:persona|jarvis)-context>\s*/i
export function contextWrapped(text) { return /^(?:<persona-context>|<jarvis-context>)/.test(text) }
export function mcpToolNames(name) { return MCP_SERVER_NAMES.map((server) => `mcp__${server}__${name}`) }
export function stripMCPPrefix(name) { return name.replace(/^mcp__(?:persona|jarvis)__/, '') }
