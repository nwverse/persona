# Client error semantics audit — 2026-11-09

Scope: #1915, including misleading gateway-status fallbacks on Web, Apple and Android.
The reviewed upstream base was `62c6cc85cab847d93996a67d8b598083149a51f7`.
This is source and synthetic-test evidence, not deployment or real-device acceptance.

## Responsibility chain

The manager's `server/internal/assistant/kb_routes.go` already emits `kb_disabled` (503),
`kb_unavailable` (503), and `kb_read_only` (403). Read-only checks precede writes;
successful empty lists remain successful. `server/internal/assistantwire/kb.go` documents
these codes. The hub's `server/internal/hub/assistant_link.go` registers the relay,
and `server/internal/hub/bus_forward.go` copies the manager's HTTP status, headers and
body unchanged. A disconnected manager instead yields `assistant_offline`.
`server/internal/apierr/apierr.go` keeps stable codes and separates localized summaries
from diagnostic detail. No new server implementation is needed.

Client boundaries:

- Web: `web/src/lib/api.ts` → shared `errorText` → knowledge list/detail/search,
  Inbox dialogs and other consumers. Business codes now precede status fallbacks;
  unknown gateway statuses no longer claim that the hub or computer is down.
  `web/src/lib/feedback.ts` no longer infers a network outage from an HTTP status alone.
- Apple: `apple/Shared/APIClient.swift` → `APIError.errorDescription` / `Error.shown`
  → knowledge screens and other callers. `APIErrorInfo` in PersonaKit parses model
  response errors, not these HTTP failures. Known configuration refusals are not hidden
  merely because their incidental message contains “timeout”.
- Android: `android/core/hub/src/main/kotlin/persona/core/hub/HubFailure.kt` → knowledge repository and shared
  domain/design-system feedback → `KnowledgeViewModel` and screens. Existing disabled/
  starting states are retained; read-only, manager, authentication, permission,
  missing-content and timeout states are distinguished.

## Module × client audit

“Fixed” describes source changes. Native runtime validation remains unavailable here.
“No reproduced issue” is bounded source review, not a comprehensive runtime pass.

| Module | Web | Apple | Android |
| --- | --- | --- | --- |
| Knowledge categories, detail, search | Fixed shared code-first handling and legacy decoding; rendered-list regressions | Fixed shared HTTP presentation; list/detail/search already use `error.shown`; native UI unverified | Existing disabled/starting handling retained; fixed legacy decoding and additional failure kinds; native UI unverified |
| Recordings | Knowledge kind inherits fix; no device recorder | Fixed retry pause and durable pending-delete explanation; no DELETE 404 acknowledgement; sync tests added but not executable here | Knowledge recording list/detail inherit fix; no phone recording-upload worker found |
| Inbox / sent ledger | Fixed inherited manager/gateway wording; existing real-REST dialog test proves no send/navigation on failed lookup | Fixed pre-send item lookup treating arbitrary gateway responses as “Can't reach the hub”; sent ledger no longer calls a timeout a hub outage; uncertain-send receipts remain unchanged | Existing typed errors and shared feedback retained; no same-class list-empty conversion reproduced in reviewed connector-thread model |
| Extensions / connectors / channels | Shared fallback fixed; status-specific extension states retained | `ExtensionsModel` preserves failure and unsupported states; shared presentation fixed; no additional reproduced issue | `ExtensionsState`, `ExtensionMarketState`, `ExtensionUiState` retain explicit states; shared feedback fixed |
| Model configuration / availability | `providers.ts` keeps import-specific offline/outdated cases; shared safe internal fallback fixed; no model probe executed | `ModelConfigurationModel` / view keep 404 compatibility and failed-list states; existing availability regressions retained | `ModelConfigurationState` preserves failed catalog vs unsupported; shared feedback fixed; no model probe executed |
| Agents / execution nodes | Existing `node_unreachable`, delivery and timeout classification retained; operation retry excludes coded refusals | Shared node wording distinguishes confirmed offline from dropped connection; existing quiet timeout behavior retained | Existing coded computer/readiness feedback retained; no additional reproduced issue |

The Mac reuses `KnowledgeView` through `apple/Mac/App/MacKnowledgePage.swift`.
The iOS, macOS, watchOS and visionOS targets compile `Shared` according to
`apple/project.yml`; the change introduces no PersonaKit dependency or newer watch API.
The watch records/transfers audio to the phone rather than running `RecordingSync`.
All four Apple build targets still require verification on a Mac.

## Compatibility and privacy boundaries

All clients recognize the older refusal only when all of these match:

1. The requested route is in `/api/v1/kb/`, `/api/v1/kb-search` or `/api/v1/recordings`
   (Web passes the equivalent path without `/api/v1`).
2. HTTP status is 503 and the original code is exactly `internal`.
3. `detail` is exactly `the knowledge base is off`.

No substring or generic 503 matching is used. Different detail, status, code or
route does not infer disabled. Remove this bridge only when those old hubs are
outside the supported rolling-upgrade window. Older replies without sufficient
evidence cannot be reliably classified; they retain a generic failure.

Stable business messages do not interpolate arbitrary diagnostics. Unknown `internal`
server errors use a generic presentation, not their exception text. Original error
objects retain status/body for existing diagnostic consumers; request IDs remain
available (Android now retains the response ID too). Configuration refusal never
enables knowledge, imports data, probes a model or triggers a retry timer.

Recording uploads and deletions stay pending through configuration refusal.
Ordinary saves cannot resume a paused worker; foreground/manual retry can recheck
after configuration is changed. A local deletion is not a claim that its knowledge
copy was deleted. Unsupported deletion remains queued.

## Verification

The initial rendered Web list reproduced the defect for stable codes and the exact
legacy response: it showed the hub-outage text (or raw read-only summary), not the
business state. The focused fixed suite passed 57 tests before the final hardening.
The full Web lane initially exposed one old Inbox expectation for raw manager text;
that expectation was updated while retaining its no-send/no-navigation assertions.

Commands and final outcomes are recorded in the associated PR. Required remaining gates:

- Apple compilation, `PersonaTests`, `RecordingSyncTests`, Inbox model tests,
  PersonaKit tests and light/dark simulator screenshots: unavailable on this Linux
  environment (`scripts/ci/apple.sh packages` requires macOS/Xcode; `swift` is absent).
- Android compilation, JVM/Robolectric tests and screenshots across all styles:
  unavailable (`scripts/ci/android.sh --lane test-core` requires JDK 21; SDK is absent).
- Rendered-browser screenshots: unavailable because `browser-use` is absent.
  jsdom component regressions are not screenshot evidence.

No merge, deployment, runtime configuration change, restart, knowledge enablement,
private data-source access or paid/live model call was performed. Installed clients
and the running hub have not been replaced by this source change.
