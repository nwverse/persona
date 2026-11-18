# To-do counts fixtures

The numbers of the To-dos list and its places (#10226; `docs/design/proposals/todos-redesign.md` 3.3 and 3.7): one list
of to-dos → `counts` (`TodoCounts`, top level on `GET /api/v1/todos`) and the places (`TodoPlaces`, the answer of
`GET /api/v1/todos/places`), both in `schema/wire/todo-view.schema.json`. Each to-do here is placed as in
`spec/fixtures/todo-view`; most are the same rows. Today `scripts/test/test_wire_schema.py` holds the expected values to
the schema and the generated types read them back unchanged (the tests named in `spec/fixtures/todo-view/README.md`);
the projection's own test replays them next.

| File | What it pins | Cases |
| --- | --- | --- |
| `todo_counts.json` | A list → its counts and places | 3: an empty list, one to-do in each section, a failed start counted open |

## Format

`{"version": 1, "cases": [...]}`; a case is `{name, description, source, input, expected}`.

- `input.now`, `input.sessions`, `input.loc` (optional): as in `spec/fixtures/todo-view`.
- `input.todos`: the list exactly as `GET /api/v1/todos` lists it (`listedTodo` rows).
- `input.someday_until` and `input.restored_at` (optional): by to-do id, what `spec/fixtures/todo-view` gives one to-do.
- `input.projects` (optional, added here): the knowledge base's project titles by slug. A row carries only its
  project's slug, and a place is titled by its project.
- `expected.counts`: the `TodoCounts`. `expected.places`: the `TodoPlaces` answer, whose `counts` equal
  `expected.counts`. Compare as JSON values.

All data is made up.

## The rules the cases pin

- `open`: every to-do that is not closed, in any section but Someday. A failed start is open, though its
  `done_at` is stored; a ready or `finished_seen` to-do is open.
- `needs_you` (N): the to-dos in the Needs you section, each once; Can wait and quiet tasks (`quiet`) are left out, as
  the badge leaves them out today (`docs/assistant/todo-status-sop.md` "Header count").
- `in_progress`: the rows of Working and Waiting. Just finished is not counted.
- `sections`: the rows of each section, every `TodoListSection` key present, 0 included. A quiet task counts here as the
  row it is, so `sections.needs_you` can be larger than `needs_you`: where N is drawn, clients show `needs_you`.
- `places`: `all`, `today`, `upcoming`, then one place for each project with an open to-do (as `open` counts), ordered
  by slug (bytes, not a locale). `all` carries N; `today` and `upcoming` carry 0, since they draw no count (3.3); a
  project carries its part of N, counted the same way. Fixed places have the title `""`; a project's title is its
  `input.projects` title, else its slug. A manager's own answer has no `manager` field: the hub stamps it when it merges
  several managers' places, and adds their counts up field by field and key by key.

## Judgement calls

- Quiet tasks are left out of `needs_you` only, not of `sections` or `open`, which count the rows the list shows.
- `today` and `upcoming` say 0 rather than a number nobody draws, so their bytes need no Today rule yet.
- Projects are ordered by slug, which stays stable across languages and managers.
- **A project whose open to-dos are all in Someday gets no place** (`reading` in `mixed_list`): places follow `open`,
  which leaves Someday out, while proposal 3.3 says only "each knowledge-base project with open to-dos". Counting
  Someday in would keep such a project in the sidebar; change `mixed_list` with this line.
- **A project the knowledge base does not name still gets a place**, titled by its slug (`errands` in
  `failed_start_counts_open`), so its to-dos stay reachable from the sidebar; 3.3 lists knowledge-base projects only.
- Not pinned yet: a step counted under its parent once (proposal 3.4 "Steps roll up"), Today's and Upcoming's own
  rows, and what the hub does across several managers (its own test).

## Run

```sh
python3 scripts/test/test_wire_schema.py
(cd server && go test ./internal/assistantwire -run TodoView)
```
