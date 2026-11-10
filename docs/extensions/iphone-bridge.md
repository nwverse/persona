# iPhone bridge (`persona-iphone-bridge`)

The assistant operates a paired iPhone the way it operates a Mac ([computer-use.md](computer-use.md)): a node on one of the user's
computers reaches the phone over the network and hands the screen and the actions to the assistant. Nothing is installed on the phone,
and no VPN or loopback tunnel runs on it. The capability is called `iphone` on the wire (`phone` already names the phone data tools).

This document is the **wire contract** of the native bridge that does the phone side on the node: what the node sends it and what it
answers. It describes primitives only; numbering elements, formatting for the model and approvals belong to the node's Go package that
calls it. Pairing and the node's session (records, finding the phone, keeping `serve` running, reconnecting) are #3448.

Status: v1. **Nothing here has been verified on a real device yet** (see "Verification status").

## What it is

- A single Rust binary, `persona-iphone-bridge`, in `native/iphone-bridge/`, built on the open-source
  [idevice](https://github.com/jkcoxson/idevice) library (MIT).
- It connects with **RemotePairing** (iOS 17 and later): a saved pairing record plus the phone's address on the LAN or on Tailscale. It
  verifies the record, opens the encrypted tunnel the phone offers, runs a **userspace** TCP stack over it, reads the service directory
  (RSD) and talks to the developer services listed there. It never changes the computer's routes, DNS or interfaces: no `utun`, no root.
- A **simulator backend** for development and testing: the same requests against an iOS simulator on this computer (see "Simulator
  backend"), so everything above the bridge can run end to end without a phone.
- Two ways to run it with the same requests and answers: **`call`** (one process per request) and **`serve`** (one process per phone,
  keeping the tunnel open). Every request has a total deadline.
- It is optional for the node, like Cua Driver for a Mac: not part of personad's build or CI. The node looks for it at
  `$PERSONA_IPHONE_BRIDGE`, then in `PATH`, then in `$PERSONA_HOME/bin` (`~/.persona/bin/`), then next to `personad`; without it the
  capability reports "not installed".
- Build: `native/iphone-bridge/build.sh` (format, lint, tests, release build; `--sim-runner` adds the simulator test runner; `--install`
  copies them to `~/.persona/bin/`). Needs a Rust toolchain.

## Invocation

```
persona-iphone-bridge version                             # "persona-iphone-bridge 0.1.0 (wire 1)"; also --version
persona-iphone-bridge call                                # one request on stdin, one answer on stdout
persona-iphone-bridge serve --udid <udid> --record <path> [--host <address>] [--port <port>] [--timeout-ms <ms>]
persona-iphone-bridge usb-list --json                     # phones attached over USB (#3448)
persona-iphone-bridge pair --udid <udid> --out <path>     # pair over trusted USB, write the record (#3448)
persona-iphone-bridge match --record <path> --identifier <id> --auth-tag <tag>   # is this Bonjour advertisement the record's phone? (#3448)
```

### One-shot commands (`call`, `usb-list`, `pair`, `match`, `version`)

| Outcome | stdout | stderr | Exit code |
| --- | --- | --- | --- |
| Success | one line: the answer | free-form diagnostics, may be empty | 0 |
| Failure | empty | last line: the error as JSON (`"ok": false`); diagnostics before it | 1 |

- The node reads only the **last line** of stderr as the error; anything before it is for logs.
- `call` reads one line (at most 1 MiB; the newline may be missing) and ignores anything after it.
- Connecting counts against the request's `timeout_ms`.

### `serve`

One process per phone. `--record` is the pairing record, `--host` the phone's address (required for a phone, not given for a simulator
record: an IP literal, an IPv6 literal with `%zone` such
as a Bonjour link-local address, or a host name), `--port` its RemotePairing port (default 49152, not verified on a real device: pass the
port Bonjour reports), `--timeout-ms` the deadline for connecting (default 30000).

- It connects, then writes `{"event":"ready","device":{…the device_info result…}}`. A failure before that is a one-shot failure (stdout
  empty, error on stderr's last line, exit 1).
- Then it reads requests from stdin, one per line, and writes one answer line per request on **stdout** (`"ok": true`, or `"ok": false`
  with `error`), with the request's `id`, which is required here. `device` in a request is ignored. Requests run one at a time, in order;
  blank lines are skipped.
- It acts only on **newline-terminated** lines: a last line cut off by the end of stdin is dropped as never sent. The node closes stdin
  when a write fails or its deadline passes halfway, and may send that request again on a new connection.
- When an operation fails because the link dropped (`disconnected`), or runs past its `timeout_ms` (`timeout`), the answer is followed by
  `{"event":"disconnected","reason":"…"}` and the process exits with code 1: an abandoned operation leaves the connection in an unknown
  state. It never reconnects by itself.
- EOF on stdin ends it with exit code 0, so a node that restarts itself (closing the pipe) also ends it.
- Read-only operations, safe to retry on a new connection: `device_info`, `screenshot`, `ax_tree`, `list_apps`. Every other operation
  changes the phone; its outcome is unknown when its answer did not arrive, and it must not be replayed.
- Input is answered once every event has been written into the tunnel. The phone does not confirm input events, so `ok` means "sent",
  not "seen on screen"; take a screenshot to check.

### Privacy

Neither stream carries the pairing record's keys or typed text; stderr never carries screenshot bytes. Requests go through stdin, not the
argument list, so they never show in the process list. Screenshots written with `out_file` are readable by this user only.

## Request

```json
{
  "v": 1,
  "id": "c-42",
  "op": "tap",
  "device": {
    "udid": "<udid>",
    "address": "<phone address>",
    "port": 49152,
    "pairing_file": "<PERSONA_HOME>/iphone/<udid>/pairing.record"
  },
  "timeout_ms": 30000,
  "args": { "x": 120, "y": 640 }
}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `v` | int | Wire version, `1`. Required in `call`, optional in `serve`; any other value fails with `bad_request`. |
| `id` | string | Echoed back in the answer and the error. Optional in `call`, required in `serve`. |
| `op` | string | One of the operations below. |
| `device` | object | `call` only; `serve` takes the device from its flags. |
| `device.udid` | string | The phone's UDID; checked against what the phone reports, a mismatch fails with `wrong_device`. |
| `device.address` | string | As `serve --host`; left out for a simulator record. |
| `device.port` | int, optional | RemotePairing port. Default 49152 (not verified on a real device). |
| `device.pairing_file` | string | The pairing record on this computer. The bridge only reads it. |
| `timeout_ms` | int, optional | Deadline for the request (in `call`, connecting included). Default 30000, clamped to 1000–120000. |
| `args` | object, optional | The operation's arguments; omitted means `{}`. |

Unknown fields are ignored, so a newer node can talk to an older bridge within the same `v`. Arguments are checked before anything is
sent to the phone: an invalid one never leaves half a gesture.

## Answer

```json
{ "v": 1, "id": "c-42", "ok": true, "op": "tap", "elapsed_ms": 412, "result": {} }
```

`result` is the operation's result below. Keys may come in any order.

## Error

```json
{ "v": 1, "id": "c-42", "ok": false, "op": "tap", "error": { "code": "unreachable", "message": "connect to <phone address>:49152: …" } }
```

`id` and `op` are present when they could be read from the request. `message` is English, for logs and the assistant; the node maps
`code` to user-facing text.

| Code | When |
| --- | --- |
| `bad_request` | Unparseable line, unknown `v` or `op`, missing or invalid arguments, a point outside the screen |
| `pairing_missing` | The pairing record is absent, unreadable or not one |
| `pairing_rejected` | The phone refused the record (removed from its trusted computers, or reset): pair again |
| `unreachable` | No TCP connection to the phone |
| `wrong_device` | The phone at that address is not `udid` |
| `tunnel_failed` | Connected, but the encrypted tunnel or the service directory failed |
| `developer_mode_off` | The operation needs Developer Mode, which is off |
| `ddi_not_mounted` | The phone does not list the service the operation needs; usually the developer disk image is not mounted |
| `service_unavailable` | A service is listed but could not be started; `message` says which |
| `locked` | The operation needs the phone unlocked |
| `app_not_found` | No app with that bundle ID |
| `unsupported` | Not possible (yet): see the operation |
| `disconnected` | `serve`: the link dropped during this operation; whether an input operation reached the phone is unknown |
| `timeout` | `timeout_ms` passed |
| `trust_pending`, `not_found` | `pair` only: tap Trust on the phone and retry; no such phone on USB |
| `internal` | Anything else |

## Coordinates

All positions are in **points** in the current interface orientation, origin at the top-left corner of what the user sees, `x` to the
right and `y` down; fractions are allowed. `screenshot` and `device_info` report `point_width` / `point_height`: a position read off a
screenshot `width` pixels wide maps to points as `x_pt = x_px × point_width / width`.

The bridge turns a point into the touchscreen's own coordinates (the panel as built, in portrait), rotating for landscape and upside down.
It takes a capture to be the panel as built too, and turns it to match the interface: a quarter turn when a portrait-shaped capture comes
with a landscape interface, a half turn for upside down. Both rotations are not verified on a real device.

## Operations

### `device_info`

No arguments. What the phone is and what works on it right now, read from the phone each time: the node's status probe and `serve`'s
ready event.

```json
{
  "udid": "<udid>", "name": "<device name>", "product_type": "iPhone16,2", "os_version": "18.1", "build": "22B83",
  "point_width": 430, "point_height": 932, "scale": 3, "orientation": "portrait",
  "locked": null, "developer_mode": true, "ddi_mounted": true,
  "battery": { "level": 0.81, "charging": false },
  "simulated": false, "transport": "network",
  "capabilities": { "screenshot": true, "ax_tree": false, "input": true, "apps": true },
  "notes": { "ax_tree": "unsupported" }
}
```

- `simulated` / `transport`: `true` / `"simulator"` for the simulator backend, `false` / `"network"` for a phone.

- `orientation`: `portrait` | `portrait_upside_down` | `landscape_left` | `landscape_right` (Home button or indicator on that side).
- Any field the phone did not answer is `null`. `locked` is always `null` in v1: no service found yet says whether the screen is locked.
- `capabilities`: whether the phone lists the services each group needs (`screenshot`; `input` for touches, keys and buttons; `apps`),
  and `ax_tree`, always `false` in v1. For each `false`, `notes` gives the reason with an error code: `developer_mode_off`,
  `ddi_not_mounted`, `unsupported`. A listed service can still fail when used; the operation's error says so.
- `ddi_mounted`: whether the developer services are listed at all; `null` when Developer Mode is off, since then nothing is listed either way.

### `screenshot`

| Arg | Default | Meaning |
| --- | --- | --- |
| `format` | `jpeg` | `jpeg` or `png` |
| `quality` | 85 | JPEG quality 1–100 |
| `max_dimension` | 0 | Scale down so the long edge is at most this many pixels; 0 keeps the native size |
| `out_file` | — | Write the image to this path (0600) instead of returning it; `data` is then left out |

```json
{ "mime": "image/jpeg", "data": "<base64>", "width": 1290, "height": 2796, "point_width": 430, "point_height": 932, "orientation": "portrait" }
```

### `ax_tree`

**On a phone, `unsupported` in v1** (the simulator backend has it, see "Simulator backend"). The library this is built on has no client for the phone's accessibility services, and an inspector protocol
written blind without a phone to test on would most likely not work. The assistant reads screenshots instead. The answer shape below is
reserved so the node can be written against it; see "Open questions".

| Arg | Default | Meaning |
| --- | --- | --- |
| `max_depth` | 0 | 0 = unlimited |
| `max_nodes` | 2000 | Stop after this many nodes; `truncated: true` |
| `include_offscreen` | false | Also elements whose frame is outside the screen |

```json
{
  "app": { "bundle_id": "com.apple.Preferences", "name": "Settings", "pid": 412 },
  "orientation": "portrait", "point_width": 430, "point_height": 932, "truncated": false,
  "root": {
    "id": "e1", "role": "application", "label": "Settings", "frame": [0, 0, 430, 932],
    "children": [
      { "id": "e2", "role": "button", "label": "Wi-Fi", "value": "Home", "frame": [16, 210, 398, 52], "enabled": true, "children": [] }
    ]
  }
}
```

Node fields: `id` (unique within one answer only), `role` (lower camel case), `label`, `value`, `identifier`, `hint`, `frame`
(`[x, y, width, height]` in points), `enabled`, `selected`, `focused`, `children`; absent when empty. `app` is `null` on the home screen.

### Input: `tap`, `long_press`, `drag`, `swipe`, `keys`, `button`

The phone takes synthetic input only while a display stream to this computer is running. The bridge starts one before the first input
(about a third of a second), reads and drops its video, and in `serve` keeps it for 10 seconds after the last input so a series of taps
does not restart it. Results are `{}`.

- `tap` `{ "x": 120, "y": 640, "count": 1 }`: `count` 1–3 (2 for a double tap).
- `long_press` `{ "x": 120, "y": 640, "duration_ms": 800 }`.
- `drag` `{ "from": {"x": 200, "y": 700}, "to": {"x": 200, "y": 200}, "duration_ms": 500, "hold_ms": 0 }`: `hold_ms` keeps the finger
  down at `from` before moving (drag and drop, moving icons).
- `swipe`: either `from` / `to` as in `drag`, or `{ "direction": "up", "origin": {"x": 215, "y": 466}, "distance": 400 }`:
  `direction` `up` | `down` | `left` | `right` is the way the finger moves; `origin` defaults to the screen center, `distance` (points) to
  half the screen along that axis. `duration_ms` defaults to 250.
- `keys`: exactly one of `{ "text": "hello" }` or `{ "key": "return", "repeat": 1 }`. Keys: `return`, `backspace`, `delete`, `tab`,
  `escape`, `space`, `up`, `down`, `left`, `right`. Text is typed as key presses on a US keyboard layout, so only printable ASCII, tab and
  newline can be typed; any other character (Chinese, accented letters, emoji) fails the whole request with `unsupported`, naming it,
  before anything is typed. At most 4,096 characters.
- `button` `{ "name": "home", "duration_ms": 0 }`: `home` | `lock` | `volume_up` | `volume_down`; `duration_ms` 0 is a short press,
  longer holds it (up to 10 s).

Durations are capped at 10 seconds.

### `launch_app`

`{ "bundle_id": "com.apple.Preferences", "args": [], "env": {}, "kill_existing": false }` → `{ "bundle_id": "…", "pid": 412 }`.

### `terminate_app`

`{ "bundle_id": "…" }` or `{ "pid": 412 }` → `{ "terminated": true }`, `false` when it was not running.

### `list_apps`

`{ "include_system": true }` → `{ "apps": [ { "bundle_id": "…", "name": "…", "version": "…", "type": "user" } ] }`: `type` `user` |
`system` (Apple's own).

## USB and Bonjour: `usb-list`, `pair`, `match`

In `native/iphone-bridge/src/pair.rs` (#3448). Pairing needs the phone on USB and already trusting this computer (the "Trust This
Computer?" prompt answered once); without that, `pair` answers `trust_pending` and nothing is written.

- `usb-list --json` → `[{"udid": "<udid>", "name": "<device name>", "product_version": "18.1", "model": "iPhone16,2"}]`: the phones
  usbmuxd sees over USB, each once. `name` is `null` until the phone trusts this computer (recent iOS gives its name only in a session).
- `pair --udid <udid> --out <path>` opens `remotepairingdeviced` through lockdown over usbmuxd and runs RemotePairing's manual pairing:
  the phone asks its user to allow it and for the passcode, then hands back the key its Bonjour advertisements are tagged with (`altIRK`).
  The record (keys, host name, that key) is written 0600, and the answer is `{"udid", "name", "model", "product_version"}`. Error codes
  besides the common ones: `trust_pending`, `locked`, `not_found` (no such phone on USB), `pairing_rejected` (the user declined).
- The record is the bridge's own format (`record.rs`: the RemotePairing keys plus the host name they were made under, which every later
  connection must send again) and opaque to the node.
- `match --record <path> --identifier <id> --auth-tag <tag>` → `{"match": true}` or `{"match": false}`: whether a `_remotepairing._tcp`
  advertisement (its TXT `identifier` and `authTag`) belongs to the record's phone. The tag is derived from a key inside the record, and the
  identifier is not a stable value to save at pairing, so the node asks the bridge for each advertisement and connects to the first match.

## Simulator backend (development and testing)

The same wire contract against an iOS simulator on this computer, through `xcrun simctl`. It is for developing and testing everything
above the bridge without a phone, and says so: `device_info` reports `"simulated": true`, `"transport": "simulator"`, and a `notes.simulated`
line.

- **Pairing**: `pair --udid <simulator udid>` (any simulator `simctl list devices` shows) writes a record marked as a simulator, 0600, with
  no USB or trust, and prints `{"udid", "name", "model", "product_version", "transport": "simulator"}`. A udid that is not a simulator here
  goes to the USB pairing above.
- **Connecting**: `serve --udid <udid> --record <path>` without `--host`, or `call` without `device.address`. The simulator must already be
  booted (`scripts/app/sim-boot.sh`): the bridge never boots, shuts down or erases one. Not booted: `unreachable`. Gone from this computer:
  `pairing_rejected`. A record for another simulator: `wrong_device`.
- **Operations**: `device_info` (model, iOS version and screen from the simulator's environment), `screenshot` (`simctl io screenshot`),
  `list_apps` (`simctl listapps`), `launch_app` (`simctl launch`; `env` is passed through as `SIMCTL_CHILD_…`), `terminate_app`
  (`simctl terminate`; by pid only for an app process of that simulator, found with its `launchctl list`, never any other process on the
  computer).
- **Accessibility tree and input** go through a test runner (`native/iphone-bridge/sim-runner/`), a UI-test bundle using only public
  XCUIAutomation API, run inside the simulator with `xcodebuild test-without-building`. No window opens on the computer and no macOS
  Accessibility permission is needed. Build it with `native/iphone-bridge/build.sh --sim-runner` (needs Xcode and XcodeGen; simulator only,
  no signing or team); it lands next to the binary as `persona-iphone-sim-runner/` (`--install` copies both), or set
  `PERSONA_IPHONE_SIM_RUNNER` to its folder. Without it, `capabilities.ax_tree` / `input` are `false` and those operations answer
  `unsupported`, naming the build command.
- **The runner's lifetime**: `serve` starts it right away, `call` on first use; it takes about 10–25 seconds to start, and an operation
  waits for it (in `serve` up to 15 seconds, then `service_unavailable`: try again). The bridge picks a free port, the runner listens only
  on `127.0.0.1` there, and the bridge passes it a random token: the runner answers only the one connection that sends it first. It ends
  when the bridge disconnects or exits (even if killed) or says `quit`; the bridge also kills `xcodebuild` if it lingers. Nothing keeps
  running after the bridge. A runner that dies mid-session is started again by the next request.
- **What the runner does**: `ax_tree` is the foreground app's element snapshot, plus any system alert (a permission prompt, which takes
  every touch and the Home button until answered); bare unlabelled containers and elements with no size are folded away. The foreground
  app is the first of the simulator's running, user-visible apps (most recent first) that reports itself in front, else the home screen;
  about 0.3 seconds. Touches are XCUICoordinate taps, presses and drags at screen points; `keys {text}` types any text, Chinese and emoji
  included; `keys {key}` presses the named key and needs a field with keyboard focus (`bad_request` otherwise); `button home` goes to the
  home screen (falling back to bringing SpringBoard forward when a runtime ignores the synthesized press). `lock` and the volume buttons
  answer `unsupported`: a simulator has none. Orientation comes from the runner in `ax_tree`; `screenshot` still reports `portrait`, or
  `landscape_right` when a capture is wider than tall.

### The simulator is not the phone

Results from the simulator backend say nothing about a real iPhone. The differences that matter:

| | Simulator backend | Phone |
| --- | --- | --- |
| Accessibility tree | yes, through the test runner | `unsupported` in v1 |
| `keys {text}` | any text | printable ASCII, tab and newline only |
| Input path | XCUIAutomation inside the simulator | HID events over the tunnel while a display stream is held open; not verified on a real device |
| `button` | `home` only | `home`, `lock`, `volume_up`, `volume_down` |
| Connection | none: local `simctl` and a loopback runner | RemotePairing, encrypted tunnel, service directory; not verified on a real device |
| Developer Mode, disk image, lock state | not applicable (`null`) | read from the phone |

### Try it

On a computer with Xcode, XcodeGen and a Rust toolchain:

```sh
native/iphone-bridge/build.sh --sim-runner
B=native/iphone-bridge/target/release/persona-iphone-bridge
SIM=$(scripts/app/sim-boot.sh "Persona iPhone Bridge <issue>")      # your own simulator
$B pair --udid "$SIM" --out /tmp/sim.record
$B serve --udid "$SIM" --record /tmp/sim.record                     # waits on stdin; type one request per line:
{"id":"1","op":"screenshot","args":{"out_file":"/tmp/sim.jpg"}}
{"id":"2","op":"launch_app","args":{"bundle_id":"com.apple.Preferences"}}
{"id":"3","op":"ax_tree"}
{"id":"4","op":"tap","args":{"x":201,"y":822}}                       # the center of an element's frame from ax_tree
{"id":"5","op":"keys","args":{"text":"Wi-Fi"}}
{"id":"6","op":"button","args":{"name":"home"}}
```

End with Ctrl-D: `serve` stops the runner and exits 0.

## Verification status

No iPhone was available while this was written. Every unit test runs against stand-ins: the request / answer / error framing, argument
checks, `call` and `serve` (with a fake phone), coordinate mapping, key tables, screenshot conversion, the record file. Nothing that talks
to a phone has run against one:

| Part | Status |
| --- | --- |
| RemotePairing verify, tunnel, userspace TCP, service directory | not verified on a real device |
| Default port 49152; reaching the phone over Tailscale | not verified on a real device |
| `device_info` fields (lockdown keys for screen size, Developer Mode, battery) | not verified on a real device |
| `screenshot`, its rotation in landscape | not verified on a real device |
| `list_apps`, `launch_app`, `terminate_app` | not verified on a real device |
| Input gate (display stream), touch coordinates and rotation, keys, buttons | not verified on a real device |
| `usb-list`, `pair` over USB (consent prompt, `altIRK`), `match` against a real advertisement | not verified on a real device; `usb-list` answered `[]` from a Mac's usbmuxd with no phone attached, `match` is unit-tested against the library's tag function |
| `ax_tree` | `unsupported` |
| Simulator backend: pair, `device_info`, `screenshot`, apps, `ax_tree` (with a system alert), tap, `keys` (ASCII and Chinese), swipe, `button home`, `serve`, runner cleanup after exit and after `kill -9` | checked on an iOS 27.2 simulator (Xcode 27.2) |

## Open questions

- **Accessibility tree**: needs a client for the phone's accessibility inspector service; to be built and tested with a phone.
- **Text beyond ASCII**: the key channel cannot type it. The phone's pasteboard service plus a paste shortcut might, at the cost of the
  user's clipboard; not done.
- **Locked screen**: no service found yet that reports it.
- **Developer disk image**: mounting it is not in v1 (`ddi_not_mounted`); #3470 tracks it.
