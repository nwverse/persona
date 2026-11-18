# To-do view fixtures

The view the manager works out for each to-do (`view` on a row of `GET /api/v1/todos`, #10226): its phase, section,
reason, line 2, times, marks, asks and steps, by the decision table of `docs/design/proposals/todos-redesign.md` 6.3.
The shape is `TodoSummary` in `schema/wire/todo-view.schema.json`. These cases come before the projection that answers
them: today `scripts/test/test_wire_schema.py` holds every expected view to the schema, and the generated types read
each one back unchanged (Go `server/internal/assistantwire/todo_view_gen_test.go`, Kotlin
`android/core/contracts/src/test/kotlin/persona/core/contracts/TodoViewWireTest.kt`, Swift
`apple/PersonaTests/TodoViewWireTests.swift`). The projection's own test replays them next, and may add cases.

| File | What it pins | Cases |
| --- | --- | --- |
| `todo_view.json` | One to-do and its runs' sessions → its view | 18: every phase and section at least once |

## Format

`{"version": 1, "cases": [...]}`. A case is `{name, description, source, input, expected}`, as in
`spec/fixtures/triage`; the descriptions are English.

- `input.now`: the moment the case is read at.
- `input.todo`: the to-do exactly as `GET /api/v1/todos` lists it today (the hub's `listedTodo`: every field Go always
  writes, `ask` while one waits). Its agents' titles equal their sessions' titles, so no case hangs on which one a run's
  title comes from.
- `input.sessions`: the linked sessions as the hub sends them to the apps (`state` already worked out), with the fields
  the case reads, as the triage fixtures carry them.
- `input.restored_at` (optional): the owner's latest restore of it, which counts as activity.
- `input.someday_until` (optional): until when the owner put it off (Someday).
- `input.loc` (optional): the owner's time zone (an IANA name); UTC when absent. No case hangs on it yet.
- `input.steps` (optional, added here): its steps, the to-dos whose `part_of` names it, as the list carries them. A row
  does not list its own pieces, so `steps {done, total}` cannot be worked out from the to-do alone.
- `expected.view`: the `TodoSummary`. Compare as JSON values: a field that is absent must stay absent. Optional
  booleans (`seen`, `can_wait`) are written only when true, an empty `args` is left out, and every time is RFC 3339 as
  Go writes it (`2026-11-10T09:00:00Z`, no `.000`).

All data is made up.

## The rules the cases pin

"Last activity" is today's (`docs/assistant/todo-status-sop.md` "Principle: real signals only"; `triageLastActivity`
in `server/internal/assistant/todo_triage.go`), with `restored_at` counting. A failed start reads as open although
`done_at` is stored: `done` holds exactly when the to-do is closed some other way.

| Case | View: phase · section · reason | Proposal 6.3 | Today's hub: section · rule |
| --- | --- | --- | --- |
| `failed_start_recent` | needs_you · needs_you · `failed_start` | row 1 | waiting · failed_start |
| `failed_start_after_two_days` | todo · todo · `rest`, with `inactive` | rows 1, 13, 14 | done · failed_start |
| `failed_start_edited_since` | todo · todo · `rest` | rows 1, 13, 14 | waiting · failed_start |
| `failed_start_due_ahead` | todo · todo · `rest`, with `inactive` | rows 1, 13, 14 | done · failed_start |
| `agent_asks_question` | needs_you · needs_you · `agent_asks` | row 2 | waiting · agent_asks |
| `interrupted_turn` | needs_you · needs_you · `interrupted` | row 2 | waiting · agent_asks |
| `owner_decides_decision` | needs_you · needs_you · `owner_decides` | row 3 | waiting · owner_decides |
| `can_wait_decision` | needs_you · can_wait · `can_wait` | row 4 | waiting · owner_decides |
| `awaits_reply` | needs_you · can_wait · `awaits_reply` | row 4 | just_done · finished_unseen |
| `agent_running_with_steps` | working · working · `agent_running` | row 5 | working · agent_running |
| `parked_external` | waiting · waiting · `parked` | row 8 | other · rest |
| `finished_unseen` | ready · just_finished · `finished_unseen` | row 9 | just_done · finished_unseen |
| `finished_seen_recently` | ready · just_finished · `finished_recent` | row 9 | done · seen |
| `finished_seen_over_a_day` | todo · todo · `finished_seen` | row 11 | done · seen |
| `closed_unseen` | done · just_finished · `closed_unseen` | row 6 | just_done · closed |
| `canceled_seen` | done · done · `canceled` | row 7 | done · closed |
| `someday_until` | someday · someday · `deferred` | row 12 | other · rest |
| `rest_restored` | todo · todo · `rest` | rows 13, 14 | other · rest |

The last column is what `triageTodo` answers for the same input today (checked once while writing the cases), for the
web's shadow comparison; the view differs from it only where 6.3 changes a rule.

### Fields

- `since`, when the reason began: `done_at` for `failed_start` and for closed to-dos; the deciding run's `state_since`
  (else its `updated_at`) for `agent_asks`, `interrupted`, `awaits_reply`, `agent_running` and the three `finished_*`
  reasons; the ask's `asked_at` for `owner_decides` and `can_wait`; the parking review's `at` for `parked`. Absent for
  `deferred` and `rest`.
- `next_change_at`, the next moment time alone changes the view: `done_at` + 48 hours for `failed_start` (then To do);
  `done_at` + 24 hours for `closed_unseen` (then Done); `seen_at` + 24 hours for `finished_recent` (then
  `finished_seen`); `someday_until` for `deferred`; the last activity + 48 hours for a `rest` row that can still get the
  Inactive mark (any failed start can). Absent otherwise.
- `rank`: 0 in every case, since each case lists one row. The order within a section comes with list cases later.
- `seen`: `seen_at` is at or after the moment that put the to-do where it is (`done_at` for a closed one, the last
  activity otherwise; the SOP's "Seen").
- `inactive`: the last activity, set when no row above 13 placed the to-do, nothing moved for 48 hours, and it is
  shelvable as the SOP's "Shelving" says (an Agent was linked or a conversation handed it out, no due date ahead, not
  an open concern) or its start failed.
- `can_wait`: true in the Can wait section.
- `asks`: only on rows in Needs you or Can wait that an ask holds there.
  - `id`: whose ask it is, `@`, and when it was asked, so a later ask of the same kind never takes an earlier one's id:
    `todo:<kind>@<round>` for the to-do's own ask with a round (`todo:decision@2026-11-10T09:00:00Z`);
    `todo:retry@<done_at>` for a failed start, since a retry that fails again stores a new `done_at`;
    `run:<machine_id>/<session_id>@<state_since>` for a run's ask (its `updated_at` when it has no `state_since`, as for
    `since`), since a session gets a new `state_since` each time it comes to wait on the owner. Each time is written as
    the input lists it.
  - A to-do's decision: `round` is the ask's `asked_at` exactly as listed, `text` its `question`, `recommend` and
    `options` as listed, `can_wait` its `for_briefing`, `default_at` its `deadline` when its `default` is `taken`.
  - A failed start: one `retry` ask on the to-do, with no round and no text (line 2 carries the error).
  - A run that waits on the owner: one `question` ask with `run`, and `text` from the session's `asks` (else its
    `attention_reason`).
- `steps`: `done` counts the closed steps (a failed start is open), `total` all of them; absent when there are none.

### Line keys

`line {key, args}`: clients say each key in their own words from stage 2 on; no hub catalog carries text. Args are
strings, and a time is absolute RFC 3339. The keys are stable identifiers: never renamed or reused.

| Key | Reason | Args |
| --- | --- | --- |
| `failed_start` | `failed_start` | `error`: the to-do's `start_error`, the hub's text (see "Judgement calls") |
| `agent_asks` | `agent_asks` | `run`: the asking run's title; `text`: what it asks (as its ask's `text`), absent when nothing says |
| `interrupted` | `interrupted` | `run` |
| `decision` | `owner_decides`, for a decision | `text`: the question; `recommend`, absent when none is recommended |
| `decision_default` | `can_wait`, for a decision Persona takes at its deadline | `text`, `recommend`, `default_at` |
| `awaits_reply` | `awaits_reply` | `run`; `text`: the session's `asks`, absent when it has none |
| `working` | `agent_running` | `run`; `text`: the run's `activity`, else the first line of its `last_text`; absent when neither |
| `parked` | `parked` | `reason`: the park's reason; `until`: the park's end |
| `finished` | `finished_unseen`, `finished_recent` | `run`: the run whose turn finished |
| `finished_seen` | `finished_seen` | `at`: when it finished (its `since`) |
| `completed` | `closed_unseen` or `completed`, a to-do closed without `cancelled_at` | `by`: its `done_by` (`auto`, `review`), absent when the owner closed it |
| `canceled` | `closed_unseen` or `canceled`, a to-do with `cancelled_at` | `reason`: its `cancelled_reason`, absent when none |
| `someday` | `deferred` | `until`: `someday_until` |

A `rest` row has no line: line 2 of a plain To do is the first line of its notes, which every client already has.

## Judgement calls

Where the proposal and today's code leave a choice open, these cases make it; change a case and this list together.

- **Can wait comes before the owner's decision.** Row 3 (a decision) would otherwise always hold before row 4 (an ask
  marked can_wait), so a decision with `for_briefing` is read as row 4.
- **A run's ask is `question` in stage 1.** The session list says that a run waits, not which card it shows (a question,
  a plan, a tool approval); the kind is refined when the hub reads the run's cards, with answering in place.
- **No ask for an interrupted turn or a reply awaited in text**: neither has a card to answer; the owner continues or
  replies in the run's session.
- **A park's end sets no `next_change_at`**: it passing changes nothing by itself (SOP rule 5a); the hub's wake at that
  time is a new fact, and so is a decision's default being taken at its deadline.
- **A failed start's 48 hours run from the failure** (`done_at`), not from its last activity as today's hub counts
  them, so an edit or a restore after the failure neither keeps it in Needs you longer nor brings it back there. After
  them it is a To do, and the Inactive mark follows its last activity like any task's, so a later edit or restore holds
  the mark back. Unlike other tasks it gets the mark with a due date ahead or with no Agent and no origin: a failed
  start waits for nothing.
- **A failed start after 48 hours has no line**: it is a `rest` row (row 14); its page still shows why it did not
  start.
- **`failed_start`'s `error` is the hub's text**, written once in the language of the moment the start failed: the
  to-do stores no error code (the plan keeps one). It is the only argument a client shows as it is. When the to-do keeps
  the code, a `code` argument (a key of its own) comes beside it, and clients say that instead.
- **A run's ask id follows the session's `state_since`**, the only moment per ask the session list carries. A session
  that asks twice without leaving Needs you keeps one id, as it keeps one ask in this list; a hub restart resets
  `state_since`, which gives an open ask a new id, so a client shows it again rather than hide a new one. The cards'
  own ids take over when the hub reads them, with answering in place.
- **A line's run text** prefers the run's `activity` (what it does now) to its `last_text`.

Not pinned yet (no case; the projection decides and adds one): `verify` (6.3 row 3 files a Check the result that
blocks under Needs you, while the SOP's "Decision table" keeps a plain "Please verify" under Just finished, which the
supervisor accepts by default: which asks block is open), `stopped_unfinished`,
`steps_proposed`, `signin`, `duplicate`, `persona_working`, `starting`, `queued`, `background`, `pr_open`, `usage_limit`,
`computer_offline`, `scheduled_report`, `someone_else`, `reported`, `superseded`, accepted and deployed work, a step's
ask rolling up to its parent, a Someday with no date, concerns, due days in `loc`, and `rank` across rows.

## Run

```sh
python3 scripts/test/test_wire_schema.py                                      # every view holds to TodoSummary
(cd server && go test ./internal/assistantwire -run TodoView)                 # Go reads and writes each one back
```
