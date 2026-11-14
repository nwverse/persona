# Android related-item and instruction actions

These are post-merge screenshot artifacts for follow-up visual review. They are not a claim that a person reviewed the images before the associated pull request merged.

## Provenance

- Source main commit: `aa5e1ec43e49a46609ada1da4906d4cf41e04db6`.
- The images are Robolectric native-graphics screenshots from the named existing Android screenshot tests; no emulator or physical device was used.
- Test: `InboxLineSectionShotsTest and InboxSaySomethingShotsTest`.
- The shared showcase captures use `spec/fixtures/inbox/showcase.json`, its fixed clock `2026-01-01T12:00:00Z`, and UTC as supplied by the test.
- Reproduce from the repository root with `./android/gradlew -p android -PpersonaApplicationId=com.example.persona :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxLineSectionShotsTest'` (the other review classes use the same Gradle task).

## Matrix

36 PNGs: the Same matter picker, processing state with Conversation and Say something, failed state with Try again, and the instruction sheet, across all three styles and light/dark, including the tests' 1x/2x variants.

## Files

- [inbox-processing-say-board-dark.png](inbox-processing-say-board-dark.png)
- [inbox-processing-say-board-light.png](inbox-processing-say-board-light.png)
- [inbox-processing-say-brief-dark.png](inbox-processing-say-brief-dark.png)
- [inbox-processing-say-brief-light.png](inbox-processing-say-brief-light.png)
- [inbox-processing-say-glass-dark.png](inbox-processing-say-glass-dark.png)
- [inbox-processing-say-glass-light.png](inbox-processing-say-glass-light.png)
- [inbox-retry-board-dark-mdpi.png](inbox-retry-board-dark-mdpi.png)
- [inbox-retry-board-dark-xhdpi.png](inbox-retry-board-dark-xhdpi.png)
- [inbox-retry-board-light-mdpi.png](inbox-retry-board-light-mdpi.png)
- [inbox-retry-board-light-xhdpi.png](inbox-retry-board-light-xhdpi.png)
- [inbox-retry-brief-dark-mdpi.png](inbox-retry-brief-dark-mdpi.png)
- [inbox-retry-brief-dark-xhdpi.png](inbox-retry-brief-dark-xhdpi.png)
- [inbox-retry-brief-light-mdpi.png](inbox-retry-brief-light-mdpi.png)
- [inbox-retry-brief-light-xhdpi.png](inbox-retry-brief-light-xhdpi.png)
- [inbox-retry-glass-dark-mdpi.png](inbox-retry-glass-dark-mdpi.png)
- [inbox-retry-glass-dark-xhdpi.png](inbox-retry-glass-dark-xhdpi.png)
- [inbox-retry-glass-light-mdpi.png](inbox-retry-glass-light-mdpi.png)
- [inbox-retry-glass-light-xhdpi.png](inbox-retry-glass-light-xhdpi.png)
- [inbox-same-matter-board-dark.png](inbox-same-matter-board-dark.png)
- [inbox-same-matter-board-light.png](inbox-same-matter-board-light.png)
- [inbox-same-matter-brief-dark.png](inbox-same-matter-brief-dark.png)
- [inbox-same-matter-brief-light.png](inbox-same-matter-brief-light.png)
- [inbox-same-matter-glass-dark.png](inbox-same-matter-glass-dark.png)
- [inbox-same-matter-glass-light.png](inbox-same-matter-glass-light.png)
- [inbox-say-sheet-board-dark-mdpi.png](inbox-say-sheet-board-dark-mdpi.png)
- [inbox-say-sheet-board-dark-xhdpi.png](inbox-say-sheet-board-dark-xhdpi.png)
- [inbox-say-sheet-board-light-mdpi.png](inbox-say-sheet-board-light-mdpi.png)
- [inbox-say-sheet-board-light-xhdpi.png](inbox-say-sheet-board-light-xhdpi.png)
- [inbox-say-sheet-brief-dark-mdpi.png](inbox-say-sheet-brief-dark-mdpi.png)
- [inbox-say-sheet-brief-dark-xhdpi.png](inbox-say-sheet-brief-dark-xhdpi.png)
- [inbox-say-sheet-brief-light-mdpi.png](inbox-say-sheet-brief-light-mdpi.png)
- [inbox-say-sheet-brief-light-xhdpi.png](inbox-say-sheet-brief-light-xhdpi.png)
- [inbox-say-sheet-glass-dark-mdpi.png](inbox-say-sheet-glass-dark-mdpi.png)
- [inbox-say-sheet-glass-dark-xhdpi.png](inbox-say-sheet-glass-dark-xhdpi.png)
- [inbox-say-sheet-glass-light-mdpi.png](inbox-say-sheet-glass-light-mdpi.png)
- [inbox-say-sheet-glass-light-xhdpi.png](inbox-say-sheet-glass-light-xhdpi.png)
