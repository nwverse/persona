# Server-first logic and thin clients

Who this serves: contributors and reviewers designing or changing Persona across the Go server and any client
(Web, iOS, Android, Watch, Vision Pro, Desktop, and HarmonyOS).

## Why

Persona ships across multiple clients—Web, iOS, Android, Watch, and companion shells—all talking to one Go hub and its
managers. Whenever business rules such as grouping, sorting, folding, counting, state derivation, or policy validation
are implemented separately in each client:

- every new feature or rule tweak must be written, tested, and debugged multiple times;
- platforms drift, so the same underlying data shows different counts, buckets, or statuses on different screens;
- older or slower-updating clients silently diverge from newer ones.

This rule applies Persona's **Consistency first** and **Rules are checkable** principles (`docs/design/PRINCIPLES.md`)
across **all Persona domains**—not only To-dos & Tasks, but also Agents, Inbox, Directions, Integrations & Channels,
Briefings, and Notifications.

## Rule 1 — Server-owned business derivation (Go server first)

By default, business derivation lives in the Go server as the single source of truth and is emitted to clients as
structured projections on the wire. Following `docs/server/architecture.md`, new domain logic belongs in the owning
manager (`server/internal/assistant`, `server/internal/devices`, `server/internal/extmgr`, or a feature module under
`server/internal/modules/`), while `server/internal/hub` stays a communication center.

Compute and emit the following on the server:

- **State projections**: panel-facing and summary states derived from raw runtime, ledger, or review data (for example,
  session `state`, `TaskCard` state, and to-do `view` projections).
- **Bucketing and grouping**: status buckets, direction/project/directory groupings, attention queues, and source
  categories.
- **Sorting and prioritization**: triage order, critical-path ranking, urgency ordering, and deterministic tie-breaking.
- **Folding and blocker penetration**: collapsing subordinate or completed items while surfacing blockers, `needs_you`
  items, or escalations without requiring clients to re-walk graphs.
- **Count definitions**: every badge, tab counter, summary total, widget count, and Live Activity number
  (`docs/protocol/counts.md`).
- **Cross-entity aggregations**: shared blockers across tasks, multi-repository delivery gates, direction rollups, and
  briefing/inbox summaries.
- **Deduplication and policy checks**: operation idempotency, permission and scope checks, SLA aging/escalation,
  eligibility rules, and module-off read-only markers.

## Rule 2 — Thin client boundary (Web / iOS / Android / Watch)

Clients render server-provided projections and own the interactive experience; they do not duplicate business
derivation.

**Clients own:**

- Rendering and platform-native presentation using the design system (`docs/design/DESIGN_SYSTEM.md`).
- Responsive layout, list virtualization, accessibility, typography, and motion.
- Pure local interaction state: composer drafts, focus and selection, transient expand/collapse toggles, local
  navigation, and immediate optimistic transitions reconciled against authoritative server outcomes
  (`docs/design/PRINCIPLES.md`, "7. Fast, local-first and stateful").
- Offline cache presentation, snapshot/cursor synchronization, and reconnect handling
  (`docs/architecture/core-panel-contract.md`).

**Clients must not:**

- Re-implement the same business derivation rules (grouping, sorting, folding, counting, status mapping, or policy
  validation) independently across Web, iOS, Android, and Watch.
- Define client-only counts or status buckets under a shared product label that can disagree with the server or another
  client.

**Bounded exceptions (low-latency or offline-only logic):**

When a computation genuinely must run on the client—for example, offline cache assembly or instant local feedback
before a round trip—consolidate it into a shared contract rather than per-platform ad-hoc code:

- share native client logic in `native/app-core`;
- pin cross-platform behavior with shared golden fixtures under `spec/fixtures/`;
- document in the PR and owning doc why the server cannot compute it.

## Rule 3 — Additive evolution and compatibility

Server-side projections must evolve without breaking older or not-yet-migrated clients (such as unmodified mobile or
watch builds, or clients running against rolling hub upgrades; see `docs/protocol/rolling-upgrade-compatibility.md`):

- **Strictly additive optional fields**: emit new projection fields as optional JSON fields (`omitempty` in Go) so
  older clients safely ignore unknown keys.
- **Preserve existing wire contracts**: do not remove, rename, or silently alter the semantics of existing fields while
  older clients still consume them. Keep deterministic compatibility mappings alongside new projections until legacy
  consumers are retired.
- **Shared fixture contract tests**: back new projections with shared fixtures under `spec/fixtures/` (such as
  `spec/fixtures/counts`, `spec/fixtures/todo-view`, `spec/fixtures/agent-view`, and `spec/fixtures/inbox`) verified by
  both server and client test suites.
- **Lossless rollback verification**: for gated or staged rollouts, test the full cycle—enable new projection/fields,
  write additive data, and disable or fall back—confirming older clients and existing user data remain intact.

## Rule 4 — Design and PR self-check

Every design proposal and PR that adds or changes derived state, lists, summaries, or policy rules should pass three
checks:

1. **Server ownership**: Can this derivation logic (state mapping, grouping, sorting, folding, aggregation, or policy
   check) be computed in the Go server and emitted on the wire instead of implemented in a client?
2. **No shadow client rules**: Does this change add any client-side count, bucket, group, or fold rule that is not
   computed by the server (or, for a documented offline/low-latency exception, shared via `native/app-core` and
   `spec/fixtures/`)?
3. **Additive compatibility and fixtures**: Are all new server projection fields strictly additive and optional
   (`omitempty`) for older or unmodified clients, with shared fixture coverage and rollback verification?
