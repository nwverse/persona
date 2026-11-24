# Watch cards (#13020)

**Who this is for**: owners who want the assistant to check something for them regularly ("check open orders for me
every morning") and see the answer at a glance, without writing an extension. Everyday Assistant feature; no setup.

A watch card is a question the assistant answers on a schedule, kept as a card at the top of Overview. Each card shows
its latest answer as one of four display kinds and keeps a short history. A card can belong to a knowledge-base project;
it then also shows on that project's node as one of its routine checks.

## The flow

1. The owner says (by voice or chat) "check X for me every day". The assistant calls `watch_draft` with the question,
   a refresh rule, the display kind, when to push an alert and, optionally, the project. The tool answers with a
   preview, which the assistant shows. The draft also appears on Overview as a dashed card with **Add to Overview** and
   **Discard**.
2. The owner agrees in the chat (the assistant calls `watch_confirm`) or taps **Add to Overview**. The card goes to the
   top of the wall and gets **one scheduled task**.
3. Each run of that task answers the question and calls `watch_report` with the reading. The card keeps the latest
   reading in full and the last 30 as points (time, number, status, alert) for the trend.
4. When the card's alert rule trips on a reading, the manager pushes the reading to the owner (title: the card's name;
   body: the reading's line; the push opens the run's conversation). No other run pushes: the task is set to
   `push: "asked"` and the run is told not to call `notify_user`.

Drafts that are never confirmed are dropped after 7 days. At most 30 cards are kept.

## Display kinds

| Kind | The reading | The card shows |
| --- | --- | --- |
| `number` | `number`, `text` | the number large with the card's unit, the line under it |
| `status` | `status` (`ok` / `warn` / `bad`), `text` | a green / orange / red light, its word, the line |
| `table` | `table` (up to 6 columns, 10 rows of short cells), `text` | the line and the first 5 rows; "N more rows" for the rest |
| `trend` | `number`, `text` | the number and a small line of the history's numbers |

A card without a reading says when it will be checked. A tripped alert rings the card in red and adds an **Alert** tag.

## Alert rules

`alert` is `{when, value?}` or none: `above` / `below` (a number above or below `value`), `bad` (the status light is
red), `change` (the reading differs from the previous one: its number, else its status, else its table, else its
text). A reading without a number never trips `above` / `below`; the first reading never trips `change`. Every reading
that trips pushes once; the rest push nothing (`TestWatchAlertRule`).

## Refresh: the existing scheduler

A card does not have a scheduler of its own. Confirming it creates a task in the scheduled-task table
(`docs/assistant/assistant.md` "Scheduled tasks") with `watch_id`, the card's name as its title, a prompt rendered from
`prompts/assistant/watch_run.md` in the owner's language, and the card's rule (`schedule`: `HH:MM` daily, `30m` / `2h` /
`1d` as an interval, or a cron expression, read in the owner's zone). The scheduler runs it like any routine: its own
conversation, missed-run rules, failure retry and run history. The task is listed on the Scheduled tasks page too.

- Editing the card (name, question, rule, display, unit) rewrites its task; pausing turns the task off, resuming on.
- Deleting the card deletes its task.
- A task turned off on the Scheduled tasks page shows the card as paused; one deleted there shows "Not refreshing" until
  the card is resumed, which makes the task again.
- **Refresh now** on a card is the task's Run now (`POST /api/v1/assistant/tasks/{id}/run`).

## Where it is shown

- **Overview** (`web/src/views/watch/WatchWall.tsx`), above Overview's sections and the extensions' cards, in the owner's
  order; drag a card by its grip, or use **Move earlier / Move later** in its ⋯. The ⋯ also holds Refresh now, Pause /
  Resume, Edit… and Delete (asks first). The wall is a lazily loaded chunk; on a hub without watch cards it draws
  nothing.
- **A project node** (`web/src/views/watch/ProjectWatchCards.tsx`): `<ProjectWatchCards project={slug} />` draws the
  confirmed cards of that project with the same actions, without reordering. It reads
  `GET /api/v1/assistant/watches?project=<slug>`. The project tree's node panel on the knowledge map mounts it under the
  project's milestones (#13019, `web/src/views/knowledge/ProjectTree.tsx`), so a card on a project shows there as that
  project's routine check.
- **iOS and Android**: nothing yet. No existing payload changed; the cards are a new route the apps do not read.

## Assistant tools

`watch_draft` (new draft, or revise one with `id`; returns the preview), `watch_confirm`, `watch_list` (optionally one
project's), `watch_update` (any draft field, `paused`), `watch_delete`, `watch_reorder` (`ids` first) and `watch_report`
(the run's reading). The tools are deferred (`assistant/tool_loading.go`); `schedule_routine`'s description points at
`watch_draft` for a result kept as a card. Code: `server/internal/assistant/watch.go`, `watch_tools.go`; descriptions in
`server/internal/prompts/mcp/watch/`.

## Storage and API

All cards are one setting, `assistant.watches`, in the manager's database, in Overview's order. Routes and the
`assistant_watches` event: `docs/protocol/protocol.md`. Mock hub: `MOCK_WATCH_CARDS=1` (one card of each kind, an alert,
a paused card, one not checked yet and a draft; `=empty` for none; `scripts/mock/watch_cards_mock.py`).
