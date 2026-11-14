Add issue/PR links to an existing to-do, including their full titles when known. Use this after finding or opening related work, without asking the user to edit metadata manually. The manager derives and groups repository identities, preserving existing aliases, refs and source snapshots. A repository need not be preconfigured. Use links: [{url,title}] for titled work, or urls for older URL-only callers; provide one to 50 links in total. Supply the actual issue/PR title from the source, never a guessed title or the to-do title. A nonempty title refreshes an existing link; URL-only calls preserve titles. Duplicate URLs do not duplicate links; an explicit call may restore a previously removed link. The batch is atomic. Read todo_get or todo_repositories_get for current associations; use the revision-checked set tools for removal or replacement. Links are context, never evidence by themselves that work is merged, deployed or complete.

```json
{
  "type": "object",
  "required": [
    "id"
  ],
  "properties": {
    "id": {
      "type": "string"
    },
    "urls": {
      "type": "array",
      "minItems": 1,
      "maxItems": 50,
      "items": {
        "type": "string"
      }
    },
    "links": {
      "type": "array",
      "maxItems": 50,
      "items": {
        "type": "object",
        "required": [
          "url"
        ],
        "properties": {
          "url": {
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
  "additionalProperties": false,
  "anyOf": [
    {
      "required": [
        "urls"
      ]
    },
    {
      "required": [
        "links"
      ]
    }
  ]
}
```
