Change a task you supervise or its acceptance criteria after dispatch. Keep the original brief. Log what changed and why, apply done_when and expect_minutes, and show the owner in the next digest. Restart overdue; restart estimate_due only with a new estimate. Only this log changes criteria (including via agent_supervise or agent_send). Give the full done_when list. Omitted criteria are recorded as dropped and shown at completion. Adding deploy, merge, release, external sends/comments, deletion, spending or production changes outside the existing scope requires the owner's message to have started this turn. Otherwise ask_user or task_review needs_user, then retry after approval. Only the supervisor can edit. Tell the Agent with agent_send. For a revised estimate or overdue clock, explain and set expect_minutes. After the settle window, a finished task without an owner card needs task_review before its scope can change. Use needs_user or accepted with user_check when the owner must act.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "required": ["todo_id", "change", "why"],
  "properties": {
    "todo_id": {
      "type": "string",
      "description": "the task's todo id (todo_…)"
    },
    "inbox_item": {
      "type": "string",
      "description": "Inbox source; omit for the conversation's sole eligible open handoff. Several require an id. Links the existing task and its Agents."
    },
    "change": {
      "type": "string",
      "description": "{{if eq .Lang "en"}}what changes, in one sentence the owner can read (English), e.g. require verification in the test environment only, instead of a staged production rollout{{else if eq .Lang "ja"}}変更内容をユーザーが読める日本語の一文で記述します。例：段階的な本番検証を求めず、テスト環境でのみ検証する{{else}}what changes, in one sentence the owner can read (Chinese), e.g. 「不再要求灰度验证，改为只在测试环境验证」{{end}}"
    },
    "why": {
      "type": "string",
      "description": "{{if eq .Lang "en"}}why, in one sentence (English){{else if eq .Lang "ja"}}理由を日本語の一文で記述します{{else}}why, in one sentence (Chinese){{end}}"
    },
    "done_when": {
      "type": "array",
      "minItems": 1,
      "maxItems": 6,
      "items": { "type": "string" },
      "description": "the criteria after the change, the whole list; leave it out to keep them as they are"
    },
    "expect_minutes": {
      "type": "integer",
      "minimum": 0,
      "description": "how long the task should take from now; leave it out (or 0) to keep it"
    }
  }
}
```
