#!/usr/bin/env node
// The one-time switch to self-updating (docs/integrations/pi.md "Automatic updates"): installs this checkout's pi package into the store
// beside the node's socket as its current version, and says what to run next — pi loads the extension from
// <store>/current/pi, the companion runs from there, and from then on the node's versions come by themselves.
//
//   node integrations/pi/install.mjs [--socket <runtime.sock>]

import { runtimeStorePath } from '../runtime-sdk/lib/compat.js'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { prepareSocketDirectory } from '../runtime-sdk/lib/identity.js'
import { defaultSocketPath, installCurrent, PACKAGES, packageHash, readShipped } from '../runtime-sdk/index.js'

const NAME = 'persona-pi-runtime'
const i = process.argv.indexOf('--socket')
const socket = (i > 0 && process.argv[i + 1]) || defaultSocketPath()
const store = runtimeStorePath(prepareSocketDirectory(socket), NAME)
const root = join(dirname(fileURLToPath(import.meta.url)), '..')
const files = readShipped(root, PACKAGES[NAME])
const hash = packageHash(files)
installCurrent(store, hash, files)
const current = join(store, 'current')
console.log(`installed ${NAME} ${hash.slice(0, 12)} as ${current}

next:
  pi install ${join(current, 'pi')}            # and remove the old path from ~/.pi/agent/settings.json "packages"
  node ${join(current, 'pi', 'companion.mjs')} [--port <pi-web port>]   # under launchd / systemd (KeepAlive / Restart=always)
  then restart pi-web (and pi terminals) once`)
