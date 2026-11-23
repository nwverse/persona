# Partner hubs: two independent hubs whose assistants talk (#13248)

Two hubs that stay **independent** (for example one at work that manages work computers, one at home that manages personal
ones, each with its own assistant, Inbox, knowledge base and state) can be paired as **partner hubs**. Their assistants then
send each other messages: the assistant on one hub writes to the other hub's main conversation and gets a reply back, as
`conversation_send` does between two conversations of one hub. Asking the other side to get something done ("have your
computers run the tests of X and tell me what failed") is an ordinary message: the other assistant plans and runs it with
its own tools, under its own rules, and answers with text.

This is not [linked hubs (mesh)](mesh.md). A mesh merges hubs into one group with one primary that holds all state and
drives every computer. Partner hubs never join a group, never hand over credentials or records, never forward App
requests or open node streams to the other's computers; each hub keeps answering only for itself. A hub that is a
linked-hubs member cannot pair, and two hubs linked in a mesh cannot also be partners.

Who it serves: people who run more than one hub. Everyday use is plain conversation ("ask the work hub whether the build
is green"); pairing and the per-hub switches live in Settings › This hub › Partner hubs (web) or `personad hub partner`.

```text
 Home hub                                                              Work hub
 ┌────────────────────────┐   partner link (WSS, pinned, one per pair, ┌────────────────────────┐
 │ hub: pairing, the link │◄═ dialed by whichever side can reach the ═►│ hub: pairing, the link │
 └───────────┬────────────┘    other; HTTP CONNECT / SOCKS5 proxy ok)  └───────────┬────────────┘
             │ bus: partner.send, partner.hubs, partner.receive, partner.changed   │ bus
 ┌───────────┴────────────┐                                            ┌───────────┴────────────┐
 │ manager: hub_send tool,│   envelope {message_id, kind, text, hop}   │ manager: dedupe by     │
 │ owner's card, scrub,   │ ─────────────────────────────────────────► │ message_id, deliver to │
 │ outbox, replies        │ ◄───────── reply / status envelopes ────── │ main as a request      │
 └────────────────────────┘                                            └────────────────────────┘
```

## Layers

1. **Hub to hub** (`server/internal/hub/partner.go` and the `partner_*.go` beside it): pairing, credentials, one
   long-lived link per pair, dialing with reconnects and proxies. The hub carries envelopes and stamps who they came
   from; it never reads or stores the text.
2. **Assistant to assistant** (`server/internal/assistant/partner.go` and the `partner_*.go` beside it): the `hub_list`
   and `hub_send` tools, the owner's confirmation, scrubbing, the durable outbox and inbox keyed by `message_id`,
   delivery into a conversation, replies and message states.
3. **No groups**: each pair is its own relationship, paired and unpaired by the two owners. With three hubs, each pair is
   paired separately, and nothing is relayed: a message goes to the hub it was sent to and no further (that hub's
   assistant may write to a third partner, but that is a new message of its own, under its own owner's confirmation).

## Pairing

- **Code**: made on the hub the other one can reach (Settings › This hub › Partner hubs › **Show my code**, or
  `personad hub partner code`), single use, 10 minutes:
  `persona://partner?hub=<hub_id>&name=<name>&fp=<public-key fingerprint>&tls=<TLS port>&url=<https address>…&code=jh_<64 hex digits>`
  (at most 6 `url`s). Only its hash is stored (`partner_codes`, expired ones pruned when a code is made). It has its own
  scheme and prefix, so pasting it into the linked-hubs page (or an older hub) fails instead of joining a group. Making
  one needs a TLS key and an https address (`public.tls_listen`, `public.advertise`): without them `409 no_tls`.
- **Pair**: on the other hub, Settings › This hub › Partner hubs › **Pair…** (or `personad hub partner add '<code>'`).
  The page shows the name and fingerprint read from the code before pairing. The pasting hub issues a credential for the
  code's hub, reaches it at the code's addresses pinned to the code's fingerprint (through the proxy when one is set,
  "Proxies") and posts `POST /api/v1/partner/pair` with `Authorization: Bearer jh_…`. In one transaction the code's hub
  uses the code up, stores the pair (never over one it has: "Refused"), revokes any partner credential left over for that
  hub and issues a credential back. Both
  sides end up with the other's pin, addresses and a credential the other issued (sealed with `~/.persona/secret.key`);
  each keeps only the hash of the one it issued. The pasting hub checks that the answer comes from the code's hub_id
  (`502 wrong_hub`) and revokes its own credential again on any failure.
- **Only the hub that gets dialed needs TLS.** The pasting hub sends its own pin and https addresses when it has them.
  With both, the code's hub stores it with **Connect to this hub** on (`dial: auto`); without them, off (`dial: never`):
  it cannot dial it and waits for its link. A pin learned later from that hub's hello is kept, and the owner can switch
  dialing on once it can be reached. The pasting hub always stores the code's hub with dial on.
- Refused: your own code (`409 own_code`); a used, expired or unknown code (the code's hub answers 401, this hub
  `401 code_refused`); this hub is a linked-hubs member (`409 member_hub`), or the other hub is already its linked-hubs
  peer (`409 already_linked`); a hub already paired, on either side (`409 already_paired`, or `409 pin_changed` when it
  names another key than the one kept); partner hubs switched off (`403 partners_off`); the pairings not read yet at
  start (`503 not_ready`). **A pairing is never replaced**: whoever holds a code names its own hub_id and pin (it never
  shows it holds that key, and a hub's pin is no secret), so pairing with a hub this one is paired with (or claiming to
  be one) is refused, with that pin or without one; the code is not used up. To pair the same two hubs again, unpair
  first, on both sides. The other hub's own refusal comes back as a 409 with its code, from a fixed list ("Security
  boundaries"), in its words for this hub's owner: where it is the one to unpair on, its words say so ("that hub", not
  this one). Anything else it answered is `502 refused`; no address worked, `502 unreachable`. "Routes" lists them.
- **One pairing with a hub at a time, on each side**: while a hub is pairing with another (pasting its code, or
  answering its request), a second pairing with that hub is refused, `409 pairing_in_progress`, before any credential is
  issued and without using the code up. Two owners pasting each other's codes at the same moment would otherwise each
  have their hub's answer revoke the credential its own paste was handing over (the code's hub revokes any partner
  credential left over for that hub), leaving both hubs paired with credentials the other had revoked. Now the request
  that reaches a hub pairing with its sender is refused; that paste fails and revokes only the credential it issued, and
  the other one goes through. When both cross exactly, both are refused and either owner pairs again; only one of the
  two needs to paste a code. The mark is the running hub's own (in memory, released when that pairing ends).
- **Unpair** (either side, Settings or `personad hub partner remove <hub_id>`): tell the other hub over the link (when it
  is up), delete the pair, close the link (4012), stop dialing, revoke the credential this hub issued. The other hub does
  the same on receipt. A hub that was not linked then learns at its next dial: the partner answers 401, its page shows
  **Couldn't connect** ("refused"), and it waits 5 minutes between tries; one that never dials keeps the pairing until
  its owner unpairs there too (until then it refuses a new code from that hub, `409 already_paired`). Revoking the
  partner's credential under Settings › Devices is the same as unpairing.
- The hub reads the pairings at start; a read that fails (the database not answering yet) is tried again, from 1 s
  doubling to a minute, until it works. Until then the apps' list is empty, codes, pairing, unpairing and the dial
  setting answer `503 not_ready`, no link is taken (`503 not_ready`) or dialed, a send waits (`partner_offline`) and the
  manager gets no list ("Bus"); once read, it is told and the apps reload.

## The link

- `GET /api/v1/partner/link`: a WebSocket on the TLS port only (plain HTTP is `403 tls_required`, even on loopback),
  pinned to the fingerprint from pairing, `Authorization: Bearer <credential the dialed hub issued>`, permessage-deflate.
  The `partner` role opens only this path, the `partner_pair` role (a `jh_` code) only `POST /api/v1/partner/pair`;
  every other route answers them 403. A valid credential whose pairing this hub has not saved yet (the code's hub may
  dial right after redeeming the code, before the pasting hub stored the pair) is `404 not_paired`, which the dialer
  retries with its usual backoff; partner hubs off here is `403 partners_off`, a member `409 member_hub`, the pairings
  not read yet `503 not_ready` (retried the same way).
- JSON-line RPC (`internal/rpc`) with its own dispatcher and three calls, nothing else (no records, no streams, no HTTP,
  no relay; another method is an error): `hello` (each side once, within 10 s: `{hub_id, name, version, proto: 1, fp,
  endpoints}`; a hub_id other than the pairing's closes the link 1008; the name, pin when none was known, and https
  addresses refresh what the other side shows and dials), `msg` (one envelope, answered with the receiving manager's
  result, "Bus") and `unpair`. Frames are at most 1 MiB.
- **One link per pair, whoever dials.** Each partner has a **Connect to this hub** switch (`dial`: `auto` or `never`).
  With both on, both dial and the link dialed by the smaller hub_id stays (the other closes with 4011); a newer link from
  the same dialer replaces the older one. With one side off, only the other dials, and the one link carries both
  directions: a hub nobody can reach (behind NAT, a private address) still receives messages over the link its partner
  dialed. Switching dialing off does not drop a link that is up; the dialer ends when it would dial again.
- Heartbeat every 10 s, the link drops after 5 s without a pong (`link.PingEvery`, `link.PingTimeout`; hub.json's `link`
  heartbeat does not apply). Reconnects back off from 1 s doubling to a minute, randomized, reset after a link that
  lasted a minute. A 401 or 403 answer, or a 4012 close, shows as refused and waits 5 minutes; a 409 (it joined a
  linked-hubs group) waits 5 minutes too.
- Close codes: 4011 duplicate (or paired again), 4012 unpaired (or a credential a new pairing replaced), 4013 the
  partner had more than 64 calls running here at once (it dials again), 1001 hub stopping.

## Proxies

Hubs dial each other directly, through a proxy, or both:

- `partners.proxy` in hub.json: `http://[user:pass@]host:port` or `https://…` (HTTP CONNECT, persona-link's code,
  `link.DialThrough`), `socks5://[user:pass@]host:port` or `socks5h://…` (default port 1080; the proxy resolves the name
  either way), or `off` (no proxy, not even the environment's). Read at the hub's start.
- Unset: the environment's `https_proxy`, `HTTPS_PROXY`, `all_proxy`, `ALL_PROXY`, the first one set (a SOCKS URL there is
  used too, unlike persona-link). `NO_PROXY` / `no_proxy` exempt addresses; loopback and `localhost` never use a proxy.
  An environment proxy that does not read is skipped. A hub run as a service rarely has these variables, so set
  `partners.proxy` there.
- Direct and proxied candidates race together, one every 300 ms: every address directly first, then each through the proxy
  (LAN addresses last of those); `partners.proxy_always: true` uses the proxy only and never dials direct. An explicit proxy that
  does not read fails every dial rather than falling back to direct, and the start log says so once.
- The tunnel is made under TLS: the partner's pinned TLS runs inside it end to end (`Transport.Proxy` stays nil), so the
  proxy can never stand in for the partner. Errors and logs never show the proxy's address or credentials; the start log
  names only its kind and whose setting it is.
- Company proxies often allow CONNECT only to port 443; put the dialed hub's TLS listener there, or have its port allowed.

## Messages

### Sending (`hub_send`)

- The `hub_list` and `hub_send` tools are listed only while at least one partner is paired (and the manager has a
  database to keep the messages), and only to the assistant's own conversations (the main one and the others of its
  space). The health space, guest conversations, a meeting's "Just me", outward readers and conversations on a work
  computer are refused; Agents never get them. `hub_list` names each partner (name, hub_id), whether it is online, and
  whether a message to it waits for the owner's OK.
- `hub_send {hub, text, reply_to?}`: `hub` is a partner's hub_id or a name only one partner has; `text` is what the
  assistant wrote, at most 12,000 characters. Only that text is sent: never transcripts, files, the knowledge base or
  anything else automatically. Before anything is stored or shown, the characters nobody would see on the card are
  removed: every Unicode format character (tag characters, bidirectional embeddings, overrides and isolates, zero-width
  space, word joiner, byte order mark, soft hyphen, the invisible operators) and every other default-ignorable one (the
  fillers, the variation selectors that carry a byte each after any character). What emoji and scripts need stays, one
  at a time: after each character, at most one emoji or Mongolian variation selector and then one zero-width joiner,
  non-joiner or directional mark (as in 🏳️‍🌈); a longer run of them, which could spell out text of its own, loses the
  rest. The marks that stand before Arabic and Syriac numbers show, and stay. So what goes is what the card shows, and
  credentials are scrubbed
  (`transcript.SanitizeShareText`: private keys, Bearer tokens, `name=value` secrets, known token formats, credentials in
  URLs; plus Persona's own `jd_`, `jp_`, `jx_`, `jb_`, `jc_`, `jh_` and `pext_` tokens); the tool says how many of each
  it removed. At most 20 messages and replies go to one partner in 10 minutes; past that the tool refuses (the count and
  the row it lets in are one step, so tool calls side by side get no more past it).
- **The owner confirms on the phone by default.** The tool raises a permission card in the conversation ("Send this
  message to the partner hub "Work" (7c1e4a2b)?", the partner's name and the first 8 characters of its hub_id, since
  a partner chooses its own name; a blank line, then the exact scrubbed text) and returns at once with the message_id:
  the conversation is never held while the card waits. Allow queues it; Deny ends it (`declined`) and the conversation
  is told; **Don't ask again for messages to "Work" (7c1e4a2b)** with Allow stops asking for that partner. A card left
  unanswered for 24 hours after the message was made, dismissed, or allowed on the owner's behalf (by the assistant or
  a supervising conversation) expires, and the conversation is told. Each partner has an **Ask me before sending**
  switch (on by default) in Settings; no tool can turn it off. A manager restart raises the waiting cards again.
  `hub_list` says when two partners have one name (name them by hub_id).
- **Everything the manager keeps for a partner belongs to that pairing** (`PartnerHub.paired_at`, when it was made): a
  hub unpaired and paired again under the same hub_id starts over. Each envelope's row keeps the pairing it was written
  for (`partner_messages.paired_at`, the hub's time as the hub's list said it then), and a setting the pairing it was
  made for (`partner_prefs.paired_at`): a row or setting belongs to an earlier pairing when its time is not the one the
  hub lists now. Both times come from the hub's clock, so a manager on another computer with a clock that differs never
  takes a message written right after a pairing for one of the pairing before; a time not known (a row written before
  the hub said its pairings, a hub that does not say when it paired) is no evidence of another pairing, and only a hub
  not listed at all counts as unpaired then. Its **Ask me before sending** is on again (one set for an earlier pairing
  counts as none), and nothing approved or queued for the earlier pairing goes to it: once the hub's list no longer has
  the pairing (the hub unpaired, or paired again), messages still waiting for a card (the card taken back) or in the
  queue fail as `unpaired`, and the conversation is told once; the delivery loop checks again before each send, and a
  restarted manager before raising a card again. Nothing the partner sent under the earlier pairing goes into a
  conversation after it either ("Receiving"), and `hub_send` refuses a `reply_to` that names one of its messages.
- The tool's answer, the card and the message list keep **receipt, delivery and answer distinct**: `confirming` (waiting
  for your OK) → `queued` (waiting for the link) → `sent` (the partner's manager stored it) → `delivered` (it is in the
  partner's conversation) → `answered`; or `declined`, `expired`, `failed` with a code (`invalid`, `too_long`,
  `hop_limit`, `rate_limited`, `no_main`, `unpaired`, `undelivered`).
- Offline partner, or one whose manager is away: the message stays queued and goes out again with the same
  `message_id`, 5 s doubling to 5 minutes apart (counted from the end of the try), and at once when the partner comes
  online. A message still queued after 7 days expires and the conversation is told. A partner the hub no longer knows
  (unpaired, or partner hubs off here) fails the message as `unpaired`.
- The delivery loop sends to each partner on its own (one envelope in flight per partner, at most 8 partners at once,
  each partner's in the order they were written): a partner that keeps its link up but never answers holds up only its
  own envelopes. One it does not take now ends that partner's turn: its other envelopes wait as long as that one.

### Receiving

- The receiving manager checks the envelope (refusals: `invalid`, `too_long`, `hop_limit`, `rate_limited`), stores each
  `(partner, message_id)` once (a resend is answered `duplicate` and changes nothing), answers `accepted` at once, and
  then delivers it. The partner's text is cleaned and scrubbed here too (the invisible characters and credentials
  above).
- What a partner sent under a pairing that ended (the hub unpaired, or paired again since) goes into no conversation
  after it: when the pairing ends, an envelope still received fails as `unpaired`, one the delivery loop takes up again
  fails before it would go in, and a message or reply already waiting in a conversation's relay queue is dropped as it
  would go in (its row failed as `unpaired`); the hub it came from, no longer that partner, gets no status for them.
  The same holds while partner hubs are switched off (`partners.enabled: false`) or this manager is no longer the
  hub's default: nothing a partner sent goes into a conversation then.
- A **message** goes to the **main conversation** as it is at delivery, through the relay queue (it waits behind a
  running turn, like any relayed message; one queued for a main conversation put away since moves to the new one). With
  no main conversation it fails `no_main`, with the main one on a work computer `undelivered`. Waiting, it shows above
  the composer as "协作 hub「name」的消息" (the partner hub's message), without its words. A round it starts is one the
  owner did not start: the owner's own message comes first (it may interrupt it, as for another conversation's message).
- It arrives **labeled with the hub it came from** (message source kind `hub`, title = the partner's name, `hub_id`; the
  web shows "@Work · Request from another hub, not from you") and framed as a **request, never the owner's
  instruction** (`server/internal/prompts/assistant/partner_message.md`): the receiving assistant handles it under its own hub's rules,
  asks its own owner where it normally would (starting Agents that change things, sending things out, spending,
  deleting), and answers only with what is needed. The framing (with the hub_id and message_id to answer with) is in a
  `kind="context"` block first, so the apps show it folded; the partner's words follow it as the message's own text,
  which the chat shows as the message (the `&lt;` the manager writes for each tag start in them, so the model never
  reads a tag there, is shown as `<` again, so code reads as written; the framing tells the model the same). A new
  main chat's carry-over of the last messages, and a conversation switch's summary, leave a partner's message out:
  there every user line reads as the user's own words. It is never read as one of Persona's own notes (`injected`: a task's notice, a
  supervision, a follow-up, the answer-again or a wake-up), whatever its words begin with: the hub never marks it,
  `prompts.ParseInjected` (the manager, the node and the transcript readers) and the web never take it for one, so the
  chat shows the words and never folds the turn as supervision. It never carries a `client_msg_id`, the owner's quoted
  words, a hand-over or any of the hub's "this came from the owner" marks.
- **Replies** go back with `hub_send {hub, reply_to: <message_id>, text}`. A reply leaves the replying hub, so its owner
  is asked first too, unless they turned that off for the partner. It arrives in the conversation that sent the original
  message (or the main conversation if that one is gone), framed as information from another assistant
  (`partner_reply.md`); the original becomes `answered`, the incoming message `replied`.
- **Status envelopes** (`delivered`, or `failed` with a code) go back once a message or reply is in its conversation or
  cannot be; they carry no text and need no confirmation. A conversation that refuses a reply for good sends it on to
  the main conversation, where it counts as delivered (it waits in that queue); anything else refused, or lost with its
  conversation while it waited, fails `undelivered`. An incoming envelope not delivered within 2 minutes is taken up
  again by the delivery loop, after checking the transcript for it. An incoming message keeps a next try until its
  status is queued (a status's `message_id` comes from the message and its state, so queuing it twice adds nothing):
  one whose status was lost to a database error or a stop in between gets it from the loop.
- Limits: an exchange is at most 6 hops (message 1, reply 2, …; the hop of a reply is taken from this hub's own record,
  never the envelope); each manager takes at most 30 messages and replies and 120 statuses per partner in 10 minutes,
  however many arrive at once (the count and the row it lets in are one step per partner).

## Routes

Errors are JSON `{"error": <text in the request's language>, "code": …}` like the hub's other routes.

**The apps' (the hub's own; full credentials only, read-only devices get 403):**

| Method | Path | Body → answer |
| --- | --- | --- |
| GET | `/api/v1/partner-hubs` | → `{enabled, member, can_issue, hubs:[view]}`; `enabled`: partner hubs are on here; `can_issue`: this hub can show a code (a TLS key and an https address) |
| POST | `/api/v1/partner-hubs/code` | → `{code, expires_at}` (`Cache-Control: no-store`); `409 no_tls`, `409 member_hub`, `403 partners_off`, `503 no_database` / `not_ready` |
| POST | `/api/v1/partner-hubs` | `{code}` → view, once the link is up (at most 5 s later); `400 bad_code`, `401 code_refused`, `403 partners_off`, `409 own_code` / `member_hub` / `already_linked` / `already_paired` / `pin_changed` / `pairing_in_progress`, a 409 with the other hub's code, `502 unreachable` / `wrong_hub` / `no_credentials` / `refused`, `503 no_database` / `not_ready` |
| PATCH | `/api/v1/partner-hubs/{hub_id}` | `{dial: "auto" \| "never"}` → view; `404 not_found`, `503 not_ready` |
| DELETE | `/api/v1/partner-hubs/{hub_id}` | → `{ok: true}`; `404 not_found`, `503 not_ready` |

- A view is `{hub_id, name, fp, state, dial, since?, last_seen?, error?, dialed_by, rtt_ms?, created_at}`. `state`:
  `connected` (a link is up; `since`, `rtt_ms`, `dialed_by` `us` or `them`), `refused` (the partner refused this hub's
  credential), `connecting` (this hub dials it), `offline` (it waits for the partner to dial). `error` is why the last
  dial failed, each address's reason in turn, or that dialing is on but the hub has no pin or https address for it.
- `401 code_refused` is the other hub refusing the pasted code, not this request's credential: a client must not sign
  out on it (the web passes it to the dialog).
- Every change (a pairing made or ended, a link up or down, a dial setting, a dial's outcome) is pushed as
  `{"type":"partner_hubs"}` (no fields); a client that shows the list reloads it.

**Between hubs** (TLS only, `403 tls_required` otherwise):

- `POST /api/v1/partner/pair`, `Authorization: Bearer jh_…` (the code's secret), body at most 64 KiB:
  `{hub_id, name, version?, fp?, tls_port?, endpoints?, token}` (`token`: the credential the pasting hub issued, `jd_…`)
  → the same fields of the code's hub, with the credential it issued. A used, expired or unknown code is 401; a bad
  hub_id or token 400; `403 partners_off`; `409 member_hub` / `already_linked` / `already_paired` (that hub_id is a
  partner here already, whatever pin it names) / `pin_changed` / `pairing_in_progress` (this hub is pairing with that
  one meanwhile; the code is kept); `503 no_database` / `not_ready`.
- `GET /api/v1/partner/link`: "The link". A linked-hubs member answers both itself (`409 member_hub`), never forwarding
  them to its primary.

**The command line**: `personad hub partner code | add <code>|- | list | dial <hub_id> auto|never | remove <hub_id>`
calls the same five routes under `/api/v1/admin/cli/partners` (`GET`, `POST …/code`, `POST`, `PATCH …/{hub_id}`,
`DELETE …/{hub_id}`) on loopback with the hub's token, on the same mux as `personad hub peer`.

**The manager's** (`assistantwire` `partnerRoutes`, forwarded by the hub like the assistant's other routes; full role
only, `403 partner_read_only`; `503 partner_store` without a database). Clients send them with no `X-Persona-Manager`:
the default manager is the one that sends. No tool reaches them: a model cannot switch its own confirmation off.

| Method | Path | Body → answer |
| --- | --- | --- |
| GET | `/api/v1/partner-messages?hub=<hub_id>&limit=<n>` | → `{messages:[{id, direction: "out" \| "in", hub_id, hub_name, kind: "message" \| "reply", state, code?, text, title?, created_at, updated_at}]}`, newest first, statuses left out; `limit` 20 by default, at most 100; `400 partner_invalid` |
| GET | `/api/v1/partner-messages/prefs` | → `{prefs:[{hub_id, confirm}]}`: the stored settings of the pairings there are now only (no row, or one of an earlier pairing under that hub_id: `confirm` is true) |
| PUT | `/api/v1/partner-messages/prefs/{hub_id}` | `{confirm: bool}` → `{hub_id, confirm}`, kept for the hub's pairing now (replacing one of an earlier pairing); `404 partner_not_found` for a hub that is not paired |

## Bus

Types in `server/internal/assistantwire/partners.go`.

- **Capability** `partner_hubs` (`CapabilityPartnerHubs`) in `manager.hello`: the hub delivers partner envelopes and sends
  `partner.changed` only to a default manager that names it, and lists the `partner_hubs` feature only then.
- `partner.send {to, envelope}` → `{status, code?}` (manager → hub; only the default manager, another gets
  `other_manager`): hands one envelope to the paired hub over the live link and returns that hub's manager's answer.
  Errors: `invalid_params` (an envelope that does not read: `message_id` not `pm_` and 32 hex digits, an unknown kind, a
  bad `reply_to`, `state` or `code` over 32 bytes, text over 64 KiB), `partner_unknown` (not paired, or partner hubs off
  here: stop), `partner_offline` (no link now, the call dropped or found no answer within 30 s, or the pairings not read
  yet at start: send it again, the partner deduplicates), `partner_unavailable` (the partner hub has no manager that
  takes it now: send it again). What the partner answered comes with its code only when it is `partner_unknown` or
  `partner_unavailable` (anything else is `partner_unavailable`), and its words cleaned and cut to one line of 200
  characters: they reach this hub's manager's log.
- `partner.hubs {}` → `{enabled, hubs:[{hub_id, name, online, dial, paired_at}]}` (manager → hub): the default manager's
  list; any other manager gets `{enabled: false, hubs: []}`; before the pairings are read, `store_unavailable` (not an
  empty list). `paired_at` is when the pairing was made (the hub's `created_at`): a hub paired again after an unpair has
  a later one, and what the manager kept for the pairing before is not this one's.
- `partner.receive {from_hub, from_name, envelope}` → `{status, code?}` (hub → its default manager, 20 s): `from_hub`
  comes from the link's credential and `from_name` from this hub's pairing record, never from the envelope. The hub
  answers an envelope that does not read itself (`refused`, `invalid` or `too_long`); with no manager that takes it, or
  a call that fails, the partner gets `partner_unavailable` in fixed words (why stays in this hub's log: the manager's
  error may name its database).
- `partner.changed` (hub → its default manager, one way, the shape of `partner.hubs`'s answer): right after the
  manager's hello, on every change, and to a manager made the default; never before the pairings are read (then once
  they are). The manager the default was before, and one that says its hello while it is not the default, get
  `{enabled: false, hubs: []}`, so only the default offers the tools. With partner hubs off, `enabled` is false and the
  list empty; the manager lists its tools only while it holds a list with `enabled` and a hub.
- **Envelope** `{message_id, kind: "message" | "reply" | "status", reply_to?, hop?, text?, state?: "delivered" | "failed",
  code?, sent_at}`; **result** `status` `accepted` | `duplicate` | `refused` with `code` `invalid`, `too_long`,
  `hop_limit`, `rate_limited` (or, in a failed status, `no_main`, `unpaired`, `undelivered`). Text is at most 12,000
  characters (`PartnerTextMax`), an exchange at most 6 hops (`PartnerHopMax`).

## Security boundaries

- **What a partner can do**: send envelopes over its link to this hub's default manager (messages to the main
  conversation, replies to a conversation that wrote to it, statuses about this hub's messages), within the manager's
  rates and limits; see this hub's id, name, version, pin and https addresses. **What it cannot**: open any App route
  (its credential opens the link alone, a code pairing alone), read anything (transcripts, tool calls, terminals, files,
  diffs, the knowledge base, the Inbox, mail, phone data, settings, credentials), answer cards, reach computers or node
  streams, join or see a group, or reach a third hub through this one.
- **A message is a request, never the owner's**: no owner provenance, no quote, no hand-over; the framing tells the
  receiving assistant to ask its own owner as always. What it can make happen is what that assistant would do for a
  stranger's request under its owner's rules.
- **The hub the data leaves decides**: its owner confirms each outgoing message by default and can stop confirming per
  partner; the receiving hub cannot approve the sender's card (its link reaches no App route). Limitation: the card goes
  by `proto.Resolution.UserAllowed()`, an allow nobody gave on the owner's behalf (not the assistant, not a supervising
  conversation). Like other permission cards, it does not check which device answered: an allow from an extension
  granted `agents.control`, or from a device using the hub's token, approves it too.
- **A partner's text is bounded and cleaned**: names and refusal reasons from the other hub lose control and
  bidirectional formatting characters (names at most 64 characters, reasons 300), and a name the manager quotes to the
  model or shows in a card loses the invisible characters too ("Sending"); envelope text is at most 64 KiB at the
  hub and 12,000 characters at the manager, keeps no control characters but line breaks and tabs and none of the
  invisible characters a model reads and the chat does not show (tag characters, bidirectional overrides and the like,
  "Sending"), is scrubbed again, and goes into the conversation as the message's own words after Persona's framing
  block, with the `<` of every tag in it (a `<`, spaces, slashes or backslashes or none, and a letter) written as `&lt;`
  (`prompts.PlainWords`), so it can neither open, close nor forge a context block, a file, a photo, a note the apps hide
  and the model takes for Persona's or the runtime's, another session's or a teammate's message, a task's notification
  or a command, whose tags the node's transcript reader would otherwise read as the whole message with another sender
  on it; a `<` that starts no tag ("2 < 3") stays. A message from a partner (its source `hub`) is never taken for one
  of Persona's notes whatever it begins with ("Receiving"). The partner's name goes inline only after its angle
  brackets and quote marks were replaced by look-alikes. Hub ids must match
  `^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`; addresses are cut to 8 https ones without `user:pass@`. A partner's error answer
  to a message reaches this hub's manager as one line of at most 200 characters, cleaned the same way.
- **A partner chooses its name** (and may change it in a later hello): the manager takes it from the hub's list only
  without hidden characters and with look-alike quote marks (so it goes the same way into `hub_list`, `hub_send`'s
  answers, the framing and the cards); the owner's card (and the web's card detail) names it with the first 8
  characters of its hub_id too, and `hub_list` says when two partners have one name. The chat label shows the name only.
- **Errors stay on their hub**: when this hub's manager does not take a partner's message, the partner gets
  `partner_unavailable` in fixed words; the manager's own error (which may name the database) goes only to this hub's
  log.
- **A pairing is a hub's own word, so it is never replaced**: the hub_id and pin a pairing hub names are not proven
  (it never shows it holds the key, and pins are public), so a code's holder claiming a partner's hub_id is refused
  (`already_paired`, `pin_changed`) on both sides, and the partner's pairing, credential and link stay as they were. A
  new pairing's hub_id is whatever the code's holder names: the code is what the owner handed out.
- **Refusal codes from a partner are mapped to known ones**: a pairing refusal is passed on only for `partners_off`,
  `member_hub`, `already_linked`, `already_paired`, `pin_changed`, `pairing_in_progress`, `tls_required` and
  `no_database` (anything else is `refused`); the manager keeps only the contract's codes (anything else is `invalid`
  for a refusal, `undelivered` for a failed status), so a partner's words never go into Persona's own notices.
- **Credentials**: codes single use, 10 minutes, hash only; issued credentials (device tokens of kind and role
  `partner`, `machine_id` the partner's hub_id) stored as hashes, received ones sealed; TLS pinned both ways, inside any
  proxy. Unpairing or revoking the credential ends the link at once.
- **A compromised partner** can send requests to the main conversation at a limited rate, which the receiving assistant
  treats as requests. It cannot read anything, answer cards, reach computers, or join a group.
- **Abuse limits**: 64 calls running at once per link (4013), 1 MiB frames, 30 messages and replies and 120 statuses per
  partner in 10 minutes at the manager; public authentication failures count as for any credential.

## Settings

- hub.json (read at start):

  ```json
  {"partners": {"enabled": true, "proxy": "", "proxy_always": false}}
  ```

  `enabled: false` is the emergency switch: no pairing, links refused (`403 partners_off`), no dialing, the tools gone;
  the pairings are kept, messages still queued fail as `unpaired`, and what partners sent that is not in a conversation
  yet stays out of it.
- In Postgres: the pairs (`partner_hubs`, `partner_codes`, hub set) with each partner's **Connect to this hub** switch;
  the messages, each with the pairing it was written for (`partner_messages`), and each partner's **Ask me before
  sending** switch with the pairing it was set for (`partner_prefs`), manager set.

## Setting up two hubs where only one can reach the other

Example: the home hub can reach the work hub (through a VPN or a proxy), the work hub cannot reach the home hub.

1. On the **work hub** (the one that gets dialed): enable TLS and name an address the home hub can reach, in hub.json:
   `"public": {"tls_listen": ":18662", "advertise": ["https://<work hub address>:18662"]}`; restart the hub. Open the work
   hub's web app › Settings › This hub › Partner hubs › **Show my code**, and copy the code.
2. On the **home hub** (the one that dials): if it needs a proxy to reach the work network, set
   `"partners": {"proxy": "http://<proxy address>:<port>"}` (or `socks5://…`) and restart. Open its web app › Settings ›
   This hub › Partner hubs › **Pair…**, paste the code, check the name and fingerprint, and pair.
3. On the work hub the home hub now appears with **Connect to this hub** off when the home hub has no TLS address to
   give; if it gave one the work hub cannot reach, switch it off there. The link comes up from the home side and carries
   both directions.
4. Each side's **Ask me before sending** is on: the first message either way shows a card on that hub's owner's phone.

Headless hubs can do the same from a shell on the hub machine: `personad hub partner code` on the work hub,
`personad hub partner add '<code>'` on the home hub, `personad hub partner list`, `personad hub partner dial <hub_id> never`.

## Code

- **Contract**: `server/internal/assistantwire/partners.go` (methods, envelope, codes, limits, the manager's routes);
  `proto.SourceHub` and `MessageSource.HubID` in `server/internal/proto/item.go`.
- **Hub** (`server/internal/hub/`): `partner.go` (state, lifecycle, telling the manager and the apps), `partner_pair.go`
  (codes, pairing, unpairing), `partner_link.go` (the link and its dispatcher), `partner_dial.go` (dialing),
  `partner_proxy.go` (candidates, proxies, the pinned client), `partner_bus.go` (`partner.send`, `partner.hubs`,
  inbound `msg`), `partner_routes.go` (the apps' and the command line's routes), `partner_cli.go`
  (`personad hub partner`), `partner_store.go` (the optional store interface), `config_partners.go` (hub.json
  `partners`). Wiring: `devicetokens.go` (the `jh_` lookup, the two roles, revoking unpairs), `http.go`, `peer_http.go`
  (a member answers the two hub-to-hub paths itself), `bus.go`, `bus_broadcast.go`, `extensions_link.go` (the feature),
  `shutdown.go`, `peer_admin.go`; `link.DialThrough` in `server/internal/link/proxy.go`.
- **Manager** (`server/internal/assistant/`): `partner.go` (states, limits, the store and bus interfaces, the partner
  list), `partner_tools.go` (`hub_list`, `hub_send`, the card), `partner_receive.go` (`partner.receive`, delivery,
  statuses), `partner_loop.go` (the delivery loop), `partner_routes.go`; small hooks in `bus.go` (the capability),
  `tools.go`, `conversations.go`, `relay_once.go`, `relay_refusal.go`, `fleet.go`, `task_brief.go`, `schedule.go`,
  `service.go`.
- **Prompts**: `server/internal/prompts/assistant/partner_message.md`, `partner_reply.md`, `partner_notice.md`
  (rendered in `server/internal/prompts/assistant_partner.go`); tool descriptions
  `server/internal/prompts/mcp/assistant/hub_list.md` and `hub_send.md`.
- **Database**: `server/internal/db/partners.go` with the hub-set migration
  `server/internal/db/migrations/hub/20261122193642_partner_hubs.sql` (`partner_hubs`, `partner_codes`);
  `server/internal/db/partner_messages.go` with the manager-set migration
  `server/internal/db/migrations/manager/20261122194225_partner_messages.sql` (`partner_messages`, `partner_prefs`).
- **Strings**: `hub-partner_pair`, `hub-partner_dial`, `hub-partner_routes`, `hub-partner_cli`, `assistant-partners` and
  `assistant-tool_titles` in `server/internal/i18n/locales/`; `settings-partnerhubs` in `web/src/i18n/locales/`.
- **Web**: `web/src/lib/partner-hubs.ts`, `web/src/views/settings/PartnerHubs.tsx` (`docs/web/web.md`); the `hub` source
  in `web/src/lib/message-source.ts` and `web/src/views/chat/SourceMessage.tsx`; the card's detail in
  `web/src/lib/tool-detail.ts`. Mock: `scripts/mock/partner_hubs_mock.py` (`MOCK_PARTNER_HUBS`).

## Verification

- **Hub, in process** (no database: the test store `meshStore` keeps partners in memory; TLS hubs on 127.0.0.1 from
  `server/internal/hub/partner_harness_test.go`, each with a key of its own where pins matter (`partnerCert`),
  scripted managers on each bus; `waitPartnerLink` waits for the link the duplicate rule keeps):
  - `TestPartnerPairAndLink`, `TestPartnerPairingCode`, `TestPartnerPairingRefusals`,
    `TestPartnerPairingNeverReplacesAPartner`, `TestPartnerPairingWordsSayWhereToUnpair`, `TestPartnerPairingCrossed`,
    `TestPartnerPairBounds`, `TestPartnerName`, `TestPartnerAnswered` (`partner_test.go`): pairing both ways, the code's
    rules, every refusal, a code's holder claiming a partner's hub_id refused on both sides (`pin_changed`,
    `already_paired`) with the partner untouched and the code's hub's words naming it as the hub to unpair on, two
    owners pasting each other's codes at once (one side held mid-pairing: one refused with `pairing_in_progress`, the
    other paired with credentials neither side revoked), bounds on what the other hub sends, cleaned names, refusal
    codes passed on only from the list.
  - `TestWithInjectedNeverAPartnersWords` (`injected_test.go`): a partner's words that begin like a task's notice, a
    supervision, the answer-again or a wake-up, as delivered, are never marked as Persona's note
    (`TestParseInjectedNeverAPartnersWords` in `server/internal/prompts/injected_test.go` for every note, message and
    reply; the web's in `web/src/lib/injected.test.ts` and `web/src/lib/turns.test.ts`).
  - `TestPartnerOneSideDialsAndReconnects`, `TestPartnerDuplicateLink`, `TestPartnerHelloMustNameThePairedHub`,
    `TestPartnerUnpair`, `TestPartnerDialSetting`, `TestPartnerLinksStopWithTheHub`, `TestPartnerDialRefusalsSaid`
    (`partner_link_test.go`).
  - `TestPartnerEnvelopeRoundTrip`, `TestPartnerSendWhenItCannotGo`, `TestPartnerTooManyCalls`, `TestPartnerErrorsStayHome`,
    `TestPartnerLoadRetried`, `TestPartnerListFollowsTheDefaultManager`, `TestPartnerListSaysWhenPaired`
    (`partner_bus_test.go`): errors kept on their hub and a partner's bounded, a failed read of the pairings at start
    retried (sends wait, no empty list told), the list following the default manager, `paired_at`.
  - `TestPartnerThroughConnectProxy`, `TestPartnerThroughSocksProxy` (credentials, an impostor behind the proxy),
    `TestPartnerBadProxyNeverGoesDirect`, `TestPartnerNoProxy`, `TestPartnerCandidates`, `TestParsePartnerProxy`
    (`partner_proxy_test.go`); the hub's `TestMain` clears the proxy variables.
  - `TestPartnerCommands`, `TestPartnerArguments` (`partner_cli_test.go`).
  - Run: `cd server && go test -count=1 -race -run 'TestPartner|TestParsePartnerProxy' ./internal/hub/`.
- **Database** (Postgres, `PERSONA_TEST_DATABASE_URL`): `TestPartnerHubsAndCodes` (`server/internal/db/partners_test.go`);
  `TestPartnerMessagesInsertOnce`, `TestPartnerMessagesCompareAndSet`, `TestPartnerMessagesClaim`,
  `TestPartnerMessagesListsCountsAndPrefs` (`server/internal/db/partner_messages_test.go`). Run:
  `go test -count=1 -run 'TestPartner' ./internal/db/`.
- **Manager** (`server/internal/assistant/partner_test.go`, a fake hub, Postgres): `TestGoldenPartnerPrompts`,
  `TestPartnerToolsOnlyWhilePaired`, `TestHubSendAsksAndSendsOnAllow`, `TestHubSendDeclinedOrExpiredIsTold`,
  `TestHubSendAlwaysStopsAsking`, `TestHubSendScrubs`, `TestPartnerHiddenCharactersGoNeitherWay` (the card's text is
  what goes; nothing invisible either way), `TestPartnerScrubLeavesNoInvisibleRun` (every spelling of nothing, no run
  of the kept joiners and marks, emoji and scripts as written), `TestHubSendReplyToAndHopLimit`, `TestHubSendRefusesHealthAndGuests`,
  `TestHubSendRefusesOutwardJustMeAndWork`, `TestPartnerAnswerOnlyTheOwnersYes`, `TestPartnerReceiveDeliversOnceIntoMain`,
  `TestPartnerReceiveRefusals`, `TestPartnerReplyGoesToTheAskingConversation`, `TestPartnerStatusUpdatesTheMessage`,
  `TestPartnerOfflineThenOnline`, `TestPartnerRestartAsksAgain`, `TestPartnerQueueExpires`,
  `TestPartnerReconcilerDeliversWhatWasLeft`, `TestPartnerMessageFollowsTheMainConversation`,
  `TestPartnerCodesAreTheContractsOnly`, `TestPartnerInlineKeepsTheNameInItsQuotes`, `TestPartnerRoutes`,
  `TestManagerCapabilitiesFit`; in `partner_pairing_test.go`: `TestPartnerPairedAgainStartsOver`,
  `TestPartnerUnpairedFailsWhatWaits`, `TestPartnerSendsNothingOfAnEarlierPairing` (with no sweep running, the loop's and
  the card's own checks; recent rows told apart by their pairing alone), `TestPartnerPairingTimesAreTheHubsOwn` (a hub
  clock ahead or set back), `TestPartnerNothingOfAnEndedPairingGoesIn` (received, taken up again, waiting in the relay
  queue; unpaired and paired again), `TestPartnerLookalikeNames`; in
  `partner_delivery_test.go`: `TestPartnerSilentPartnerHoldsUpOnlyItself`, `TestPartnerRateLimits` (both limits, envelopes
  and tool calls side by side), `TestPartnerReplyRefusedGoesToMainOnce`, `TestPartnerReconcilerQueuesALostStatus`,
  `TestPartnerRefusedThereIsFailedHere` (two managers: refused there, failed and told here),
  `TestPartnerLostOrWorkComputerFailsUndelivered`. The framing's prompt test is `TestPlainWordsOpenAndCloseNothing`
  (`server/internal/prompts/context_body_test.go`); `TestPartnerWordsReadAsOneHubMessage`
  (`server/internal/transcript/partner_words_test.go`) reads framed partner words with forged tags back through the
  node's transcript reader: one message, from the hub, all of the words. Run:
  `go test -count=1 -race -run 'Partner|HubSend|TestManagerCapabilitiesFit' ./internal/assistant/`.
- **Two hubs end to end** (`server/internal/hub/partner_e2e_test.go`: real managers on two in-process hubs, each with
  its delivery loop, a node for its main conversation and an app answering its cards; their partner messages kept in
  memory by `server/internal/hub/partner_e2e_store_test.go`): `TestPartnerE2E` (pairing lists the tools; a message waits
  for the owner's OK, scrubbed on the card, goes once allowed and arrives in the partner's main conversation as another
  hub's request; its status comes back; the answer, sent without a card, arrives in the conversation that asked; a
  message written while the link is down goes once when it is back; a resend is a duplicate; a declined card sends
  nothing and says so; unpairing takes the tools away) and `TestPartnerE2EThroughProxy` (a hub that reaches its partner
  only through an HTTP CONNECT proxy, `proxy_always`). Run: `go test -count=1 -race -run 'TestPartnerE2E' ./internal/hub/`.
- **Web** (vitest, zh-Hans): `web/src/lib/partner-hubs.test.ts`, `web/src/views/settings/PartnerHubs.test.tsx`,
  `web/src/lib/message-source.test.ts`, `web/src/views/chat/SourceMessage.test.tsx`,
  `web/src/views/chat/PermissionBody.hub.test.tsx`, `web/src/lib/tool-detail.test.ts`, `web/src/lib/turns.test.ts`. Run:
  `cd web && npx vitest run src/lib/partner-hubs.test.ts src/views/settings/PartnerHubs.test.tsx`. In a browser, against
  the mock hub with `MOCK_PARTNER_HUBS=1`: `node web/e2e/partner-hubs.mjs <web> --hub <mock> --shots <dir>` and
  `web/e2e/message-sources.mjs` (`web/e2e/README.md`); the mock's own test is `scripts/mock/test_partner_hubs_mock.py`.

## Not implemented / follow-ups

- **iOS and Android settings**: no Partner hubs page yet (pair, switches, unpair); use the web app or the command line.
  Messages from a partner already show on iOS and Android as a source message labeled with the hub's name. iOS still
  reads Persona's notes off an unmarked message's text itself (`apple/Shared/Injected.swift`, for older hubs) without
  skipping a partner's: there a partner's words that begin exactly like a supervision message (its head and marker)
  fold the turn, the assistant's reaction included, into one supervision line (`apple/Persona/Views/Chat/ChatLines.swift`),
  and words that begin like a follow-up show only the follow-up's text. The fix, `Injected.of` returning nil for an
  item whose source is `hub` as the web's `injectedOf` does, still needs an iOS build and test run.
- **Binding approvals to the owner's own device**: the card accepts any answer `UserAllowed()` accepts ("Security
  boundaries"). Requiring the owner's own device (carrying it through the card's answer as the secret hand-off card
  does) would also stop approvals from the watch and from Lark: a product decision.
- **Early stop of a candidate race**: a 401 or a pin mismatch on one address does not stop the others; the dial waits
  for every candidate (as the mesh does), and its error lists each.
- **No relay between three hubs**: each pair is separate, and a hub never passes a partner's message on.
- The manager's message list (`GET /api/v1/partner-messages`) has no screen yet.
- The source label in the chat (`@Work`) is the partner's name alone; two partners of one name are told apart in
  `hub_list` and on the owner's card (the start of the hub_id), not in the label.
