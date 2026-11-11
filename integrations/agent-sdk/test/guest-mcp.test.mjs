// Only the guest's declared MCP tool is registered with the SDK and calls the
// same stdio Persona server; no built-in tools or arbitrary command is added.
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { guestMcpServer } from '../src/guest-mcp.mjs'

test('guest MCP registers and invokes only its declared tools', async () => {
  const { server, close } = await guestMcpServer({
    command: process.execPath,
    args: [fileURLToPath(new URL('./guest-mcp-server.mjs', import.meta.url))],
    env: { PERSONA_GUEST: '1', PERSONA_SESSION_ID: 'guest-test' },
  })
  try {
    assert.equal(server.type, 'sdk')
    assert.equal(server.name, 'persona')
    const definitions = server.instance._registeredTools
    assert.deepEqual(Object.keys(definitions), ['exampledocs_search'])
    const answer = await definitions.exampledocs_search.handler({ query: 'synthetic' })
    assert.deepEqual(answer.content, [{ type: 'text', text: 'synthetic:synthetic' }])
  } finally {
    await close()
  }
})
