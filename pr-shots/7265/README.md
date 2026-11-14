# Android large-text section headers

These are post-merge screenshot artifacts for follow-up visual review. They are not a claim that a person reviewed the images before the associated pull request merged.

## Provenance

- Source main commit: `aa5e1ec43e49a46609ada1da4906d4cf41e04db6`.
- The images are Robolectric native-graphics screenshots from the named existing Android screenshot tests; no emulator or physical device was used.
- Test: `InboxSectionHeaderShotsTest`.
- This test uses its own English-qualified synthetic records (fixture timestamp `2026-11-13`); it does not read `showcase.json` or inject the showcase clock/UTC.
- Reproduce from the repository root with `./android/gradlew -p android -PpersonaApplicationId=com.example.persona :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxSectionHeaderShotsTest'` (the other review classes use the same Gradle task).

## Matrix

12 section-header PNGs in Board, Brief and Glass, light and dark, at 1x and 2x text. The existing test is explicitly en-qualified.

## Files

- [board-dark-1.0x.png](board-dark-1.0x.png)
- [board-dark-2.0x.png](board-dark-2.0x.png)
- [board-light-1.0x.png](board-light-1.0x.png)
- [board-light-2.0x.png](board-light-2.0x.png)
- [brief-dark-1.0x.png](brief-dark-1.0x.png)
- [brief-dark-2.0x.png](brief-dark-2.0x.png)
- [brief-light-1.0x.png](brief-light-1.0x.png)
- [brief-light-2.0x.png](brief-light-2.0x.png)
- [glass-dark-1.0x.png](glass-dark-1.0x.png)
- [glass-dark-2.0x.png](glass-dark-2.0x.png)
- [glass-light-1.0x.png](glass-light-1.0x.png)
- [glass-light-2.0x.png](glass-light-2.0x.png)
