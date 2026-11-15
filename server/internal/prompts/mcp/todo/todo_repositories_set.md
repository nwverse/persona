Atomically replace using revisions from todo_repositories_get/repositories_list. [] removes only metadata. Frozen source snapshots: remove/reselect to adopt catalog identity changes. Editable after execution. Omit links to keep; [] clears. Use {url} or {kind,number}; omitted/empty titles keep. Conflict: reread/reconcile. No files, progress or completion changes.

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
                "type": "string",
                "minLength": 1
              }
            },
            "additionalProperties": false,
            "required": [
              "kind"
            ],
            "anyOf": [
              {
                "properties": {
                  "kind": {
                    "const": "default"
                  }
                },
                "not": {
                  "required": [
                    "value"
                  ]
                }
              },
              {
                "properties": {
                  "kind": {
                    "enum": [
                      "branch",
                      "tag",
                      "commit",
                      "ref"
                    ]
                  }
                },
                "required": [
                  "value"
                ]
              }
            ]
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
            },
            "maxItems": 50
          }
        },
        "additionalProperties": false
      }
    }
  },
  "additionalProperties": false
}
```
