# Physical complication test face

This resource-only Watch Face Format v2 package is a temporary host for testing Persona's complication data sources on a real watch. It is separate from the Persona app and from the user's existing face. It contains no service, credential, network request, or automatic provider selection. Install/select it only during the explicit physical-watch test, then switch back to the recorded original face and uninstall this package.

The audience is developers verifying the system data-source contract. The three outlined regions deliberately accept one data type each:

| Slot | Type | Logical bounds on the 480 × 480 face |
| --- | --- | --- |
| 1, upper left | `SHORT_TEXT` | x=60, y=140, width=165, height=130 |
| 2, lower center | `LONG_TEXT` | x=70, y=305, width=340, height=98 |
| 3, upper right | `SMALL_IMAGE` | x=255, y=140, width=165, height=130 |

All slots initially use `EMPTY`, display a dash, and are configurable through the system editor. Tap behavior is supplied by the selected complication; the XML does not intercept taps or hard-code a launch target. The preview is an illustration of this empty layout, not live evidence.

Build independently of the main app, using JDK 21, Android SDK 35 and Gradle 8.13:

```powershell
.\wear\gradlew.bat -p wear/qa/watchface assembleDebug lintDebug --console=plain
```

Set `ANDROID_HOME` to the local SDK or create an ignored `local.properties` containing `sdk.dir=<SDK path>`. The APK is `build/outputs/apk/debug/PersonaWatchQaFace-debug.apk`; its application ID is `dev.persona.watch.qa.face`.

Before using it, record the active face's actual package/instance identity, appearance and complication selections, and verify that this temporary package is not already a user-owned installation. Also save the original values of any system settings changed for the test. A shared WFF runtime component alone may not identify which face to restore.

Select this separate face in the system picker, configure a source through each slot's real picker, and verify visible content and tap navigation. For extensions, test configuration cancellation and explicit selection, plus independent selections in two slots. For Milk Frog, test all three advertised data types separately. Never count a source preview or an app-page screenshot as proof of face rendering.

With Persona `0.2.1` or later, the test APK's `pet_expiry` mode can inject a guarded synthetic needs-you count of `42` with 45 seconds of cache validity. The corresponding `pet_expiry_restore` mode restores the saved synthetic cache. Bind the source through the real system picker first; injection does not select or observe a slot. Stay on the face during observation, and record its change after the returned expiry time and the next face redraw without opening the app. For both pet and extension timelines, assess this minute-based face after its next minute redraw following expiry, not at the exact expiry timestamp. `SEEDED` and a successful instrumentation exit indicate setup only, not a passing expiry observation. See the main [system complication acceptance procedure](../../README.md#system-complication-acceptance) for the guards, exact commands and required cleanup.

Restore the synthetic cache before changing Persona's connection. Restore the original face and every temporarily changed system setting before uninstalling `dev.persona.watch.qa.face`, then restore the real connection and remove the test package/tunnel. A successful build or XML validation is not physical acceptance; keep screenshots and device outcomes in the main [QA report](../../QA.md). This document is a procedure, not a claim that a current device run has completed.

The resource structure and rendering contract follow Android's official [WFF setup](https://developer.android.com/training/wearables/wff/setup) and [complication rendering](https://developer.android.com/training/wearables/wff/complications) documentation. The main app's services advertise the supported types; this face does not expand those types or grant privileges.
