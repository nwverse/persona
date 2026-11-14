Replace the business repository associations of a to-do. Read todo_repositories_get and repositories_list first, and supply their exact revisions. An explicit empty array removes associations only, without deleting code or changing task progress. Existing source snapshots remain unchanged; remove and reselect a repository to adopt a changed catalog identity. Associations remain editable after execution. Omitted per-repository links preserve existing links; [] clears them. On conflict read again and reconcile, never blindly retry. Optional title is the full source title as plain text; omitted or empty titles preserve known titles for retained links.

```json
{
  "type": "object",
  "required": [
    "id",
    "expected_revision",
    "expected_catalog_revision",
    "repositories"
  ],
  "properties": {
    "id": {
      "type": "string"
    },
    "expected_revision": {
      "type": "integer",
      "minimum": 0
    },
    "expected_catalog_revision": {
      "type": "integer",
      "minimum": 0
    },
    "repositories": {
      "type": "array",
      "maxItems": 20,
      "items": {
        "type": "object",
        "required": [
          "repo_id"
        ],
        "properties": {
          "repo_id": {
            "type": "string"
          },
          "directory_alias": {
            "type": "string"
          },
          "requested_ref": {
            "type": "object",
            "properties": {
              "kind": {
                "type": "string",
                "enum": [
                  "default",
                  "branch",
                  "tag",
                  "commit",
                  "ref"
                ]
              },
              "value": {
                "type": "string"
              }
            },
            "additionalProperties": false
          },
          "links": {
            "type": "array",
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
    }
  },
  "additionalProperties": false
}
```
