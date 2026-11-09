import XCTest

/// The GUI speaks to the actual isolated pre-rename hub, not the screenshot mock or a live paired hub.
final class OldHubCompatibilityUITests: XCTestCase {
    @MainActor func testLight() throws { try exercise("light") }
    @MainActor func testDark() throws { try exercise("dark") }

    @MainActor private func exercise(_ appearance: String) throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        let suffix = "_" + appearance.uppercased()
        guard let pair = env["PERSONA_COMPAT_TEST_PAIR_URL"],
              let session = env["PERSONA_COMPAT_TEST_SESSION" + suffix] ?? env["PERSONA_COMPAT_TEST_SESSION"],
              let main = env["PERSONA_COMPAT_TEST_MAIN_SESSION" + suffix] ?? env["PERSONA_COMPAT_TEST_MAIN_SESSION"],
              let base = env["PERSONA_COMPAT_TEST_URL"], URL(string: base)?.host == "127.0.0.1" else {
            throw XCTSkip("requires an explicitly supplied isolated pre-rename fixture hub")
        }
        let app = XCUIApplication()
        let open = ["-openSession", session, "-ui.scheme", appearance, "-dev.openChat", ""]
        app.launchArguments = ["-pairURL", pair, "-openChatsFresh", "YES"] + open
        app.launchPinned()
        dismissSystemAlerts()
        let baseline = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "echo: Compatibility screenshot fixture")).firstMatch
        XCTAssertTrue(baseline.waitForExistence(timeout: 30), "the GUI did not load the real old hub's history")
        let composer = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(composer.waitForExistence(timeout: 10))
        composer.tap()
        // Replies from an earlier failed run must never satisfy this run's send or cold-reconnect assertions.
        let runID = String(UUID().uuidString.prefix(8))
        let sent = "Compatibility UI " + appearance + " " + runID
        composer.typeText(sent)
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 5))
        send.tap()
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "echo: " + sent)).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 30), "the GUI send did not stream a reply through the old hub/node")
        shot(app, appearance + "-sent")
        app.terminate()

        // No pairing injection on the second launch: the saved device credential must reconnect to the old hub.
        app.launchArguments = open
        app.launchPinned()
        dismissSystemAlerts()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "echo: " + sent)).firstMatch.waitForExistence(timeout: 30),
                      "cold relaunch lost pairing or old-hub history")
        shot(app, appearance + "-cold-reconnect")
        app.terminate()

        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main,
                               "-ui.scheme", appearance]
        app.launchPinned()
        dismissSystemAlerts()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "echo: Compatibility main screenshot fixture")).firstMatch.waitForExistence(timeout: 30),
                      "the Persona tab did not load the old hub's main conversation")
        let mainComposer = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(mainComposer.waitForExistence(timeout: 10))
        mainComposer.tap()
        let mainText = "Compatibility main UI " + appearance + " " + runID
        mainComposer.typeText(mainText)
        app.buttons["发送"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "echo: " + mainText)).firstMatch.waitForExistence(timeout: 30),
                      "the main conversation did not stream its old-hub reply")
        shot(app, appearance + "-main-conversation")
        app.terminate()
    }

    @MainActor private func dismissSystemAlerts() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        if springboard.alerts.firstMatch.waitForExistence(timeout: 3) {
            for label in ["允许", "Allow", "好", "OK"] {
                let button = springboard.alerts.buttons[label].firstMatch
                if button.exists { button.tap(); return }
            }
        }
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "old-hub-" + name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_COMPAT_TEST_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("old-hub-" + name + ".png"))
        }
    }
}
