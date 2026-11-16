# Phase 4 design: storage in the core (#6793, design issue #9085)

This is the design for phase 4 of `docs/plans/plan-rust-core.md`: the per-hub caches and the snapshot that launch
draws from (D6) move into the Rust core, files first (decision 4). Its exit condition is "cold launch draws from the
snapshot before any sync". Nothing here is implemented yet; the open questions at the end are for the maintainer, and
this document records the recommendation for each.

**Who it serves**: everyone who uses the phone apps, indirectly, exactly as the parent plan. The user-visible goal is
that a cold launch paints the same lists and the same last two turns it painted when the app was last open, without
waiting for the hub. Nothing the user sees is meant to change; the storage layer is what moves.

## 1. What each platform stores today

The survey below is the contract the core has to reproduce. Sources are the current sources, not the redesign doc;
where D6 (`docs/archive/redesign/README.md`, "D6. Local data and scopes") differs, the difference is called out.

### 1.1 iOS

| Store | Location | Format | Versioning |
| --- | --- | --- | --- |
| `ChatCache` (`apple/Shared/ChatStore.swift`) | `Caches/chats-v19/[group-<id>/]<machine>_<session>.json` | One JSON `Entry { items, hasMore, history, covered }`, atomic `Data.write(options: .atomic)` | Folder name `chats-vN`; bump drops all older folders on next launch. Currently v19; v15 was skipped (v14 → v16) |
| `StateCache` (`apple/Packages/PersonaWire/Sources/PersonaWire/StateCache.swift`) | `Caches/state/<name>.json` (63 call sites) | One JSON value per name, atomic write, writes on a detached utility task | No version field; each consumer owns its name and shape |
| Launch snapshot (`AppModel.DevSnapshot`) | `Caches/state/dev.json`, `dev.<group>.json` | `{ machines, sessions, interactions, assistant, managers, default_manager, picked, picked_manager }` | Unversioned; a decode failure is silently skipped (`guard let … else { continue }`) |
| `AgentSync` revs | `Caches/state/agent-sync.json` | `[String: Int64]` of `<machine>/<session>` → last pulled rev | Unversioned |
| Secrets (hub token, push keys) | Keychain, `WhenUnlockedThisDeviceOnly` | — | Out of scope; stays native through `PlatformHost` |

Cold-launch read order (P10 in `docs/apple/performance.md`): `App.init` and the first `body` read only the cached
snapshot; network, most keychain reads and migrations wait until after the first frame. `PerfTrace.event("Snapshot
restored")` marks the decode, `First frame (root appeared)` the paint, `Connected` the hub. The snapshot write is
throttled to at most one per minute (`snapshotWriter`, #4684) and flushed on backgrounding; it is about 390 KB on a
hub with about 230 sessions.

`ChatStore.init` reads its `ChatCache` synchronously, keeps the last two user-question turns plus the exchanges that
launched any retained subagent task (`ChatCache.tail`), caps one chat's file at 6 MiB and drops image items when the
cap is exceeded. `AgentSync` warms the disk cache of up to 40 recent sessions (24 h window, three concurrent pulls)
so a chat that was never opened this launch still paints instantly.

Multi-hub separation: the default group lives at the top of `chats-v19/` and `state/`; every other group's files sit
in `group-<id>/` (ChatCache) or carry a `.<group>` suffix (snapshot names). Group ids are sanitized to
`[A-Za-z0-9_-]`. The same machine and session ids may exist in two groups, so the group is part of every key.

Encryption boundary: caches are plain JSON in the app sandbox. iOS file protection is the default for the Caches
directory; no additional encryption is applied, and no keychain key wraps cache bytes. This is the behavior the core
must reproduce, not a recommendation to change it (see Q6).

### 1.2 Android

| Store | Location | Format | Versioning |
| --- | --- | --- | --- |
| `ItemsDatabase` (Room, `core/data/DataDatabases.kt`) | `cacheDir/hub-data/<scope>/items.db` | SQLite, `items(scope, machine, session, seq, part, id, json)` + `cursors(scope, machine, session, history, covered, has_more)` | Room `version = 6`; migrations 3→4, 4→5, 5→6 each `DELETE FROM items/cursors` (cache rows only, refilled from hub) |
| `PersistentDatabase` (outbox, drafts, main conversation) | `filesDir/hub-data/<scope>/data.db` | SQLite, version 6 | Out of phase-4 scope: durable, not a cache |
| Images / inbox media | `cacheDir` file caches with LRU budgets | Raw bytes | Unversioned |
| Credentials | Keystore-generated key + Tink | — | Out of scope |

`<scope>` is `HubScope(hubId, generation).path`: hub instance UUID + pairing generation, so re-pairing or changing
hubs opens a fresh directory and `eraseScope` deletes the old one. Android already separates per-hub directories the
way D6 describes; iOS separates by group id, not by hub id + generation.

`SessionItemsCache.save` writes the reducer's merged view as a row difference (only changed rows hit the WAL), keeps
the same "last two user turns + subagent launches" window as iOS, strips inline image data (`image.data = ""`,
`lazy = true`), normalizes refusal items (`publicRefusalItem`, #2528), and maintains a byte ledger with a 32 MiB
budget; eviction is LRU by last access, pinned sessions on screen excluded. The full scan runs only when the ledger
says the budget might be exceeded (#4045).

Cold launch: Android paints from `SessionItemsCache.load` through Room; there is no separate launch snapshot of the
Agents list comparable to iOS's `DevSnapshot` (the session list comes from `PersistentDatabase` / hub replay). This
asymmetry matters for the design: phase 4 must not force Android to adopt iOS's snapshot shape, but the core-owned
snapshot gives Android the same cold-start behavior iOS already has.

### 1.3 HarmonyOS

| Store | Location | Format | Versioning |
| --- | --- | --- | --- |
| `LocalStore` (`platform/LocalStore.ets`) | RDB `persona.db`, table `client_state(key TEXT PRIMARY KEY, value TEXT)` | JSON strings, security level S3 | None |
| Transcript cache (`core/TranscriptCache.ts` + `models/TranscriptModel.ets`) | One `client_state` row per chat | `TranscriptSnapshot { items, covered, history, hasMore, loadedOnce }` as JSON, bounded to 384 KiB / 256 items, complete suffix only | None |
| Credentials | Asset Store | — | Out of scope |

`LocalStore` carries per-hub retirement epochs: a hub's keys are namespaced (`isHubStateKey`), and unpairing bumps
the epoch so late writes with an older epoch are rejected rather than landing in a new pairing's rows. Writes are
serialized per store name through a promise queue.

The bounded-suffix rule (never cache a partial message) is a HarmonyOS-specific behavior the shared design should
adopt for every platform: a snapshot must only ever contain items the reducer considers complete.

## 2. Constraints from the parent plan and from #8227

- Decision 4: `persona-core-runtime` writes the per-hub caches and the launch snapshot itself, in the same binary
  encoding as the boundary (bincode), on its worker thread. SQLite joins only when a domain needs indexed queries
  over large data.
- D6: durable data gets a versioned envelope — newer reads older, older refuses to overwrite newer, and **unreadable
  data preserves the original file, stops writes, and warns; never silently replace with empty data**. Caches are
  disposable. The launch snapshot is a cache by this table (it can always be rebuilt from the hub), but the durable
  rule about preserving unreadable files still informs the corruption design below.
- PlatformHost (phase 4 row): the host passes the per-hub scope directories when it creates the core. Storage is not
  a host effect; only the directories and, if needed, file-protection attributes come from the host.
- #8227 (being fixed in #8922): the core accepts repeated item keys on the wire with first-value-wins semantics and
  preserves the original JSON, so its snapshot reader must accept those same bytes. Whatever byte format phase 4
  chooses, the snapshot restore path must use the same `Item` parsing as the wire path (`Item::from_wire` semantics,
  first value wins), not a stricter derived deserializer. This is a hard constraint, not an option: any design that
  re-serializes items through a normalizing encoder and re-reads them with a strict decoder reintroduces the bug.
- The runtime's existing `Action::OpenChat { chat, snapshot }` already carries `ChatSnapshot { items_json, history,
  covered, has_more, loaded_once }`. Phase 4 is about who produces and persists those bytes, not about changing the
  action's shape.

## 3. The proposed design (recommended)

### 3.1 Directory layout

One directory per hub scope, handed to the core at creation:

```text
<core-root>/<scope>/                       scope = group id on iOS, hub_id+generation on Android/HarmonyOS
  snapshot.bin                             the launch snapshot (Agents list, interactions, assistant roster)
  snapshot.bin.tmp                         in-flight atomic write
  chats/
    <machine>__<session>.bin               one chat's cached tail
    <machine>__<session>.bin.tmp
  manifest.json                            format version, scope identity, created-by core version
```

- `<core-root>` comes from the host: `Caches/persona-core/` on iOS, `cacheDir/persona-core/` on Android, the app
  sandbox cache directory on HarmonyOS. The host picks the root because only the OS knows the sandbox; everything
  below it belongs to the core.
- File names reuse today's sanitization (`[A-Za-z0-9_-]`, everything else `_`). The separator between machine and
  session becomes `__` (double underscore) so a machine id containing a single underscore cannot collide with the
  boundary; today's iOS name `<machine>_<session>.json` has that ambiguity and is not carried over.
- `manifest.json` is the only JSON the core writes. It exists so that a future core can tell "my scope, older
  format" from "not my directory" without opening every `.bin`.

### 3.2 Byte format

Each `.bin` file is:

```text
magic "PCACHE01"        8 bytes
kind                    1 byte (1 = launch snapshot, 2 = chat tail)
bincode payload         the same wire types the boundary uses
```

- The launch snapshot payload is a new wire type `LaunchSnapshot` (see 3.3).
- The chat-tail payload is exactly `ChatSnapshot` from `persona-core-runtime/src/wire.rs`: `items_json` holds the raw
  item JSON array **byte for byte as the hub sent it**, which is what makes #8227's first-value-wins restore rule
  natural: the restore path is the same `items_from` the runtime already uses for `Action::OpenChat`.
- No per-file checksum in v1: a torn write is prevented by the atomic rename, and a semantically corrupt file is
  caught by the bincode decode plus `items_from` validation. Adding a CRC32 of the payload (4 bytes) is cheap
  insurance against silent disk corruption and is Q4 below.
- The format version lives in the magic bytes, not in a per-file header field. A version bump is a new magic
  (`PCACHE02`), so an old core refuses a new file by failing the magic check rather than misreading it, and a new
  core can choose to keep a reader for `PCACHE01` (caches: usually not worth it, see 3.4).

### 3.3 What the launch snapshot contains

`LaunchSnapshot` mirrors iOS's `DevSnapshot`, because iOS is the behavioral reference (decision 7):

```rust
pub struct LaunchSnapshot {
    pub machines_json: String,       // raw JSON arrays, preserved byte for byte
    pub sessions_json: String,
    pub interactions_json: String,
    pub assistant_json: Option<String>,
    pub managers_json: Option<String>,
    pub default_manager: Option<String>,
    pub picked_json: Option<String>,
    pub picked_manager: Option<String>,
    pub saved_at_unix: i64,          // supplied by the host clock at write time; the core has no clock
}
```

Raw-JSON-string fields, rather than typed Rust structs for `Machine`, `Session`, `Interaction`, are deliberate:
those types have not been ported into the core, and phase 4 is not the phase that ports them. The core treats the
lists as opaque payloads it persists and hands back; decoding them into view models stays native until a later
phase moves those domains. This keeps phase 4 small and avoids freezing a typed schema for structures the hub is
still changing.

### 3.4 Versioning and migration

Two independent version axes, matching how the platforms already work:

1. **Envelope version** (the magic). Bumped when the bincode layout changes. Caches: on mismatch, the core deletes
   the file (or quarantines it, see Q4) and the client falls back to a sync, exactly like Android's
   `DELETE FROM items` migrations and iOS's folder-name bumps. No migration code for cache payloads.
2. **Content version** (semantic, like iOS's `chats-v19` history). Several of iOS's bumps were not layout changes
   but "the hub now words this item differently" (v6, v10, v18, v19). The core must reproduce those invalidations:
   the manifest carries `content_version`, and the launch/chat payloads are dropped when it moves. The parent plan's
   rule applies: the content version is bumped whenever the item structure or the text the hub gives an item
   changes, and the bump is recorded in the manifest's history the way the `chats-vN` comment records it today.

Because caches are disposable, "migration" always means "drop and re-fetch". Durable stores (outbox, drafts) are
explicitly out of phase 4; when they later move, they get a real versioned envelope per D6, not this drop rule.

### 3.5 Atomic writes and crash recovery

- Every write is: serialize to `<file>.tmp`, `fsync` the temp file, `rename` over the target, `fsync` the directory
  (on filesystems that need it for rename durability). This is the same guarantee iOS's `.atomic` write gives today.
- A `.tmp` file found at load time is an interrupted write: delete it. The target file, if present, is the last good
  state by construction.
- A target file that fails the magic check, the bincode decode, or `items_from` is corrupt: rename it to
  `<file>.corrupt-<n>` (bounded, at most one kept per file) and start empty. Never overwrite it in place, per D6's
  "preserve the original file" rule; but unlike durable data, a corrupt cache does **not** stop later writes — the
  next save writes a fresh good file, because a cache's correct recovery is a refetch, not a halt. Diagnostics
  record the kind and the decode error code, never item content (the boundary's existing rule).
- Partial directories (a `chats/` folder with no manifest, or a manifest whose scope does not match) are treated as
  not-mine and left untouched; the host is responsible for deleting scopes on unpairing, as today (`eraseScope`).

### 3.6 Budgets and eviction

The core owns the byte budget the way Android's `SessionItemsCache` does today, since it is the only writer:

- Per-chat cap: 6 MiB after the window is applied (iOS's `maxBytes`), dropping image items first, then trimming
  further turns if still over.
- Window: the last two user-question turns plus the exchanges that launched retained subagent tasks. This logic is
  currently duplicated (`ChatCache.tail`, `SessionItemsCache.save`, `boundedTranscriptCache`); phase 4 moves it into
  `persona-core` as a pure function over the reducer's items, with fixtures from the existing iOS behavior (it is a
  small domain of its own and follows the migration rules: fixtures first).
- Scope budget: 32 MiB of chat tails per scope (Android's default), LRU by last access, chats on screen pinned.
  iOS has no scope budget today; adopting Android's is a behavior change for iOS and is Q3.
- The launch snapshot is exempt from eviction (one file per scope, hundreds of KB) but is written at most once a
  minute with a flush on backgrounding, reproducing iOS's throttle. The core has no clock, so "a minute has passed"
  and "the app is backgrounding" arrive as actions (`Action::Tick`, `Action::FlushStorage`) from the host.

### 3.7 PlatformHost capabilities to add

| Capability | Why | Shape |
| --- | --- | --- |
| Scope directories | Only the OS knows the sandbox; decided already (phase 4 row) | `CoreConfig.scopes: Vec<(ScopeId, PathBuf)>` at creation; adding/removing a hub is a new core or an action |
| Clock reads for throttling | The pure core has no clock | `Action::Tick { now_unix: i64 }` from the host; writes compare against the last write's timestamp |
| Background/quit flush | iOS flushes the snapshot writer on backgrounding | `Action::FlushStorage`; the worker finishes the pending write before acknowledging |
| File protection level (iOS) | Caches inherit the directory's protection; the core must not accidentally raise it | Host sets attributes on `<core-root>` at creation; the core never changes them (Q6) |
| Low-disk / purge signal | OS may purge Caches; the core should rebuild rather than fail | On load, a missing file is simply "no cache", which is already the cold path |

No new host *effects* are needed for storage itself: decision 4 keeps the actual IO on the runtime worker.

### 3.8 How it connects to the continuity snapshot

Today a client opens a chat by reading its native cache and dispatching `Action::OpenChat { chat, snapshot }`. After
phase 4:

1. The host asks the core to open a chat (`Action::OpenChat` gains a `from_storage: bool`, or a new
   `Action::OpenChatStored { chat }`; Q5).
2. The worker reads `chats/<machine>__<session>.bin`, decodes `ChatSnapshot`, and restores the session through the
   same `session.restore` the action uses today. Items go through `items_from`, which after #8922 accepts
   duplicate keys first-value-wins, so any bytes the core itself wrote (raw hub JSON) round-trip by construction.
3. The first `ChatPatch` the client drains is therefore the cached tail, before any network effect is emitted. The
   fetch effects that continuity would issue after a restore (`FetchAfter` for a gap, etc.) are unchanged: the
   snapshot is a starting state, not a sync replacement.
4. Saving is the mirror: after each applied step that changes the chat, the worker recomputes the windowed tail
   (pure function) and writes it, coalesced. Writes never cross the boundary; the client does not see them.

The launch snapshot follows the same pattern with a separate `Action`: the host dispatches
`Action::RestoreLaunch` at cold start before creating any subscriptions, and the worker publishes a launch patch the
native list models consume. Until the domains for machines/sessions/interactions move into the core (later phases),
the launch patch carries the raw JSON fields of 3.3 and the native side decodes them exactly as it decodes
`DevSnapshot` today.

### 3.9 Switch order per platform

Following the migration rules (adapter, shadow run, delete) and "iOS shadow first":

1. **iOS shadow.** `ChatCache` and the `DevSnapshot` path gain a behind-the-scenes mirror: the native code stays
   authoritative, and every save also dispatches to a DEBUG-only core that writes its own files. On every load, the
   shadow compares what it would have restored (item ids/order, `history`, `covered`, `hasMore`, snapshot field
   hashes) and reports mismatches to diagnostics with no content. One TestFlight cycle, mirroring #8970's approach.
2. **iOS authoritative.** After a clean shadow, `ChatStore.init` and `restoreSaved()` read from the core. The old
   `chats-v19` and `state/dev*.json` files are left in place for one release (read-only fallback if the core file is
   absent) and then deleted; the folder version is bumped so old copies are not misread.
3. **Android.** `SessionItemsCache` moves to the core behind the existing `ChatItemsCache` interface; Room's
   `items.db` is kept read-only for one release as fallback, then dropped. Because Android already has per-scope
   directories and drop-migrations, this is the closest port.
4. **HarmonyOS.** `TranscriptModel`'s `client_state` rows move to core files; `LocalStore` keeps its other keys.
   The retirement-epoch rule (late writes to an unpaired hub are rejected) maps onto the scope directory being
   deleted on unpairing; the core must also refuse writes to a scope its config no longer lists.

Each step is its own PR with its own measurements; none changes what the user sees.

### 3.10 Metrics to measure

All compared against the current native implementation on the same device, build and workload, per decision 6/7 and
`docs/apple/performance.md` ("Measuring"). Physical-device evidence is currently blocked by the maintainer's
2026-11-14 decision; simulator/host numbers are labelled as such and the cells stay open.

| Metric | Budget / expectation |
| --- | --- |
| Cold launch to first frame | No regression beyond run-to-run noise; snapshot read happens off the main thread and before sync, as today |
| Snapshot read latency (decode + restore) | Reported per scope size; target: not slower than today's `StateCache.load` + JSON decode of ~390 KB |
| Chat tail read latency | Reported; compared with Room `load` on Android and `ChatCache.load` on iOS |
| Snapshot write cost on the worker | Reported; writes are throttled as today and must not delay patches |
| File sizes | Chat tails comparable to today's JSON (bincode envelope + raw JSON items adds ~9 bytes per file); snapshot comparable |
| Bytes on disk per scope after a week | Within the 32 MiB budget (Q3 for iOS) |
| Corruption recovery | Fault-injection tests: truncated file, bad magic, duplicate-key items (the #8227 corpus), interrupted write leaves a usable previous file |
| Boundary cost | `dispatch`/`drain` p99 budgets unchanged; storage adds no new per-frame work |

## 4. Alternatives considered

### A. Files in the core, bincode envelope + raw item JSON (recommended)

As above. **Pros**: follows decision 4 literally; no new dependencies; items round-trip byte for byte, which #8227
requires; one writer, so budgets and eviction live in one place; cold-launch read never crosses the boundary.
**Cons**: no indexed queries (fine for "load whole tail" workloads); two formats on disk during migration; the
window/eviction logic must be ported and fixture-tested.

### B. SQLite in the core (rusqlite, bundled)

One database per scope, tables mirroring Android's `items`/`cursors` plus a `launch` row. **Pros**: Android's
current shape, incremental row writes already proven there; queries (by seq, by session) are natural; atomic commits
come from SQLite rather than hand-rolled rename. **Cons**: adds a compiled dependency to every ABI and the XCFramework
(decision 4 says SQLite joins only when a domain needs indexed queries over large data — the launch snapshot and
chat tails are whole-blob reads, not queries); iOS would move *away* from plain files to a database for data it
currently reads with one `Data(contentsOf:)`; size and build cost on three platforms; raw JSON preservation needs the
same care, plus SQLite-level corruption recovery is harder to reason about than "one file, atomic rename".
**Verdict**: premature; revisit when a domain (e.g. full-history search) actually needs indexed queries.

### C. Keep storage native, pass snapshots across the boundary

The core never touches disk; the host reads its existing caches and dispatches `Action::OpenChat` with the snapshot
as today, and the core emits `Effect::SaveSnapshot` for the host to persist. **Pros**: zero new IO in the core; each
platform keeps its tuned store; smallest change. **Cons**: this is exactly what decision 4 rejected — every save
copies the full tail across the boundary, and launch reads it back natively and copies it in again; three storage
implementations keep drifting (the window logic and refusal normalization already differ subtly); the "cold launch
draws from the snapshot before any sync" exit condition stays a per-platform property instead of a core guarantee.
**Verdict**: rejected by decision 4; recorded here because it is the do-nothing baseline the measurements in 3.10
must beat or match.

## 5. Questions for the maintainer

**Q1. Scope identity: group id (iOS model) or hub id + pairing generation (Android/HarmonyOS model)?**
Recommendation: **hub id + pairing generation everywhere**, with the group id kept as an additional key inside the
scope for iOS's multi-hub grouping. Rationale: D6 already specifies `<hub_id>/<pairing generation>/`, and
generation-scoped directories make "re-pairing starts clean" a filesystem property instead of per-key epoch checks
(HarmonyOS) or group bookkeeping (iOS). Cost: iOS gains a scope-mapping step at pairing time and a one-time move of
existing caches (or accepts a cold cache on upgrade — caches are disposable).

**Q2. Is dropping-and-refetching on every content-version bump acceptable as the only migration strategy for phase
4?** Recommendation: **yes**, matching iOS's folder bumps and Android's `DELETE FROM` migrations. The risk is a
visible empty state on upgrade if the hub is unreachable at that moment; mitigate by keeping the previous version's
files readable for one release (read-only fallback) when the bump is semantic-only rather than layout-incompatible,
as iOS already does with `dropOlder`.

**Q3. Should iOS adopt Android's 32 MiB per-scope chat-cache budget?** Recommendation: **yes**, with chats on screen
pinned, because an unbounded Caches directory grows for the app's lifetime and Settings › Storage already presents
it as one bucket; but this is a user-visible behavior change on iOS (old chats may need a refetch where they
previously painted instantly), so it needs an explicit decision. Alternative: keep iOS unbounded in phase 4 and add
the budget in a separate change with its own acceptance.

**Q4. Add a CRC32 checksum to each cache file, and quarantine corrupt files instead of deleting them?**
Recommendation: **add the checksum** (4 bytes, catches silent corruption that a valid-magic/garbage-payload file
would otherwise pass off as a decode error) and **quarantine one copy** per file for diagnostics, auto-deleted on
the next successful write. Both are cheap and align with D6's "preserve the original file" instinct without
treating caches as durable.

**Q5. Does opening a chat from storage reuse `Action::OpenChat` (with the core reading the file when the snapshot
is empty) or a separate `Action::OpenChatStored`?** Recommendation: **a separate action**. `OpenChat` today takes
its snapshot from the caller; overloading "empty snapshot means read from disk" hides an IO operation inside an
action whose current contract is pure. A separate action keeps the boundary honest and makes the shadow-run
comparison (native-loaded snapshot vs core-loaded snapshot) trivial to wire. Cost: one more wire variant and codec
regeneration.

**Q6. Any change to the encryption / file-protection posture?** Recommendation: **no change in phase 4**. Caches
stay plain bytes in the OS sandbox with the platform default protection, exactly as on all three platforms today;
secrets never enter these files. If per-file encryption is later wanted, it belongs with a threat-model discussion
(see the open hub-token-boundary proposal) and a Keystore/Keychain/Asset-Store key-handle capability on
`PlatformHost`, not with this phase. Flagging because moving files into a core-written directory is the natural
moment someone would ask.

**Q7. Where does the windowing/eviction logic live while it is being ported?** Recommendation: **in `persona-core`
as a new pure domain `cache_window`**, with fixtures derived from current iOS `ChatCache.tail` behavior (including
the subagent-launch retention and the same-seq continuation rule), replayed by Android and HarmonyOS the way every
moved domain is. It is small, but it is real behavior with real edge cases, and treating it as "just part of
storage" would skip the fixtures-first rule.

## 6. Out of scope for phase 4

- Durable stores: outbox, drafts, read positions, navigation, `PersistentDatabase`. They follow D6's durable
  envelope rules when their phase comes.
- Image and media caches (display images, inbox media): byte blobs with their own budgets and native decode paths.
- Moving `Machine`, `Session`, `Interaction`, assistant-state types into the core: the launch snapshot carries them
  as raw JSON until those domains migrate (phase 3 and later).
- SQLite for indexed queries (alternative B): revisit with the first domain that needs it.
- Web and desktop: out of the parent plan's scope.
