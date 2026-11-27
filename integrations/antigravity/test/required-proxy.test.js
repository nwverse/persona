import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { chmodSync, copyFileSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { createServer as httpServer } from 'node:http'
import { createServer as tcpServer } from 'node:net'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode } from '../../runtime-sdk/testing/fake-node.js'
import { supervisedNativeProxy } from '../lib/required-proxy.js'

async function listen(server) {
  const ready = once(server, 'listening')
  server.listen(0, '127.0.0.1')
  await ready
  return server.address().port
}

async function fixture(t, { configured = true, supervised = true, setting } = {}) {
  const home = mkdtempSync(join(tmpdir(), 'agy-required-proxy-'))
  const bin = join(home, 'agy')
  copyFileSync(new URL('./fake-agy-proxy.mjs', import.meta.url), bin)
  // The fake is a .mjs module even on Node versions without syntax detection.
  writeFileSync(join(home, 'package.json'), '{"type":"module"}')
  chmodSync(bin, 0o700)
  const traffic = { direct: 0, proxy: [], status: 200 }
  const origin = tcpServer(socket => { ++traffic.direct; socket.end() })
  const originPort = await listen(origin)
  const proxy = httpServer()
  proxy.on('connect', (req, socket) => {
    traffic.proxy.push({ command: req.headers['x-test-command'], pid: req.headers['x-test-pid'] })
    if (traffic.status === 'disconnect') { socket.destroy(); return }
    socket.end(`HTTP/1.1 ${traffic.status} Result\r\nConnection: close\r\n\r\n`)
  })
  const port = await listen(proxy)
  if (configured) writeFileSync(join(home, 'agy-proxy'), setting ?? `http://127.0.0.1:${port}\n`)
  const node = await startFakeNode(t)
  const args = [fileURLToPath(new URL('../companion.mjs', import.meta.url)), '--socket', node.path,
    '--agy-bin', bin, '--agy-dir', home, '--no-update', '--no-media-guard']
  args.push('--parent-stdin')
  if (supervised) args.push('--native-proxy-file', join(home, 'agy-proxy'))
  const child = spawn(process.execPath, args, {
    env: { HOME: home, PERSONA_HOME: home, PATH: dirname(process.execPath) + ':/usr/bin:/bin',
      NO_PROXY: '*', no_proxy: '*', HTTPS_PROXY: 'http://unused.invalid', https_proxy: 'http://unused.invalid',
      ORIGIN_PORT: String(originPort) },
    stdio: ['pipe', 'ignore', 'pipe'],
  })
  let stderr = ''
  child.stderr.on('data', chunk => { stderr += chunk })
  const exit = once(child, 'exit')
  t.after(async () => {
    child.kill()
    await exit
    proxy.close()
    origin.close()
    rmSync(home, { recursive: true, force: true })
  })
  await Promise.race([node.ready, exit.then(() => { throw new Error(stderr) })])
  return {
    traffic, proxy,
    models: (channel, refresh = true) => node.peer.call('launchers.models', { launcher: 'agy', refresh, channel }),
    acp: (channel) => node.peer.call('session.start', { cwd: home, channel }),
  }
}

for (const operation of ['models', 'acp']) {
  test(`supervised agy ${operation} uses the selected proxy and blocks a deliberate direct retry`,
    { skip: process.platform !== 'darwin' }, async t => {
      const f = await fixture(t)
      const run = f[operation]
      const healthy = await run()
      if (operation === 'models') assert.equal(healthy.models[0].id, 'proxied')
      else assert.match(healthy.session.session_id, /proxied-session/)
      const requests = () => f.traffic.proxy.filter(hit => hit.command === operation)
      assert.ok(requests().length)
      const firstPid = requests().at(-1).pid
      f.traffic.status = 502
      await assert.rejects(run(), /proxy returned 502; direct socket: E(?:PERM|ACCES)/)
      f.traffic.status = 'disconnect'
      await assert.rejects(run(), /socket hang up; direct socket: E(?:PERM|ACCES)/)
      if (operation === 'acp') assert.equal(requests().at(-1).pid, firstPid)
      assert.equal(f.traffic.direct, 0)
      await new Promise(resolve => f.proxy.close(resolve))
      await assert.rejects(run(), /configured native AGY proxy is unavailable/i)
      if (operation === 'models') {
        await assert.rejects(f.models(undefined, false), /configured native AGY proxy is unavailable/i)
      }
      assert.equal(f.traffic.direct, 0)
    })
}

test('invalid, unreadable and unsupported settings cannot enable a direct launch', async t => {
  const dir = mkdtempSync(join(tmpdir(), 'agy-proxy-setting-'))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const file = join(dir, 'proxy')
  const check = (path, platform = 'darwin') => () => supervisedNativeProxy(path, platform).check()
  assert.equal(supervisedNativeProxy(file), undefined)
  assert.throws(check(dir), /Cannot read/)
  for (const value of ['', 'socks5://localhost:1080', 'http://localhost:0', 'https://proxy.example',
    'http://localhost/path', 'http://localhost?query', 'http://localhost/#fragment']) {
    writeFileSync(file, value)
    assert.throws(check(file), /agy-proxy/)
  }
  writeFileSync(file, 'https://[::1]')
  assert.throws(check(file, 'linux'), /macOS only/)
  const env = { https_proxy: 'stale', NO_PROXY: '*', no_grpc_proxy: '*', PATH: '/example' }
  const command = supervisedNativeProxy(file, 'darwin').command('agy', ['models'], env)
  for (const name of ['HTTP_PROXY', 'HTTPS_PROXY', 'GRPC_PROXY', 'ALL_PROXY']) {
    assert.equal(command.env[name], 'https://[::1]')
    assert.equal(command.env[name.toLowerCase()], command.env[name])
  }
  for (const name of ['NO_PROXY', 'no_proxy', 'NO_GRPC_PROXY', 'no_grpc_proxy']) assert.equal(command.env[name], '')
  assert.equal(command.env.PATH, env.PATH)
  assert.equal(env.NO_PROXY, '*')
  assert.match(command.args[1], /localhost:443/)
  if (process.platform !== 'win32') {
    const dangling = join(dir, 'dangling')
    symlinkSync(join(dir, 'missing'), dangling)
    assert.throws(check(dangling), /Cannot read/)
  }
})

test('bad proxy fails only the supervised native pool and does not disclose its value', async t => {
  const f = await fixture(t, { setting: 'http://dont-print-this@unsupported.example' })
  for (const run of [f.models, f.acp]) {
    await assert.rejects(run(), err => /agy-proxy/.test(err.message) && !err.message.includes('dont-print-this'))
  }
  assert.equal(f.traffic.direct, 0)
  const channel = { id: 'managed', auth_mode: 'oauth', rev: '1', env: {} }
  assert.equal((await f.models(channel)).models[0].id, 'direct')
  assert.match((await f.acp(channel)).session.session_id, /direct-session/)
})

for (const options of [{ configured: false }, { supervised: false }]) {
  test(`unconfigured or standalone native AGY preserves the ambient route ${JSON.stringify(options)}`, async t => {
    const f = await fixture(t, options)
    assert.equal((await f.models()).models[0].id, 'direct')
    assert.match((await f.acp()).session.session_id, /direct-session/)
    assert.ok(f.traffic.direct >= 2)
  })
}
