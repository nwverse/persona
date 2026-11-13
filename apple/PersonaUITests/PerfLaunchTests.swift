import XCTest

/// Cold and warm application launches against the launch lane's large mock data (#4266).
/// Unlike the other performance suites, these tests never enable `FrameProbe` or read `FrameStats`.
///
/// Cold resets the saved pairing and snapshot on every launch with the existing DEBUG `-openWelcome` switch, then pairs
/// in `AppDelegate`; warm uses the pairing and large snapshot saved by an unmeasured seed launch. Both measure fresh
/// processes to the first displayed frame, not a resume. XCTest discards its own warm-up, so cold here means fresh
/// pairing/snapshot state with warm OS caches, not a rebooted-device cold launch. Simulator results do not enforce the
/// 400 ms device budget.
final class PerfLaunchTests: PerfTestCase {
    @MainActor private func prepareLaunch(pairing: Bool) {
        app = XCUIApplication()
        app.launchArguments = ["-dev.openChat", ""]
        if pairing { app.launchArguments += ["-pairURL", pair] }
    }

    @MainActor private func measureLaunch(_ screenshotName: String) {
        var firstLaunch = true
        measure(metrics: [XCTApplicationLaunchMetric(waitUntilResponsive: false)], options: options) {
            app.launchPinned()
            if firstLaunch {
                allowNotifications()
                acceptIconAlert()
                firstLaunch = false
            }
        }
        let screenshot = XCTAttachment(screenshot: app.screenshot())
        screenshot.name = screenshotName
        screenshot.lifetime = .keepAlways
        add(screenshot)
    }

    @MainActor func testColdLaunch() throws {
        prepareLaunch(pairing: true)
        app.launchArguments += ["-openWelcome", "YES"]
        app.terminate()
        measureLaunch("cold-launch")
    }

    @MainActor func testWarmLaunch() throws {
        prepareLaunch(pairing: true)
        app.launchPinned()
        allowNotifications()
        acceptIconAlert()
        see(app.buttons["assistant-sidebar-button"].firstMatch, "the paired Assistant page", timeout: 30)
        sleep(4) // receive the large data sets and let the snapshot throttle and detached writer finish
        XCUIDevice.shared.press(.home) // didEnterBackground flushes the snapshot; terminate() alone is not graceful
        sleep(2)
        app.terminate()
        prepareLaunch(pairing: false) // saved pairing: no adopt(), registry write or link rebuild in the measured launch
        measureLaunch("warm-launch")
    }
}
