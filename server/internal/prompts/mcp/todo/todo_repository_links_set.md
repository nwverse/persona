Replace one repository's issue/PR links using the exact revision from todo_repositories_get. Each entry is either {url} or {kind,number}; links must belong to the saved repository identity. Multiple PRs are independent, and task progress and completion remain independently verified. [] clears links. On conflict read again and reconcile. Optional title is the full source title as plain text; omitted or empty titles preserve known titles for retained links.

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
            "type": "string"
          },
          "title": {
            "type": "string",
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
