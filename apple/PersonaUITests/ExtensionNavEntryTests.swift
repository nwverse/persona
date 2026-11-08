import XCTest

/// Extensions' main-navigation pages (nav_page) opened straight from the navigation (#2961): the new shell's drawer on
/// iPhone, the sidebar column on iPad, after the places to go, with the extension's badge. Against
/// scripts/mock/mock_hub.py with MOCK_EXTENSIONS=1 (its example-gateway › Usage, badge 3): `TEST_RUNNER_PERSONA_EXTENSIONS_HUB=
/// http://127.0.0.1:<port>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots, named by
/// `TEST_RUNNER_PERSONA_EXTENSIONS_APPEARANCE` and the style.
final class ExtensionNavEntryTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private let entry = "sidebar-extension-example-gateway-usage"

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let look = env["PERSONA_EXTENSIONS_APPEARANCE"] ?? "light"
        let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir)
            .appending(path: "nav-entry-\(device)-\(name)-\(UITestStyle.name)-\(look).png"))
    }

    /// The entry is in the navigation, says its badge, and opens the extension's page there.
    @MainActor private func checkEntry(_ app: XCUIApplication, _ name: String) {
        let row = app.descendants(matching: .any)[entry]
        XCTAssertTrue(row.waitForExistence(timeout: 20), "the extension's entry is not in the navigation:\n\(app.debugDescription)")
        XCTAssertEqual(row.label, "用量")
        XCTAssertEqual(row.value as? String, "3", "the extension's badge")
        shot(app, name)
        row.tap()
        XCTAssertTrue(app.descendants(matching: .any)["extension-ui-ready"].waitForExistence(timeout: 15),
                      "the extension's page did not open:\n\(app.debugDescription)")
        shot(app, name + "-page")
    }

    @MainActor func testTheShellDrawerListsTheExtensionsPages() throws {
        try XCTSkipIf(UIDevice.current.userInterfaceIdiom == .pad, "the shell's drawer is the phone's")
        let app = try launch(["-tab", "assistant", "-dev.openChat", ""])
        let face = app.buttons["assistant-sidebar-button"]
        XCTAssertTrue(face.waitForExistence(timeout: 15), app.debugDescription)
        face.tap()
        XCTAssertTrue(app.buttons["sidebar-inbox"].waitForExistence(timeout: 5), "the drawer did not open")
        checkEntry(app, "drawer")
    }

    @MainActor func testTheIPadSidebarListsTheExtensionsPages() throws {
        try XCTSkipIf(UIDevice.current.userInterfaceIdiom != .pad, "the sidebar column is iPad's")
        for orientation in [UIDeviceOrientation.landscapeLeft, .portrait] {
            XCUIDevice.shared.orientation = orientation
            let app = try launch(["-tab", "assistant"])
            let row = app.descendants(matching: .any)[entry]
            // Portrait folds the column away: Chat History brings it back.
            if !row.waitForExistence(timeout: 8) {
                let toggle = app.buttons["assistant-sidebar-button"]
                if toggle.waitForExistence(timeout: 5) { toggle.tap() }
            }
            checkEntry(app, orientation == .portrait ? "sidebar-portrait" : "sidebar-landscape")
            app.terminate()
        }
        XCUIDevice.shared.orientation = .portrait
    }
}
