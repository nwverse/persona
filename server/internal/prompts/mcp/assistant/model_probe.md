Run bounded samples through a saved provider; real inference, configuration unchanged. Saved connection settings only; no overrides or secrets in output. Ordered results: status, latency, block types, usage, redacted text (8 KiB cap), generation_truncated for token limits. Messages off sends thinking.type=disabled; Chat off sends reasoning_effort=none. Retries reported; 429 returns Retry-After without retry.{{template "shared/mcp_language" .}}

```json
{
  "type": "object",
  "properties": {
    "provider": {"type": "string", "description": "Saved provider name"},
    "api": {"type": "string", "enum": ["messages", "chat"], "description": "Protocol on the same saved gateway; default: provider's API"},
    "model": {"type": "string", "description": "Model override; default: provider's model"},
    "effort": {"type": "string", "description": "Effort override; default: provider's effort"},
    "samples": {
      "type": "array", "minItems": 1, "maxItems": 10,
      "items": {
        "type": "object",
        "properties": {
          "id": {"type": "string", "maxLength": 128},
          "label": {"type": "string", "maxLength": 128},
          "system": {"type": "string", "description": "system+user <=16384 UTF-8 bytes"},
          "user": {"type": "string", "minLength": 1}
        },
        "additionalProperties": false,
        "required": ["user"]
      }
    },
    "concurrency": {"type": "integer", "minimum": 1, "maximum": 4, "default": 1},
    "max_tokens": {"type": "integer", "minimum": 1, "maximum": 1024, "default": 256},
    "timeout_seconds": {"type": "integer", "minimum": 1, "maximum": 30, "default": 30}
  },
  "additionalProperties": false,
  "required": ["provider", "samples"]
}
```
