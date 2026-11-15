Record requested/agreed work or reminders immediately, without confirmation; agents execute via task_plan. Open title/due duplicates return the existing to-do but refuse bindings: use todo_repositories_get/set. Short action title in the user's language; details in notes. In leader chats: record that direction's work; task_plan(todo_id) dispatches, todo_complete finishes unassigned work. Optional bindings save atomically; IDs/catalog_revision from repositories_list. No files or task progress changes.

```json
{
  "type": "object",
  "required": [
    "title"
  ],
  "properties": {
    "title": {
      "type": "string",
      "description": "Short action."
    },
    "notes": {
      "type": "string",
      "description": "Details."
    },
    "due": {
      "type": "string",
      "description": "Day only: YYYY-MM-DD; otherwise YYYY-MM-DD HH:MM (user time zone) or RFC3339. Relative 2h/3d accepted."
    },
    "remind": {
      "type": "string",
      "description": "Future reminder (user time zone/RFC3339 or relative 30m/2h/1d). Only if requested or deadline close and important."
    },
    "priority": {
      "type": "string",
      "enum": [
        "high",
        "normal",
        "low"
      ],
      "description": "normal when left out"
    },
    "kind": {
      "type": "string",
      "enum": [
        "todo",
        "concern"
      ],
      "description": "todo (default); concern (main chat only) tracks a user-raised matter until concern_report reports its outcome; other completion tools cannot close it."
    },
    "project": {
      "type": "string",
      "description": "Existing kb_list project slug/name; prefer long-term work. Only when clear; no near-duplicates."
    },
    "repository_bindings": {
      "type": "object",
      "required": [
        "catalog_revision",
        "repositories"
      ],
      "properties": {
        "catalog_revision": {
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
  }
}
```
