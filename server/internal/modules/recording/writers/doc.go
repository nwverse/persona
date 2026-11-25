// Package writers is the recording module's minutes and day-recap writers: the rounds that make a meeting's minutes
// and a day recording's recap with a language model and store them next to the recording, and the requests that ask
// for them again or record the to-dos made of them. They reach the knowledge base that holds the recordings only
// through the storage port (recordingwire.Core); the knowledge base reaches them only through recordingwire.Provider
// (docs/plans/plan-kb-recording-port.md, section 3; docs/architecture/core-and-modules.md "Recording").
//
//persona:layer application
package writers
