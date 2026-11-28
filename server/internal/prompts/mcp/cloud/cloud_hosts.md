The cloud servers the user's cloud host extensions see: each one's state, addresses, the actions it takes now with their safety (`read`, `confirm`, `destructive`) and the computer bound to it (`machine`). With `machine`, only that computer's server. Changes nothing.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "machine": {
      "type": "string",
      "description": "computer id or name; leave out for every server"
    },
    "refresh": {
      "type": "boolean",
      "description": "read from the vendor now, not a recent answer"
    }
  }
}
```
