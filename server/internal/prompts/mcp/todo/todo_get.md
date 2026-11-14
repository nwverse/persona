One of the user's Persona to-dos in full: its title, state, priority, due date, reminder (and whether it was pushed), when it was made and changed, the conversation and plan it came from when it is a task, its notes, the agents that work or worked on it, and the decisions in force on it. Includes its canonical repository and issue/PR associations. Manage discovered links with todo_repository_links_add, or use the revision-checked association tools for edits and removal. Historical PR references are imported once; related PRs do not determine task completion.

```json
{
  "type": "object",
  "required": ["id"],
  "properties": {
    "id": {
      "type": "string",
      "description": "the to-do's id (todo_…), as todo_list gives it"
    }
  }
}
```
