# Web update upstream synchronization evidence

Source: `173e051006da53a65f0fbc040b73acc3caaddacb`, ordinary merge of pinned upstream `cf2e5c03934f0be6e7a8463643561cece14b4277`.
Web source tree: `ac4d2c77343a077839ee2b9d106f547480f2ea23`.

Ten fresh captures of the actual App/Hub settings with synthetic local API data. Wide light uses Standard; narrow dark uses the Web maximum Large. Source, automation, program-only backup, idle wait and interruption-confirmation assertions passed. No uncaught page errors or page-width overflow. All ten captures were visually inspected by the author. Browser Use separately read the rendered source/version controls.

The runner reduces the existing `web/e2e/hub-development.mjs` matrix without changing its assertion bodies. Run the fixture with `PORT=<port> node web/e2e/hub-development-fixture.mjs`, then run the [runner](browser-smoke.mjs) against that owned loopback origin and a disposable output directory, supplying `--executable <chromium>`. See the [source/interaction manifest](capture.json) and [checksums](SHA256SUMS).

The capture-adding commit is immediately followed by removal of these review files, leaving the integrated source tree unchanged. Historical full galleries retain their original source bindings. These author checks do not establish production updates, writer-hook/restoration acceptance, native-host runtime or maintainer visual approval.

## 1440-light-standard-source

![1440-light-standard-source](1440-light-standard-source.png)

## 1440-light-standard-options

![1440-light-standard-options](1440-light-standard-options.png)

## 390-dark-large-source

![390-dark-large-source](390-dark-large-source.png)

## 390-dark-large-options

![390-dark-large-options](390-dark-large-options.png)

## automatic-enabled

![automatic-enabled](automatic-enabled.png)

## release-source

![release-source](release-source.png)

## backup-confirm

![backup-confirm](backup-confirm.png)

## backup-waiting

![backup-waiting](backup-waiting.png)

## interrupt-confirm

![interrupt-confirm](interrupt-confirm.png)

## backup-confirm-narrow-dark-large

![backup-confirm-narrow-dark-large](backup-confirm-narrow-dark-large.png)
