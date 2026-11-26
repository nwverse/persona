# Quick notes

The sample of `docs/extensions/core-only-features.md`: a feature added to a Persona Core install without changing
Core. It is an ordinary extension, installed onto a running core-only hub.

| It adds | Where |
| --- | --- |
| A page on the main navigation, **Quick notes** (en, zh-Hans, ja) | `persona-extension.json` (`ui`), `page.go`, `text.go`, `l10n/` |
| An app-facing route, `GET` and `POST /api/v1/quicknotes/notes` | `persona-extension.json` (`api`), `api.go` |
| A hub feature flag, `quicknotes` | `persona-extension.json` (`api.features`) |
| A stored value, `notes.json` in its data directory | `notes.go` |

Switched off (`personad extension disable quicknotes`), the page, the route and the flag are gone and `notes.json`
stays as it is; switched on again, the same notes are back.

```sh
go test ./...                                    # its tests and the SDK's conformance check
personad extension test                          # the hub's checks, no hub needed
personad extension pack                          # dist/quicknotes-0.1.0.tar.gz
personad extension install "$PWD/dist/quicknotes-0.1.0.tar.gz" --yes
```

On a test hub, say which hub every time: `env -i HOME=<test home> PERSONA_HOME=<test home>/.persona PATH="$PATH"
personad extension …` (an inherited `PERSONA_HOME` otherwise sends the command to the machine's own hub).
`python3 scripts/ci/core-sample.py` runs all of this on a core-only hub in a temporary home.

It builds against the SDK beside it (`../../go`); an extension of your own starts from `personad extension new`,
which writes a copy of the SDK next to your code.
