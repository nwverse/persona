# Android inbox Thread section

These are post-merge screenshot artifacts for follow-up visual review. They are not a claim that a person reviewed the images before the associated pull request merged.

## Provenance

- Source main commit: `aa5e1ec43e49a46609ada1da4906d4cf41e04db6`.
- The images are Robolectric native-graphics screenshots from the named existing Android screenshot tests; no emulator or physical device was used.
- Test: `InboxLineSectionShotsTest`.
- The shared showcase captures use `spec/fixtures/inbox/showcase.json`, its fixed clock `2026-01-01T12:00:00Z`, and UTC as supplied by the test.
- Reproduce from the repository root with `./android/gradlew -p android -PpersonaApplicationId=com.example.persona :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxLineSectionShotsTest'` (the other review classes use the same Gradle task).

## Matrix

18 Thread PNGs: the section at 1x/2x density and the event page containing it, across Board, Brief and Glass in light and dark, zh-Hans.

## Files

- [inbox-event-line-board-dark.png](inbox-event-line-board-dark.png)
- [inbox-event-line-board-light.png](inbox-event-line-board-light.png)
- [inbox-event-line-brief-dark.png](inbox-event-line-brief-dark.png)
- [inbox-event-line-brief-light.png](inbox-event-line-brief-light.png)
- [inbox-event-line-glass-dark.png](inbox-event-line-glass-dark.png)
- [inbox-event-line-glass-light.png](inbox-event-line-glass-light.png)
- [inbox-line-board-dark-mdpi.png](inbox-line-board-dark-mdpi.png)
- [inbox-line-board-dark-xhdpi.png](inbox-line-board-dark-xhdpi.png)
- [inbox-line-board-light-mdpi.png](inbox-line-board-light-mdpi.png)
- [inbox-line-board-light-xhdpi.png](inbox-line-board-light-xhdpi.png)
- [inbox-line-brief-dark-mdpi.png](inbox-line-brief-dark-mdpi.png)
- [inbox-line-brief-dark-xhdpi.png](inbox-line-brief-dark-xhdpi.png)
- [inbox-line-brief-light-mdpi.png](inbox-line-brief-light-mdpi.png)
- [inbox-line-brief-light-xhdpi.png](inbox-line-brief-light-xhdpi.png)
- [inbox-line-glass-dark-mdpi.png](inbox-line-glass-dark-mdpi.png)
- [inbox-line-glass-dark-xhdpi.png](inbox-line-glass-dark-xhdpi.png)
- [inbox-line-glass-light-mdpi.png](inbox-line-glass-light-mdpi.png)
- [inbox-line-glass-light-xhdpi.png](inbox-line-glass-light-xhdpi.png)
