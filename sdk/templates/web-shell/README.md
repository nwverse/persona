# Focus 4-Layer Web Shell (Template Extension)

A reference purely declarative `web_shells` extension (#15396) that renders a custom Persona Web/Desktop interface on the Hub origin with zero background processes and zero external runtime dependencies.

## Features

- **4-Layer Sidebar (Scheme A)**
  - **L1 Action**: Today Focus, Inbox / Needs You, Main Chat shortcut.
  - **L2 Work & Sessions**: Unified Agent session list grouped by **Status (default)**, **Project**, or **Machine**, with a background noise toggle that **never hides `needs_you` sessions**.
  - **L3 Context & Extensions**: Collapsible `nav_page` extension links fetched from `GET /api/v1/extensions/-/ui` (shown only when Hub capabilities include extensions) and enabled optional module links gated by `module.<id>`.
  - **L4 Footer Infra**: Online machine counter, same-origin shell switcher (`GET /api/v1/extensions/-/web-shells`), and a permanent one-click **Built-in UI (`?shell=builtin`)** escape hatch.
- **Core-Only Module Awareness**: Reads Hub `features` from `/api/v1/health` and authenticated WebSocket `hello` (supporting both local and public hubs) and only surfaces enabled optional modules (`module.<id>`).
- **Live Hub API Integration**: Reuses the browser's existing `localStorage["persona.token"]` on the same origin to query `/api/v1/machines`, `/api/v1/sessions`, `/api/v1/interactions`, `/api/v1/inbox`, and send messages directly to sessions (preserving drafts and surfacing errors on failed sends).

## Pack & Install

```sh
personad extension pack sdk/templates/web-shell --out /tmp/focus-web-shell
personad extension install /tmp/focus-web-shell/focus-web-shell-0.1.0.tar.gz
```

Then open **Settings › Advanced › Appearance › Web interface shell** in Persona Web/Desktop and pick **Focus 4-Layer**.

At any time, append `?shell=builtin` to any Persona URL to return immediately to the built-in interface.
