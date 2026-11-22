#!/usr/bin/env node
// The DSH companion (docs/integrations/dsh.md "When DSH is not running"): the Persona plugin lives in `dsh web`, so while DSH is not running
// Persona would see none of its sessions. This small resident process lists the recent sessions in DSH's store as
// offline and pages them, read-only. Sending to one runs the wake command (how this machine starts DSH: a launchd or
// systemd service, say) and hands the message over to the plugin once DSH runs; while it runs, the plugin's rows win.
//
//   node integrations/dsh/companion.mjs --dsh <DSH install or checkout> [--home <DSH_HOME>] [--wake '<command>']
//     [--runtime dsh-files] [--name DSH] [--socket <runtime.sock>]
//
// It reads with DSH's own session store (@deepseek-ai/dsh-session-persistence-jsonl) from that DSH. --wake has no
// default: without it a send is still handed over, and reaches DSH only if DSH is running.

import { spawn } from 'node:child_process'
import { existsSync, readFileSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import { homedir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createDshFiles } from './lib/files.js'

function arg(name) {
  const i = process.argv.indexOf(`--${name}`)
  return i > 0 ? process.argv[i + 1] : undefined
}

/** An import from the DSH install: resolved from its CLI package (an npm install) or its checkout's base bundle. */
function dshImporter(dir) {
  const bases = [join(dir, 'package.json'), join(dir, 'apps', 'cli', 'package.json'), join(dir, 'packages', 'bundle', 'base', 'package.json')]
  return async (specifier) => {
    for (const base of bases.filter((b) => existsSync(b))) {
      try {
        return await import(pathToFileURL(createRequire(base).resolve(specifier)).href)
      } catch {
        // not resolvable from this base: the next one
      }
    }
    throw new Error(`${specifier} is not found under ${dir}: pass --dsh <the DSH install or a built checkout>`)
  }
}

const dshDir = arg('dsh') || process.env.DSH_DIR
if (!dshDir) {
  console.error('persona DSH companion: --dsh <DSH install or checkout> is required')
  process.exit(2)
}
const home = resolve(arg('home') || process.env.DSH_HOME || join(homedir(), '.dsh'))
const wakeCommand = arg('wake')
const imp = dshImporter(resolve(dshDir))
const { Context } = await imp('@deepseek-ai/cordis')
const Persistence = await imp('@deepseek-ai/dsh-session-persistence-jsonl')
const ctx = new Context()
ctx.plugin(Persistence, { root: join(home, 'sessions') })
const persistence = await new Promise((done, fail) => {
  const start = Date.now()
  const look = () => {
    const p = ctx.get('sessionPersistence')
    if (p) return done(p)
    if (Date.now() - start > 10_000) return fail(new Error('DSH\'s session store did not load'))
    setTimeout(look, 50)
  }
  look()
})

const log = (m) => console.error(m)
const files = createDshFiles({
  persistence,
  readJson: (path) => {
    try {
      return JSON.parse(readFileSync(path, 'utf8'))
    } catch {
      return undefined // missing, or being rewritten by DSH: read again next time
    }
  },
  mtime: (path) => {
    try {
      return statSync(path).mtimeMs
    } catch {
      return undefined // gone since it was listed
    }
  },
  wake: wakeCommand ? () => new Promise((done, fail) => {
    log('persona DSH companion: waking DSH')
    const child = spawn('/bin/sh', ['-c', wakeCommand], { stdio: 'ignore' })
    const timer = setTimeout(() => child.kill('SIGTERM'), 30_000)
    child.once('error', (err) => { clearTimeout(timer); fail(err) })
    child.once('exit', (code) => { clearTimeout(timer); code === 0 ? done() : fail(new Error(`the wake command exited ${code}`)) })
  }) : undefined,
  log,
}, { home, runtime: arg('runtime') || undefined, name: arg('name') || undefined, socketPath: arg('socket') || undefined })
files.start()
// The SDK's timers do not hold a process; this one is only the runtime.
const alive = setInterval(() => {}, 1 << 30)
log(`persona DSH companion: listing the sessions in ${join(home, 'sessions')}`)
for (const sig of ['SIGTERM', 'SIGINT']) process.on(sig, () => { clearInterval(alive); files.stop(); process.exit(0) })
