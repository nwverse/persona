# Logical input reconciliation

A claimed input stores `dispatch_segment`, `dispatch_machine`, `dispatch_session`
and `dispatch_epoch` before any RPC. Its queue position and client message ID stay
stable. A missing receipt is uncertain delivery; it never authorizes replay.

The reconciler queries `assistant.segment.input` on the recorded target. An
`accepted: true` receipt settles that attempt. Only an explicit `rejected: true`
receipt proves non-delivery and requeues the original message at its original FIFO
position, recording the refused attempt and full payload in audit as actor
`runtime_receipt`. `accepted: false` means unknown. Contradictory receipts
fail closed. Historical settlement checks the current root epoch and original
attempt CAS, then settles only the old input. It cannot accept a new switch,
consume its handoff or move its target. A positive historical receipt consumes
only the original epoch's idle-accepted handoff, so a later rollback cannot attach
that already-delivered context again. Current first-turn settlement and switch
acceptance remain one transaction.

New switch preparation/activation refuses unresolved input under the root lock.
For pre-upgrade cross-epoch rows, dispatch identity is backfilled from committed
historical activation (or the epoch-1 root), including rows written by a rolling
old binary. It is never guessed from today's active segment. An unprovable target
stays blocked until an operator explicitly resolves it. The original target is
kept open while its receipt remains unresolved. Later inputs remain FIFO queued.

## Diagnostics

Full-role devices can read:

- `GET /api/v1/assistant/conversations/{id}/segments?machine_id=...`
- `GET /api/v1/assistant/conversations/{id}/switch/{operation}?machine_id=...`

`blocked_input` identifies the client message, queue position, dispatch identity,
state, attempt version, reason and last reconciliation time. Batch switch items
also expose `blocked_input` and a nonempty blocking reason. Payload is excluded
from these responses.

## Explicit resolution of one input

Use a full-role authenticated device, the public logical root and the exact
`blocked_input` CAS tuple. This endpoint is intentionally unavailable to reader
or extension roles:

`POST /api/v1/assistant/conversations/{id}/inputs/resolve?machine_id=...`

```json
{
  "request_id": "unique-operator-intent-id",
  "expected_epoch": 4,
  "input": {
    "client_msg_id": "original-client-id",
    "position": 30,
    "epoch": 3,
    "state": "in_doubt",
    "dispatch_segment": "original-segment-id",
    "dispatch_machine": "original-machine",
    "dispatch_session": "original-native-session",
    "dispatch_epoch": 3,
    "version": 1
  },
  "action": "replay",
  "reason": "operator reviewed the original target and explicitly accepts replay risk"
}
```

Choose `accepted` to confirm delivery, `rejected` to settle it as terminally
refused, or `replay` to requeue this exact input at its original FIFO position.
Replay can duplicate uncertain execution; it is solely an explicit operator
choice. There is no default replay. Payload, attachments, provenance and hash
are preserved. The next claim records its new dispatch identity and advances
its attempt version; stale dispatch callbacks and pre-RPC cleanup cannot affect
that attempt even at the same epoch. Before the physical RPC the manager validates
the original claim again while holding the root epoch lock through dispatch. A manual current-target rejection rolls back the switch without
implicitly requeueing the rejected input.

A repeated identical request with the same actor and request ID succeeds without
another transition. Reusing that ID for different intent or using a stale CAS
returns HTTP 409. Each successful intent writes `conversation_input_resolutions`
in the same transaction, including actor, reason, action, dispatch identity,
exact request, payload hash and the complete original payload. This audit copy
survives the normal terminal input payload cleanup. It follows logical-root
retention/deletion. Historical settlement never confirms the new target's first
turn; the new target still needs its own receipt or independent idle acceptance.

This code migration requires the normal Hub schema migration on a future
separately authorized deployment. The implementation task does not run this
endpoint, modify production data or deploy services.
