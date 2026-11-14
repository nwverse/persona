# Native production-HAP UI fixture

This small loopback TLS backend supplies synthetic data for manual UI regression checks against a production Harmony HAP. It is **not an actual Codex session, a production Hub, or real user data**. It makes no external service calls. The transport framing and certificate-creation helper come from `../native-transport-fixture.py`; this fixture keeps its own TLS key so a restart with the same runtime directory preserves the pairing pin.

Run it from the repository with a private runtime directory outside the checkout:

```sh
fixture_runtime="$(mktemp -d)"
chmod 700 "$fixture_runtime"
python3 harmony/tests/native-ui-fixture.py --runtime "$fixture_runtime" --scenario normal
```

Keep the fixture running in this host terminal. In another terminal, connect the device's loopback port to the host fixture port with HDC reverse forwarding:

```sh
hdc rport tcp:38663 tcp:38663
hdc fport ls
```

If more than one device is connected, add `-t '<serial>'` to both commands. On the device, open the app's Hub pairing screen and paste the `pair_url` value from `$fixture_runtime/descriptor.json`. Pairing through the UI is important: the URI contains `&` separators, which a shell treats as command separators if the value is pasted into an unquoted CLI start argument.

The descriptor includes the synthetic pairing token and certificate pin. Keep the runtime directory private and do not commit or share it; it contains the TLS key, pairing descriptor, state, and request journal. A controlled restart can reuse the same directory and pairing pin. After finishing, stop the fixture with Ctrl+C, remove the reverse forwarding task, and delete the runtime directory:

```sh
hdc fport rm 'tcp:38663 tcp:38663'
hdc fport ls
rm -rf "$fixture_runtime"
```

If more than one device is connected, use `hdc -t '<serial>' fport rm 'tcp:38663 tcp:38663'`. The `fport ls` output should no longer include the fixture port.

Scenarios are `normal` (no active question), `question` (one open `ask_user` question), `pending` (chat sends return HTTP 202 pending), and `failed` (retry of the failed synthetic receipt returns a fixture error). Every scenario includes a synthetic open plan, one pending Inbox review item, and done, stopped, failed, and interrupted receipt examples. Opening the Inbox item and confirming its handoff marks only that synthetic item done and returns the synthetic main session; it does not start a real agent or contact any service.

The plan card's start request accepts the UI action but returns the canned plan. Edited task fields in that request are deliberately ignored. Use this fixture to check the plan editor UI and that confirmation sends one POST; it does not verify that edits persist or start work.

The fixture creates `control.json`, `state.json`, and `requests.jsonl` in the private runtime directory. It rereads `control.json` for each request; set `scenario` to one of the four names and use `receipts` to resolve a pending operation by its id from `requests.jsonl`, for example:

```json
{"scenario":"pending","receipts":{"OPERATION_ID":"done"}}
```

The journal records request counts, route names, status codes, and operation ids. It never writes request bodies. The fixture replays a stored response for a repeated operation id to make UI checks deterministic; this local behavior does not establish exactly-once guarantees for a real Hub.
