# Mac acceptance manual for the desktop app

This is the checklist for verifying the Persona desktop app **on a real Mac, by a person**, for
[#6770](https://github.com/nwverse/persona/issues/6770). The automated smoke and hosted-runner evidence
(`desktop.md`, "Acceptance evidence") stop at the main-process boundary; this manual is the missing physical-Mac
half. Work top to bottom. Every step says what to do, what should happen, what to capture, and which #6770
acceptance criterion it serves.

**Scope and honesty rules**

- Signing and notarization are **skipped**: there is no Developer ID certificate yet. You will test the **unsigned
  development package** from CI. Anything an unsigned package cannot prove (signed installation, Gatekeeper's
  notarized verdict, a published auto-update) stays unverified — record it, do not work around it.
- Windows is deferred. Do not infer Windows results from this manual.
- Use placeholders everywhere in your notes: `<hub address>`, `me@example.com`. Never paste real tokens, pairing
  links, or personal messages into an issue or PR.
- If a step fails, capture the failure exactly as it appears (screenshot + what you did), and keep going to the
  next section if the app still runs. A failed step is evidence, not something to hide.
- Known blocker to watch for: on an **unsigned/ad-hoc-signed** build the hub window may never finish loading,
  because Electron's cookie encryption (like `safeStorage`) needs a consistent valid signature on macOS
  (draft PR #7561, "macOS root cause narrowed"). If you hit this, record it as the **signing blocker** — do not
  disable fuses, do not strip signatures, do not switch to a different app ID to make it "pass".

Suggested evidence layout, one folder per run:

```text
mac-acceptance-<date>/
  00-environment.txt      # macOS version, Mac model, the CI run URL you downloaded from
  01-install/ … 08-screen-recording/
```

Record in `00-environment.txt`: macOS version, chip (Apple silicon / Intel), the exact artifact file names, and
whether this Mac has ever had Persona or Persona Link installed before (a clean Mac and an upgrade are different
runs; prefer a Mac that has never had it for the first run).

## 1. Get the package and get past Gatekeeper

The development workflow builds unsigned packages on a macOS runner and uploads them as the
`persona-desktop-macos-unsigned` artifact (the universal DMG and ZIP). Signed packages do not exist yet.

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 1.1 | Download `persona-desktop-macos-unsigned` from a recent Desktop CI run (Actions › the run › Artifacts), or with `gh run download <run-id> -n persona-desktop-macos-unsigned -D <new empty folder>`. Unzip it. | A `.dmg` and a `.zip`, universal (x86_64 + arm64). | `00-environment.txt`: run URL, file names, byte sizes. | Install on macOS |
| 1.2 | Open the DMG, drag **Persona** to `/Applications`. First launch: double-click it. | macOS refuses: a dialog that the app is from an unidentified developer / "cannot be opened because Apple cannot check it for malicious software". This is expected for an unsigned package. | Screenshot of the exact dialog wording. | Install on macOS |
| 1.3 | Right-click (Control-click) the app in Finder › **Open**, then **Open** in the dialog. | The same warning now offers an **Open** button; the app launches. If it still will not open, in Terminal run `xattr -dr com.apple.quarantine "/Applications/Persona.app"` on the downloaded copy and try 1.3 again. Record which route you needed. | Screenshot of the first window you see (the local setup page, "Welcome to Persona"). | Install on macOS |

What this proves: the app installs and starts on a real Mac. What it does not: anything about signed distribution
or notarization. If 1.3 shows an immediate crash or a blank window, stop and file that with the screenshot.

## 2. First run and sign-in (root path and sub-path)

Use a hub you can create test data on, not a production hub full of private conversations. You need its address
(`<hub address>`, for example `https://hub.example.com`) and a way to sign in (the existing web sign-in: pairing
link or account, whichever your hub offers). The second part needs a hub served under a path prefix, for example
`https://hub.example.com/persona` — if you have no sub-path hub, mark that part "not tested" rather than skipping
it silently.

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 2.1 | On the setup page, enter `<hub address>` (root path) and **Continue**. | The hub's own sign-in page loads in the window — the same page a browser shows, no Electron-specific login. | Screenshot of the sign-in page. | Sign in to a root-path hub |
| 2.2 | Sign in as you would in a browser. | You land on the main chat. No blank window, no endless spinner. If the window never loads on this unsigned build, that is the known signing blocker above: screenshot it, note "unsigned full-hub blocked (#7561)", and continue with what still works. | Screenshot of the main chat after sign-in. | Sign in; main flows |
| 2.3 | Sign out or use the local options window (Settings › General › **This computer**) to enter a different hub: `<hub address>/persona` (the sub-path one). **Continue** there. | The old hub's window closes, a new window opens on the sub-path hub, and its sign-in page loads. Links and API calls stay under `/persona`. | Screenshots before and after the switch; the sub-path hub's main chat after signing in. | Sign in to a sub-path hub |

## 3. Main flows

Still signed in. These are the existing web screens; you are checking they work inside the shell, not re-testing
the web app itself.

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 3.1 | Send a message in the main chat and wait for the reply. | The reply streams in; scrolling, markdown and code blocks render as on the web. | One screenshot of the conversation. | Chat works |
| 3.2 | Open **Agents**, start or open an Agent session on one of your computers. | The session opens, its output streams, and its status is visible. | Screenshot of the Agents list and one open session. | Agents work |
| 3.3 | Open **Inbox** and the to-dos. | Items load; opening one works. | One screenshot each. | Inbox and to-dos work |
| 3.4 | Make an Agent ask for approval (run something that needs a tool approval or an answer). | A waiting card appears with working buttons; answering it lets the Agent continue. | Screenshot of the card before answering; note what happened after. | Approval cards work |

## 4. Window closed: notification, click-through, Dock badge

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 4.1 | Close the window (Cmd-W or the red light). The app keeps running in the menu bar / Dock. | The app does not quit; the tray icon stays. | — | Close-to-background |
| 4.2 | From another device, make a session need you (an approval card or a question). | A **macOS system notification** appears with the session's title. The Dock icon shows a badge with the same Needs-you count the web shows. | Screenshot of the notification as delivered by macOS, and of the Dock badge. | Closed-window notification + badge |
| 4.3 | Click the system notification. | The Persona window comes forward **on that session**, not on a home page. | Screenshot of the window it opened. | Click opens the session |
| 4.4 | If macOS never shows notifications: open System Settings › Notifications › Persona and allow them, then repeat 4.2. | The permission prompt or setting is honored; no duplicate notifications for one item. | Note whether you had to grant permission manually. | Notification permission |

What this proves that CI cannot: the OS notification center actually displays and delivers a click, and the Dock
badge is really drawn. The smoke only reads the count the shell received.

## 5. Lock / unlock, `register_desktop`, and Phone Muted

You need your iPhone with the Persona app, signed in to the same hub, and **Phone Muted** turned on
(Settings on the phone; it suppresses "Needs you" pushes while a desktop is active).

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 5.1 | With the desktop app open and unlocked, make a session need you. | The Mac notifies; the phone stays silent (Phone Muted). | Note the time; screenshot of the Mac notification. | `register_desktop`; Phone Muted while active |
| 5.2 | Lock the Mac (Ctrl-Cmd-Q), wait ~30 s, make another session need you. | The phone **does** get its push now: a locked desktop withdraws its registration instead of swallowing alerts. | Screenshot of the phone's notification; note the delay. | Withdrawal on lock |
| 5.3 | Unlock the Mac, wait ~30 s, make another session need you. | The Mac notifies again and the phone is muted again. | Note what happened on both devices. | Re-registration on unlock |

## 6. Launch at login, tray, updates

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 6.1 | In the local options window (first-run page, or Settings › General › **This computer** after sign-in), turn on **Open Persona when you sign in to this computer**. Log out of macOS and back in. | Persona starts at login. If macOS asks for approval in System Settings › General › Login Items, the setting must say so rather than claim success; approve it there and log out/in once more. | Screenshot of the setting, and of the app running after login. | Launch at login |
| 6.2 | Click the menu-bar tray icon. | A menu with **Open Persona**, **Check for updates**, **Quit Persona**. Open brings the window back; Quit really exits (no icon left). | Screenshot of the menu. | Tray |
| 6.3 | Choose **Check for updates**. | It turns into "Checking for updates…", then either "Persona is up to date" or a failure line, then back to **Check for updates**. An unsigned build has no published feed yet, so record exactly what it says; a download here is not expected. | Screenshot of the menu after the check. | Update UI (not a published update) |

A real published auto-update (old signed version → new version after restart) is **not testable** until signed
releases exist. Leave that row open in #6770.

## 7. Persona Link: pairing, reconnect, service management

Link is a separate install for a Mac whose Agents the hub should reach. If this Mac already runs Link, use that;
otherwise install it first with the [independent Link install guide](../operations/persona-link-install.md) (source build; ad-hoc signed). Do not
expect the desktop app to install Link for you.

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 7.1 | With Link **not installed** (or before installing): open the local options window. | The Persona Link section says Link is not installed and points to the separate guide; pairing/reconnect buttons are disabled. Chat works without it. | Screenshot of that section. | Independent installs |
| 7.2 | Install Link (`./scripts/app/mac-link.sh install`). Reopen the local options window or **Refresh**. | Its redacted status appears: connection state and names only — no `link.json`, no token, no internal addresses. | Screenshot. | Link status in the app |
| 7.3 | In the signed-in web app, **Machines › Add computer**, copy the pairing link, then in the desktop's Link section choose **Pair this computer** and paste it. Confirm on the web screen. | Pairing succeeds; the computer shows up on the hub's Machines page. | Screenshots of the pairing flow and the Machines entry. | Pairing from the app |
| 7.4 | Disconnect the connector from the hub side (stop the hub's connection, or briefly turn off this Mac's network), then use **Reconnect** in the app once the network is back. | The connector reconnects immediately on your request, without you editing `link.json` or running `persona-link reconnect` in a shell. | Before/after status screenshots. | Reconnect from the app |
| 7.5 | Check the background service: quit the desktop app entirely, then on the hub look at this Mac's node. | Link and the resident node keep running with the desktop app closed. `persona-link status` shows the launchd agents `com.persona.link` and `com.persona.node`. | Screenshot of `persona-link status` output (redact any real names). | Service independent of the app |

## 8. Screen Recording permission

Persona Screen (part of the Link package, not of the Electron app) owns this permission.

| Step | Do | Expected | Capture | #6770 item |
| --- | --- | --- | --- | --- |
| 8.1 | Start a live-screen session for this Mac from the hub (Persona Screen). | macOS asks for **Screen Recording** for Persona Screen; after you allow it in System Settings › Privacy & Security › Screen Recording and restart the helper, the live screen works. | Screenshot of the permission prompt and of the working live screen (with synthetic content only). | Screen Recording permission |
| 8.2 | Rebuild/reinstall Link without changing Persona Screen's sources, and start the live screen again. | The grant survives (the helper is reused byte-identically). If you did change its sources or signing, macOS asks once more — that is expected, note it. | Note whether you were asked again. | Permission persistence |

## 9. What stays open after this manual

- **Signed + notarized installation** and everything Gatekeeper does differently for it.
- **A published auto-update** from a real GitHub Release, including macOS's Squirrel.Mac accepting the new
  signature.
- **Windows**: every equivalent step, on a real Windows PC.
- The packaged **first-run** gate is covered by draft PR #7561 and its owner; if your 1.3/2.2 hit the unsigned
  full-hub signing blocker, that is the same boundary, not a new bug to route around.

Report results on #6770 with the evidence folder's screenshots, naming the exact package artifact and run you
tested. Mark each criterion you verified; leave the rest explicitly unverified.
