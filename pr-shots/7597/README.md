# Android inbox status-layout evidence

Exactly **60 PNGs** are in this directory. Every final image was opened and visually inspected, and every file family below passes. `{style}` is `glass`, `brief`, or `board`; `{theme}` is `light` or `dark`, giving six images per family and ten families overall.

This is **native Robolectric evidence only**. No physical device or emulator was run, and these captures make no device or emulator rendering, accessibility, or performance claim. All screenshot tests use the fixed clock `2026-01-01T12:00:00Z` in UTC.

| Filename family | Test and content | Language / font scale | Verdict |
| --- | --- | --- | --- |
| `list-en-{style}-{theme}.png` | `InboxShowcaseShotsTest.drawsTheSharedListInEveryLanguageStyleAndAppearance`: shared inbox list | English / 100% | PASS: expected list content and current-state text are visible. |
| `list-{style}-{theme}.png` | Same shared inbox list | zh-Hans / 100% | PASS: expected localized content and current-state text are visible. |
| `detail-folded-en-{style}-{theme}.png` | `InboxShowcaseShotsTest.drawsTheSharedDetailFoldedAndExpandedInEveryLanguageStyleAndAppearance`: folded sc-1 detail | English / 100% | PASS: complete status and offered actions, with no overlap or clipping. |
| `detail-folded-zh-Hans-{style}-{theme}.png` | Same folded sc-1 detail | zh-Hans / 100% | PASS: complete localized status and offered actions, with no overlap or clipping. |
| `detail-expanded-en-{style}-{theme}.png` | Same test: expanded sc-1 detail | English / 100% | PASS: complete who / look-again / next sentence, Conversation and Say something below it, no overlap; summary → recap → timeline remains intact. |
| `detail-expanded-zh-Hans-{style}-{theme}.png` | Same expanded sc-1 detail | zh-Hans / 100% | PASS: complete Chinese status, both actions present, no overlap or clipped next step. |
| `detail-sc-2-zh-Hans-{style}-{theme}.png` | `InboxShowcaseShotsTest.drawsTheSharedSc2DetailInChineseInEveryStyleAndAppearance`: pending suggestion | zh-Hans / 100% | PASS: status + Conversation, suggestion below summary, no Say something. |
| `status-sc-1-expanded-en-1x-{style}-{theme}.png` | `InboxStatusLayoutShotsTest.englishExpandedDefaultFontInEveryStyleAndAppearance`: focused expanded sc-1 status/actions crop | English / 100% | PASS in all six captures: complete sentence and both usable actions on separate lines. |
| `status-sc-1-en-2x-{style}-{theme}.png` | `InboxStatusLayoutShotsTest.englishLargeTextInEveryStyleAndAppearance`: focused sc-1 status/actions crop | English / 200% | PASS in all six captures, including Brief light/dark: complete sentence without the former ellipsis; both actions remain whole, separate, and usable. |
| `status-sc-1-suggestion-en-2x-{style}-{theme}.png` | `InboxStatusLayoutShotsTest.pendingSuggestionLargeTextInEveryStyleAndAppearance`: sc-1 with the existing test suggestion | English / 200% | PASS in all six captures, including Brief light/dark: complete sentence + Conversation only; Say something remains absent. |

The earlier 36-image set recorded Brief 200% failures because the stacked status branch had a three-line cap. Before this final evidence set was collected, a subsequent local-only `InboxEventScreen.kt` fix removed that cap from the stacked branch without changing global components. The final Brief 200% images above are therefore passing post-fix evidence, not the superseded failures.

## Sources and provenance

- `android/feature/inbox/src/test/kotlin/persona/feature/inbox/InboxShowcaseShotsTest.kt`
- `android/feature/inbox/src/test/kotlin/persona/feature/inbox/InboxStatusLayoutShotsTest.kt`
- `android/feature/inbox/src/test/kotlin/persona/feature/inbox/InboxDetailStatusLayoutTest.kt`
- `spec/fixtures/inbox/showcase.json` (read-only fixture)
- Full-page PNGs came from `android/feature/inbox/build/inbox-showcase-shots/`; focused crops came from `android/feature/inbox/build/inbox-status-layout-shots/`.
- PR head: `d0b62547a1a55d223bfb6bb983ae75f91ebf8b3f`, containing `pub/main` `4618c0e93075e99c1a9e0d664a529700f9beb3e5`.

## Final focused verification

Run from `android/`:

```sh
flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxDetailStatusLayoutTest' --tests 'persona.feature.inbox.InboxShowcaseShotsTest' --tests 'persona.feature.inbox.InboxStatusLayoutShotsTest'
```

Result: **11/11 tests passed, 0 failures** — `InboxDetailStatusLayoutTest` 5/5, `InboxShowcaseShotsTest` 3/3, and `InboxStatusLayoutShotsTest` 3/3. `python3 scripts/app/android_token_baseline.py` also passed.
