import XCTest

/// Render the actual machine sparklines in both appearances, with no system alert covering them.
final class MachineSparklineTests: XCTestCase {
    @MainActor func testLightAndDarkMachineGrid() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("requires the mock hub (PERSONA_MOCK_HUB)")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", ""]
        app.launchPinned()
        acceptIconAlert()
        app.terminate()
        for scheme in ["light", "dark"] {
            app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-tab", "settings", "-openMachines", "YES",
                                   "-ui.scheme", scheme]
            app.launchPinned()
            acceptIconAlert()
            let card = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "服务器、")).firstMatch
            XCTAssertTrue(card.waitForExistence(timeout: 30), "the real machine grid did not load")
            acceptIconAlert()
            XCTAssertFalse(XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch.exists,
                           "a system alert covers the machine grid")
            for _ in 0..<3 where card.frame.maxY > app.windows.firstMatch.frame.maxY - 80 {
                app.swipeUp(velocity: .slow)
            }
            sleep(3)
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "machine-sparklines-" + scheme
            shot.lifetime = .keepAlways
            add(shot)
            app.terminate()
        }
    }
}
