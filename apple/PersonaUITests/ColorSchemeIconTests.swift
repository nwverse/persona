import XCTest

/// Settings › Appearance › Light & Dark (docs/apple/ios.md「Light & Dark」): picking Dark or Light draws the app that way (checked on `probe.scheme`,
/// the look the page is actually in) and puts the default Avatar (Mochi)'s matching icon on the home screen, through the
/// system's alert where the simulator supports alternate icons; Match System hands both back. Runs in the demo mode (no
/// hub). Screenshots go to PERSONA_ICON_SHOTS when set: the page and the home screen after each pick.
final class ColorSchemeIconTests: XCTestCase {
    override func setUpWithError() throws { continueAfterFailure = false }

    /// A failure part way must not leave Dark / Light saved for the tests after: back to Match System.
    override func tearDown() {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-tab", "settings", "-openAppearance", "scheme"]
        app.launchPinned()
        let button = app.buttons["scheme-system"]
        let row = button.waitForExistence(timeout: 10) ? button : app.staticTexts["scheme-system"]
        if row.exists, app.probe("scheme", as: Scheme.self)?.choice != "system" {
            row.tap()
            let ok = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.buttons.firstMatch
            if ok.waitForExistence(timeout: 5) { ok.tap() }
        }
        app.terminate()
        super.tearDown()
    }

    private struct Scheme: Decodable {
        var choice: String
        var shown: String
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-tab", "settings", "-openAppearance", "scheme"]
        app.launchPinned()
        return app
    }

    /// The picker's row for a choice (a button, or its text where the row is not exposed as one).
    @MainActor private func row(_ app: XCUIApplication, _ scheme: String) -> XCUIElement? {
        let button = app.buttons["scheme-\(scheme)"]
        return button.waitForExistence(timeout: 10) ? button : app.staticTexts["scheme-\(scheme)"]
    }

    /// The system's 「你已更改“…”的图标」 alert, from SpringBoard (none where alternate icons are not supported).
    @MainActor private func acceptSystemAlert() {
        let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let ok = board.alerts.buttons.firstMatch
        if ok.waitForExistence(timeout: 5) { ok.tap() }
    }

    /// The notification permission prompt left over from launch, declined; any other alert is left alone.
    @MainActor private func dismissPermissionPrompt() {
        let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["不允许", "Don’t Allow", "Don't Allow"] {
            let button = board.alerts.buttons[label]
            if button.waitForExistence(timeout: 1) {
                button.tap()
                return
            }
        }
    }

    @MainActor private func shot(_ name: String, _ image: XCUIScreenshot) throws {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_ICON_SHOTS"] else { return }
        try image.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name).png"))
    }

    /// Picks `scheme`, checks the page is drawn in `shown`, then looks at the home screen.
    @MainActor private func pick(_ app: XCUIApplication, _ scheme: String, shown: String) throws {
        let target = try XCTUnwrap(row(app, scheme), "no row for \(scheme)")
        target.tap()
        acceptSystemAlert()
        var seen: Scheme?
        for _ in 0..<50 {
            seen = app.probe("scheme", as: Scheme.self)
            if seen?.choice == scheme, seen?.shown == shown { break }
            usleep(100_000)
        }
        XCTAssertEqual(seen?.choice, scheme)
        XCTAssertEqual(seen?.shown, shown, "the page is drawn \(seen?.shown ?? "?") after picking \(scheme)")
        try shot("scheme-\(scheme)-page", app.screenshot())
        XCUIDevice.shared.press(.home)
        sleep(2)
        try shot("scheme-\(scheme)-home", XCUIScreen.main.screenshot())
        app.activate()
    }

    @MainActor func testTheAppAndItsIconTakeLightOrDark() throws {
        let app = launch()
        dismissPermissionPrompt()
        let start = try XCTUnwrap(app.probe("scheme", as: Scheme.self), "no probe.scheme: launched without -probes?")
        XCTAssertEqual(start.choice, "system")
        try shot("scheme-start-page", app.screenshot())
        let system = start.shown
        try pick(app, "dark", shown: "dark")
        try pick(app, "light", shown: "light")
        try pick(app, "system", shown: system)
    }
}
