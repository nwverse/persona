# Plan: a recording port in the knowledge base (#13407)

Status: design for review, no code moved. Measured on `origin/main` at 4f5ff696c (2026-11-25).
Audience: maintainers of the server core and of the `recording` module.

## Goal

Meeting recording's storage still lives in `server/internal/kb`. The module (`server/internal/modules/recording`) may import
it only through one debt line, `recording internal/kb` in `server/internal/archguard/testdata/module_services.txt`. This
plan moves the storage and its writers into the module in a few pure-move PRs behind a narrow port, so the line can be
deleted. By the owner's definition of Core (UI-free capabilities and interfaces), the knowledge base's memory files and
search stay in Core; recordings, minutes and day recaps are the module's.

The earlier blocker is true and is the starting point: the writers share the store's private locks and are reached from
the core (https://github.com/nwverse/persona/issues/13407#issuecomment-7121773826).

## 1. Inventory

Measured with `git ls-files server/internal/kb | xargs wc -l` over the files below (prompt goldens in `testdata/prompts`
excluded: 30 recording-related golden files, about 400 lines).

| File | Non-test lines | Main types | Methods on |
| --- | --- | --- | --- |
| `recordings.go` | 1177 | `Recording`, `RecordingSegment`, `RecordingBookmark`, `RecordingCalendar`, `RecordingUpload`, `MeetingRef`, `RecordingState`, `RecordingPut` | 13 Engine, 7 Store, 13 other funcs |
| `minutes.go` | 1374 | `Minutes`, `MinutesItem`, `MinutesPoint`, `MinutesAction` | 10 Engine, 2 Store |
| `minutes_overview.go` | 551 | `MinutesOverview`, `MinutesConclusion`, `MinutesGroup`, `MinutesTopic`, `MinutesBullet` | none (pure) |
| `minutes_timeout.go` | 77 | | 1 Engine |
| `day_recap.go` | 626 | `DayRecap`, `RecapTarget`, `RecapSpan`, `DayRecapEntry`, `RecapTodo` | 5 Engine, 6 Store |
| `day_recap_write.go` | 1406 | | 25 Engine, 9 Store |
| `recording_day.go` | 596 | `RecordingCoverage`, `CoverageGap`, `RecordingAppend(ed)`, `BaseChangedError`, `SegmentsGapError`, `DayFullError` | 7 Engine, 2 Store |
| `recording_photos.go` | 1012 | `Photo`, `PhotoUpload`, `PhotoRef`, `PhotoText` | 20 Engine, 11 Store |
| `recording_audio.go` | 940 | `AudioWriter` | 15 Engine, 10 Store |
| `recording_commit.go` | 272 | | 9 Store (crash recovery, commit) |
| `recording_title.go` | 256 | | 3 Engine |
| `recording_trim.go` | 110 | | 3 Store |
| `recording_space.go` | 18 | `RecordingUpload.Clean` | 1 Engine |
| `live.go` | 890 | `Live`, `LiveSegment`, `LiveAsk`, `AskSource`, `AskAttempt`, `LiveTopic`, `LiveHighlight`, `SessionRef`, `LiveGapError` | 10 Engine, 5 Store |
| `meeting.go` | 636 | `Meeting`, `MeetingRoom`, `MeetingRoomLean`, `MeetingFollowup`, `MeetingHandoff`, `RoomView`, `ConvView` | 7 Engine, 3 Store |
| `meeting_material.go` | 364 | `MeetingMaterial`, `EarlierMeeting`, `EarlierProject` | 4 Engine |
| `transcription.go` | 560 | `Timeline`, `TimelineRole`, `MeetingTranscription`, `TranscriptionView` | 4 Engine |
| `hub_segments.go` | 365 | `HubTranscript` | 4 Engine, 7 Store |
| `echo.go` | 270 | `SpokenSpan` | pure functions |
| **Total** | **11,500** | | 64 exported Engine/Store methods |

Tests: 24 files, 12,331 lines (`recordings_test.go`, `recordings_day_test.go`, `recordings_echo_test.go`, `minutes*_test.go`,
`day_recap_test.go` (1,746), `recording_photos_test.go` (1,353), `live_test.go`, `transcription*_test.go`, `meeting*_test.go`,
`recording_*_test.go`, `echo_test.go`, `hub_segments_test.go`), 158 Test functions per the earlier count in #13407. They use
kb's unexported fixtures, so they move only with an exported fixture (PR 2).

Not counted as recording: `attribution.go` (490, extension write attribution; it takes `recFilesMu` once and names
`Recording` 9 times, so it needs a look in PR 5) and `day_identity.go` (107, chat identity, no recording reference).

The brief's figure of about 7,400 lines does not match 11,500; I could not determine which subset it counts. The difference
is most likely the contract types and the store primitives that stay (section 5), plus the files that need a decision
(`attribution.go`, `echo.go`). Measure again with `scripts/diag/core_size.py` once the plan's PRs land; it needs a Go
toolchain with the checksum database reachable, which this machine lacks (`GOSUMDB=off` made it fail), so I did not run it.

## 2. Coupling

### 2a. Core to recording (what the core needs of recordings)

| Where | What it needs | How |
| --- | --- | --- |
| `kb/store.go:229-235` | the five recording locks live in `Store` | state |
| `kb/store.go:267,273` | creates `recordings/`; runs crash recovery `recoverRecordings` (defined `recording_commit.go:186`) before ingest recovery | write, once at open |
| `kb/archive.go:61-94,154` | recording settings and callbacks as Engine fields: `RecordingDaysFunc`, `MinutesDaysFunc`, `MeetingChanged`, `MinutesWritten`, `DayRecapWritten`, `DayRecapClock`, `DayRecordingName`, the retry set `recordings` | callbacks, already module-filled by `Layer.Hook` (`modules/recording/layer.go:25-30`) |
| `kb/find.go:104-114,137,165,263-265` | which files of a recording are searchable (`recordingJournal`, recap, live, meeting, photos, hub-segment files); lists recordings as known entries | read |
| `kb/find_cache.go:162`, `kb/find_matches.go:27-43,114` | `meta.json`/`transcript.md` handling, id normalising (`RecordingID`), which files count as hits | read |
| `kb/items.go:285-297,514-546` | `kb_list`/`List` of recordings, newest first; `recordingEntry` | read a list, per call |
| `kb/search_index_update.go:80` | `recordingViewFile`: meta.json and transcript.md are not indexed | read, per file walked |
| `kb/update.go:216-277,332-343,768-773` | `Detail` for a recording (meta, minutes, recap), the card, and `recMu` held while a recording's detail changes (`update.go:771`) | read + lock |
| `kb/edit.go:77-95,150,237-247` | edit/delete of a recording: `recoverRecordingLocked` under `recFilesMu` (4 critical sections) | write + lock |
| `kb/dedupe.go:518-527` | takes `recMu` around a read-compare-write of a recording | lock |
| `kb/readonly.go:80` | decodes `Recording` for a read-only knowledge base | read |
| `kb/recordings.go:856` (`PruneRecordings`), `assistant/kb.go:538,543` | retention tick and `SummarizeRecordings(ctx, 3)` | write, every tick |
| `assistant/kb_lanes.go:118,121,124` | lanes `WriteMinutes(ctx,2)`, `TitleRecordings(ctx,2)`, `WriteDayRecaps(ctx,1)` | background jobs |
| `assistant/briefing_day.go:36,81,92,97` | `TodaysDayRecordings`, `DaySpan`, `ReadDayRecap`, `MinutesPending/Failed` for the evening briefing | read, per briefing |
| `assistant/tools_apps.go:154-189,748-789` | `kb_get` of minutes, recap, photos (`PhotosMarkdown`); `Recordings()`, `Recording(id)` for the meeting facts | read |
| `assistant/profile.go:90,536-547,877`, `assistant/settings_tool.go:989` | `kb.MinutesDaysFor` (retention window arithmetic) | pure function |
| `assistant/space_recordings.go` (15 refs), `assistant/recording_host.go:206,349,368`, `assistant/recording_todos.go` (10), `assistant/todo_images.go:67,75` | Space recordings, action-item to-dos, photo MIME and id checks | types and `RecordingID`, `Clean` |
| `assistant/kb_rpc.go:259` | `ApplyExtension` (extension writes; attribution) | write |
| `extmgr/kb_events.go:15` | names the kinds it forwards (`kb.KindChat`... ), not recordings | none beyond the kind list |
| `hub` | none (0 calls of the 64 methods) | |
| `prompts/meeting.go:5` | a comment only | |

Outside `kb` the core calls 13 distinct recording methods (13 call sites) and names kb recording types in 8 files (about 60
references). That is the surface the port must keep serving when the module is off.

### 2b. Recording to core kb (what the module uses of kb)

The module (`modules/recording`, 41 import lines of `internal/kb`) names 73 times `kb.Engine`, 54 `kb.Recording`, and the
live/meeting/photo/timeline types; it calls 50 distinct of the 64 exported methods (112 call sites; the rest are called by
the core only). Besides those it depends on kb's internals through the methods themselves:

| Shared state | Declared | Order and role | Taken in |
| --- | --- | --- | --- |
| `recMu` (Mutex) | `kb/store.go:229` | first; a recording's read-compare-write | `recordings.go` 4, `minutes.go` 6, `day_recap_write.go` 8, `recording_photos.go` 5, `recording_audio.go` 2, `recording_title.go` 2, `hub_segments.go`, `minutes_timeout.go`, `day_recap.go`, `recording_day.go`, `recording_space.go` 1 each; **core**: `update.go:771`, `dedupe.go:519` |
| `meetMu` (Mutex) | `store.go:232` | after `recMu`; `meeting.json` read-modify-write | `meeting.go` 2, `recording_audio.go` 1, `transcription.go` 1 |
| `liveMu` (Mutex) | `store.go:231` | after `meetMu`; live log read-modify-write | `live.go` 4 |
| `audioMapMu` + each audio rec's own mutex | `store.go:235` | leaf map lock; each audio rec's `mu` after `liveMu`, before `recFilesMu` | `recording_audio.go` 3 |
| `recFilesMu` (RWMutex) | `store.go:230` | after the audio mutexes; recording snapshots and recovery | `recording_commit.go` 5, `recording_photos.go` 7, `recording_audio.go` 8, `hub_segments.go` 5, `recordings.go` 3, `recording_*` others; **core**: `edit.go:78,95,238,247` |
| `metaMu` (Mutex, core's) | `store.go:226` | after `recFilesMu`; also guards the project log | taken by recording code at `recording_commit.go:199`, `recording_trim.go:91`, `minutes.go:197`, `day_recap.go:240,276` |
| `Store.changed`, `putEntity`, `notify`, `path` | `store.go` | change feed for the event stream | every writer |
| search index (`Store.search`) | `store.go:261` | `SearchIndexPurge` after retention (`recordings.go:865`) | prune, writers of summaries |

The documented total order is `recMu` -> `meetMu` -> `liveMu` -> audio rec mutex -> `recFilesMu` -> `metaMu`
(`kb/recording_audio.go:32`, `kb/store.go:229-235`). The core's four sections (`edit.go`, `update.go`, `dedupe.go`) are the
only code outside the recording files that holds any of them; everything else in the core touches recordings through the
public methods.

The recording writers also reach out through: the `Model` and `ModelFor` interfaces (`kb/archive.go:49-50`; the writers import
`internal/ai` only for error sentinels, `kb/minutes.go:354`), `prompts` (golden tests), `lazyre`, and `kbwire`.

## 3. The port

Three pieces, split the way the precedents were (#14249: Layer core to module, Host module to core; #15000: a temporary group
on the port; #14442: every seam follows the switch).

**Types move to a contract package, `recordingwire`** (pure: standard library only, listed in `contracts.txt`; it
carries every type in section 1 plus `RecordingID`, the `Kind*`/`Part*`/state constants and the sentinel errors). kb keeps
alias declarations (`type Recording = recordingwire.Recording`, the pattern of `kb/fields.go:15`) so the core's code and tests
compile unchanged. A module may import a contract package, so the module no longer names `internal/kb` for types.

**What the module asks of kb** (consumer-defined in the module, satisfied by an adapter kb offers): a file store under a
directory it owns, change notification, the search-index and retention hooks, a clock and the model.

```go
// package recordingwire (contract): interfaces are fine in a contract.

// Files is the module's view of the knowledge base: files under recordings/, nothing else.
type Files interface {
	Dir() string                                   // <data>/recordings, owned by the module
	WriteAtomic(rel string, data []byte) error     // fsync + atomic replace, as writeFile does today
	ReadFile(rel string) ([]byte, error)
	Remove(rel string) error
	ReadOnly() bool
	// Changed tells the event stream and the search index that rel changed (store.notify + index dirty mark).
	Changed(rel string)
	// UnderMeta runs fn holding the core's metaMu (taken last in the lock order).
	UnderMeta(fn func() error) error
	// IndexPurge asks the index to drop entries of removed files at its next update.
	IndexPurge()
	Now() time.Time
	Zone() *time.Location
}

// Env is the engine's settings and models the writers use (today Engine fields).
type Env interface {
	Model(id string) Model                       // Engine.modelFor
	RecordingDays() int                          // RecordingDaysFunc
	MinutesDays() int                            // MinutesDaysFunc
	Logf(format string, args ...any)
}

// Events replaces the five Engine callback fields filled by Layer.Hook.
type Events interface {
	MinutesWritten(ctx context.Context, r Recording, m Minutes)
	MeetingChanged(id string, parts []string)
	DayRecapWritten(ctx context.Context, r Recording, d DayRecap) (took []string, err error)
	LiveChanged(id string)
	DayRecordingName(day string) string
}
```

**What the module offers kb and the core** (`recordingwire.Provider`; kb holds `Engine.Recordings Provider`, nil when the module
is out of the build or switched off; every kb call site checks it):

```go
type Provider interface {
	// Kind-level reads the core's find, items and update paths need (section 2a).
	List() []Entry                                     // recordings as kb entries, newest first
	Get(id string) (Recording, bool)
	Detail(id string) (Detail, bool)                   // meta, minutes, recap for Detail / kb_get
	// File classification for the search walk and the finder.
	OwnsPath(rel string) (searchable bool, mine bool)  // replaces recordingViewFile, find.go:104-114
	// Store lifecycle under the module's own locks.
	Recover() error                                    // replaces recoverRecordings at open
	Edit(id string, fn func(*Recording) error) error   // replaces edit.go/update.go/dedupe.go sections
	Delete(id string) error
	// Background rounds, same cadence and lane names as today.
	Prune(now time.Time) int                           // PruneRecordings
	Summarize(ctx context.Context, max int) int
	WriteMinutes(ctx context.Context, max int) int
	TitleRecordings(ctx context.Context, max int) int
	WriteDayRecaps(ctx context.Context, max int) int
	// Reads for the briefing and the assistant's tools.
	TodaysDayRecordings(now time.Time, loc *time.Location) []Recording
	DayRecap(id string) (DayRecap, error)
	PhotosMarkdown(id string) string
}
```

**Absent module.** `Engine.Recordings == nil`: `List`/`Get`/`Detail` answer empty or not found (so `kb_get` returns the
ordinary not-found, the Knowledge page's Recordings list is empty), `OwnsPath` is false, so the finder and the index treat
the files as unknown and skip them (they do not appear in search or `kb_list`), lanes and prune do nothing, `Edit`/`Delete`
return `ErrNotFound`, `Recover` is skipped (pending commits stay on disk until the module is back). The briefing reads zero
day recordings. Nothing panics because each call goes through one helper (`e.recordings()`), the same shape as
`recordinghost`'s present/absent check. `moduleswitch`: the provider is registered in `internal/modules/all/recording.go`,
the same place and build constraint as the layer today, and the port hands it out only to a caller whose Set has the module on
(#14442), so a switched-off module is absent exactly as one built out.

**Where it lives and why archguard passes.** The interfaces and types are in the contract package (the new contract package recordingwire):
pure, so `TestContractsArePure` holds (standard library plus other contracts only; `Files.Now` returns `time.Time`, nothing
more) and the module imports it under `TestModulesUseOnlyTheSPI`. kb imports the contract, never the module
(`TestCoreImportsNoModule`). The adapter that implements `Files` and `Env` stays in `internal/kb` (a new a new recording-port file in kb)
and is handed to the module through the existing `recordinghost.Layer` (`Hook(e *kb.Engine)` becomes
`Hook(p recordingwire.Files, env recordingwire.Env)`), so `internal/modules/recording` imports only the contract and the
library packages it already imports (`prompts`, `lazyre`, `proto`, ... all in `libraries.txt` or contracts). With no import of
`internal/kb` left, the `recording internal/kb` pair is deleted; a stale pair fails the guard, so the PR that removes the last
import also removes the line. The `knowledge-ui internal/kb` line is a different module and not touched.

## 4. Locks and data

Rule: no change of behaviour, no change of lock order.

- `recMu`, `meetMu`, `liveMu`, `audioMapMu` and the per-audio mutexes move into the module (a `recordingStore` struct it
  owns) with the same documented order. They protect only recording files, and after PR 5 every taker is module code.
- `recFilesMu` moves too: its core takers are `edit.go` (4 sections) and recovery. Both become `Provider.Edit`/`Delete`/`Recover`
  calls, so the module takes `recMu` then `recFilesMu` itself. This is the main correctness risk (section 6).
- `metaMu` stays in the core: it also guards the project log. The module takes it last through `Files.UnderMeta(fn)`, which
  keeps today's order (`recFilesMu` before `metaMu`, `kb/store.go:230`). Its recording users are five (`recording_commit.go:199`,
  `recording_trim.go:91`, `minutes.go:197`, `day_recap.go:240,276`). PR 1 must say what `metaMu` protects there; I could not
  determine from reading alone whether it is only the change-feed ordering or also the project log's reads.
- `dedupe.go:519` and `update.go:771` stop locking; they call `Provider.Edit`.
- During the transition (PRs 3 and 4) the moved writers still use the core's locks through a temporary method on the port,
  `Files.WithRecordingLocks(fn)`, the same device as the temporary record group in #15000; it is deleted in PR 5 when the locks
  move.

On-disk layout does not change: `<data>/recordings/<id>/...` (meta.json, transcript.md, minutes.json, live and meeting files,
photos, `attachments/audio/`) stays where it is, "files first". After the move the module owns everything under `recordings/`;
the core owns the data directory, `README.md`, `projects/`, `chats/` and the index. No database migration: the knowledge base
keeps no recording state in Postgres (the module's other tables are untouched). Switched off: files stay, hidden from lists and
search (the index drops their entries at its next update because `OwnsPath` reports not mine; I did not verify the index's
purge on a path class change and PR 2 must test it), no new minutes or recaps are written, retention does not run, and turning the module back on picks the files
up again; crash recovery runs at that start. Retention clock: a recording older than `recording_days` is deleted by the first
prune after the module is back, which is today's behaviour after a long outage.

## 5. PR plan

Each PR is a pure move or an interface extraction; the default build and the wire stay byte-identical. Verification for all:
the kb and module test suites with a test database under the default build, `persona_without_recording` and `persona_core_only`
(tests of the module skip without it, as `needsRecordingModule` does today); `python3 scripts/ci/module-e2e.py --modules recording`
(close and reopen); the "Builds without the recording module" lane; and `go test ./internal/archguard/...`. Line counts are the
files' current non-test lines.

| # | PR | Files | Core lines out | Tests that move | Conflicts |
| --- | --- | --- | --- | --- | --- |
| 1 | `recordingwire` contract: the types, constants, errors, `RecordingID`; kb aliases | new contract (about 700 lines of type declarations, not measured); trims `recordings.go`, `minutes.go`, `minutes_overview.go`, `day_recap.go`, `live.go`, `meeting.go`, `transcription.go`, `recording_photos.go`, `recording_day.go` | about 0 net (types move to a contract, which counts in `core_size.py` unless excluded; ask) | none | any PR touching the kb recording types; all clients' codecs are unaffected (JSON tags unchanged) |
| 2 | The port: `Files`, `Env`, `Events`, `Provider`; adapter in kb; core call sites (2a) go through `e.recordings()`; exported fixture for tests (`kbtest`) | `find.go`, `find_cache.go`, `find_matches.go`, `items.go`, `search_index_update.go`, `update.go`, `edit.go`, `dedupe.go`, `store.go`, `archive.go`, `readonly.go`, `assistant/kb.go`, `kb_lanes.go`, `briefing_day.go`, `tools_apps.go` | 0 (adds about 600) | the core's recording-aware tests keep running on an in-kb implementation of `Provider` | hot files `items.go`, `update.go`, `assistant/tools_apps.go` |
| 3 | The minutes and day-recap writers | `minutes.go` 1374, `minutes_overview.go` 551, `minutes_timeout.go` 77, `day_recap.go` 626, `day_recap_write.go` 1406 | about 4,034 | `minutes*_test.go` (3,368), `day_recap_test.go` (1,746), `recordings_day_test.go` (610), prompt goldens for minutes and recap | `assistant/kb_lanes.go`, any prompt change |
| 4 | Photos, audio, titles, transcription | `recording_photos.go` 1012, `recording_audio.go` 940, `recording_title.go` 256, `transcription.go` 560, `hub_segments.go` 365, `echo.go` 270 | about 3,403 | `recording_photos_test.go` (1,353), `recording_audio_test.go` (582), `recording_title_test.go` (312), `transcription*_test.go` (803), `hub_segments_test.go`, `echo_test.go`, `recordings_echo_test.go` | the audio socket and `meeting_audio.go` in the module |
| 5 | The store itself and the locks; delete the pair | `recordings.go` 1177, `recording_commit.go` 272, `recording_day.go` 596, `recording_trim.go` 110, `recording_space.go` 18, `live.go` 890, `meeting.go` 636, `meeting_material.go` 364; `Store`'s five locks; `Files.WithRecordingLocks` removed | about 4,063 | `recordings_test.go`, `recording_commit_test.go` (700), `recording_trim_test.go`, `live_test.go` (829), `meeting*_test.go`; kb keeps only a black-box test with a fake `Provider` | `store.go`, `edit.go` |

Total expected: about 11,500 non-test lines leave `internal/kb` (12,331 test lines move with them), minus what the contract
package and the adapter keep (about 700 and 600 lines, so the core's net reduction is about 10,000 to 10,500 depending on
whether contracts count). What stays in kb: the directory layout and kind list, the file-class helpers, the search index, the
`Provider` seam with its nil behaviour, `attribution.go` (unless PR 5 shows it is recording-only), `day_identity.go`, and
`MinutesDaysFor` if the settings code keeps calling it (otherwise it moves with the contract).

The three-PR middle (3, 4, 5) can each be reviewed as a move; PR 2 is the only one with logic. PRs 3 and 4 are independent
after 2 and may be reordered or run in parallel by two workers; PR 5 must come last.

## 6. Risks and open questions

1. **Search index through the provider.** `OwnsPath` is asked once per file in the index walk; keep it a pure string check in
   the adapter, never a call into the module (measure with `find_bench_test.go`).
2. **Live transcription hot path.** `live.go` writers take `liveMu` many times a second while a meeting runs; any added
   indirection must stay a plain method call, no goroutine hop (PR 5; benchmark with `live_test.go`).
3. **The retention job.** `PruneRecordings` also trims all-day recordings (`recording_trim.go`) and sweeps hub transcripts; it
   must still run in the same tick as the other pruning (`assistant/kb.go:538`). Decision: keep one tick that calls `Provider.Prune`.
4. **Crash recovery order.** `recoverRecordings` runs inside `NewStore` today, before the module exists. After the move it
   runs when the module starts, so a recording with a half-committed upload is visible to the core in between. Decision
   needed: run recovery lazily on first `Provider` call, or accept the window (the finder skips it with `OwnsPath` false).
5. **`metaMu` semantics** (section 4): confirm what it guards before PR 2.
6. **Attribution and extension writes.** `attribution.go` and `kb_rpc.go:259` take `recFilesMu`/name `Recording`; decide whether
   extensions may write recording files at all (if not, drop that path in PR 2).
7. **Contracts and core size.** Does `core_size.py` count the new contract package recordingwire as Core? The owner's definition suggests types
   shared by core and module are Core-side contracts; the plan's numbers show both.
8. **Read-only knowledge base** (`readonly.go:80`) decodes `Recording` for a second reader; it must keep working with no module,
   or the read-only reader gets recordings only through the contract types.
9. **The `kb_get`/search behaviour with the module off** changes from "recordings found" (files exist) to "not found". This
   is intended (hidden when off) but visible; confirm with the owner.
10. **Test fixture.** An exported kb fixture (`kbtest`) is a new stateful package under `internal/`; check it passes
    `TestStandInsHoldNoModuleCode` (stand-ins must not import the module).

## 7. Not in scope

The Apple, Android and web recording clients, the wire (JSON shapes stay byte-identical), prompts (they stay under
`server/internal/prompts/`), `recordinghost.Host`'s 75 methods (a separate narrowing, #13407), the hub's meeting glue
(`hub/meeting_*.go`) and `internal/transcribelive`.
