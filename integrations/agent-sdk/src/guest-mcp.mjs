// A guest's declared Persona tools as an in-process SDK MCP server. The same
// stdio Persona server and manager whitelist authorize every call; only its
// advertised tools cross this boundary, never the built-in or other MCP tools.
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StdioClientTransport } from '@modelcontextprotocol/sdk/client/stdio.js'
import { createSdkMcpServer, tool } from '@anthropic-ai/claude-agent-sdk'
import { fromJSONSchema } from 'zod'

export async function guestMcpServer(config) {
  const transport = new StdioClientTransport({ ...config, stderr: 'pipe' })
  const client = new Client({ name: 'persona-guest-bridge', version: '1.0.0' })
  await client.connect(transport)
  const close = async () => { await client.close() }
  try {
    const { tools } = await client.listTools()
    if (tools.length === 0) throw new Error('the guest MCP server advertised no tools')
    const mapped = tools.map((definition) => tool(
      definition.name,
      definition.description ?? '',
      fromJSONSchema(definition.inputSchema ?? { type: 'object', properties: {} }),
      (args) => client.callTool({ name: definition.name, arguments: args }),
      { alwaysLoad: true },
    ))
    const server = createSdkMcpServer({ name: 'persona', tools: mapped, alwaysLoad: true })
    return { server, close }
  } catch (err) {
    await close().catch(() => {})
    throw err
  }
}
