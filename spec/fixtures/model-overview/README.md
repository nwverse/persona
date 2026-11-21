# Model overview fixtures

`overview.json` shows a known configured launcher, a confirmed fallback and a computer with no launcher snapshot.
`running-cases.json` describes the client-side grouping inputs for the Running now section. A null group excludes a
session. A null launcher inside a group means the client displays its localized unidentified launcher label.

The overview API uses stable fit, status, source, problem and action codes. Human-readable notes and problem text
may change with the manager's locale. Clients can use `revision` to compare responses in the same locale.
