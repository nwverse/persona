Run bounded diagnostic samples through a saved provider. This sends inference requests but never changes Persona configuration. Only the provider's stored URL, credentials, headers and route are used; none can be supplied or returned. Results stay in sample order, with status, latency, content-block types, redacted text (at most 8 KiB) and optional numeric usage. Chat compatibility retries are reported per sample; 429 responses are not retried and include Retry-After.{{template "shared/mcp_language" .}}

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
          "system": {"type": "string", "maxLength": 16384},
          "user": {"type": "string", "minLength": 1, "maxLength": 16384}
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
