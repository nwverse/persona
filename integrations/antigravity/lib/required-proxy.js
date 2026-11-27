import { lstatSync, readFileSync } from 'node:fs'
import { connect } from 'node:net'

// Only the node-supervised companion reads this operator-owned file. Keep a
// failed policy on the native pool so a typo cannot enable direct access or
// take an independent managed channel offline.
export function supervisedNativeProxy(file, platform = process.platform) {
  if (!file) return
  let value
  try {
    value = readFileSync(file, 'utf8').trim()
  } catch (error) {
    if (error.code === 'ENOENT') {
      // A dangling configuration symlink is an error, even though reading it
      // reports the same errno as an installation with no setting.
      try { lstatSync(file) } catch (statError) { if (statError.code === 'ENOENT') return }
    }
    return refused('Cannot read the native AGY proxy setting (agy-proxy).')
  }
  const url = URL.parse(value)
  if (!url || !['http:', 'https:'].includes(url.protocol) ||
      !['127.0.0.1', '[::1]', 'localhost'].includes(url.hostname) ||
      url.username || url.password || url.pathname !== '/' || url.search || url.hash) {
    return refused('agy-proxy requires a loopback HTTP(S) proxy URL without credentials, a path, a query or a fragment.')
  }
  if (platform !== 'darwin') return refused('Required native AGY proxy enforcement is supported on macOS only.')
  const port = Number(url.port || (url.protocol === 'https:' ? 443 : 80))
  if (!port) return refused('agy-proxy requires a nonzero TCP port.')
  // Seatbelt checks each outbound IP connection, including retries made
  // inside the CLI. Local Unix sockets and the ACP stdin/stdout remain usable.
  const profile = `(version 1)(allow default)(deny network-outbound (remote ip "*:*"))(allow network-outbound (remote tcp "localhost:${port}"))`
  const proxyEnv = {}
  for (const name of ['HTTP_PROXY', 'HTTPS_PROXY', 'ALL_PROXY', 'GRPC_PROXY']) {
    proxyEnv[name] = proxyEnv[name.toLowerCase()] = url.origin
  }
  for (const name of ['NO_PROXY', 'NO_GRPC_PROXY']) {
    proxyEnv[name] = proxyEnv[name.toLowerCase()] = ''
  }
  return {
    // A running ACP worker can outlive its proxy. Report a local outage on
    // its next operation as well as on cold starts; the sandbox also covers
    // the interval between this check and AGY's actual request.
    check() {
      return new Promise((resolve, reject) => {
        const socket = connect({ host: url.hostname.replace(/^\[|\]$/g, ''), port })
        const unavailable = () => {
          socket.destroy()
          reject(new Error('The configured native AGY proxy is unavailable.'))
        }
        socket.setTimeout(1500, unavailable)
        socket.once('error', unavailable)
        socket.once('connect', () => { socket.destroy(); resolve() })
      })
    },
    command(bin, args, env) {
      return { bin: '/usr/bin/sandbox-exec', args: ['-p', profile, bin, ...args], env: { ...env, ...proxyEnv } }
    },
  }
}

function refused(message) {
  return { check() { throw new Error(message) } }
}
