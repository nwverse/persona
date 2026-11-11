# Proposal: one vocabulary for icons, state marks, badges and tags

Status: proposal. Tracking issue: #4538. Owner decisions this proposal needs are listed in "Open questions for the owner" at the end; everything else follows decisions already recorded in `docs/design/DECISIONS.md` and the shared tokens in `spec/tokens/`.

Persona draws the same few facts on four clients: this agent needs you, it is working, it finished and you have not looked, N things wait. Each client grew its own marks for them. This proposal takes stock of every icon source and every dot, badge, count and tag; compares them with how mature products do it; and sets one meaning table, one set of components per platform and one set of interaction rules, so a fact looks and behaves the same wherever it appears (principle 4 in `docs/design/PRINCIPLES.md`).

It builds on `docs/design/DESIGN_SYSTEM.md` (sections 2.1, 2.8, 3.8, 6.3 and 8) and on the Android alignment plan (`docs/plans/android-ios-alignment/phase-1-tokens-and-guards.md`: T1 tokens, T5 Needs You color, T7 icons by meaning), and does not repeat them. Where the two overlap, phase 1 owns the token pipeline and the icon meaning map, and this proposal supplies the marks and badges part of both.

## 1. Inventory

Taken on `main` at commit `d27900624d`. Apple counts come from `scripts/app/icon_inventory.py`; web, Android and server counts from searching the sources.

### 1.1 Icon sources

| Platform | Source | How sizes and weights are set | What drifts |
| --- | --- | --- | --- |
| iOS, iPadOS, macOS, visionOS | SF Symbols through `Theme.Icon` (`apple/Shared/Design/Icons.swift`, 389 entries, guarded by `IconVocabularyTests`), one symbol per meaning | text styles through `ThemedIcon` | 121 symbol names and 15 registered names written as literals outside `Theme.Icon`; 20 symbols sized in raw points; 101 dots drawn by hand |
| Web | Persona's own stroke set, `web/src/ui/icons.tsx` (145 glyphs, 24-unit grid, round caps) | `size` and `stroke` at each of 889 call sites | 16 different sizes (9 to 28 px); 12 different stroke widths (1.8 to 3.2) where the default is 1.9 |
| Android | Phosphor, `android/core/designsystem/src/main/kotlin/persona/core/designsystem/icons/PhIcons.kt` (170 glyphs, regular and fill), named by glyph rather than meaning | `JIcon(size =)` at each call site | 14 different sizes (12 to 40 dp); pages pick glyphs directly (phase 1 T7 counts 156 glyphs in 1,139 places) |
| Lark cards (server) | emoji in card Markdown | n/a | the state emoji contradict the apps (MB-09) |

The apps show emoji only where they render third-party content, such as the ⏰ and 📄 blocks of Lark documents. That is content, not interface, and stays.

### 1.2 State marks

A state mark shows one agent's or task's state on a row, a header or an avatar.

| Platform | Component | Needs you | Working | Unread (done, not seen) | Idle or seen | Offline | Waiting in background |
| --- | --- | --- | --- | --- | --- | --- | --- |
| iOS | `StatusDot` (`apple/Persona/DesignSystem/Components.swift`) | blue dot whose ring breathes; a plain dot under Reduce Motion | amber dot with a still ring | green dot | gray dot | faint gray dot | amber clock |
| iOS | three private `mark()` copies in the Assistant sidebar and auto-conversation rows (`apple/Persona/Views/Assistant/AssistantSidebar.swift`, `apple/Persona/Views/Assistant/AutoConversationsView.swift`) | blue dot, still | gray system spinner | **accent dot**, which in the default style is the same `#007AFF` as Needs you | nothing | nothing | nothing |
| iOS | `AgentStateBadge` (BeautifulUI style) | red "!" badge | turning ring | green check | empty ring | gray dot | amber clock |
| Web | `StatusMark` (`web/src/ui/StatusMark.tsx`), the one place states are drawn | **amber** dot with a thin ring (`--status-needs: var(--attention)`) | amber arc that turns | green glowing dot | gray dot | hollow ring | faded amber clock |
| Android | `JStatusMark`, `JStatusDot` (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/StatusComponents.kt`) | Glass: blue glowing dot that pulses. Brief: ringed dot. Board: a hand in a blue square | Glass: **the same glowing dot** in amber. Brief and Board: spinner | Glass: **the same glowing dot** in green. Brief: filled check | Glass: the same dot in gray. Brief: check | gray dot | as Working |

### 1.3 Count badges

| Platform | Component | Looks in use | Cap | Height |
| --- | --- | --- | --- | --- |
| iOS | `CountBadge` | blue text on a neutral fill (`.tone(needsYou)`) in the switch, the Agents rows and the Mac sidebar; white on solid blue (`.solid(needsYou)`) in the Assistant sidebar's groups and visionOS; solid gray (`countBadge`) for Lark and connectors; solid accent for inbox mentions and message groups; tones for unread, idle and extensions | 99+ | `Theme.Size.badge`, 18 pt |
| iOS | `WaitingBadge`, `AssistantWaitingBadge`, `InboxDot` (`apple/Persona/DesignSystem/ShellComponents.swift`) | needs-you tone; the inbox dot is needs-you blue at 65 % opacity | 99+ | 18 pt; the dot 10 pt (`Theme.Size.dotXL`) |
| Web | none shared: about 15 hand-built pills across `web/src/views/` (sidebar rows, tree, rail, shell switch, messages, chat tabs), `Count` in `web/src/views/inbox/InboxParts.tsx` and the menu badge in `web/src/ui/atoms.tsx` | needs-you as amber text on `--field`; unread as white on `--accent` or accent text on `--accent-tint`; neutral as `--ink-2` on `--field` | 99+ in three places and nowhere else; the favicon says 9+ | 16 px and 18 px; text at 10, 10.5 and 11 px |
| Android | `JBadge` (`android/core/designsystem/src/main/kotlin/persona/core/designsystem/Controls.kt`) | **every count in solid needs-you blue**, unread message counts included; the Glass drawer uses the accent instead | 99+ | 18 dp; text 11 sp written as a literal |

`spec/tokens/color.semantic.json` already defines `countBadge`: `#636366` with white text, checked at 4.5:1 by `spec/tokens/rules.json`. iOS uses it. Android maps its `countBadge` role to the needs-you dot instead (`legacy()` in `StatusPalette.kt`), and the web has no such token.

### 1.4 Dot badges and tags

- **Dot badges** say "something here" without a number, on an icon's corner.
  - iOS: `InboxDot` on the persona button, 10 pt (`Theme.Size.dotXL`), with a cut-out ring.
  - Web: drawn by hand at 6 px and 8 px in the chat tabs, the rail, the assistant tabs and `AssistantAvatar`; about half have a cut-out ring.
  - Android: no dot badge component. The state mark `JStatusDot(Status.NeedsYou)` is reused as a corner dot (the Assistant settings button and drawer pages, the manager avatars in `Managers.kt`, the other-agents dot in `android/app/src/main/java/persona/android/ui/Root.kt`), without a cut-out ring.
- **Tags:** iOS `Chip` (neutral, tint, outline) and `LinkBadge`; web `Pill`, `StatePill` and `BADGE_CLASS` in `web/src/views/SidebarParts.tsx`; Android `JTag` (status, tone or hue). All three are text in a rounded rectangle; radius and type size differ, which `docs/design/DESIGN_SYSTEM.md` already tracks as DS-21.

### 1.5 Problems found

| Id | Problem | Where | Effect |
| --- | --- | --- | --- |
| MB-01 | Needs You is amber on the web and blue on iOS and Android, against the 11-11 decision "Needs You is blue on every client" | `--status-needs` in `web/src/styles.css` | the same session reads differently on the web |
| MB-02 | The web's amber `--attention` means Needs you, a caution note, "changes requested", a failed step and an interrupted task | about 45 uses in `web/src/views/` | one color, five meanings |
| MB-03 | On iOS, Unread is drawn in the accent, which in the default style is the Needs You blue | Assistant sidebar, auto conversations | Needs you and Unread look identical |
| MB-04 | iOS sidebar Working is the untinted system spinner; every other Working mark is amber | the same three `mark()` copies | Working changes color by screen |
| MB-05 | The needs-you count has two looks on iOS: a tone in the switch and rows, solid blue in the sidebar and visionOS | `CountBadge` call sites | the same count looks different by screen |
| MB-06 | Android paints every count, unread messages included, in the Needs You blue | `JBadge` call sites | "3 unread messages" reads as "3 need you" |
| MB-07 | The web has no count component: heights of 16 and 18 px, three text sizes, a cap in three places only | `web/src/views/` | a count of 120 overflows its row |
| MB-08 | Android Glass draws Needs you, Working, Unread and Idle as the same glowing dot, told apart only by color | `JStatusMark` | breaks "color is never the only cue" (`docs/design/DESIGN_SYSTEM.md` section 6.3) |
| MB-09 | Lark cards mark Needs you 🔴, Working 🟢 and Unread 🔵, three colors the apps use for other states | `server/internal/assistant/feishu_workbench.go`, `server/internal/assistant/feishu_commands.go` | a state looks different in Lark and in the apps |
| MB-10 | Icon size and stroke are chosen at each call site on the web and Android | section 1.1 | a 13 px glyph at stroke 2.6 sits next to a 14 px one at 1.9 |
| MB-11 | Under Reduce Motion the needs-you dot loses its ring and is told apart by color only | `StatusDot` (HIG-040 in `docs/design/HIG_AUDIT.md`) | as MB-08 |

## 2. What mature products do

Sources were read directly where public; claims marked "unverified" come only from third-party descriptions.

| Product | What they do with badges and marks |
| --- | --- |
| Apple HIG ([tab bars](https://developer.apple.com/design/human-interface-guidelines/tab-bars), [notifications](https://developer.apple.com/design/human-interface-guidelines/notifications), [badgeProminence](https://developer.apple.com/documentation/swiftui/badgeprominence), [SF Symbols](https://developer.apple.com/design/human-interface-guidelines/sf-symbols), [accessibility](https://developer.apple.com/design/human-interface-guidelines/accessibility)) | A badge counts only what is unread or needs action, never totals; zero hides it. Three prominences: decreased for passive totals, standard for unread, increased for required action. Outline symbols in lists, filled ones for emphasis and selection. Pulse and Breathe for ongoing activity, used sparingly, and nothing repeating with Reduce Motion. Never color alone. |
| Material 3 ([badges](https://m3.material.io/components/badges/guidelines), [Compose badges](https://developer.android.com/develop/ui/compose/components/badges), [MDC Badge](https://github.com/material-components/material-components-android/blob/master/docs/components/Badge.md)) | Small badge: a 6 dp dot. Large badge: 16 dp high, at most four characters including "+". It sits on the icon's top-end corner, never over the glyph, and hides once its destination is open. Screen readers get plural strings. |
| Apple Mail, Reminders | A blue dot for unread that goes away when the message is opened; flags and stars for importance, not counts. |
| Linear ([inbox](https://linear.app/docs/inbox.md)) | The inbox badge counts what the user chooses; read is separate from done; snoozed items come back. Each status category has its own shape (unverified: ring, half pie, check). |
| Slack ([notifications](https://slack.com/help/articles/360025446073-Guide-to-Slack-notifications)) | Unread is only a bold name; mentions and direct messages add a red number. Muted channels still badge a mention. |
| Discord ([unread](https://support.discord.com/hc/en-us/articles/209791877)) | A white pill for unread, a red number for mentions; muting hides unread but never mentions. Presence by shape and color. |
| Telegram ([theme keys](https://telegra.ph/Android-Theming-Variables-Glossary-Chats-List-09-29)) | Muted chats' counters turn gray; mentions, replies and reactions get their own badges. |
| GitHub Primer ([state label](https://github.com/primer/design/blob/main/content/components/state-label.mdx), [icons](https://github.com/primer/design/blob/main/content/foundations/icons/design-guidelines.mdx)) | Each state has its own icon shape as well as its color. A counter sits next to text that says what it counts. Octicons come at 16 and 24 px, and 12 only when 16 does not fit. |
| Things 3 ([badge](https://culturedcode.com/things/support/articles/3340494/)), Notion ([inbox](https://www.notion.com/help/updates-and-notifications)), Raycast ([list](https://developers.raycast.com/api-reference/user-interface/list)) | The user chooses what the app badge counts; nothing notifies about the page already open; row metadata is a quiet tag (colored text on its own faint tint), not a badge. |
| WCAG 2.2 ([1.4.1](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color), [1.4.11](https://www.w3.org/WAI/WCAG22/Understanding/non-text-contrast.html), [2.5.8](https://www.w3.org/WAI/WCAG22/Understanding/target-size-minimum.html)) | Never color alone; a status mark without text needs 3:1 against its background; targets at least 24 px. |

Rules that carry over:

1. Four kinds of mark, each with one job:
   - a state mark, coded by shape, on a row or an avatar;
   - a count badge, a number on summary surfaces;
   - a dot badge, "something new" without a number, on an icon;
   - a tag, text metadata.
2. One action color, one "new" color, and gray for everything passive. Status colors live in the marks; row text stays neutral.
3. Shape before color: every state must read in grayscale.
4. A count shows only above zero and caps at a fixed width. Screen readers hear the exact number as part of the host control's label.
5. At most one count badge per row. The state mark keeps its own slot at the row's leading or trailing edge.
6. Reading clears Unread; only answering clears Needs you.
7. At most one looping animation, for one state, and none with reduced motion.
8. Touch targets belong to the host control (44 pt, 48 dp, and at least 24 px on the web), never to the badge.

## 3. The design

### 3.1 Meaning table

One row per fact. Colors are the shared tokens in `spec/tokens/color.status.json` and `spec/tokens/color.semantic.json`; each platform reads them through its own palette (iOS `StatusPalette`, Android `PersonaPalette`, web CSS variables).

| Fact | Token | State mark | Count badge | Motion | Spoken |
| --- | --- | --- | --- | --- | --- |
| Needs you: an agent or task waits for the user's answer | `status.needsYou`: dot `#007AFF`/`#0A84FF`, text `#0064C8`/`#409CFF` | dot with a ring | needs-you tone | the ring breathes on prominent marks, not in dense lists; with reduced motion the ring stays, still | "Needs you"; "3 need you" |
| Working | `status.working` (amber) | an amber arc that turns, or the platform spinner tinted amber; in dense lists a still ring around a dot | none | the arc or spinner turns; slower or still with reduced motion | "Working" |
| Waiting in the background | the working amber at 60 % (iOS `StatusPalette.backgroundWaiting`, web `--status-waiting`); not in `spec/tokens` yet, where `status.waiting` is the loop's blue | clock | none | still | "Waiting in the background" |
| Unread: finished, not yet seen | `status.unread` (green) | solid dot (glowing on the web) | neutral, when a number is shown | still | "Unread" |
| Seen or idle | `status.idle` | gray dot, or nothing in a row whose text names the state | none | still | "Idle" |
| Offline | `status.offline` | hollow ring | none | still | "Offline" |
| Failed or error | `danger` | warning glyph, never a dot | none | still | the error's words |
| Unread messages, totals | `countBadge` | none | neutral | none | "12 unread" |
| Source: Lark, mail, terminal, desktop app | source and brand tokens | leading brand mark or a tag | never | none | the source's name |

What this changes:

- Needs You is blue everywhere. The web's amber `--attention` keeps only its caution meanings (MB-01, MB-02).
- Unread is green everywhere and never the accent (MB-03).
- The accent is blue too, so a blue state mark always carries its ring or its words.

### 3.2 Count badges

Three tones and one component per platform: iOS `CountBadge`, a web `CountBadge` atom, Android `JBadge`.

| Tone | Means | Look | iOS | Web | Android |
| --- | --- | --- | --- | --- | --- |
| needs-you | something waits for the user to act | needs-you text on the neutral status fill. The tokens make `status.needsYou.tint` neutral on purpose: approvals and Needs you sit on neutral surfaces | `.tone(StatusPalette.needsYou)` | `tone="needs"` | `BadgeTone.NeedsYou` |
| neutral | passive counts: unread messages, items in a group | `countBadge` gray with white text | `.solid(Theme.countBadge)` | `tone="neutral"` | `BadgeTone.Count` |
| accent | the selected item's count, or a direct mention where the product already uses it | accent fill with its contrasting text | `.solid(Theme.accent)` | `tone="accent"` | the Glass drawer's existing fill |

- **Cap:** "99+" in every badge inside the apps. The favicon and the app icon may say "9+" where one digit is all that fits. Screen readers always get the exact number.
- **Size:** 18 pt, 18 dp or 18 px high, as wide as its text with a minimum of its height. The web's dense sidebar rows use a 16 px small size. Text is the platform's small bold label with tabular digits.
- **Zero** hides the badge. A row carries at most one count badge.
- **Placement:** trailing in a row; on an icon, at the top-trailing corner, mirrored for right-to-left languages.
- **Focus:** a badge is never its own focus stop or touch target. Its number joins the host control's label ("Agents, 3 need you").

### 3.3 Dot badges

A dot badge says "something here" without a number: the persona button's inbox dot, or a tab or rail icon hiding a needs-you item.

- 8 pt, 8 dp or 8 px on an icon (iOS `Theme.Size.dot`); 10 on a 44-unit face such as the persona button (iOS `Theme.Size.dotXL`). In the color of the fact it stands for (needs-you blue by default), at its host's top-trailing corner.
- Always with a 2-unit ring cut out in the surface color, so it holds 3:1 against any icon or photo behind it.
- It joins its host control's label, as count badges do.

### 3.4 Icons

- **One source per platform, one glyph per meaning:** SF Symbols through `Theme.Icon` on Apple, Persona's stroke set in `web/src/ui/icons.tsx` on the web, Phosphor through `PhIcons` on Android. The meaning-to-glyph map the three share is phase 1's T7; the marks in section 3.1 are its first entries.
- **Outline by default.** A filled glyph means selected (a selected tab) or is a state glyph (a filled check for done). An icon never doubles as a badge.
- **Size steps, not per-call values:** 12 (inside badges and tags only), 14, 16, 20 and 24 on the web and Android; relative to the text style on Apple.
- **Web stroke:** `Icon` sets the stroke from the size (2.2 at 12 and 13 px, 1.9 from 14 px up), and call sites stop passing `stroke`. A heavier stroke is kept for a check or a cross inside a filled mark.
- **Emoji** are never interface marks in the apps. Lark cards can't draw our marks in Markdown, so there the state emoji take the state's color: 🔵 Needs you, 🟠 Working, 🟢 Unread, ⚪ idle (MB-09; open question 3).

### 3.5 Tokens

The token source is `spec/tokens/` in DTCG format; `scripts/assets/gen_tokens.py` generates Kotlin, CSS and a Markdown table from it, and `scripts/assets/test_gen_tokens.py` checks the hand-written Swift values against it (phase 1 T1). It already holds the status colors and `countBadge`. This proposal adds the following, in phase 1's files and through phase 1's checks:

| Token | Value | Replaces |
| --- | --- | --- |
| `size.mark.dot` / `size.mark.dotSmall` | 8 / 6 | iOS `Theme.Size.dot*` (five sizes from 6 to 10), the web's `--mark-dot`, Android literals |
| `size.badge.height` / `size.badge.heightSmall` | 18 / 16 | `Theme.Size.badge`, the web's `h-4` and `h-4.5`, Android `18.dp` |
| `size.badge.dot` / `size.badge.dotLarge` | 8 / 10 | iOS `InboxDot`'s size, the web's hand-drawn corner dots, Android's reused `JStatusDot` |
| `size.badge.cutout` | 2 | the rings around dot badges and avatar marks |
| `status.backgroundWaiting` | the working dot at 60 % | iOS `StatusPalette.backgroundWaiting`, the web's `--status-waiting` |
| `rules.badge.cap` | 99 | each platform's own literal |

The web does not read the generated files yet. Until it does, its CSS variables carry the same values, and a web test compares them with `spec/tokens/color.status.json`.

### 3.6 Interaction and accessibility

- **Tap:** a mark or a badge is never a separate target. Tapping the row, tab or icon opens what the mark points at. On the web, hovering the host shows the mark's words through its `title`.
- **Clearing:**
  - Opening a session clears its Unread on every device.
  - Needs you clears only when its request is answered, declined or expires.
  - A destination's own badge hides while that destination is open.
  - Muting turns unread gray but never hides Needs you.
- **Motion:** one looping animation, the needs-you breath, on a single prominent mark. A count changes with the system's numeric transition. Nothing loops with reduced motion, and the needs-you ring stays drawn, still (MB-11).
- **Contrast:** badge text 4.5:1 on its fill; marks without text 3:1 against the surface. The cut-out ring keeps dot badges legible on any background.
- **Screen readers:** the host control speaks one phrase with the count and the state ("Persona, 2 need you"), and decorative marks inside it are hidden. Android uses plural strings; the web puts the number in the accessible name.

### 3.7 Before and after

Each implementation pull request carries light and dark screenshots before and after. In short:

| Surface | Before | After |
| --- | --- | --- |
| Web sidebar and session list, a session that needs you | amber ringed dot, amber count text | blue ringed dot and blue count text, as on iOS |
| Web counts: sidebar, tree, rail, switch, inbox, menu | hand-built pills at 16 or 18 px, 99+ in some places only | one `CountBadge`, one height per density, 99+ everywhere |
| iOS Assistant sidebar, an unread chat | blue dot, identical to Needs you | green dot, as `StatusDot` and the web |
| iOS Assistant sidebar, a working chat | gray system spinner | amber spinner (`WorkingIndicator`) |
| iOS Assistant sidebar, a group that needs you | white on solid blue | the needs-you tone, as the switch and the Agents rows |
| Android, unread message counts | solid Needs You blue | neutral gray `countBadge`, as on iOS |

## 4. Rollout

Small pull requests: tokens and shared components first, then screens. Each one shows light and dark screenshots before and after.

1. This document.
2. **Web:** Needs You in blue, with `--needs` and `--needs-ink` taken from `spec/tokens`, `--status-needs` pointing at them, and the needs-you uses of `--attention` moved over. One `CountBadge` atom with the three tones and the cap replaces the hand-built pills.
3. **iOS:** one row state mark in the design system replaces the three `mark()` copies: Unread green, Working amber, Needs you as before. Needs-you counts use the needs-you tone everywhere.
4. **Android:** `JBadge` gets tones. Unread and passive counts use the neutral `countBadge`, and the `countBadge` role maps to the shared token.
5. **Later, one pull request each:**
   - the dot badge component on the web and Android;
   - Android Glass state marks told apart by shape (MB-08);
   - the still ring under Reduce Motion on iOS (MB-11);
   - the Lark card emoji (MB-09);
   - icon size steps and the web's stroke rule, with a ratchet (MB-10);
   - the size tokens of section 3.5, through phase 1.

## 5. Open questions for the owner

1. **The neutral count on the web.** iOS and Android draw passive counts as a solid gray pill. The web draws them as gray text on a light fill, which is quieter. Should the web adopt the solid gray, or the apps the web's lighter look? Either way the needs-you count must stay the most visible badge on a screen; where both appear today, the solid gray is heavier than the needs-you tone.
2. **Mentions.** iOS colors an @-mention count with the accent, which in the default style is the Needs You blue. Is a mention a needs-you count with the same tone, or does it keep the accent?
3. **Lark cards.** Lark users read 🔴 as urgent. Should the cards follow the apps (🔵 for Needs you), or keep 🔴 as Lark's own convention?
4. **Working in dense lists.** The web turns an amber arc in every row; iOS keeps a still ring in lists and turns a spinner only in the sidebar. Should every client turn it, or only the focused session's?
