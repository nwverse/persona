# Phone control: pairing and sessions (#3448)

Phone control lets the assistant look at and operate a paired iPhone. Nothing is installed on the phone and no VPN
runs on it: a **node** on one of your computers reaches the phone over the network through RemotePairing, the same
trusted-computer channel the phone already offers to a computer it trusts. This doc covers the node's part that
**pairs** a phone and keeps a **session** with it. The device bridge (the program that speaks RemotePairing:
screenshots, accessibility tree, input), the node capability with its hub RPC, and the assistant's tools are separate
pieces with their own docs.

Code: `server/internal/phone` (records, Bonjour, sessions, the `personad phone` command).

> **Not verified on a real device yet**: the pairing prompts, the contents of the phone's Bonjour TXT record, and how
> a sleeping phone's Wi-Fi behaves were tested only against a fake bridge and a Bonjour service registered on a Mac.
> Remove this note once someone has paired a real iPhone and driven it over Wi-Fi.

## Pairing once over USB

```sh
personad phone pair            # the only iPhone plugged in over USB
personad phone pair <udid>     # one of several
personad phone list            # paired here, and plugged in now
```

1. Plug the phone in with a cable and unlock it.
2. `personad phone pair` asks the bridge (`persona-phone-bridge pair --udid <udid> --out <path>`) for the trusted
   pairing. The phone asks its user to **Trust** this computer and for its passcode; the command waits up to 3 minutes.
3. The bridge writes the pairing record; the node keeps it, and the phone's name, model, iOS version and its
   RemotePairing **identifier** (what the phone advertises over Bonjour) next to it.

The app can start the same pairing as a background job on the machine (`StartPair`, one job at a time, its state in
the machine's phone status like the computer extension's install job). Someone still has to be at the phone to tap Trust.

Pairing again replaces the record and keeps a manual address. `personad phone forget <udid>` removes the pairing from
this computer only; the phone keeps listing the computer as trusted until its user resets that on the phone (Settings
› General › Transfer or Reset iPhone › Reset › Reset Location & Privacy).

### Where the record lives

`<PERSONA_HOME>/phone/<udid>/` (by default `~/.persona/phone/…`): the directory 0700, both files 0600, written
through a temporary file and a rename.

| File | Contents |
| --- | --- |
| `pairing.record` | The bridge's own format, holding this computer's private key for the phone. Only the bridge reads it; the node never parses it |
| `device.json` | Nothing secret: name, model, iOS version, Bonjour identifier, manual address, the last address that worked, when it was paired |

This is node-local state, not hub state: the pairing keys exist only on the computer that paired the phone and never
go to the hub, its database or the app. That is also why it isn't in the hub's Postgres, which lives on another
machine. The phone's network address stays on the node as well (`Device.Address` is local; the node capability doesn't
send it to the hub).

## Finding the phone

A phone on Wi-Fi changes its address with DHCP, so the node never keeps an address it resolved once. Every time a
session (re)connects it tries, in order:

1. **The manual address**, if set: `personad phone address <udid> <host[:port]>`, for a VPN address or name where
   Bonjour doesn't reach (`--clear` to go back to Bonjour).
2. **Bonjour**: a one-shot multicast DNS query for `_remotepairing._tcp.local.` on every multicast interface, IPv4 and
   IPv6, from an ephemeral port with the unicast-response bit (RFC 6762 §5.1), so it runs beside the system's own
   responder without joining the group. The advertisement whose TXT `identifier` equals the record's is the phone;
   its SRV port goes to the bridge with each address (IPv4 first, link-local IPv6 last with its interface). It stops
   as soon as the phone is found, at most 3 seconds. `personad phone find` shows what it sees.
3. **The last address that worked**, remembered after each Bonjour connection: the fallback when Bonjour finds nothing.

Without an identifier from the bridge (or a real phone that advertises a different TXT key), only the manual and last
addresses work; `personad phone pair` says so.

## Sessions

One session per phone, owned by the node process (`phone.Default()`), not by a hub connection or a client:

- **Started by the first call**, then **kept while idle**: a connected phone stays connected with nothing to do, so a
  client closing or the hub restarting (or being redeployed) doesn't drop it. It needs the resident node (Persona
  Link); a node the hub runs over SSH lives only as long as that connection.
- **The bridge's `serve`** holds the link: one process per phone, JSON lines on stdin / stdout, requests answered one
  at a time in order. The node writes a request only once the bridge said it is ready.
- **Reconnect with backoff**: when the link drops, the supervisor finds the phone again (above) and starts `serve`
  again after 1 s, doubling to 2 minutes, ±20%. A call waiting for the phone cuts the backoff short (at most one
  attempt a second). A link that lasted 30 seconds resets the backoff. Unreachable and unwanted for 10 minutes, it
  stops trying until the next call.
- **Failures waiting won't fix** (not paired, no bridge, Developer Mode off, developer disk image not mounted, the
  pairing rejected) end a waiting call at once with that code; retrying goes on in the background.
- The node updating itself (`exec`) closes `serve`'s stdin, and `serve` stops; the new node starts a session on the next call.

### Never replaying input

An operation that changes the phone (tap, swipe, typing, keys, launching an App…) is sent **at most once**:

| What happened | Read-only operation (`device_info`, `screenshot`, `ax_tree`, `list_apps`) | Any other operation |
| --- | --- | --- |
| Write to the bridge failed | Sent on the next link | Sent on the next link: the bridge acts only on whole lines, so nothing reached the phone |
| Link dropped before the answer, or the bridge answered `disconnected` | Sent once more on the next link | **`uncertain`**, never sent again |
| The caller's deadline passed before the answer | `timeout` | **`uncertain`**, never sent again |
| The bridge answered `timeout` | `timeout` | **`uncertain`** |
| Any other answer | Returned as it is | Returned as it is |

`uncertain` tells the assistant the action may or may not have happened and to look at the screen before trying
again. A late answer to an earlier call is told apart by its request id and dropped. The caller's deadline goes to
the bridge as `timeout_ms`.

## Node-side interface

What the node capability calls (`server/internal/phone/manager.go`):

| Call | Does |
| --- | --- |
| `Default()` | The node process's Manager, rooted at `<PERSONA_HOME>/phone` |
| `Bridge(ctx)` | Whether the bridge is installed here, and its version |
| `Devices(ctx)` | Paired phones with their session state (`idle` / `connecting` / `connected` / `backoff`, last error and code), then phones plugged in over USB that aren't paired. Starts no session |
| `Call(ctx, udid, op, args)` | One bridge operation; connects first if needed, within ctx's deadline |
| `Sent(err)` | For a `Call` error: `no` (never sent), `unknown` (uncertain), `answered` (the bridge said how it ended) |
| `StartPair` / `StartForget` / `Cancel` / `Job` | The background job the app starts |
| `Connect(udid)` | Starts a session without sending anything |

Errors are `*phone.Error{Code, Message}`: the bridge's codes, plus `not_paired`, `bridge_missing`, `unreachable`,
`uncertain` and `busy`.

## Security

- Addresses are only hints. Anything on the network can answer the Bonjour query with the phone's identifier, and a
  manual address can be wrong; the bridge authenticates the phone with the keys in the pairing record before anything
  is sent, so a wrong address fails to connect rather than reaching another device.
- The pairing record holds this computer's private key for the phone: 0600 in a 0700 directory of the user the node
  runs as, never logged, never sent anywhere. The node passes only its path to the bridge.
- Device ids and manual addresses are checked before they become paths or arguments (no `/`, `..`, or leading `-`).

## Command line

`personad phone list | pair | forget | address | find | call` (`personad phone -h`). `call <udid> <op> [json]` runs one
operation through a session of the command's own, for checks; it doesn't go through the node.

## Tests

`server/internal/phone`: the store's permissions and ids, Bonjour responses assembled into services, every row of the
replay table against an in-memory bridge, re-resolution after an address change, backoff, and the real process path
against a shell-script bridge. Locally checked once against a `_remotepairing._tcp` service registered with
`dns-sd -R` on a Mac (found, matched by identifier, `serve` started at its address and port).
