#!/usr/bin/env node
// The pi-web companion (docs/integrations/pi.md "pi-web"): pi-web loads extensions only when a session opens, so after it starts
// Persona would see none of its sessions until one is opened in a browser. This small resident process lists the recent
// pi sessions on disk, pages them, and starts or wakes them through pi-web's HTTP API; the extension inside pi-web takes
// over a session while it runs there (the node prefers the runtime running a session).
//
//   node integrations/pi/companion.mjs [--port 30141] [--host 127.0.0.1] [--socket <runtime.sock>]
//
// It reads pi's sessions with pi's own SessionManager, from the pi install on PATH (or PI_PACKAGE_DIR).
//
// Updates (docs/integrations/pi.md "Automatic updates"): run from a store (<store>/current/pi/companion.mjs, as launchd / systemd keep it),
// it installs the version the node carries as current and exits; its supervisor starts it again on the new code
// (KeepAlive / Restart=always). --no-update: never.

import { execFileSync } from 'node:child_process'
import { existsSync, realpathSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { selfPackage } from '../runtime-sdk/index.js'
import { createPiHost } from './lib/pi.js'

function arg(name) {
  const i = process.argv.indexOf(`--${name}`)
  return i > 0 ? process.argv[i + 1] : undefined
}

/** The @earendil-works/pi-coding-agent package directory: PI_PACKAGE_DIR, beside the pi on PATH, or npm's global root. */
function piPackage() {
  const candidates = []
  if (process.env.PI_PACKAGE_DIR) candidates.push(process.env.PI_PACKAGE_DIR)
  try {
    const bin = realpathSync(execFileSync('/usr/bin/which', ['pi'], { encoding: 'utf8' }).trim()) // …/pi-coding-agent/dist/cli.js
    for (let d = dirname(bin); d !== dirname(d); d = dirname(d)) {
      if (existsSync(join(d, 'package.json')) && d.endsWith(join('@earendil-works', 'pi-coding-agent'))) {
        candidates.push(d)
        break
      }
    }
  } catch {}
  try {
    candidates.push(join(execFileSync('npm', ['root', '-g'], { encoding: 'utf8' }).trim(), '@earendil-works', 'pi-coding-agent'))
  } catch {}
  const found = candidates.find((d) => existsSync(join(d, 'dist', 'index.js')))
  if (!found) throw new Error('pi is not installed (npm i -g @earendil-works/pi-coding-agent), or set PI_PACKAGE_DIR')
  return found
}

const { SessionManager } = await import(pathToFileURL(join(piPackage(), 'dist', 'index.js')).href)
let host = arg('host') || process.env.PI_WEB_HOSTNAME || '127.0.0.1'
if (host === '0.0.0.0' || host === '::') host = '127.0.0.1'
const self = selfPackage(import.meta.url, 'persona-pi-runtime')
let alive
const piHost = createPiHost({
  SessionManager,
  web: { port: Number(arg('port') || process.env.PORT) || 30141, host, password: process.env.PI_WEB_PASSWORD || undefined },
  log: (m) => console.error(m),
}, {
  listDisk: true,
  socketPath: arg('socket') || undefined,
  update: self.store && !process.argv.includes('--no-update') ? {
    name: self.name, hash: self.hash, store: self.store,
    exit: (hash) => {
      console.error(`persona pi-web companion: ${hash.slice(0, 12)} installed; exiting for the supervisor to start it`)
      clearInterval(alive)
      process.exit(0)
    },
  } : undefined,
})
piHost.start()
// The SDK's timers do not hold a process (it usually lives in someone else's: DSH's, pi's). This process is only the
// runtime: without this it would exit whenever it is not connected (no node yet, a node restarting, an older node).
alive = setInterval(() => {}, 1 << 30)
console.error('persona pi-web companion: listing pi sessions for the node')
for (const sig of ['SIGTERM', 'SIGINT']) process.on(sig, () => { clearInterval(alive); piHost.stop(); process.exit(0) })
