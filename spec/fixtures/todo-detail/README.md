# To-do detail fixtures

The elements of a to-do's page, the paper (#10465; `docs/design/proposals/todos-redesign.md` ("3.5 The paper")): given
what the item route answers for a to-do and where its page is shown, which elements the page draws, in which order,
which action is its one primary, and whom its composer addresses first. Presence and order only: words, sizes and looks
are each client's (proposal 7.2). Every client replays these cases, iOS first (its paper, #10465), the web and Android
from their stages. `scripts/test/test_todo_fixtures.py` checks the format, holds every case to the rules below, and
keeps the everyday cases free of machinery (proposal 7.7).

| File | What it pins | Cases |
| --- | --- | --- |
| `todo_detail.json` | One to-do as the item route answers it, and where its page is shown → its elements, primary action and addressee | 17: the proposal's three columns (everyday, developer, several Agents), a fresh to-do, a closed one, failed starts with and without an origin, Just finished, the Inactive mark, and the conversation pane on the to-do's conversation and on a run |

## Format

`{"version": 1, "cases": [...]}`. A case is `{name, audience, description, source, input, expected}`; the descriptions
are English.

- `audience`: `everyday`, for the Assistant's audience, whose page needs no programming or Agent tools
  (`docs/SUPER_RULES.md` rule 4), or `agents`, for work Agents do on a computer.
- `input.item`: `{"todo": row}`, what `GET /api/v1/todos/{id}` answers from a manager that names `todo_view`
  (`docs/protocol/protocol.md`): the listed row with its `brief`, `agents`, the legacy `ask` while a decision waits, and
  `view` (`TodoSummary` in `schema/wire/todo-view.schema.json`). The view is given here, not worked out:
  `spec/fixtures/todo-view` pins how the manager works it out.
- `input.timeline` (optional): what the item answer adds while the hub lists `todo_timeline` (#10465), by count only,
  since only presence is pinned: `rounds` (rounds after the to-do was written down), `results`, and `recap` (whether
  there is a Since you looked). Absent: a hub without the feature, whose page draws none of the three.
- `input.context`: where the page is shown.
  - `wide`: a regular-width window (an iPad, the open iPhone Duo, the web), where the conversation can open beside the
    paper; false on an iPhone or the folded Duo, where it is pushed over the paper.
  - `pane_open`: the conversation shows beside the paper (only when `wide`; `spec/fixtures/todo-layout` says when it
    fits).
  - `pane_channel`: what the pane shows: `steward`, the to-do's conversation (its steward is Persona until a to-do can
    have another), or `run:<machine_id>/<session_id>`, one of its runs (`agents`); `null` while the pane is closed.
  - `opened_from`: what opened the page: `list` (the To-dos list or one of its places), `assistant` (the main chat: a
    task card, the tray), or `run:<machine_id>/<session_id>` (that run, from Agents).
- `expected.elements`: the elements drawn, top to bottom (the ids below). Compare as lists.
- `expected.primary`: the one action filled with the accent: `ask`, `mark_done`, `tick` or `none`.
- `expected.addressee`: whom the composer's chip names before the owner picks: `steward` ("To Persona") or
  `run:<machine_id>/<session_id>` ("To ‹run›").

All data is made up.

## Elements

| Id | Proposal 3.5 row | Drawn when |
| --- | --- | --- |
| `bar` | 0 | Always. Its leading item is the host's: ‹ with the place or what opened the page while it is pushed, the list or sidebar button while it is a root region (`spec/fixtures/todo-layout` says which region steps aside); then Conversation and ⋯ |
| `title` | 1 | Always: the tick and the title |
| `state_line` | 2 | Always, except a plain To do with nothing to say: phase `todo`, no `line`, no Inactive mark (`inactive`) and no `start_error` |
| `recap` | 3 | `timeline.recap` |
| `ask_card` | 4 | The first ask of `view.asks` the page can answer is the to-do's own (home `todo`): its card. A `retry` counts only on a to-do with an `origin` |
| `ask_door` | 4 | That ask is a run's (home `run`), and the pane does not show that run: the door "From ‹run› · ‹ask›" |
| `ask_pointer` | 4 | That ask is a run's, and the pane shows that run: the door's one line, "‹ask› · answer in the conversation →" |
| `chips` | 5 | The to-do is open (any phase but `done`: the + chip), or it has a due date, a reminder, a priority, a project or a parent (`part_of`) |
| `notes` | 6 | It has notes, or it has no brief (the Add Notes line) |
| `brief` | 6 | It has a `brief`: What you asked, with Done when |
| `help` | 7 | Never yet: Persona can help waits for the steps routes (#10465), so no case draws it |
| `steps` | 8 | `view.steps.total` is above 0 |
| `results` | 9 | `timeline.results` is above 0 |
| `conversation_block` | 10 | `timeline.rounds` is above 0, and the pane does not show the to-do's conversation |
| `conversation_pointer` | 10 | `timeline.rounds` is above 0, and the pane shows the to-do's conversation (`pane_channel` `steward`): "Conversation · open beside →" |
| `work_details` | 11 | Code work (`pr_url`, `pr_status`, `other_prs`, or `stage_kind` `code`), linked workspaces (`spaces`), or a run (`agents`, past ones included) |
| `composer` | 12 | The pane is closed; while it shows, the composer is in the pane with its draft |

The order is the table's. `ask_card`, `ask_door` and `ask_pointer` take one place, and so do `conversation_block` and
`conversation_pointer`.

### Primary action and addressee

- `primary` (proposal 3.5, "One primary action, chosen by state"): `ask` when the page draws `ask_card` or `ask_door`
  (the card's primary answer, or the door's Review ›); `none` when it draws `ask_pointer`, since the request's home in
  the pane holds the screen's one primary; else `mark_done` for phase `ready` (finished, open); else `tick`.
- `addressee` (`docs/design/proposals/todos-redesign.md` ("The addressee chip")): while the pane shows, its channel (a
  run, or the steward for the to-do's conversation); else the run the page was opened from; else `steward`.

## Cases

| Case | To-do | Audience | Shown | Elements after `bar` and `title` | Primary |
| --- | --- | --- | --- | --- | --- |
| `kyoto_needs_your_choice` | Plan the Kyoto trip | everyday | compact | state_line, recap, ask_card, chips, notes, brief, steps, results, conversation_block, composer | ask |
| `kyoto_selected_beside_the_list` | Plan the Kyoto trip | everyday | wide | state_line, recap, ask_card, chips, notes, brief, steps, results, conversation_block, composer | ask |
| `kyoto_from_the_main_chat` | Plan the Kyoto trip | everyday | compact, from the main chat | state_line, recap, ask_card, chips, notes, brief, steps, results, conversation_block, composer | ask |
| `kyoto_pane_on_the_steward` | Plan the Kyoto trip | everyday | wide, pane on the conversation | state_line, recap, ask_card, chips, notes, brief, steps, results, conversation_pointer | ask |
| `login_fix_waits_for_merge` | Fix login crash for SSO users | agents | compact | state_line, chips, brief, steps, results, conversation_block, work_details, composer | tick |
| `release_door_from_widget` | Release Weather App 1.3 | agents | compact | state_line, recap, ask_door, chips, brief, steps, results, conversation_block, work_details, composer | ask |
| `release_from_agents` | Release Weather App 1.3 | agents | compact, from `run:m1/s-notes` | state_line, recap, ask_door, chips, brief, steps, results, conversation_block, work_details, composer | ask |
| `release_pane_on_the_steward` | Release Weather App 1.3 | agents | wide, pane on the conversation | state_line, recap, ask_door, chips, brief, steps, results, conversation_pointer, work_details | ask |
| `release_pane_on_the_widget_run` | Release Weather App 1.3 | agents | wide, pane on `run:m1/s-widget` | state_line, recap, ask_pointer, chips, brief, steps, results, conversation_block, work_details | none |
| `fresh_todo` | Renew passport | everyday | compact | chips, notes, composer | tick |
| `closed_done_by_persona` | Fix widget refresh crash | agents | compact | state_line, chips, brief, results, conversation_block, work_details, composer | tick |
| `failed_start_with_origin` | Sort last year’s photos | everyday | compact | state_line, ask_card, chips, brief, conversation_block, composer | ask |
| `failed_start_without_origin` | Rename the holiday videos by date | everyday | compact | state_line, chips, notes, composer | tick |
| `failed_start_without_origin_older_hub` | Rename the holiday videos by date | everyday | compact, no `todo_timeline` | state_line, chips, notes, composer | tick |
| `failed_start_two_days_on` | Print the holiday photo book | everyday | compact | state_line, chips, notes, conversation_block, composer | tick |
| `just_finished_mark_done` | Book the dentist | everyday | compact | state_line, chips, notes, results, conversation_block, composer | mark_done |
| `inactive_todo` | Call the plumber about the boiler | everyday | compact | state_line, chips, notes, conversation_block, composer | tick |

The addressee is `steward` in every case but `release_from_agents` (`run:m1/s-notes`) and
`release_pane_on_the_widget_run` (`run:m1/s-widget`). The to-dos are the V1 prototype's
(`docs/design/prototypes/todos/data/todos.js`), with a few added for the rules the prototype does not draw.

## Judgement calls

Where the proposal and the prototype leave a choice open, these cases make it; change a case and this list together.

- **Notes and the brief both show** when the owner wrote notes on a delegated to-do, as the prototype's `iphone/kyoto`
  draws them. The Add Notes line shows only on a to-do with no brief, so a delegated one with no notes shows What you
  asked alone (`iphone/release`). Proposal 3.5 row 6 says "notes always" and its everyday column lists notes only; the
  prototype is followed.
- **A page offers no Retry the hub refuses.** `POST /api/v1/todos/{id}/retry` refuses a to-do that was not handed out
  by the assistant, so the hub lists a retry ask only on a to-do with an origin (#10465), and a page from an older hub,
  which still lists one, does not draw it (`failed_start_without_origin_older_hub`). That page says why it did not
  start, and its tick is the primary.
- **A failed start's page keeps saying why it did not start** after its row stops saying it
  (`spec/fixtures/todo-view/README.md` ("Judgement calls"): two days on it is a plain To do row with no line).
- **The Inactive mark is something to say**: an Inactive To do has a state line.
- **The pane on a run leaves the conversation block on the paper** (`ipad11-landscape/open~widget`): the pane shows the
  run, not the to-do's conversation, so nothing is drawn twice. The block turns into the pointer only while the pane
  shows the conversation.
- **While the pane shows a request's home, the pane holds the primary** (`none` on the paper), so the screen still has
  one.
- **A run's ask is a door on the paper whatever its kind and risk**: a door that answers a safe ask in place
  (proposal 3.7) differs in what it holds, not in which element it is.
- **A closed to-do's primary is its tick** (done, a tap reopens it); Mark Done is for open, finished work only.
- **The composer's chip follows the pane first, then what opened the page**: a pane on a run addresses that run
  (proposal 3.6, "The pane shows a run"), and a page opened from a run under Agents addresses it.
- **Where the page is shown changes nothing by itself**: the same to-do selected beside the list, pushed from the list
  or pushed from the main chat draws the same elements (`kyoto_selected_beside_the_list`, `kyoto_from_the_main_chat`).

## Not pinned yet

- Several asks on one page (proposal 3.5: "same-kind asks merge"). Each case has one; the page draws the first one it
  can answer. The order and merging of several come with the first case that needs them.
- One of Persona's own conversations linked as the to-do's run (`persona_working` in `spec/fixtures/todo-view`) is
  Persona's work, not an Agent's on a computer; whether it unfolds Work details waits for a case that carries the
  session list.
- Persona can help (`help`), suggested steps and the chat topic channel: with the steps routes and the chat topic's
  reader. `help` then joins `fresh_todo`, which shows five things instead of four.
- The narrow paper in the Duo's halves (the prototype folds Since you looked and Steps at 335 pt,
  `duo-inner-portrait/list-paper`): decided with the Duo's measurement (proposal 3.9).
- The keyboard's folded paper (proposal 3.8, "The keyboard"): a layout of these elements, not another set.

## Run

```sh
python3 -B scripts/test/test_todo_fixtures.py   # the format, the rules above, the everyday guard
```
