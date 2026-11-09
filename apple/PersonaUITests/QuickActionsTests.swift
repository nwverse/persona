import XCTest

/// The home screen icon's long-press menu (#3199, `QuickAction`): each item opens its page.
final class QuickActionsTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor private func launch() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-newAssistant", "YES", "-dev.openChat", "",
                               "-tab", "assistant"]
        app.launchPinned()
        XCTAssertTrue(app.buttons["assistant-sidebar-button"].firstMatch.waitForExistence(timeout: 15), "no shell")
    }

    /// Home, then the icon's menu: `title` tapped.
    @MainActor private func pick(_ title: String) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        XCUIDevice.shared.press(.home)
        let icon = springboard.icons["Persona"].firstMatch
        XCTAssertTrue(icon.waitForExistence(timeout: 10), "no Persona icon on the home screen")
        icon.press(forDuration: 1.5)
        let item = springboard.buttons[title].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no \(title) in the icon's menu")
        let shot = XCTAttachment(screenshot: springboard.screenshot())
        shot.name = "menu"; shot.lifetime = .keepAlways; add(shot)
        item.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10), "the app did not come back")
    }

    @MainActor func testEachItemOpensItsPage() {
        launch()
        pick("Agents")
        XCTAssertTrue(app.waitAgentsDrawn(true), "Agents is not in front: \(app.shellTrace)")
        pick("收件箱")
        XCTAssertTrue(app.navigationBars["收件箱"].firstMatch.waitForExistence(timeout: 5), "the inbox did not open")
        pick("新对话")
        XCTAssertTrue(app.waitAgentsDrawn(false), "助手 is not in front: \(app.shellTrace)")
        XCTAssertTrue(app.navigationBars["收件箱"].firstMatch.waitForNonExistence(timeout: 5), "the inbox stayed over 助手: \(app.shellTrace)")
        XCTAssertTrue(app.descendants(matching: .any)["shell-composer-field"].firstMatch.waitForExistence(timeout: 5), "no composer")
    }
}
