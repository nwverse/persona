Replace a conversation's tags ([] clears them); at most 10 of 32 characters. Own space only, and only when the user asks: never invent tags. This conversation is a valid id.

```json
{
  "type": "object",
  "required": ["id", "tags"],
  "properties": {
    "id": {
      "type": "string",
      "description": "full id or its first 8 characters"
    },
    "tags": {
      "type": "array",
      "items": { "type": "string" },
      "description": "the full new list"
    }
  }
}
```
