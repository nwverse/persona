// Loopback HTTP MCP proxy owned by the opencode runtime (mirrors integrations/grok/lib/mcp.js).
// Exposes Persona's MCP tools (via `rt.tools` / `rt.callTool` over `~/.persona/runtime.sock`) to opencode sessions
// without ever passing hub credentials to opencode. Each session (or directory binding) gets its own URL path and
// requires a random per-process Bearer token checked in constant time.

import { randomBytes, randomUUID, timingSafeEqual } from 'node:crypto'
import { createServer } from 'node:http'

const MAX_BODY_BYTES = 2 * 1024 * 1024

/** Build a regex matching any gated shell command or script path (`tool.shell`). */
export function shellWordsRe(words) {
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const alts = (Array.isArray(words) ? words : [])
    .filter((w) => typeof w === 'string' && w !== '')
    .map((w) => (w.includes('/') ? `[^\\s;&|()\`"'<>]*${esc(w)}` : `(?:[^\\s;&|()\`"'<>]*/)?${esc(w)}`))
  return alts.length ? new RegExp(`(^|[\\s;&|(\`$"'><])(${alts.join('|')})([\\s;&|)\`"'<>]|$)`) : null
}

const SHELL_BINARIES = new Set(['sh', 'bash', 'zsh', 'dash', 'ksh', 'ash'])

/**
 * Tokenize a shell command string into unquoted/unescaped words while splitting on shell
 * operators (`;`, `&&`, `||`, `|`, `&`, `(`, `)`, `<`, `>`, `` ` ``, `$(`, newlines).
 * Also collects embedded `$(...)` / `` `...` `` command-substitution bodies for recursive checks.
 */
function tokenizeShell(command) {
  const tokens = []
  const embedded = []
  let cur = ''
  let hasToken = false
  let i = 0
  const n = command.length
  const flush = () => {
    if (hasToken) {
      tokens.push(cur)
      cur = ''
      hasToken = false
    }
  }
  while (i < n) {
    const ch = command[i]
    // Whitespace or shell control operators terminate the current token.
    if (/[\s;&|()<>]/.test(ch)) {
      flush()
      i++
      continue
    }
    if (ch === '`') {
      flush()
      i++
      const start = i
      while (i < n && command[i] !== '`') {
        if (command[i] === '\\' && i + 1 < n) i += 2
        else i++
      }
      embedded.push(command.slice(start, i))
      if (i < n) i++
      continue
    }
    if (ch === '$' && command[i + 1] === '(') {
      flush()
      i += 2
      const start = i
      let depth = 1
      while (i < n && depth > 0) {
        if (command[i] === '\\' && i + 1 < n) { i += 2; continue }
        if (command[i] === '(') depth++
        else if (command[i] === ')') depth--
        if (depth > 0) i++
      }
      embedded.push(command.slice(start, i))
      if (i < n) i++
      continue
    }
    if (ch === "'") {
      hasToken = true
      i++
      while (i < n && command[i] !== "'") {
        cur += command[i++]
      }
      if (i < n) i++
      continue
    }
    if (ch === '"') {
      hasToken = true
      i++
      while (i < n && command[i] !== '"') {
        if (command[i] === '\\' && i + 1 < n) {
          const next = command[i + 1]
          cur += (next === '"' || next === '\\' || next === '$' || next === '`') ? next : `\\${next}`
          i += 2
          continue
        }
        if (command[i] === '$' && command[i + 1] === '(') {
          i += 2
          const start = i
          let depth = 1
          while (i < n && depth > 0) {
            if (command[i] === '\\' && i + 1 < n) { i += 2; continue }
            if (command[i] === '(') depth++
            else if (command[i] === ')') depth--
            if (depth > 0) i++
          }
          embedded.push(command.slice(start, i))
          if (i < n) i++
          continue
        }
        if (command[i] === '`') {
          i++
          const start = i
          while (i < n && command[i] !== '`') {
            if (command[i] === '\\' && i + 1 < n) i += 2
            else i++
          }
          embedded.push(command.slice(start, i))
          if (i < n) i++
          continue
        }
        cur += command[i++]
      }
      if (i < n) i++
      continue
    }
    if (ch === '\\' && i + 1 < n) {
      hasToken = true
      cur += command[i + 1]
      i += 2
      continue
    }
    hasToken = true
    cur += ch
    i++
  }
  flush()
  return { tokens, embedded }
}

function tokenMatchesWord(token, word) {
  if (!token || !word) return false
  const normTok = token.replace(/\\/g, '/')
  const normWord = word.replace(/\\/g, '/')
  if (normTok === normWord) return true
  return normTok.endsWith(`/${normWord}`)
}

function basenameOf(token) {
  const norm = token.replace(/\\/g, '/')
  const idx = norm.lastIndexOf('/')
  return idx >= 0 ? norm.slice(idx + 1) : norm
}

/** Extract command-gate shell matchers from Persona tool descriptors returned by `rt.tools({ assistant: true })`. */
export function extractShellGates(tools) {
  const out = []
  for (const t of Array.isArray(tools) ? tools : []) {
    if (!Array.isArray(t?.shell) || t.shell.length === 0) continue
    const words = t.shell.filter((w) => typeof w === 'string' && w !== '')
    const re = shellWordsRe(words)
    if (re) out.push({ name: t.name, command: words[0], words, re })
  }
  return out
}

/** Check whether a shell command string hits a Persona command gate (returns the matching gate or undefined). */
export function matchShellGate(command, gates, depth = 0) {
  if (depth > 4 || typeof command !== 'string' || !command.trim() || !Array.isArray(gates) || gates.length === 0) {
    return undefined
  }
  const { tokens, embedded } = tokenizeShell(command)
  for (const tok of tokens) {
    for (const g of gates) {
      const words = Array.isArray(g.words) ? g.words : (g.command ? [g.command] : [])
      if (words.some((w) => tokenMatchesWord(tok, w))) return g
    }
  }
  // Inspect nested commands passed to `sh -c ...` / `bash -lc ...` / `eval ...` and command substitutions.
  for (let i = 0; i < tokens.length; i++) {
    const base = basenameOf(tokens[i])
    if (base === 'eval' && i + 1 < tokens.length) {
      const sub = matchShellGate(tokens.slice(i + 1).join(' '), gates, depth + 1)
      if (sub) return sub
    } else if (SHELL_BINARIES.has(base)) {
      for (let j = i + 1; j < tokens.length; j++) {
        const arg = tokens[j]
        if (arg === '--') break
        if (/^-[A-Za-z]*c[A-Za-z]*$/.test(arg) && j + 1 < tokens.length) {
          const sub = matchShellGate(tokens[j + 1], gates, depth + 1)
          if (sub) return sub
          break
        }
        if (!arg.startsWith('-')) break
      }
    }
  }
  for (const inner of embedded) {
    const sub = matchShellGate(inner, gates, depth + 1)
    if (sub) return sub
  }
  return gates.find((g) => g.re?.test(command))
}

/**
 * Create a loopback HTTP MCP server bound to `127.0.0.1:0`.
 * @param {{
 *   tools: (binding: any) => Promise<any[]>,
 *   call: (binding: any, name: string, args: Record<string, any>) => Promise<any>,
 *   log?: (msg: string) => void,
 * }} handlers
 */
export function createMcpProxy({ tools, call, log = () => {} }) {
  const token = randomBytes(32).toString('hex')
  const expectedAuth = Buffer.from(`Bearer ${token}`)
  const routes = new Map() // pathname -> binding object
  let starting
  let closed = false

  const server = createServer(async (req, res) => {
    const supplied = Buffer.from(String(req.headers.authorization || ''))
    if (supplied.length !== expectedAuth.length || !timingSafeEqual(supplied, expectedAuth)) {
      res.writeHead(401).end()
      return
    }
    const pathname = (req.url || '').split('?')[0]
    const binding = routes.get(pathname)
    if (!binding) {
      res.writeHead(404).end()
      return
    }
    if (req.method === 'DELETE') {
      res.writeHead(204).end()
      return
    }
    if (req.method === 'GET') {
      // Streamable HTTP MCP clients may probe GET for SSE; 405 tells them this endpoint is POST-only.
      res.writeHead(405, { Allow: 'POST, DELETE' }).end()
      return
    }
    if (req.method !== 'POST') {
      res.writeHead(405, { Allow: 'POST, DELETE' }).end()
      return
    }
    let size = 0
    const chunks = []
    try {
      for await (const chunk of req) {
        size += chunk.length
        if (size > MAX_BODY_BYTES) {
          res.writeHead(413).end()
          return
        }
        chunks.push(chunk)
      }
      // Retirement can revoke/replace the binding while a slow request body
      // is arriving. Possession of the former URL is no longer authority.
      if (closed || routes.get(pathname) !== binding) {
        res.writeHead(404).end()
        return
      }
      const msg = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      if (msg.id === undefined) {
        res.writeHead(202).end()
        return
      }
      let result
      if (msg.method === 'initialize') {
        result = {
          protocolVersion: '2024-11-05',
          capabilities: { tools: {} },
          serverInfo: { name: 'persona', version: '0.1.0' },
        }
      } else if (msg.method === 'ping') {
        result = {}
      } else if (msg.method === 'tools/list') {
        const list = await tools(binding)
        result = {
          tools: (Array.isArray(list) ? list : []).map((t) => ({
            name: t.name,
            description: t.description || t.name,
            inputSchema: t.input_schema && typeof t.input_schema === 'object' ? t.input_schema : { type: 'object', properties: {} },
          })),
        }
      } else if (msg.method === 'tools/call') {
        const name = typeof msg.params?.name === 'string' ? msg.params.name : ''
        const args = msg.params?.arguments && typeof msg.params.arguments === 'object' ? msg.params.arguments : {}
        const value = await call(binding, name, args)
        result = {
          content: (Array.isArray(value?.content) ? value.content : []).map((b) => (
            b?.type === 'image'
              ? { type: 'image', data: b.data, mimeType: b.mimeType || b.mime_type || 'image/png' }
              : b
          )),
          isError: Boolean(value?.is_error),
        }
      } else {
        res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({
          jsonrpc: '2.0',
          id: msg.id,
          error: { code: -32601, message: 'Method not found' },
        }))
        return
      }
      res.writeHead(200, { 'Content-Type': 'application/json' }).end(JSON.stringify({
        jsonrpc: '2.0',
        id: msg.id,
        result,
      }))
    } catch (err) {
      log(`persona: opencode mcp request failed: ${err?.message ?? err}`)
      if (!res.headersSent) {
        res.writeHead(500, { 'Content-Type': 'application/json' }).end(JSON.stringify({ error: 'Persona tool request failed' }))
      } else {
        res.end()
      }
    }
  })

  async function ensureListening() {
    if (closed) throw new Error('MCP proxy closed')
    starting ??= new Promise((resolve, reject) => {
      server.once('error', reject)
      server.listen(0, '127.0.0.1', () => {
        server.off('error', reject)
        server.unref?.()
        resolve()
      })
    })
    await starting
    return server.address().port
  }

  function makeEndpoint(port, pathname) {
    return {
      url: `http://127.0.0.1:${port}${pathname}`,
      headers: [{ name: 'Authorization', value: `Bearer ${token}` }],
      headersObject: { Authorization: `Bearer ${token}` },
    }
  }

  return {
    /**
     * Allocate a route before a session ID exists (ACP `session/new` connects to `mcpServers` during creation).
     * Call `bind(session)` once `sessionId` is known, and `release()` when the session closes or creation fails.
     */
    async allocate(initial = {}) {
      const port = await ensureListening()
      const pathname = `/mcp/${randomUUID()}`
      const binding = { ...initial, session: initial.session ?? null }
      routes.set(pathname, binding)
      const ep = makeEndpoint(port, pathname)
      return {
        ...ep,
        path: pathname,
        acpServer: { type: 'http', name: 'persona', url: ep.url, headers: ep.headers },
        remoteConfig: { type: 'remote', url: ep.url, headers: ep.headersObject },
        bind(session) {
          binding.session = session
          if (session?.id) binding.sessionId = session.id
          if (typeof session?.assistant === 'boolean') binding.assistant = session.assistant
        },
        release() {
          routes.delete(pathname)
        },
      }
    },

    /** Bind a route for a known session (ACP `session/load` or plugin per-session/directory). */
    async forSession(session) {
      const port = await ensureListening()
      const key = encodeURIComponent(String(session?.id || randomUUID()))
      const pathname = `/mcp/session/${key}`
      const binding = { session, sessionId: session?.id || '', assistant: Boolean(session?.assistant) }
      routes.set(pathname, binding)
      const ep = makeEndpoint(port, pathname)
      return {
        ...ep,
        path: pathname,
        acpServer: { type: 'http', name: 'persona', url: ep.url, headers: ep.headers },
        remoteConfig: { type: 'remote', url: ep.url, headers: ep.headersObject },
        release() {
          routes.delete(pathname)
        },
      }
    },

    stop() {
      closed = true
      routes.clear()
      try { server.closeAllConnections?.() } catch {}
      try { server.close() } catch {}
    },
  }
}
