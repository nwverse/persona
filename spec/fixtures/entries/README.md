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

The closeout cases cover reported done versus actual acceptance, unfinished and missing closeouts, the exact one-hour
boundary, successful supervisor follow-up versus automatic writes, read receipts and restarts, resumed execution,
shared-Agent request ownership, usage limits and explicit parks. An unfinished stop within its follow-up window
remains on To-dos, not a finished tray row. A hold wins over an Agent's generic blocked/external snapshot unless
actual execution resumes. A named Agent or external wait stays Waiting under #10411, even after an hour and with
a pending-review ledger; a generic blocked/external hint without a named wait is not that arrangement.

Runners: `web/src/lib/assistant-entries.test.ts`, `apple/PersonaTests/AssistantEntriesFixtureTests.swift`, and Android's
`AssistantEntriesFixtureTest`. The new `spec/fixtures/todo-view` and `spec/fixtures/todo-counts` belong to #10226;
these older fixtures do not claim the new projection, routes or client rollout have shipped.
