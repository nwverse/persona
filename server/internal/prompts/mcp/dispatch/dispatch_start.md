Hand a subtask to a new agent on another of the user's connected computers, through Persona, and get its session id back at once. Use it when the work needs that computer (the repository or data is there, it needs that system or hardware, or this computer is busy); work this computer can do belongs to your own in-session subagents. Call dispatch_read with no arguments first to see the computers and their home directories. The new agent starts with only your prompt, not your conversation: write everything it needs (the goal, the repository and paths on that computer, what to return, what not to touch). It runs as an ordinary agent session the user can watch and answer in the apps. Read its result with dispatch_read. An agent that is itself a dispatched task cannot dispatch, and only a few dispatched tasks may be working at a time; the answer says so when it refuses.

Give `worktree.branch` when the work needs a branch and checkout of its own on that computer: Persona makes the worktree through that computer's engine and starts the agent in it. Do not tell the agent to run `git worktree add` itself.

```json
{
  "type": "object",
  "required": ["machine", "cwd", "prompt"],
  "properties": {
    "machine": {
      "type": "string",
      "description": "the other computer's id or name, as dispatch_read lists it"
    },
    "cwd": {
      "type": "string",
      "description": "the directory on that computer: an absolute path or ~/…; with worktree, a directory of the repository to branch from"
    },
    "prompt": {
      "type": "string",
      "description": "the whole subtask, self-contained"
    },
    "launcher": {
      "type": "string",
      "description": "a launcher that computer has; empty: the user's default for new agents"
    },
    "worktree": {
      "type": "object",
      "description": "start the agent in a new worktree of cwd's repository, made by that computer's engine",
      "required": ["branch"],
      "properties": {
        "branch": {
          "type": "string",
          "description": "the branch to check out (made from the workspace's HEAD when it does not exist)"
        }
      }
    }
  }
}
```
