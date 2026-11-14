# Android final inbox detail and timeline parity

These are post-merge screenshot artifacts for follow-up visual review. They are not a claim that a person reviewed the images before the associated pull request merged.

## Provenance

- Source main commit: `aa5e1ec43e49a46609ada1da4906d4cf41e04db6`.
- The images are Robolectric native-graphics screenshots from the named existing Android screenshot tests; no emulator or physical device was used.
- Test: `InboxShowcaseShotsTest`.
- The shared showcase captures use `spec/fixtures/inbox/showcase.json`, its fixed clock `2026-01-01T12:00:00Z`, and UTC as supplied by the test.
- Reproduce from the repository root with `./android/gradlew -p android -PpersonaApplicationId=com.example.persona :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxShowcaseShotsTest'` (the other review classes use the same Gradle task).

## Matrix

42 shared showcase PNGs, including the six sc-2 detail images added for the final parity change, across Board, Brief and Glass in light and dark.

## Files

- [detail-expanded-en-board-dark.png](detail-expanded-en-board-dark.png)
- [detail-expanded-en-board-light.png](detail-expanded-en-board-light.png)
- [detail-expanded-en-brief-dark.png](detail-expanded-en-brief-dark.png)
- [detail-expanded-en-brief-light.png](detail-expanded-en-brief-light.png)
- [detail-expanded-en-glass-dark.png](detail-expanded-en-glass-dark.png)
- [detail-expanded-en-glass-light.png](detail-expanded-en-glass-light.png)
- [detail-expanded-zh-Hans-board-dark.png](detail-expanded-zh-Hans-board-dark.png)
- [detail-expanded-zh-Hans-board-light.png](detail-expanded-zh-Hans-board-light.png)
- [detail-expanded-zh-Hans-brief-dark.png](detail-expanded-zh-Hans-brief-dark.png)
- [detail-expanded-zh-Hans-brief-light.png](detail-expanded-zh-Hans-brief-light.png)
- [detail-expanded-zh-Hans-glass-dark.png](detail-expanded-zh-Hans-glass-dark.png)
- [detail-expanded-zh-Hans-glass-light.png](detail-expanded-zh-Hans-glass-light.png)
- [detail-folded-en-board-dark.png](detail-folded-en-board-dark.png)
- [detail-folded-en-board-light.png](detail-folded-en-board-light.png)
- [detail-folded-en-brief-dark.png](detail-folded-en-brief-dark.png)
- [detail-folded-en-brief-light.png](detail-folded-en-brief-light.png)
- [detail-folded-en-glass-dark.png](detail-folded-en-glass-dark.png)
- [detail-folded-en-glass-light.png](detail-folded-en-glass-light.png)
- [detail-folded-zh-Hans-board-dark.png](detail-folded-zh-Hans-board-dark.png)
- [detail-folded-zh-Hans-board-light.png](detail-folded-zh-Hans-board-light.png)
- [detail-folded-zh-Hans-brief-dark.png](detail-folded-zh-Hans-brief-dark.png)
- [detail-folded-zh-Hans-brief-light.png](detail-folded-zh-Hans-brief-light.png)
- [detail-folded-zh-Hans-glass-dark.png](detail-folded-zh-Hans-glass-dark.png)
- [detail-folded-zh-Hans-glass-light.png](detail-folded-zh-Hans-glass-light.png)
- [detail-sc-2-zh-Hans-board-dark.png](detail-sc-2-zh-Hans-board-dark.png)
- [detail-sc-2-zh-Hans-board-light.png](detail-sc-2-zh-Hans-board-light.png)
- [detail-sc-2-zh-Hans-brief-dark.png](detail-sc-2-zh-Hans-brief-dark.png)
- [detail-sc-2-zh-Hans-brief-light.png](detail-sc-2-zh-Hans-brief-light.png)
- [detail-sc-2-zh-Hans-glass-dark.png](detail-sc-2-zh-Hans-glass-dark.png)
- [detail-sc-2-zh-Hans-glass-light.png](detail-sc-2-zh-Hans-glass-light.png)
- [list-board-dark.png](list-board-dark.png)
- [list-board-light.png](list-board-light.png)
- [list-brief-dark.png](list-brief-dark.png)
- [list-brief-light.png](list-brief-light.png)
- [list-en-board-dark.png](list-en-board-dark.png)
- [list-en-board-light.png](list-en-board-light.png)
- [list-en-brief-dark.png](list-en-brief-dark.png)
- [list-en-brief-light.png](list-en-brief-light.png)
- [list-en-glass-dark.png](list-en-glass-dark.png)
- [list-en-glass-light.png](list-en-glass-light.png)
- [list-glass-dark.png](list-glass-dark.png)
- [list-glass-light.png](list-glass-light.png)
