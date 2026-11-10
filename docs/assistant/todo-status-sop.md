# To-do status SOP

How the To-dos page decides where each to-do shows (#3156). The page's default list is by **status section**; the older
grouping by folder stays available as a secondary view (List › Status | Folder). The code follows this document:
`web/src/lib/todo-triage.ts` (`triage`, `triageTodos`), with tests in `web/src/lib/todo-triage.test.ts`. A change to
the rules changes both in the same PR.

## Principle: real signals only

A to-do's section comes from what the hub and the computers report, never from what an agent says about its own
work. An agent writing "done" in its reply does not finish anything; its turn ending, a review, a merge or a deploy does.

| Signal | Where it comes from | Values used |
| :--- | :--- | :--- |
| Agent alive and running | The linked, not released agents' sessions (herdr and other engines, via the hub's session list) | asking (`needs_you`), cut off by a model error (`turn_error`), working or starting, finished unseen (`unread`), idle, gone |
| PR / delivery | `delivery.state` and `pr_url` on the to-do (`docs/assistant/todos.md`, "Delivery") | none, PR open (`pr`, or a `pr_url` with no delivery past it), merged not deployed (`merged`, `off`), deployed (`live`) |
| Review verdict | The latest `task_review` in the to-do's supervision (`docs/assistant/unified-entry.md`) | `accepted`, `rework`, `needs_user`, `cancelled`, pending review |
| Ask | The to-do's `ask` (`docs/assistant/todos.md`, "Waiting for the owner") | a decision, "cannot be done", "done", "please verify" |
| Last activity | The latest of: the to-do's created, updated, reopened and done times; its supervision's update, last report and last review; its delivery and ask times; each linked agent's session update; a restore from Shelved | a moment |

## Sections

| Section | Shown | What it holds |
| :--- | :--- | :--- |
| Waiting on you | open | Something only the owner can move: an answer, an approval, a decision, a retry |
| Working | open | An agent is running on it now |
| Just finished | open | Finished in the last 24 hours and worth a look: closed, accepted, deployed, or a turn nobody has seen yet |
| PR to merge | open | Its work stopped at a pull request that is still open and nothing runs: however old, it stays here, with the PR link and how long since it moved |
| Other open | folded | Open, recently active, none of the above (an agent stopped and seen, a reminder of the user's own) |
| Shelved | folded | No activity for 48 hours and nothing running (see "Shelving") |
| Done | folded | Closed, accepted or deployed more than 24 hours ago |

## Decision table

The first rule that holds wins.

| # | Rule | Condition | Section |
| :--- | :--- | :--- | :--- |
| 1 | `failed_start` | The task's agent never started (`start_error`) | Waiting on you; Done once 48 hours passed |
| 2 | `agent_asks` | An agent waits on the owner (question, approval, plan) or was cut off by a model error | Waiting on you |
| 3 | `owner_decides` | Open, and the review says `needs_user`, or its ask is a decision or "cannot be done" | Waiting on you |
| 4 | `agent_running` | An agent is working or starting | Working |
| 5 | `closed` | `done_at` is set | Just finished for 24 hours, then Done |
| 6 | `accepted` | The latest review accepted it and no review came since, even when the hub reopened it (see "Accepted tasks") | Just finished for 24 hours after the review, then Done |
| 7 | `deployed` | `delivery.state` is `live` | Just finished for 24 hours, then Done |
| 8 | `finished_unseen` | An agent finished a turn nobody has seen, the review is pending, or its ask says done / please verify | Just finished |
| 9 | `pr_open` | Its PR is open (rule "PR / delivery" above) | PR to merge |
| 10 | `stale` | Shelvable (see "Shelving") and no activity for 48 hours | Shelved |
| 11 | `rest` | Anything else | Other open |

**Waiting on you is only for what the owner must do in person**: answer a question, grant a permission, decide on a
large feature. A deployed to-do's "Please verify" ask and a "done" conclusion are not: the supervising assistant
checks and accepts them by default, so they sit in Just finished (rule 8), their one-tap button still there for the
owner who wants to accept it themselves.

A merged but not deployed delivery (`merged`, `off`) has no section of its own: the row says where it is held and
who can unhold it, and the to-do falls under rules 10 and 11.

### Deployed needs evidence

"Deployed" must mean the running hub contains the work: the commit of the hub's `health.version` includes the PR's merge
commit. Today the hub sets `delivery.state = live` from the agent's own delivery line ("交付：已上线并实测",
`server/internal/assistant/delivery.go`) or the completion judge, without that check, so a PR merged but not yet
deployed can be marked deployed and ask "Please verify". The page can only read the state the hub gives; the check
belongs on the hub and is tracked separately (#3205). Until it lands, rule 7 trusts `live` as the hub reports it.

## Header count

The header reads "N open · N shelved · N done". **Open** counts to-dos that are not closed, not accepted or deployed,
and not shelved: Waiting on you, Working, PR to merge, Other open, and Just finished rows still open (a finished turn
is not a closed to-do; only a review or the user closes it). **Done** counts closed, accepted and deployed ones. The
sidebar's To-dos badge shows the same open count. Quiet tasks (#1731) are left out of all of them, as before.

## Shelving

A to-do is shelvable when it is a task (an agent was ever linked, or a conversation handed it out) and has no due date
ahead. The user's own to-dos without agents (reminders, errands) and ones due later never shelve themselves.

- Shelved to-dos are folded under **Shelved**, not counted as open, and not changed on the hub: nothing is deleted, closed
  or released. Any new activity (an agent working, a report, a review, an edit) takes it out again at once.
- **Restore** on a shelved row counts as activity now: the to-do goes back to its section and shelves again only after
  another 48 hours without a move. The restore is kept in this browser (`localStorage`, `persona.todos.restored`), not
  on the hub; another browser or device still shows it shelved until it moves.

## Related to-dos

To-dos that are the same piece of work share one row, led by its most urgent member (by section, then most recent
activity); "N related" under the row opens the others. Two to-dos are related when they share any key:

- the same PR link (case and a trailing slash ignored);
- the same issue number in the title (`#412`);
- the same title once case, spaces and punctuation are dropped;
- the same plan (`plan_id`: tasks one request was split into).

Relations chain (A shares a PR with B, B an issue with C: one row). A closed to-do never joins an open one's row.

## Accepted tasks

When `task_review` accepts a task, the hub closes it. If its agent is then sent more work, the hub reopens it
(`taskWorking` → `reopenTodo`, `docs/assistant/todos.md`) while the supervision still says `accepted`. Earlier the
page treated every reopened task as not reviewed: it showed "Follow up", its badge "Supervising", and criteria added
with the extra work (`agent_supervise` / `agent_send` replace `done_when`, so the list grew from 5 to 8) as open
with a clock and counted them ("5/8").

The acceptance now stands until a newer review replaces it (`acceptanceStands` in `web/src/lib/supervision.ts`):

- with nothing of it running or asking, the task reads **Accepted** in the row and the detail, and sits in Just finished
  or Done (rule 6);
- with its agent working or asking again, it is in Working or Waiting on you like any task, its badge still Accepted;
- criteria no review judged after the acceptance are marked "not checked" (a dash, not a clock), and the count only
  covers judged ones ("5/5"). The next review, if the supervisor runs one, judges them as usual.

A task the user reopens by hand also keeps the accepted verdict until the next review; the checkbox closes it again.

## Clients

The web follows this document. iOS, Mac and Android still show the earlier list (`docs/assistant/todos.md`,
"Interaction design across clients"); bringing them to the same sections is a follow-up.
