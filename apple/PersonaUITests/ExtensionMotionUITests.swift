import XCTest

/// An extension page drawn again (#37), against the mock hub's `showcase › motion` (`MOCK_EXTENSIONS=1`,
/// `TEST_RUNNER_PERSONA_EXTENSIONS_HUB`): after `ui.invalidate` the page ends on exactly the new values, the row that went
/// is gone and the one that came is there, in light and dark. With `PERSONA_SHOTS`, screenshots before and after.
final class ExtensionMotionUITests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment

    @MainActor func testARedrawEndsOnTheNewValues() throws {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("Needs MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        continueAfterFailure = false
        for scheme in ["light", "dark"] {
            try mock(hub, "reset")
            let app = XCUIApplication()
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-ui.scheme", scheme,
                                   "-openExtension", "showcase", "-openExtensionPage", "motion"]
            app.launchPinned()
            dismissSystemAlerts(wait: 4)
            XCTAssertTrue(text(app, "2/8").waitForExistence(timeout: 20), app.debugDescription)
            XCTAssertTrue(text(app, "Running").exists && text(app, "Review").exists)
            // The icon note can come seconds after the notification question, and while an alert is up the app is not
            // in front, so the page holds its redraw back until the alert is gone.
            dismissSystemAlerts(wait: 2)
            shot(app, "before-" + scheme)

            try mock(hub, "next")
            dismissSystemAlerts()
            XCTAssertTrue(text(app, "6/8").waitForExistence(timeout: 10), app.debugDescription)
            for t in ["$36.20", "8.4M", "4 min", "5/5", "Passed", "Deploy"] {
                XCTAssertTrue(text(app, t).waitForExistence(timeout: 5), "\(t) missing\n\(app.debugDescription)")
            }
            for t in ["2/8", "$1.20", "Running", "Review"] {
                XCTAssertTrue(text(app, t).waitForNonExistence(timeout: 5), "\(t) still shown\n\(app.debugDescription)")
            }
            shot(app, "after-" + scheme)
            app.terminate()
        }
    }

    /// An element whose label has `t`: a stat's and a metric's words are one element ("Done, 6/8").
    @MainActor private func text(_ app: XCUIApplication, _ t: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", t)).firstMatch
    }

    private func mock(_ hub: String, _ step: String) throws {
        _ = try Data(contentsOf: URL(string: hub + "/mock/motion/" + step)!)
    }

    /// The notification question and the icon change note are SpringBoard's.
    @MainActor private func dismissSystemAlerts(wait: TimeInterval = 1) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        var deadline = Date.now.addingTimeInterval(wait)
        // Only an alert it could answer buys more time (one it cannot, left up, would keep it here for ever).
        while Date.now < deadline {
            let alert = springboard.alerts.firstMatch
            guard alert.waitForExistence(timeout: 0.5) else { continue }
            guard let label = ["允许", "Allow", "好", "OK"].first(where: { alert.buttons[$0].exists }) else {
                XCTFail("a system alert nobody answers:\n\(springboard.debugDescription)")
                return
            }
            alert.buttons[label].tap()
            deadline = Date.now.addingTimeInterval(1.5) // another may follow
        }
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let png = app.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("extension-motion-\(name).png"))
        }
    }
}
