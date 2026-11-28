# Optional Hub Status widget provider

This small extension is for owners whose real Hub has no published widget. It supplies actual aggregate Hub data for physical watch-complication acceptance. It is optional and is not part of the watch app. Preparing or testing this package does not install it on any Hub.

The program makes only three authenticated reads during each render: `GET /api/v1/machines`, `GET /api/v1/sessions`, and `GET /api/v1/interactions`. It displays their array lengths as Computers, Sessions and Pending. Computers includes every computer returned, including offline ones; Sessions is the returned session count, not a claim that all are working; Pending counts the returned requests, including muted/delegated requests. Scope-based server filtering still applies. It never renders names, IDs, conversation text or request contents, and a failed/malformed read shows Unavailable rather than zero or sample data.

For independent read comparison, the watch's test-only `real_surfaces` probe reports `raw_machines`, `raw_sessions` and `raw_interactions` before client filtering. Its ordinary snapshot counts may be lower: the watch removes quiet/archived/non-live sessions and some interaction kinds from its own interface. A difference from those filtered counts is not by itself an aggregate error. Account for the extension credential's server scope and the projection's cache timestamp when comparing raw values; a cached projection is not a simultaneous snapshot of the later watch read.

The circular complication displays Pending. Rectangular text contains all three counts, and the large projection/detail page displays three metrics. Rendered labels support English, Simplified Chinese and Japanese according to the Hub-supplied locale. The catalog name is the generic English title `Hub Status`.

## Package and permissions

The manifest requests only `agents.read` and `machines.read`. These permissions allow reading Agent/session data and computer data, even though this program emits only aggregate counts. It requests no control, message-sending, Inbox, notification, arbitrary network-host or full-Hub permission. The Hub supplies a fresh scoped credential to the process during initialization; no credential or Hub address belongs in this package.

The package contains self-contained Go executables selected through the manifest's platform map: Linux amd64/arm64 and Android arm64. It requires no installed Node, Go or other language runtime on the Hub. Build cross-compilation is not evidence of execution on every target platform. Container installations must use the platform and filesystem of the running Hub/manager container.

Native Termux and a Linux distribution under proot are different targets. The Android executable is built specifically with `GOOS=android`, `GOARCH=arm64`, no cgo, and Go's default Android PIE mode; it is not a renamed Linux binary. The manager selects the manifest key matching its own Go build platform. Inspect the authenticated admin Overview OS/architecture, or use `go version -m /path/to/the/running/manager` where Go is available. `uname` alone does not distinguish native Android from proot Linux. Execution and the Hub's sandbox support must still be verified on the owner's actual environment; this package changes no sandbox settings.

Widget cache lifetime is the standard **15 minutes**. The Hub owns `expires_at`; this program does not shorten it, inject test values, change the clock, or poll in the background. Foreground rendering uses current Hub responses when the Hub invokes the extension; the Hub may serve a still-valid widget cache instead. Real expiry acceptance must wait for the returned deadline and the face's next redraw.

## Build and local verification

From this directory, with Go 1.24 or newer:

```sh
go test ./...
python package.py --go go --out ../../dist/watch-hub-status-0.1.0.zip
```

The source uses the repository's existing zero-dependency Go extension SDK through `go.mod`; run these commands within this checkout. `package.py` builds each declared platform, writes executable ZIP modes, includes both supported manifest filenames, and prints the archive SHA-256. Only build outputs are placed under the ignored `build/` directory and the requested output path.

Tests use local stub HTTP servers only. They check the standard SDK protocol/UI contract, exact permission set, all three widget families and locales, fresh count changes, exclusion of private contents, malformed/unavailable responses, redirect refusal and missing-scope refusal. They are not real-Hub, installation, or physical-watch acceptance.

## Recorded real-Hub verification

Hub Status `0.1.0` was installed through the authenticated admin Extensions page on a Persona `0.5.3` Hub running Linux ARM64. Its two declared read permissions were reviewed. This establishes execution on that runtime; the other packaged platforms still require independent runtime verification.

A Samsung Galaxy Watch8 running Android 16 / API 36 used its existing answer-role credential over pinned HTTPS. The real widget catalogue and both accessory projections were available. Samsung's actual system editor selected the provider for separate short- and long-text slots, and each slot opened the real read-only detail when tapped.

The displayed aggregates matched independent raw API counts. Those totals were kept distinct from the watch's filtered session/request list and from projections with older cache timestamps. The real projections expired independently at their returned deadlines, then a foreground refresh restored both and reflected changed Hub data. No synthetic counts, altered deadlines or clock changes were used for this observation.

Some ADB captures omitted layers while the physical screen remained complete, so incomplete frames were assessed with complete captures, accessibility information and physical-screen confirmation. Private device logs, screenshots and account-derived totals are not published. See the [watch verification guide](../../QA.md) for the distinction between these physical observations, read-only interface probes and synthetic tests.

These results do not imply that a package upload, successful build, catalogue response or app preview alone proves visible complication rendering. Repeat the system-picker, tap, count-comparison and expiry checks for a new runtime or watch configuration.

## Owner installation and removal

Installing this extension runs a new program on the Hub/manager machine. Use the authenticated admin Extensions page to upload the ZIP, inspect its preview and two read permissions, then confirm installation. Alternatively, copy the ZIP onto the Hub machine/container and use the Hub owner's `personad extension` CLI. The watch's answer-only credential cannot install it. Do not copy a user's full Hub token into the package or widen the watch's permissions.

In the same environment that runs the Hub/manager, using a path visible there:

```sh
personad extension install file:/absolute/path/watch-hub-status-0.1.0.zip
personad extension show watch-hub-status
```

The install command previews permissions and asks for confirmation; do not add `--yes` when reviewing a new package. To remove the optional provider after acceptance:

```sh
personad extension uninstall watch-hub-status
```

After installation, confirm that Hub Status is enabled and running and that `GET /api/v1/extensions/-/widgets` contains `watch-hub-status-counts`. On the watch, refresh Extension widgets and select Hub Status through each real system complication picker. Check both text sizes, tap navigation, and values against independent real-Hub reads. The all-zero state is valid only when those actual reads return empty arrays.

For a temporary acceptance-only installation, remove the provider using the admin Extensions page or owner CLI when testing is complete, unless the owner chooses to keep it. Restore the original watch face and any temporary settings separately. Record installation, removal and physical-face observations independently from these general instructions.

The package follows the repository's [extension runtime, permission and widget contract](../../../docs/extensions/extensions.md).
