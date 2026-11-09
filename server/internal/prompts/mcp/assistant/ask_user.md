Ask the user one or more multiple-choice questions on their iPhone (a card in this conversation plus a push notification) and wait for the answers. Use it to confirm anything risky (sending messages / email for them, deleting, changing live systems) or to let them pick between options. AskUserQuestion does not work here; use this instead. The user can also type a free-form answer. Waits up to 2 hours; if the user writes in this conversation instead of picking an option, the question is withdrawn and the tool returns at once, with their message following. If nobody answers in time, the question becomes a to-do that waits for the user's call (in their to-do list and the next briefing), and their answer comes to this conversation later as a new message.

```json
{
  "type": "object",
  "required": ["questions"],
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {
        "type": "object",
        "required": ["question", "options"],
        "properties": {
          "question": {
            "type": "string"
          },
          "header": {
            "type": "string",
            "description": "short label, ≤12 chars"
          },
          "multiSelect": {
            "type": "boolean"
          },
          "options": {
            "type": "array",
            "minItems": 2,
            "maxItems": 4,
            "items": {
              "type": "object",
              "required": ["label"],
              "properties": {
                "label": {
                  "type": "string"
                },
                "description": {
                  "type": "string"
                }
              }
            }
          }
        }
      }
    }
  }
}
```
