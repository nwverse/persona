import { appToolName, clipInput, snippet } from '../../runtime-sdk/index.js'

const tools = { execute: 'Bash', terminal: 'Bash', read: 'Read', edit: 'Edit', write: 'Write',
  patch: 'Edit', search: 'Grep', web_extract: 'WebFetch', todo: 'TodoWrite' }

export const toolName = (update) => appToolName(update.kind ?? update.title ?? 'Tool', tools)

export function transcript() {
  return { items: [], segment: null, activeTools: new Set(), lastText: '' }
}

export function addItem(state, item) {
  const seq = state.items.length + 1
  const value = { id: `${seq}#0`, seq, ts: new Date().toISOString(), ...item }
  state.items.push(value)
  if (item.role === 'user' && item.kind === 'text') state.lastText = snippet(item.text)
  if (item.role === 'assistant' && item.kind === 'text') state.lastText = snippet(item.text)
  return value
}

export function flush(state) {
  if (!state.segment) return
  const { role, kind, text } = state.segment
  state.segment = null
  addItem(state, { role, kind, text })
}

function chunk(state, update, role, kind) {
  if (update.content?.type !== 'text' || !update.content.text) return
  const message = update.messageId
  if (state.segment?.kind !== kind || state.segment?.role !== role || state.segment?.message !== message) {
    flush(state)
    state.segment = { role, kind, message, text: '' }
  }
  state.segment.text += update.content.text
}

function toolText(update) {
  return (update.content ?? []).map(({ content }) => content?.text).filter(Boolean).join('\n')
}

export function toolInput(update) {
  if (update.rawInput && typeof update.rawInput === 'object') return clipInput(update.rawInput)
  const text = toolText(update)
  if (update.kind === 'execute' && text.startsWith('$ ')) return clipInput({ command: text.slice(2) })
  const path = update.locations?.[0]?.path
  return path ? { file_path: path } : {}
}

export function applyUpdate(state, update, replay) {
  switch (update.sessionUpdate) {
    case 'user_message_chunk':
      if (replay) chunk(state, update, 'user', 'text')
      break
    case 'agent_message_chunk':
      chunk(state, update, 'assistant', 'text')
      break
    case 'agent_thought_chunk':
      chunk(state, update, 'assistant', 'thinking')
      break
    case 'tool_call': {
      flush(state)
      state.activeTools.add(update.toolCallId)
      addItem(state, { role: 'assistant', kind: 'tool_use', tool_use_id: update.toolCallId,
        tool_name: toolName(update), tool_input: toolInput(update) })
      break
    }
    case 'tool_call_update': {
      if (!['completed', 'failed'].includes(update.status) || !state.activeTools.delete(update.toolCallId)) break
      flush(state)
      const fallback = typeof update.rawOutput === 'string' ? update.rawOutput : JSON.stringify(update.rawOutput ?? '')
      const text = toolText(update)
      addItem(state, { role: 'user', kind: 'tool_result', tool_use_id: update.toolCallId,
        text: text || fallback, is_error: update.status === 'failed' })
      break
    }
  }
}
