# iPhone control: pairing and sessions (#3448)

iPhone control lets the assistant look at and operate a paired iPhone. Nothing is installed on the phone and no VPN
runs on it: a **node** on one of your computers reaches the phone over the network through RemotePairing, the same
trusted-computer channel the phone already offers to a computer it trusts. This doc covers the node's part that
**pairs** a phone and keeps a **session** with it. The device bridge `persona-iphone-bridge` (the program that speaks
RemotePairing: screenshots, accessibility tree, input; its wire contract is #3451's), the node capability `iphone`
with its hub RPC (#3450), and the assistant's tools are separate pieces (epic #3452).

Code: `server/internal/iphone` (records, Bonjour, sessions, the `personad iphone` command).

> **Not verified on a real device yet**: the pairing prompts, the contents of the phone's Bonjour TXT record, and how
> a sleeping phone's Wi-Fi behaves were tested only against a fake bridge and a Bonjour service registered on a Mac.
> Remove this note once someone has paired a real iPhone and driven it over Wi-Fi.

## Pairing once over USB

```sh
personad iphone pair            # the only iPhone plugged in over USB
personad iphone pair <udid>     # one of several
personad iphone list            # paired here, and plugged in now
```

1. Plug the phone in with a cable and unlock it.
2. `personad iphone pair` asks the bridge (`persona-iphone-bridge pair --udid <udid> --out <path>`) for the trusted
   pairing. The phone asks its user to **Trust** this computer and for its passcode; the command waits up to 3 minutes.
3. The bridge writes the pairing record; the node keeps it, and next to it what the bridge reported: the phone's
   name, product type (model identifier such as `iPhone16,2`) and iOS version.

The app can start the same pairing as a background job on the machine (`StartPair`, one job at a time, its state in
the machine's iPhone status like the computer extension's install job). Someone still has to be at the phone to tap Trust.

Pairing again replaces the record and keeps a manual address. `personad iphone forget <udid>` removes the pairing from
this computer only; the phone keeps listing the computer as trusted until its user resets that on the phone (Settings
› General › Transfer or Reset iPhone › Reset › Reset Location & Privacy).

### Where the record lives

`<PERSONA_HOME>/iphone/<udid>/` (by default `~/.persona/iphone/…`): the directory 0700, both files 0600, written
through a temporary file and a rename.

| File | Contents |
| --- | --- |
| `pairing.record` | The bridge's own format, holding this computer's private key for the phone. Only the bridge reads it; the node never parses it |
| `device.json` | Nothing secret: name, product type, iOS version, manual address, the last address that worked, when it was paired |

The **UDID** names the directory and is the phone's stable id: the same across addresses, reconnections and iOS
updates. Name and iOS version are refreshed from the bridge's `device_info` whenever a session comes up.

This is node-local state, not hub state: the pairing keys exist only on the computer that paired the phone and never
go to the hub, its database, logs or the app. That is also why it isn't in the hub's Postgres, which lives on another
machine. The phone's network address stays on the node as well (`Device.Address` is local; the node capability doesn't
send it to the hub).

## Finding the phone

A phone on Wi-Fi changes its address with DHCP, so the node never keeps an address it resolved once. Every time a
session (re)connects it tries, in order:

1. **The manual address**, if set: `personad iphone address <udid> <host[:port]>`, for a VPN address or name where
   Bonjour doesn't reach (`--clear` to go back to Bonjour).
2. **Bonjour**: a one-shot multicast DNS query for `_remotepairing._tcp.local.` on every multicast interface, IPv4 and
   IPv6, from an ephemeral port with the unicast-response bit (RFC 6762 §5.1), so it runs beside the system's own
   responder without joining the group. An advertisement doesn't name its phone: its TXT `identifier` changes, and
   its `authTag` is derived from the identifier with a key kept inside the pairing record. So for each new
   advertisement (with an address, a port, an identifier and a tag) the node asks the bridge
   `persona-iphone-bridge match --record <path> --identifier <id> --auth-tag <tag>` → `{"match": true|false}`, and
   connects to the first that matches: its SRV port goes to the bridge with each address (IPv4 first, link-local IPv6
   last with its interface, as `fe80::…%en0`). It stops as soon as the phone is found, after at most about
   4.5 seconds. `personad iphone find` shows what it sees and which paired phone each advertisement is.
3. **The last address that worked**, remembered after each Bonjour connection: the fallback when Bonjour finds nothing.

`match` belongs to the bridge's USB pairing module (with `pair` and `usb-list`); until a bridge has it, Bonjour finds
nothing and only the manual and last addresses work.

### Simulator devices

For end-to-end checks without a phone, the bridge can drive a Simulator device on the same computer with the same
wire schema (pending in the bridge, #3451). `personad iphone pair <simulator udid>` pairs it without USB; the bridge
marks it `"transport": "simulator"` on its success line and the node keeps that in `device.json`. For such a record
the node looks for nothing on the network: no Bonjour, no manual or last address, and `serve` runs with only
`--udid` and `--record` (the bridge takes the device from the record). Everything else is the same: one session,
the health check, backoff and restarts, and never replaying input. `Devices` reports it with transport `simulator`.

## Sessions

One session per phone, owned by the node process (`iphone.Default()`), not by a hub connection or a client:

- **Started by the first call**, then **kept while idle**: a connected phone stays connected with nothing to do, so a
  client closing or the hub restarting (or being redeployed) doesn't drop it. It needs the resident node (Persona
  Link); a node the hub runs over SSH lives only as long as that connection.
- **The bridge's `serve`** holds the link: one process per phone, JSON lines on stdin / stdout, requests answered one
  at a time in order. The node writes a request only once the bridge said it is ready.
- **Health check**: every minute an idle link is probed with `device_info` (15 s). A probe that times out or finds the
  link gone ends it, so a half-open connection (the phone left the Wi-Fi without a goodbye) is noticed without a call;
  its answer refreshes the lock state and iOS version shown in status.
- **Record changes from another process**: the command line runs with a Manager of its own. Before every call and on
  every health check the session compares the record on disk (pairing file, manual address) with the one its link
  started from; when they differ (paired again, forgotten, a new address) it ends the link on purpose and connects
  again at once, or reports `not_paired`. Ending a link on purpose isn't a failure: no backoff, no error in status.
- **Reconnect with backoff**: when the link drops, the supervisor finds the phone again (above) and starts `serve`
  again after 1 s, doubling to 2 minutes, ±20%. A call waiting for the phone cuts the backoff short (at most one
  attempt a second). A link that lasted 30 seconds resets the backoff. Unreachable and unwanted for 10 minutes, it
  stops trying until the next call.
- **Failures waiting won't fix** (not paired, no bridge, Developer Mode off, developer disk image not mounted, the
  pairing rejected, the wrong phone at the address) end a waiting call at once with that code; retrying goes on in
  the background.
- The node updating itself (`exec`) closes `serve`'s stdin, and `serve` stops; the new node starts a session on the next call.

### Never replaying input

An operation that changes the phone (tap, swipe, typing, keys, launching an App…) is sent **at most once**:

| What happened | Read-only operation (`device_info`, `screenshot`, `ax_tree`, `list_apps`) | Any other operation |
| --- | --- | --- |
| Write to the bridge failed (also when the deadline cut it off) | Sent on the next link | Sent on the next link, or `unreachable` at the deadline: the bridge acts only on whole lines, so nothing reached the phone |
| The write finished just as the deadline passed | `timeout` | **`uncertain`**: the whole line reached the bridge |
| Link dropped before the answer, or the bridge answered `disconnected` | Sent once more on the next link | **`uncertain`**, never sent again |
| The caller's deadline passed before the answer | `timeout` | **`uncertain`**, never sent again |
| The bridge answered `timeout` | `timeout` | **`uncertain`** |
| Any other answer | Returned as it is | Returned as it is |

`uncertain` tells the assistant the action may or may not have happened and to look at the screen before trying
again. A late answer to an earlier call is told apart by its request id and dropped. The caller's deadline goes to
the bridge as `timeout_ms`. The bridge must ignore a last request line without its newline at the end of its input,
which is what makes a broken write safe to send again.

## Node-side interface

What the node capability calls (`server/internal/iphone/manager.go`):

| Call | Does |
| --- | --- |
| `Default()` | The node process's Manager, rooted at `<PERSONA_HOME>/iphone` |
| `Bridge(ctx)` | Whether the bridge is installed here, and its version |
| `Devices(ctx)` | Paired phones (UDID, name, product type, iOS version, locked, session state `idle` / `connecting` / `connected` / `backoff`, address and port, last error and code), then phones plugged in over USB that aren't paired. Starts no session; the USB list is reused for 5 seconds |
| `Call(ctx, udid, op, args)` | One bridge operation; connects first if needed, within ctx's deadline |
| `Sent(err)` | For a `Call` error: `no` (never sent), `unknown` (uncertain), `answered` (the bridge said how it ended) |
| `StartPair` / `StartForget` / `Cancel` / `Job` | The background job the app starts |
| `Connect(udid)` | Starts a session without sending anything |

Errors are `*iphone.Error{Code, Message}`: the bridge's codes, plus `not_paired`, `bridge_missing`, `unreachable`,
`uncertain` and `busy`. Messages are English, for logs and the assistant; the app maps codes to its own text.

## The node program `iphone` (#3450)

The manager reaches the paired phones through the node's built-in program `iphone` (`server/internal/node/iphone.go`,
listed in `node.info`'s `builtin_programs`), over `plugin.node.call {id: "iphone", method, params}` only: it has no old
method names, and a node without it answers the manager's `iphone.*` with "unknown method". It reads the phones through
the sessions above (`node/iphone_host.go`) and hands looks and steps to the phone control core; types are in
`proto/iphone.go`.

| Method | Params → result | What it does |
| --- | --- | --- |
| `status` | `{}` → `IPhone` | The bridge (installed, version, kept 30 s), the paired phones and those on USB with their session's state, and for a connected one its latest `device_info`: `connected`, `locked`, `capabilities`, `ready` (connected, unlocked, screenshot, accessibility tree and input available) and `note`. An idle paired phone's session is started. `enabled` and `allowed` are the manager's to fill |
| `call` | `IPhoneCall {device?, session?, op, args?, approved?, timeout_ms?}` → `IPhoneResult {output?, text?, image?, needs_approval?, approval_summary?, approval_id?}` | `op` is `status` (answered here, with a fresh `device_info`), `look` or `do` (the core's, with the bridge's operations on that phone). `device` empty is the only paired phone. The wait is clamped to 1–120 s (30 s by default). A screenshot the core wrote into the call's temporary directory becomes `image`; the directory is removed after the call |
| `pair` / `forget` | `{device}` → `IPhone` | Start the pairing job above, or forget a phone; its state is `job` |
| `cancel` | `{}` → `IPhone` | Stop the job |

- **Whether a failed step reached the phone**: every `call` error is an RPC error with a code and `data: {"sent": "no" | "unknown"}`.
  `no` only when nothing that changes the phone was handed to it: the node's own checks (`bad_op`, `bad_args`,
  `not_paired`, `ambiguous_device`, `bridge_missing`), the session's refusals before sending, and the bridge's refusals
  before acting (`locked`, `developer_mode_off`, `ddi_not_mounted`, `unreachable`, …). Once an operation that changes the
  phone went out during the step, the whole step is `unknown`, as are `uncertain`, `timeout`, `cancelled` (the manager
  gave up) and anything without a code.
- **Notes**: the computer's note, each phone's and a job's error are fixed Simplified Chinese strings chosen by the
  bridge's or the session's code (`iphoneNotes`); the hub translates them by their original from its catalog
  (`hub-iphone.json`, `rpc_*`), so the two must stay identical. A code without a note shows a generic failure (a job keeps
  its own words).

## Security

- Addresses are only hints. Anything on the network can answer the Bonjour query, and a manual address can be wrong;
  the bridge authenticates the phone with the keys in the pairing record before anything is sent, so a wrong address
  fails to connect rather than reaching another device.
- The pairing record holds this computer's private key for the phone: 0600 in a 0700 directory of the user the node
  runs as, never logged, never sent anywhere. The node passes only its path to the bridge.
- Device ids and manual addresses are checked before they become paths or arguments (no `/`, `..`, or leading `-`).

## Command line

`personad iphone list | pair | forget | address | find | call` (`personad iphone -h`). `call <udid> <op> [json]` runs
one operation through a session of the command's own, for checks; it doesn't go through the node.

The bridge is found at `$PERSONA_IPHONE_BRIDGE`, else in `PATH`, else `<PERSONA_HOME>/bin`, else next to `personad`.

## Tests

`server/internal/iphone`: the store's permissions and ids, Bonjour responses assembled into services and matched, every row of the
replay table against an in-memory bridge, re-resolution after an address change, the health check, record changes
from another process, backoff, and the real process path against a shell-script bridge. Locally checked once against
two `_remotepairing._tcp` services registered with `dns-sd -R` on a Mac (both found, the one whose tag the record
accepts matched, `serve` started at its address and port).
