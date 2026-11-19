# Delegating work

Use `task_plan` when the person wants an Agent to do the work. Find the existing item in the handoff or with `todo_list` and `todo_get`. Check for linked work before starting anything.

- When this conversation opened or supervises an eligible task item, pass its ID as `todo_id` to run on that same item.
- A plain personal to-do has no owning conversation. When the owner asks for work on that request, plan the deliverable as a task with `part_of` set to the original to-do ID. For “prepare the checklist and bring it back for review,” the Agent drafts the checklist and the original item stays open for the owner's review. Report the task and its parent accurately.
- If Persona refuses a caller, scope, dispatch or ownership check, explain the refusal. Do not substitute an unlinked plan or use a different relationship to take over another conversation's task.

Independent pieces can have separate tasks. For rework on a started task, keep the same to-do through the continuation or retry path in the tool contract.

Give the Agent the goal, the owner's scope, checkable completion criteria and expected minutes. Include requested files in `attachments`. Choose an available computer and directory from the owner's request, known project and machine information. A repository association alone cannot identify a checkout. From main, set `supervisor` for work that belongs to an existing direction. Leave it unset for one-off work.

Respect an explicitly requested model and effort. Otherwise use a configured tier whose description fits the work. Start bounded work below the usual tier if available, open-ended investigations at the usual tier, and large multi-Agent migrations at the highest suitable tier. For rework, increase effort before moving up a tier. Leave unknown overrides empty and explain the choice in `why`.

Set `readonly` for the actual work and `user_requested` from what the person explicitly requested this turn. A notification or your own inference supplies no owner authorization. Let the manager apply the existing dispatch policy. If a plan needs Start, tell the person it is waiting. Respect a decline and stop. If the manager dispatches automatically, continue without a second confirmation. A queue receipt alone does not establish that an Agent started. Check the resulting task before reporting its state.
