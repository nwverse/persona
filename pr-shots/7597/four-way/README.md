# Android four-way final captures

## Source and reproducibility

- Repository: `nwverse/persona`, existing detached worktree at merged main / squash commit **`559b8b2af16e33d0269e1c2a435775ceeaf3a9a7`**, from **PR #7601** (`fix(android): keep the Inbox status sentence clear of its actions`). All seven images were replaced from a fresh test run at this exact commit.
- Scenario: `spec/fixtures/inbox/showcase.json`, including its `sessions` (`s-agent` is `unread`, `s-handling` is `idle`). The test reads the fixture's fixed `now_clock` **`2026-01-01T12:00:00Z`** and supplies **`ZoneOffset.UTC`** through `LocalInboxZone`.
- Locales: **`zh-Hans`** for the Chinese list and sc-1 captures, plus **`en`** for the extra expanded sc-1 capture. Fixture content remains Chinese where the shared scenario defines Chinese text, including `follow.next`.
- Style and rendering: **Glass** only, reduced motion, 400 dp wide by 1800 dp high, Robolectric SDK 35 with **`GraphicsMode.Mode.NATIVE`**. These are native Robolectric captures only; no emulator or physical device was used.
- Final test result: **`BUILD SUCCESSFUL in 1m 40s`**, **103 actionable tasks executed**, process exit code **0**. Exact command:

  ```sh
  cd /root/jarvis-work/jobs/inbox-matters/inbox-matters-android-status-layout/android && source /root/android-tools-v9/env.sh && export PATH=/root/.cargo/bin:$PATH && flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxShowcaseShotsTest' --rerun-tasks
  ```

## Fixed rendering conditions and final images

`folded` is the default timeline with the latest three entries. `expanded` is the timeline after clicking its header to show all seven entries. The clock and UTC zone above are injected for every image.

| Final image | Meaning | SHA-256 |
| --- | --- | --- |
| [`list-zh-Hans-glass-light.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/list-zh-Hans-glass-light.png) | Chinese list, Glass, light | `d3586cd11d3ccbd93f73eeeea0f2815c4fee8ae7b094876ae20e892b80f7d0b6` |
| [`list-zh-Hans-glass-dark.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/list-zh-Hans-glass-dark.png) | Chinese list, Glass, dark | `faaa49dc7c9bf340d59dcfb071968441aa2dd19155dfff608936682fc2c851f0` |
| [`sc1-detail-folded-zh-Hans-glass-light.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/sc1-detail-folded-zh-Hans-glass-light.png) | Chinese sc-1 detail, folded timeline, light | `9e87d212b059c3be1932cead3a16ec9b756794cfd79b28471523ccb1eb0b13de` |
| [`sc1-detail-folded-zh-Hans-glass-dark.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/sc1-detail-folded-zh-Hans-glass-dark.png) | Chinese sc-1 detail, folded timeline, dark | `da2af3d291e36fb7a70077060f939f9afee00b856db590fd009fdc8f5370370e` |
| [`sc1-detail-expanded-zh-Hans-glass-light.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/sc1-detail-expanded-zh-Hans-glass-light.png) | Chinese sc-1 detail, expanded timeline, light | `ad9460d4ad63afd6353f6c8a9b9b3eaffda4496b21b0bc9dd571a12185eb19d4` |
| [`sc1-detail-expanded-zh-Hans-glass-dark.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/sc1-detail-expanded-zh-Hans-glass-dark.png) | Chinese sc-1 detail, expanded timeline, dark | `afe88cee1c4c71c45ff9c9c59d96f7785799f23c01abbb1f5ce36ddcafdc4c3c` |
| [`sc1-detail-expanded-en-glass-light.png`](https://github.com/nwverse/persona/blob/artifacts/7597-screenshots/pr-shots/7597/four-way/sc1-detail-expanded-en-glass-light.png) | English sc-1 detail, expanded timeline, light | `3e5cee1880043e3c6d30a5c171fd1660f7bb195876a30aac2f76fb6bfdfd532f` |

Exactly these seven requested Glass PNGs are included. The complete three-style showcase matrix remains in the test output and is uploaded separately with the display PR's artifact set.

## Final visual review

Every final PNG was opened and inspected. The Chinese sc-1 folded and expanded captures show the complete status sentence, including the next action, with `处理对话 ›` and `说一句` clear below it in both light and dark appearances. The English expanded sc-1 capture shows the full waiting person (`张三确认新报价`), `look again tomorrow 09:00`, the complete next action (`问更正后的报价行不行`), and unobstructed `Conversation ›` and `Say something` actions. No status/action overlap, missing action, or clipped status sentence remains in the sc-1 detail captures.
