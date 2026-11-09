import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtempSync, rmSync } from 'node:fs'
import { createServer } from 'node:http'
import { join } from 'node:path'
import { WsConnection } from '../../runtime-sdk/lib/listen.js'
import { dialWebSocket } from '../lib/ws.js'

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'

// A WebSocket server for the client to dial: the SDK's server connection behind a plain upgrade.
async function server(t, { on, socketPath, status } = {}) {
  const conns = []
  const http = createServer()
  http.on('upgrade', (req, socket, head) => {
    if (status) {
      socket.end(`HTTP/1.1 ${status}\r\nContent-Length: 0\r\n\r\n`)
      return
    }
    const accept = createHash('sha1').update(req.headers['sec-websocket-key'] + GUID).digest('base64')
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`)
    const c = new WsConnection(socket, head)
    c.req = req
    conns.push(c)
    on?.(c)
  })
  await new Promise((r) => (socketPath ? http.listen(socketPath, r) : http.listen(0, '127.0.0.1', r)))
  t.after(() => {
    for (const c of conns) c.socket.destroy() // upgraded sockets are the test's to end, not the server's
    http.close()
  })
  return { port: http.address().port, conns }
}

test('a text message each way, over TCP and over a unix socket, and a closure the other end sees', async (t) => {
  const got = []
  const s = await server(t, { on: (c) => c.on('data', (line) => { got.push(line); c.write(`echo ${line}`) }) })
  const ws = await dialWebSocket({ port: s.port })
  const back = new Promise((r) => ws.once('message', r))
  ws.send('{"type":"hello"}')
  assert.equal(await back, 'echo {"type":"hello"}')
  assert.equal(got[0], '{"type":"hello"}\n')
  assert.equal(s.conns[0].req.url, '/ws')
  assert.equal(s.conns[0].req.headers.host, `127.0.0.1:${s.port}`)
  assert.equal(s.conns[0].req.headers.origin, undefined, 'no Origin: the daemon refuses browsers it does not know')
  const closed = new Promise((r) => s.conns[0].once('close', r))
  ws.close()
  await closed

  const dir = mkdtempSync('/tmp/pws-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const u = await server(t, { socketPath: join(dir, 'd.sock'), on: (c) => c.write('hi over unix') })
  const ws2 = await dialWebSocket({ socketPath: join(dir, 'd.sock') })
  assert.equal(await new Promise((r) => ws2.once('message', r)), 'hi over unix')
  assert.equal(u.conns[0].req.headers.host, 'localhost')
  const gone = new Promise((r) => ws2.once('close', (code) => r(code)))
  u.conns[0].destroy()
  assert.equal(await gone, 1000)
})

test('a large message, a fragmented one and a ping', async (t) => {
  const big = 'x'.repeat(200_000)
  const s = await server(t, {
    on: (c) => {
      c.write(big)
      // Fragmented by hand: a text frame and a continuation, a ping between them.
      c.socket.write(Buffer.from([0x01, 3, ...Buffer.from('abc')]))
      c.socket.write(Buffer.from([0x89, 2, ...Buffer.from('hi')]))
      c.socket.write(Buffer.from([0x80, 3, ...Buffer.from('def')]))
    },
  })
  const ws = await dialWebSocket({ port: s.port })
  const msgs = []
  await new Promise((r) => ws.on('message', (m) => { msgs.push(m); if (msgs.length === 2) r() }))
  assert.equal(msgs[0].length, big.length)
  assert.equal(msgs[1], 'abcdef')
})

test('UTF-8 text round-trips across the extended frame-length boundary', { timeout: 5000 }, async (t) => {
  const payload = 'é🚀'.repeat(40)
  assert.ok(payload.length < 126)
  assert.ok(Buffer.byteLength(payload) > 125)
  const received = []
  const s = await server(t, { on: (c) => c.on('data', (line) => { received.push(line); c.write(line) }) })
  const ws = await dialWebSocket({ port: s.port })
  t.after(() => ws.close())
  const back = new Promise((resolve, reject) => {
    ws.once('message', resolve)
    ws.once('error', reject)
  })
  assert.equal(ws.send(payload), true)
  assert.equal(await back, payload)
  assert.deepEqual(received, [payload + '\n'])
})

test('a refused handshake and a closed port fail the dial', async (t) => {
  const s = await server(t, { status: '401 Unauthorized' })
  await assert.rejects(dialWebSocket({ port: s.port }), /refused: HTTP\/1.1 401/)
  const free = createServer()
  await new Promise((r) => free.listen(0, '127.0.0.1', r))
  const port = free.address().port
  await new Promise((r) => free.close(r))
  await assert.rejects(dialWebSocket({ port, timeoutMs: 2000 }), /ECONNREFUSED|closed/)
})
