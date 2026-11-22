// Antigravity persists embedded ACP resources inside USER_INPUT.content. Only
// remove a complete leading Persona envelope; ordinary labels remain user text.
const CONTEXT_RESOURCE = /^(?:Content of persona-context:\/\/[^\r\n]*:\r?\n<persona-context>[\s\S]*?<\/persona-context>\s*)+/

export function stripContextResource(text) {
  return text.replace(CONTEXT_RESOURCE, '')
}
