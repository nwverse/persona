// Normalize Antigravity's local file:// Markdown links and images to plain machine paths
// so Persona clients (which intentionally exclude the file: scheme) open and render them.

const FENCE_RE = /^((?:[ \t]*>[ \t]?)*)[ \t]*(?:(?:[-*+]|\d{1,9}[.)])[ \t]+)?[ \t]*(`{3,}|~{3,})(.*)$/
const LIST_ITEM_RE = /^(?:[ \t]*>)*[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]/
const REF_DEF_RE = /^([ ]{0,3}\[[^\]\n]+\]:[ \t]+)(<([^>\r\n]+)>|(\S+))(.*)$/
const LINK_TAIL_RE = /^[ \t]*(?:(?:"(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*'|\((?:\\[\s\S]|[^)\\])*\))[ \t]*)?\)/
const WIN_DRIVE_RE = /^\/([A-Za-z])(?::|%3[Aa])\/(.+)$/
const CTRL_OR_NUL_RE = /[\x00-\x1f\x7f]|%00/i

function decodeSafe(s) {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

export function localFileURLToPath(dest) {
  if (typeof dest !== 'string' || dest.length <= 8 || dest.slice(0, 7).toLowerCase() !== 'file://') return ''
  if (CTRL_OR_NUL_RE.test(dest)) return ''
  const hash = dest.indexOf('#')
  const raw = hash >= 0 ? dest.slice(7, hash) : dest.slice(7)
  const frag = hash >= 0 ? dest.slice(hash) : ''
  if (raw.includes('?')) return ''
  let path = ''
  if (raw.startsWith('/')) path = raw
  else if (raw.slice(0, 10).toLowerCase() === 'localhost/') path = raw.slice(9)
  else return ''
  if (path === '/' || path.startsWith('//')) return ''
  const decoded = decodeSafe(path).toLowerCase()
  if (decoded === '/api/v1' || decoded.startsWith('/api/v1/')) return ''
  const win = WIN_DRIVE_RE.exec(path)
  if (win) return `${win[1]}:/${win[2]}${frag}`
  return `${path}${frag}`
}

function unescapedBracket(line, index) {
  let slashes = 0
  for (let i = index - 1; i >= 0 && line[i] === '\\'; i--) slashes++
  return slashes % 2 === 0
}

function rewriteLine(line) {
  if (!line.toLowerCase().includes('file://')) return line
  const def = REF_DEF_RE.exec(line)
  if (def) {
    const dest = def[3] ?? def[4]
    const next = localFileURLToPath(dest)
    if (next) return `${def[1]}${def[3] !== undefined ? `<${next}>` : next}${def[5]}`
  }
  let out = ''
  for (let i = 0; i < line.length; ) {
    if (line[i] === '`') {
      let n = 0
      while (i + n < line.length && line[i + n] === '`') n++
      let j = i + n
      let closed = -1
      while (j < line.length) {
        if (line[j] === '`') {
          let m = 0
          while (j + m < line.length && line[j + m] === '`') m++
          if (m === n) {
            closed = j + m
            break
          }
          j += m
        } else {
          j++
        }
      }
      const end = closed >= 0 ? closed : i + n
      out += line.slice(i, end)
      i = end
      continue
    }
    if (line[i] === ']' && line[i + 1] === '(' && unescapedBracket(line, i)) {
      let j = i + 2
      while (j < line.length && (line[j] === ' ' || line[j] === '\t')) j++
      if (line[j] === '<') {
        let k = j + 1
        while (k < line.length && line[k] !== '>' && line[k] !== '<') {
          if (line[k] === '\\' && k + 1 < line.length) k += 2
          else k++
        }
        if (k < line.length && line[k] === '>' && LINK_TAIL_RE.test(line.slice(k + 1))) {
          const next = localFileURLToPath(line.slice(j + 1, k))
          if (next) {
            out += `${line.slice(i, j)}<${next}>`
            i = k + 1
            continue
          }
        }
      } else {
        let k = j
        let depth = 0
        while (k < line.length) {
          const ch = line[k]
          if (ch === '\\' && k + 1 < line.length) {
            k += 2
            continue
          }
          if (ch === ' ' || ch === '\t' || ch === '\r') break
          if (ch === '(') {
            depth++
            k++
          } else if (ch === ')') {
            if (depth === 0) break
            depth--
            k++
          } else {
            k++
          }
        }
        if (k > j && depth === 0 && LINK_TAIL_RE.test(line.slice(k))) {
          const next = localFileURLToPath(line.slice(j, k))
          if (next) {
            out += `${line.slice(i, j)}${next}`
            i = k
            continue
          }
        }
      }
    }
    out += line[i++]
  }
  return out
}

export function cleanAssistantText(raw) {
  if (typeof raw !== 'string' || !raw.toLowerCase().includes('file://')) return raw ?? ''
  const lines = raw.split('\n')
  let fence = null
  let blank = true
  let inList = false
  let inIndented = false
  const out = new Array(lines.length)
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trim()
    if (fence) {
      out[i] = line
      const rest = line.replace(/^(?:[ \t]*>[ \t]?)+/, '').trim()
      if (rest.length >= fence.len && rest[0] === fence.ch && /^(`+|~+)$/.test(rest)) fence = null
      blank = false
      continue
    }
    if (inIndented) {
      if (!trimmed || /^(?: {4}|\t)/.test(line)) {
        out[i] = line
        continue
      }
      inIndented = false
    }
    if (trimmed && !/^[ \t]/.test(line)) inList = LIST_ITEM_RE.test(line) || (inList && !blank)
    const open = FENCE_RE.exec(line)
    if (open && !(open[2][0] === '`' && open[3].includes('`'))) {
      fence = { ch: open[2][0], len: open[2].length }
      out[i] = line
      blank = false
      continue
    }
    if (blank && !inList && trimmed && /^(?: {4}|\t)/.test(line)) {
      inIndented = true
      out[i] = line
      continue
    }
    blank = !trimmed
    out[i] = rewriteLine(line)
  }
  return out.join('\n')
}
