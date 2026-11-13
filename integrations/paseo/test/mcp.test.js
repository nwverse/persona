import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { personaMcpServers } from '../lib/mcp.js'

function configFile(t, content) {
  const dir = mkdtempSync('/tmp/ppaseo-mcp-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const path = join(dir, 'mcp.json')
  writeFileSync(path, typeof content === 'string' ? content : JSON.stringify(content))
  return path
}

test('stdio servers come out as Paseo takes them, with the session id in each environment', (t) => {
  const path = configFile(t, { mcpServers: { persona: { command: 'personad', args: ['mcp'], env: { PERSONA_HOME: '/p', N: 1 } } } })
  assert.deepEqual(personaMcpServers(path, 'paseo-a1'), {
    persona: { type: 'stdio', command: 'personad', args: ['mcp'], env: { PERSONA_HOME: '/p', PERSONA_SESSION_ID: 'paseo-a1' } },
  })
})

test('a session id already in the file is replaced; servers that are not stdio are left out', (t) => {
  const path = configFile(t, { mcpServers: {
    persona: { type: 'stdio', command: 'personad', env: { PERSONA_SESSION_ID: 'other' } },
    remote: { type: 'http', url: 'https://example.com/mcp' },
    broken: { args: ['x'] },
  } })
  assert.deepEqual(personaMcpServers(path, 'paseo-a2'), {
    persona: { type: 'stdio', command: 'personad', env: { PERSONA_SESSION_ID: 'paseo-a2' } },
  })
})

test('nothing usable gives undefined and a logged reason', (t) => {
  const logs = []
  const log = (m) => logs.push(m)
  assert.equal(personaMcpServers(undefined, 's', log), undefined)
  assert.equal(personaMcpServers('/nonexistent/mcp.json', 's', log), undefined)
  assert.equal(personaMcpServers(configFile(t, '{not json'), 's', log), undefined)
  assert.equal(personaMcpServers(configFile(t, { mcpServers: {} }), 's', log), undefined)
  assert.equal(personaMcpServers(configFile(t, {}), 's', log), undefined)
  assert.equal(logs.length, 5)
  assert.ok(logs.every((l) => l.startsWith('paseo: ') && l.includes('no Persona tools')), logs.join('\n'))
})
