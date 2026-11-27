#!/usr/bin/env node
import { request } from 'node:http'
import { connect } from 'node:net'
import { createInterface } from 'node:readline'

const operation = process.argv.includes('models') ? 'models' : 'acp'

function direct() {
  return new Promise((resolve, reject) => {
    const socket = connect(Number(process.env.ORIGIN_PORT), '127.0.0.1')
    socket.once('connect', () => { socket.end(); resolve('direct') })
    socket.once('error', reject)
  })
}

async function route() {
  if (process.env.no_proxy === '*' || process.env.NO_PROXY === '*') return direct()
  try {
    return await new Promise((resolve, reject) => {
      const proxy = new URL(process.env.https_proxy)
      const req = request({ host: proxy.hostname, port: proxy.port, method: 'CONNECT', path: 'service.example:443',
        headers: { 'X-Test-Command': operation, 'X-Test-Pid': String(process.pid) } })
      req.on('error', reject)
      req.on('connect', (res, socket) => {
        socket.destroy()
        if (res.statusCode === 200) resolve('proxied')
        else reject(new Error(`proxy returned ${res.statusCode}`))
      })
      req.end()
    })
  } catch (err) {
    // Deliberately disregard the environment on retry, like a CLI regression.
    try { return await direct() } catch (denied) { throw new Error(`${err.message}; direct socket: ${denied.code}`) }
  }
}

if (operation === 'models') {
  try { console.log(`${await route()}\tTest model`) }
  catch (err) { console.error(err.message); process.exitCode = 1 }
} else {
  for await (const line of createInterface({ input: process.stdin })) {
    const { id, method } = JSON.parse(line)
    if (id == null) continue
    let reply
    try {
      const result = method === 'initialize' ? { protocolVersion: 1, authMethods: [] } :
        method === 'session/new' ? { sessionId: `${await route()}-session` } : {}
      reply = { result }
    } catch (err) { reply = { error: { code: -32000, message: err.message } } }
    console.log(JSON.stringify({ jsonrpc: '2.0', id, ...reply }))
  }
}
