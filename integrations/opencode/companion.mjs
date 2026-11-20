#!/usr/bin/env node
// Standalone ACP companion runtime for opencode (docs/integrations/opencode.md).
// Connects opencode to ~/.persona/runtime.sock without requiring a resident `opencode serve` process,
// supporting both new Agent sessions and Persona Assistant conversations via `opencode acp`.
//
//   node integrations/opencode/companion.mjs [--socket <runtime.sock>] [--opencode-bin <path>]
//     [--db <opencode.db>] [--launcher <name>] [--display-name <label>] [--model <provider/model>]
//     [--no-assistant] [--parent-stdin]

import { parseArgs } from 'node:util'
import { createOpencodeCompanion } from './lib/companion.js'

const { values } = parseArgs({
  options: {
    socket: { type: 'string' },
    'opencode-bin': { type: 'string' },
    db: { type: 'string' },
    'config-dir': { type: 'string' },
    launcher: { type: 'string' },
    'display-name': { type: 'string' },
    model: { type: 'string' },
    runtime: { type: 'string' },
    instance: { type: 'string' },
    'no-assistant': { type: 'boolean' },
    'parent-stdin': { type: 'boolean' },
  },
  strict: false,
})

const log = (msg) => process.stderr.write(`persona-opencode: ${msg}\n`)
const companion = createOpencodeCompanion({
  socketPath: values.socket || undefined,
  bin: values['opencode-bin'] || process.env.OPENCODE_BIN || 'opencode',
  dbPath: values.db || undefined,
  configDir: values['config-dir'] || undefined,
  launcher: values.launcher,
  displayName: values['display-name'],
  model: values.model,
  runtime: values.runtime,
  instance: values.instance,
  assistant: values['no-assistant'] ? false : true,
  log,
})

void companion.start()
const alive = setInterval(() => {}, 1 << 30)
log('started opencode ACP companion for Persona')

let stopping = false
function shutdown() {
  if (stopping) return
  stopping = true
  clearInterval(alive)
  void companion.stop().finally(() => process.exit(0))
}

for (const sig of ['SIGTERM', 'SIGINT']) {
  process.on(sig, shutdown)
}

if (values['parent-stdin'] && !process.stdin.isTTY) {
  process.stdin.on('end', shutdown)
  process.stdin.on('error', shutdown)
  process.stdin.resume()
}
