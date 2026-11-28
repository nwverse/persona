# Cloud host operations for computers

Many of the computers Persona reaches are rented: an instance at a cloud vendor, a small server behind a hosting
panel. When one of them goes away (a spot instance the vendor took back, a server that was shut down) the computers'
page could only say "offline", and the user had to open the vendor's console to start it again. The `cloud_hosts`
extension point lets an extension speak for a vendor: it lists the instances of the user's accounts, says how each is,
and starts, stops and restarts them, reads their addresses back, snapshots them and opens their console. Persona binds a
computer to its instance and puts those operations on the computer's page, in the offline sheet and in the assistant's
hands.

**Who it serves.** People who run their own servers and collaborate with Agents on them: the computers' page and the
Agents side of the product. Nothing appears for a user who has no such extension: no section, no badge, no assistant
tool, no extra request beyond one list of providers. The words on screen are plain (Start, Shut down, Restart,
Force restart, Power off); vendor terms stay in the vendor's own console.

Code: `server/internal/extension/cloud_hosts.go` (the contribution's rules, the registry's providers and the safety
table), `server/internal/extmgr/extension_cloud.go` (the routes, calling a program, the re-checks),
`server/internal/extmgr/extension_cloud_ops.go` (bindings, the gate, actions, snapshots, the audit and the computer
routes' handlers), `server/internal/extmgr/extension_cloud_recovery.go` (start and reconnect),
`server/internal/extmgr/extension_cloud_tools.go` (the assistant's tools); the web's client and its rules
`web/src/lib/cloud-hosts.ts`, and `web/src/views/machines/CloudSection.tsx` (the computer's page), `CloudOffline.tsx`
(the offline sheet's Start and reconnect), `CloudUnbound.tsx` (the computers' list's line of unbound instances) and
`CloudParts.tsx` (what they share). Tests: `server/internal/extension/cloud_hosts_test.go`,
`server/internal/extmgr/extension_cloud_test.go`, `server/internal/extmgr/extension_cloud_tools_test.go`,
`web/src/lib/cloud-hosts.test.ts`, `web/src/views/machines/CloudSection.test.tsx`, `web/src/views/Machines.cloud.test.tsx`,
`web/src/views/machines/AlertOffline.cloud.test.tsx`. Types:
`schema/extension/cloud.schema.json` and `CloudHostItem`, `CloudCustomAction`, `CloudSafety` in
`schema/extension/manifest.schema.json`.

## Principles

1. **Core never names a vendor.** The core knows instances, states, actions and safety levels. Which vendor, which API
   and which credentials are the extension's. `vendor` is a free id the extension chooses; the apps draw it as text.
2. **Standard operations are typed, the rest is declared.** Power and the common operations are a closed vocabulary
   with a closed state set, so every client draws and gates them the same way. What only one vendor has (renew, a
   keep-alive probe) is a custom action the manifest declares with its own safety level and confirmation sentence.
3. **The hub holds the gate, not the program.** A standard action's safety level is the hub's table; a program cannot
   lower it. A program hears of a write only after the gate passed, and every answer is held to the schema and
   re-checked before it is used.
4. **Credentials stay in the extension.** API keys and private keys are the extension's own `secrets` (sealed by the
   hub, handed only to that program). No `cloud.` call carries one, no answer may, the hub stores none in a binding,
   and the audit and the logs name ids and outcomes only.
5. **An action is a request, the state is the truth.** `cloud.action` answers `done`, `accepted` or `refused`. There
   are no jobs: what happens next is read from the instance's state. A lost answer costs nothing, since the next read
   says where the instance is.

**Named types, not strings.** The schemas use the generator's opt-in typed keywords (`schema/wire/README.md`, "Typed
keywords (opt-in)"), so every client gets the same closed sets and ids: the closed enums `CloudSafety`,
`CloudErrorCode`, `CloudInstanceState`, `CloudActionKind`, `CloudActionStatus`, `CloudSnapshotState` and
`CloudRecoveryPhase` (Go: a named string type with its constants, the ordered list and `Valid()`; TypeScript: the closed
union); the ids `CloudAccountID`, `CloudInstanceID`, `CloudSnapshotID`, `CloudRequestID`, `CloudMachineID`,
`CloudProviderID` and `CloudCustomActionID` (Go: a named string type; TypeScript: a branded string with a constructor
such as `cloudInstanceID(raw)`); and `CloudMessage` for the error fields' text. That is why the new contract adds no
bare string enum, id or error, and `scripts/ci/wire_types_ratchet.py` needs no exception for it.

## The contribution

```json
"contributes": {"cloud_hosts": [{
  "id": "main",
  "name": "Example Hosts",
  "vendor": "examplehosts",
  "custom_actions": [
    {"id": "renew", "title": "Renew", "safety": "confirm", "confirm": "Renew this server for another term? Your account is charged."},
    {"id": "probe", "title": "Keep-alive check", "safety": "read"}
  ]
}]}
```

- `id`: the contribution's id (`a-z0-9_-`). `name` (1–40 characters): what the apps call the provider.
- `vendor`: a short lowercase id (`aws`, `gcp`, `oci`, a panel's own name). Several contributions may share one.
- `custom_actions` (optional, at most 8, each id once): `title` (1–40 characters), `safety` (`read`, `confirm` or
  `destructive`) and `confirm`, the sentence the confirmation asks, required unless `safety` is `read`.
- The program must speak the protocol (`runtime` and `engines.extension_protocol`). The manager asks it only while the
  extension is enabled, without a problem, with the contribution switched on and its program running.
- The install page shows one permission line for the point: it can list the servers of the accounts you set it up with
  and act on them, and Persona asks it to change one only after you confirmed.
- A provider's vendor gives it no network access: a sandboxed program reaches only `permissions.network`, so declare
  the vendor's API hosts there.

One extension may contribute several providers (one per vendor), and one provider answers for several accounts: an
instance is named by `(account, id)`, where `account` is the provider's own name for a set of credentials (a profile, a
project, a panel login) and never a secret.

## Instances

`CloudInstance` is what a provider says of one instance:

| Field | Meaning |
| --- | --- |
| `account`, `id` | Its name: both opaque to the hub, kept as given. Required. |
| `display_name` | What people call it. |
| `state` | `running`, `stopped`, `starting`, `stopping`, `rebooting`, `preempted`, `terminated` or `unknown`. Required. |
| `state_detail` | One line more about the state, in the caller's language. |
| `region`, `zone` | Where it is, in the vendor's words. |
| `public_ips`, `private_ips` | Plain IPv4 or IPv6 addresses, the one to reach it by first. |
| `spec` | `size` (the vendor's name for the size), `cpu`, `memory_mb`, `disk_gb`, `arch`, `os`. |
| `lifecycle` | `spot` (the vendor may take it back), `expires_at`, `renews_at`, `keepalive_at`, `auto_renew`. |
| `console_url` | An `https` address of its page at the vendor, without credentials. |
| `actions` | What can be asked of this instance now (the standard kinds below, never `custom`). |
| `custom_actions` | The ids of the manifest's custom actions that apply to it. |
| `observed_at` | When the provider read it from the vendor. |

Mapping a vendor's states: anything on its way up is `starting`, on its way down `stopping`; a spot or preemptible
instance the vendor stopped is `preempted` (not `stopped`), so the apps can say why it is off; deleted or being deleted
is `terminated`; `unknown` only when the vendor's answer cannot be read.

`actions` is per instance and per moment: a stopped instance offers `start`, a running one `stop` and `reboot`. The
apps draw exactly these, so a provider that leaves an action out hides its button.

## Actions and safety levels

| Action | Safety | What it asks |
| --- | --- | --- |
| `start` | `confirm` | Power the instance on (also a preempted spot instance). |
| `stop` | `confirm` | Shut it down gracefully. |
| `reboot` | `confirm` | Restart it gracefully. |
| `hard_reset` | `destructive` | Reset it without shutting the system down. |
| `force_stop` | `destructive` | Power it off without shutting the system down. |
| `sync_ip` | `read` | Read its addresses back from the vendor now. |
| `snapshot_list` | `read` | Its snapshots (`cloud.snapshots`; never sent as `cloud.action`). |
| `snapshot_create` | `confirm` | Take a snapshot, optionally named. |
| `console` | `read` | A link to its page or console (may be signed and short-lived). |
| `custom` | the manifest's | One of the manifest's `custom_actions`, by id. |

The table is `extension.CloudActionSafety`. The gates:

| Safety | From the apps (`POST …/cloud/actions`) | From the assistant (`cloud_host_action`) |
| --- | --- | --- |
| `read` | Nothing. | Nothing. |
| `confirm` | `confirm: true`: a person agreed to exactly this action. Otherwise 400 `confirmation_required`. | The user approves a card on their phone naming the action, the computer and the instance. |
| `destructive` | `confirm: true` and `acknowledge` equal to the instance's id. Otherwise 400 `acknowledgement_required`. | The same card, worded as destructive. |

The clients turn the gate into interface: a `confirm` action opens a dialog that names the computer and what will
happen; a `destructive` one a dialog in the danger tone that says what may be lost. One exception is deliberate: the
offline sheet's **Start and reconnect** is a single, specifically labelled press on a computer that is already down,
and that press is the confirmation for `start`. Nothing else is one press.

Before the gate, the request itself is checked: it must hold to `CloudActionRequest`, name a kind other than
`snapshot_list` (snapshots are read with `GET …/cloud/snapshots`), carry `id` exactly when the action is `custom`,
`reconnect` only with `start`, `name` only with `snapshot_create`, and no control characters in `id` or `name`;
otherwise 400 `bad_request`. A custom action the provider's manifest does not declare is 422 `unsupported`, also
before the gate.

Before the gate's verdict, a fresh `cloud.instance` says what the instance offers now, and an action it does not offer
is 422 `unsupported` (so the assistant puts no approval card up for something that would be refused). Every write
that passed the gate is audited (below), whether it was done, accepted, refused or failed, and runs on to its answer
(within its bound) when the client that asked goes away. A request stopped at the gate asked the program for that
read only, never `cloud.action`, and writes no audit row.

## Methods

Namespace `cloud.`, types in `schema/extension/cloud.schema.json`. Only the manager calls them; no other extension,
device or token can. Every params object carries `contribution` and `locale`.

| Method | Parameters → answer | Bound | Notes |
| --- | --- | --- | --- |
| `cloud.instances` | `{refresh?}` → `CloudInstanceList` `{instances, partial?, detail?}` | 20 s | Every instance of every account, at most 500. `partial` when an account could not be read. |
| `cloud.instance` | `{instance: {account, id}, refresh?}` → `CloudInstance` | 15 s | One instance now; `not_found` when it is gone. The answer must be that account and id. |
| `cloud.action` | `{instance, action, custom?, name?, request_id}` → `CloudActionResult` `{status, instance?, snapshot?, url?, expires_at?, message?}` | 30 s | The manager makes a new `request_id` for every request it passes on; a provider treats it as an idempotency key (the same id again: answer what was answered). `sync_ip` answers `instance`; `console` answers `url`; `snapshot_create` answers `snapshot`. |
| `cloud.snapshots` (optional) | `{instance}` → `CloudSnapshotList` `{snapshots}` | 15 s | Asked only of an instance whose `actions` name `snapshot_list`. At most 200, newest first. |

Errors a provider answers with a code (`CloudErrorCode`): `not_found`, `not_signed_in`, `unsupported`,
`invalid_state`, `rate_limited`, `capacity`, `refused` and `failed`, each with a one-line message for people. The
routes pass the first seven on under the same code, with the provider's one line as `detail`; `failed` and anything
else is `plugin_failed` with the program's words as `detail` (one line, clipped). `unknown_method` and
`no_contribution` are `plugin_failed` that says the extension does not provide it; a program that is not running is
`plugin_failed` too, and one that does not answer within the bound is 504 `deadline`.

**Re-checks.** Every answer is held to the schema (an answer that breaks it, or a re-check below that fails the whole
answer, is 502 `plugin_failed`; the log names the definition and the path, never the value), then: an instance's
`account` and `id` are the ones asked for (from `cloud.instance`; another instance fails the answer); a list of more
than 500 instances or 200 snapshots fails, and a second entry for the same instance is dropped; an address that is
not a plain IP is dropped; a `console_url` or `url` that is not `https` with a host and without credentials is
dropped, and `console` without such a link or `sync_ip` without the instance asked for fails; `actions` outside the
standard kinds and `custom_actions` the manifest does not declare are dropped; text is put on one line and clipped. An
action the instance's own `actions` (or `custom_actions`) do not offer is refused before the program is asked (422
`unsupported`, audited as refused when the request passed the gate), read from a fresh `cloud.instance`; a `refused` answer is 422 `refused` with the
provider's message as `detail`.

## Binding a computer to an instance

A binding is `machine_id ↔ (extension, provider, account, instance)`, at most one per computer and one computer per
instance. It is the user's statement that this computer runs on that instance; nothing is guessed. The bindings are
one row of the manager's settings in Postgres (`machine_cloud_bindings`), with what the hub last read of each instance
(`provider_name`, `vendor`, `display_name`, `state`, `public_ip`, `seen_at`) so the computers' list draws a badge
without asking a program.

- Binding asks the provider for the instance first (`cloud.instance` with `refresh`): an instance it does not answer
  cannot be bound. Binding a computer again replaces its binding; either way a start-and-reconnect still running for
  it stops.
- A binding survives its extension being switched off or failing: `GET …/cloud` answers the binding with a `problem`
  (409 `provider_off`, 502 `plugin_failed`, …) and the computer's page says why and offers only Unlink. Uninstalling the
  extension leaves the binding too (its problem is `not_found`; a reinstall finds it); the user removes it. A binding
  keeps the extension id it was made with and is matched through a later rename.
- A computer removed from the hub is dropped from the bindings the next time they are read (not while the devices list
  no computer at all, which may only mean they have not loaded yet).
- Each read of a bound instance (the list, the computer's page, an action's answer) refreshes what the binding last
  had, keeping the last public address when the read has none (a stopped instance usually has none).

## Start and reconnect

The case this is for: a spot instance was taken back, the computer shows offline, and the user wants it back with
one press. `POST …/cloud/actions {action: "start", confirm: true, reconnect: true}` asks the program to start the
instance and, once it accepted, the manager follows up on its own (`CloudRecovery`):

1. `starting`: it reads `cloud.instance` every 5 seconds until the state is `running` (at most 5 minutes; an instance
   that reads `terminated` ends it at once).
2. `reconnecting`: it asks the devices to connect the computer's node again, as the computer's own Reconnect does
   (again every 20 seconds), and looks every 2 seconds whether it is online, for up to 2 minutes while the system
   boots.
3. `done`, or `failed` with one line saying what stopped it. The instance may be running even then; Reconnect on the
   computer's page still works.

A second start-and-reconnect while one is still starting or reconnecting is still passed on as a `start`, but its
`recovery` is the one already running; no second follow-up begins. Unbinding or rebinding the computer stops it.

`ip_changed` says the instance came back with another public address than the binding last had. A computer that
connects to the hub by itself (Persona Link) does not care; one the hub reaches by an address in its own configuration
does, and the apps say so: the hub never rewrites the user's connection settings.

The recovery lives in the manager's memory and is read with `GET …/cloud`. A restart of the manager forgets it, and
nothing breaks: the instance's state says where it is, and Reconnect remains.

## Routes for the apps

For a device with full credentials only; an extension's token or an answer-only device gets 403 `read_only`. Errors
are `CloudError` `{error, code, safety?, detail?, machine_id?}`, the error in the reader's language.

| Method | Path | Answer |
| --- | --- | --- |
| GET | `/api/v1/extensions/-/cloud` | `CloudProviders`: the enabled providers. No program is asked. |
| GET | `/api/v1/extensions/-/cloud/instances?refresh=1` | `CloudHosts` `{hosts, problems, partial?}`: every provider's instances, each with its actions and safety levels and the computer bound to it. A provider that failed is in `problems`; the others are still listed. |
| GET | `/api/v1/extensions/-/cloud/bindings` | `CloudBindings`: every binding with what was last read. No program is asked. |
| GET | `/api/v1/machines/{mid}/cloud?refresh=1` | `CloudMachine` `{bound, binding?, host?, problem?, recovery?}`. A bound computer's provider is asked `cloud.instance`; when it cannot be asked or fails, the answer is still 200 with the binding and the `problem`. |
| PUT | `/api/v1/machines/{mid}/cloud` | `CloudBindRequest` `{extension, provider, account, instance}` → `CloudMachine`. 409 `already_bound` (with `machine_id`) when another computer holds the instance. |
| DELETE | `/api/v1/machines/{mid}/cloud` | Unbind → `CloudMachine` `{bound: false}`. |
| POST | `/api/v1/machines/{mid}/cloud/actions` | `CloudActionRequest` `{action, id?, confirm?, acknowledge?, name?, reconnect?}` → 200 `CloudActionOutcome` (`done`) or 202 (`accepted`). |
| GET | `/api/v1/machines/{mid}/cloud/snapshots` | `CloudSnapshots`, newest first; 422 `unsupported` when the instance does not offer `snapshot_list`. |
| GET | `/api/v1/machines/{mid}/cloud/audit?limit=` | `CloudAudit`: what was asked of this computer's instances, newest first; `limit` 50 by default, at most 200, 400 `bad_request` when it is not a positive number. |

A body (PUT and POST) is at most 16 KiB, held to its schema definition, and may not carry fields the definition does
not name: otherwise 400 `bad_request`.

Error codes: 400 `bad_request`, `confirmation_required`, `acknowledgement_required` (the last two with the action's
`safety`); 403 `read_only`; 404 `not_found` (no such computer, provider or instance), `not_bound`; 409
`already_bound`, `invalid_state`, `provider_off`; 422 `unsupported`, `refused`; 401-like states of the vendor as 409
`not_signed_in`; 429 `rate_limited`; 503 `capacity`, `no_database` (no database, or the bindings could not be read or
written); 502 `plugin_failed`; 504 `deadline`.

An older hub answers 404 to all of them, which the apps read as "no cloud hosts".

## Audit

Binds, unbinds and every action that is not `read` are written to the extensions' audit (`plugin_audit`, read back by
`GET /api/v1/extensions/-/audit` and, per computer, by `…/cloud/audit`): the events `cloud_bind`, `cloud_unbind` and
`cloud_action`, with the actor (the device, or `assistant` for a tool call a person approved) and a detail of
`{machine_id, provider, instance, action, safety, outcome}` (`action` and `safety` only for `cloud_action`; a custom
action is written `custom:<id>`). `outcome` is `done`, `accepted`, `refused` (the provider refused, or the instance
did not offer the action or was in the wrong state) or `failed`. `read` actions are not written.
No address, no provider's words and nothing of a credential is written. The manager's log says the same in one line.

## The assistant's tools

Two tools, offered only to the assistant's conversations and only while a cloud provider is enabled:

- `cloud_hosts {machine?, refresh?}`: the hosts and the computers bound to them (each named), or one computer's host.
  Reads only. `machine` is a computer's id or name.
- `cloud_host_action {machine, action, id?, name?, reconnect?}`: an action on the instance a computer is bound to,
  through the same function as `POST …/cloud/actions` (the same request checks, gate, re-checks and audit, with the
  actor `assistant`). A `read` action runs at once (`snapshot_list` answers the snapshots). A `confirm` or
  `destructive` one stops at the gate first and then puts an approval card on the user's phone, in the user's
  language, that names the action, the computer and the instance (and a custom action's own sentence, and that the
  computer is reconnected after a start-and-reconnect); declined or unanswered, nothing is asked of the program.
  Allowed, the action is asked again with `confirm` (and, when destructive, the instance's id as `acknowledge`) and
  the binding the card named: a computer bound to another instance meanwhile is not acted on (400 `bad_request`).
  `reconnect` with `start` runs the start-and-reconnect above.

The assistant cannot bind or unbind: which computer is which instance stays the user's own statement.

## The web

- **A computer's page** (`MachineDetail`, `CloudSection`): a "Cloud server" section, drawn only when the computer has
  a binding or a provider is enabled. Unbound: one line and **Link a cloud server…**, which reads the instances
  (`GET …/-/cloud/instances`) and lists the unbound ones. Bound (`GET …/machines/{mid}/cloud`): the provider and
  vendor, the state as a plain word, the address, place and spec (CPUs, memory, disk), the lifecycle notes (a spot
  instance, the end of what was paid for, the next renewal, the activity rule), Refresh (with `refresh=1`), and the
  actions the instance offers: Start, Shut down, Restart in the row; Force restart, Power off, Snapshots, custom
  actions, Open console and Unlink in the menu. A `read` action runs at once; a `confirm` one opens a dialog naming the
  computer; a `destructive` one a dialog in the danger tone that says what may be lost and sends the instance's id
  back. The row shows progress and the outcome. The section reads the host again after a write and every 4 seconds
  while the instance is on its way or a recovery runs, not while the page is hidden and for at most 8 minutes. With a
  `problem` (the provider off or failing) it shows the provider's words and only Unlink.
- **The offline sheet** (`AlertOffline`, `CloudOffline`): only for a bound computer, it reads the computer's host when
  the sheet opens; when the host offers `start` and has no problem, **Start and reconnect** is the first action, with
  the recovery's phase under it, read again every 3 seconds until it ends (while the page is shown, for at most 8
  minutes). Without a binding the sheet is as before.
- **The computers' list** (`Machines`): a bound computer shows a small vendor badge with the state the binding last
  had; while a provider is enabled, a quiet line under the cards (`CloudUnbound`, no count) opens the instances no
  computer is bound to, each with **Link…**, which asks which of the computers not linked yet runs on it.

Loading the list asks `GET …/-/cloud` and `GET …/-/cloud/bindings` once (no program is asked; the list and the
computer's page share one store of both); the instances are read only when the unbound line or the link picker is
opened, and an instance's state only when a computer's page or the offline sheet shows it. Any failure to read the
providers or the bindings (an older hub's 404 included) reads as no cloud hosts, and nothing is drawn.

## Writing a provider (Part 2)

A provider is an ordinary extension with a program. In the Go SDK:

```go
ext.Methods{
    "instances": ext.Method(func(ctx context.Context, p wire.CloudInstancesParams) (wire.CloudInstanceList, error) { … }),
    "instance":  ext.Method(func(ctx context.Context, p wire.CloudInstanceParams) (wire.CloudInstance, error) { … }),
    "action":    ext.Method(func(ctx context.Context, p wire.CloudActionParams) (wire.CloudActionResult, error) { … }),
    "snapshots": ext.Method(func(ctx context.Context, p wire.CloudSnapshotsParams) (wire.CloudSnapshotList, error) { … }),
}.For("main") // registered under ext.CloudNamespace
```

Keep the vendor's credentials in the manifest's `secrets` (or the extension's settings) and read them with the SDK's
secret calls; never put one in an answer. Answer a vendor's error with the matching `CloudErrorCode` and a one-line
message. Treat `request_id` as an idempotency key for `cloud.action`.

How each kind of vendor fits:

| Provider | `account` | States | Typical `actions` | Notes |
| --- | --- | --- | --- | --- |
| A hyperscaler's virtual machines (EC2-style) | A credentials profile, or profile and region | pending → `starting`; stopping, shutting-down → `stopping`; a spot interruption → `preempted` | `start`, `stop`, `reboot`, `force_stop`, `sync_ip`, `snapshot_list`, `snapshot_create`, `console` | An address changes on every start unless it is reserved: answer `sync_ip` from the vendor, not from a cache. `capacity` when a spot request cannot be placed. |
| A project-based cloud with preemptible instances | The project | provisioning, staging → `starting`; suspended or terminated after a preemption → `preempted`; plain terminated (stopped) → `stopped` | `start`, `stop`, `hard_reset`, `sync_ip`, `snapshot_list`, `snapshot_create`, `console` | Set `lifecycle.spot`. This is the start-and-reconnect case: `start` answers `accepted`, and `cloud.instance` says `running` once it is. |
| A cloud with tenancies and always-free shapes | The tenancy and compartment | The vendor's lifecycle words mapped one to one | `start`, `stop`, `reboot`, `hard_reset`, `force_stop`, `sync_ip`, `console` | Set `lifecycle.keepalive_at` when the free tier reclaims idle instances, and offer a `read` custom action that probes it. |
| A hosting panel for small servers | The panel login | Usually only online and offline: `running`, `stopped`; `unknown` when the panel does not say | `start`, `stop`, `reboot`, `hard_reset`, `console` | Set `lifecycle.expires_at` / `renews_at` and declare `renew` as a `confirm` custom action. Panels without an API for an action leave it out of `actions`; `console_url` still opens the panel. |

Test a provider with the SDK's fake hub (`exttest.Start`) and hold every answer to the schema with
`wire.ValidateResult("cloud.instance", raw)`. The manager's own end-to-end test with a mock provider is
`server/internal/extmgr/extension_cloud_test.go`.

## Not in this version

- Creating, resizing or deleting instances, restoring or deleting snapshots, firewall rules and billing.
- Rewriting how the hub reaches a computer when its address changed (it is reported, not acted on).
- The Apple, Android and HarmonyOS clients: the types are generated for them, the interface is the web's for now.
- Events: a binding changed on one client is read by another when it next opens the page.
