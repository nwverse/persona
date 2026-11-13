Replace one repository's issue/PR links using the exact revision from todo_repositories_get. Each entry is either {url} or {kind,number}; links must belong to the saved repository identity. Multiple PRs are independent, and these links never replace legacy PR progress or completion. [] clears links. On conflict read again and reconcile.

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
          }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```
