// Package recordingwire is the meeting recording's part of the contract (docs/server/architecture.md "Contract
// packages"): the data types of a recording's files (photos, live log, meeting room, minutes lines, hub segments),
// their constants, sentinel errors and the error types that carry a position. It holds only types, constants and
// trivial pure helpers and imports only the standard library and other contract packages; internal/kb aliases these
// types and re-exports the constants and errors, so no caller changes (docs/plans/plan-kb-recording-port.md).
//
// Types that carry methods tied to the knowledge base's logic (Recording, RecordingUpload, Meeting, Minutes,
// Timeline and others) still live in internal/kb: a Go alias cannot take methods, so they move with their methods
// in the later steps of the plan.
package recordingwire
