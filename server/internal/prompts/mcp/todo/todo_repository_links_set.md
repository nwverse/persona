Replace repo_id links at the todo_repositories_get revision; URLs must match its saved identity. Use {url} or {kind,number}. [] clears; omitted/empty titles keep. Conflict: reread/reconcile. No files, progress or completion changes.

```json
{
  "type": "object",
  "required": [
    "id",
    "repo_id",
    "expected_revision",
    "links"
  ],
  "properties": {
    "id": {
      "type": "string"
    },
    "repo_id": {
      "type": "string"
    },
    "expected_revision": {
      "type": "integer",
      "minimum": 0
    },
    "links": {
      "type": "array",
      "maxItems": 50,
      "items": {
        "type": "object",
        "properties": {
          "url": {
            "type": "string"
          },
          "kind": {
            "type": "string",
            "enum": [
              "issue",
              "pull_request"
            ]
          },
          "number": {
            "type": "string",
            "pattern": "^[1-9][0-9]{0,19}$"
          },
          "title": {
            "type": "string",
            "description": "Full plain source title; ≤1,024 UTF-8 bytes after trimming.",
            "maxLength": 1024
          }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```
