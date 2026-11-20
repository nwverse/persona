# Assistant entry fixtures

Shared inputs for the Web, Apple and Android compatibility entries while #10174 builds the hub's single projection.
Needs you is the owner's action; In progress counts Working plus Waiting. An Agent's reported finish awaiting
acceptance is in `just_finished`, shown below In progress but uncounted. There are no peer acceptance or stopped-work
entries. The Web keeps its existing internal `parked` array, drawn inside Waiting; the shared expected `running`
combines that array with real execution. This is a compatibility representation, not another section model.

`needs_you`, `running`, `shelved` and `just_finished` contain stable row keys. `finished` lists the last 24 hours of
finished work (#10922), and `finished_new` identifies rows the owner has not opened since they finished. The last group sorts newest first,
then by key on equal timestamps. `activity` and `waiting` describe real background activity and external holds.
`supervisor_review` is empty: a pending acceptance is not counted active work or an owner decision because it aged.

- Structure: `{"version": 1, "cases": [...]}`. `input` has `conversation`, `main` (the main conversation's id), `now`
  (RFC 3339, for the 48-hour shelving line), and the hub's wire objects: `todos`, `sessions` (with `state` worked out),
  `interactions` (pending cards), `plans` (by id) and `feed` (the conversation's items).
- Row keys: `task:<todo id>`, `agent:<machine id>/<session id>`, `plan:<plan id>`, `message:<feed item id>`.
- `expected.finished` and `expected.finished_new` are optional (absent means none). Finished rows are in no other
  entry, and the entries' counts do not include them.
- Background activity cases (#3588) carry `expected.activity` keyed by running row: `tasks`, `subagents`,
  `workflows`, and optional `phase` / `progress`; null means no background work.
- Not covered here: cards of the conversation's own session (they are always Needs you rows and are added by the
  screen), message cards the user put off (`later`), Android's inbox drafts, and subtasks.
- All data is made up.

The closeout cases cover reported done versus actual acceptance, unfinished and missing closeouts, the exact one-hour
boundary, successful supervisor follow-up versus automatic writes, read receipts and restarts, resumed execution,
shared-Agent request ownership, usage limits and explicit parks. An unfinished stop within its follow-up window
remains on To-dos, not a finished tray row. A hold wins over an Agent's generic blocked/external snapshot unless
actual execution resumes. A named Agent or external wait stays Waiting under #10411, even after an hour and with
a pending-review ledger; a generic blocked/external hint without a named wait is not that arrangement.

Runners: `web/src/lib/assistant-entries.test.ts`, `apple/PersonaTests/AssistantEntriesFixtureTests.swift`, and Android's
`AssistantEntriesFixtureTest` (`cd web && npx vitest run src/lib/assistant-entries.test.ts`,
`cd android && ./gradlew :core:domain:test --tests '*AssistantEntriesFixtureTest*'`). The new `spec/fixtures/todo-view`
and `spec/fixtures/todo-counts` belong to #10226; these older fixtures do not claim the new projection, routes or
client rollout have shipped.
