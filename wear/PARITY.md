# Apple Watch capability comparison

The comparison targets the checked-out Apple implementation, not a promise that watchOS and Wear OS expose identical system surfaces. The intended audience is someone reading and responding to Persona from their wrist. Voice, recording and transcription work was explicitly deferred; the earlier system-dictation entry remains unchanged and unverified.

| Capability | Wear OS implementation | Evidence boundary |
| --- | --- | --- |
| Pending questions, plans and permissions | Same answer-only contract; protected inputs require another device; decisions remain with their task | Synthetic action tests are separate from live approvals |
| Terminal choices | Structured options, guarded numbered legacy choices, Esc, confirmed Enter, dismiss to computer | No arbitrary terminal text or Always grant |
| Assistant and Agents | Existing conversations, short replies, Continue, seen, interrupt, persistent drafts and idempotent retry | Agent execution is distinct from Hub acknowledgment |
| Conversation display | Plain text and compact attachment/context marks, structured provenance, collapsed work with manual inspection | Does not infer unknown old prompt prose or render interactive assistant cards |
| Inbox | Primary-Hub preview, existing suggested draft, explicit AI draft generation, review/send and Done | Never sends a generated draft automatically |
| Multiple hubs | Primary plus three additional encrypted credentials; concurrent refresh, owner-scoped actions/drafts, partial failure cache | Phone-based credential synchronization is not present |
| Milk Frog | Original verified assets, six states, tap animation, static/low-motion rendering, cached complication | Unsupported hubs show no fabricated pet |
| Extension widgets | Authenticated directory and read-only projection, charts with accessible values, scoped deep links, cached complication | Does not open a full extension interaction page or execute provider actions |
| Refresh and attention | Foreground polling and request-arrival feedback | No phone summary pushes or background request notifications |
| Pairing | Address/token or one-time link, answer-role exchange, TLS pin, revocation through Hub | No Android phone companion |
| System voice input | Earlier entry unchanged | No new microphone test or voice feature is claimed |
| Meeting/note, all-day and rolling recording | Deferred | No audio capture service or microphone permission in the app |
| Transcription, Knowledge sync, minutes and health retention | Deferred phone-dependent pipeline | No cloud speech fallback or substitute audio upload |
| Record/all-day complications | Deferred with recording | Pet and extension data sources do not replace the selected watch face |

## Source of the comparison

The existing Apple contract lives in `apple/PersonaWatch/PersonaWatchApp.swift`, `apple/Shared/WatchText.swift`, `apple/Shared/MessageSource.swift`, `apple/Shared/WatchGroupRefresh.swift`, `apple/Shared/WatchPet.swift`, `apple/PersonaWatch/WatchPetView.swift`, `apple/PersonaWatch/WatchExtensionSurfaces.swift`, and `apple/PersonaWatchWidgets/`.

The recording boundary is documented in [Recordings](../docs/apple/recording.md): watch audio travels to the paired iPhone, which normally transcribes locally before syncing text. This Wear OS implementation neither silently grants a watch full-access credentials nor substitutes a remote transcription service.

Source-level implementation, unit checks, synthetic physical-watch flows, a real Hub connection, and a selected physical-watch complication are different evidence levels. See [QA](QA.md) for the exact version and completed checks.
