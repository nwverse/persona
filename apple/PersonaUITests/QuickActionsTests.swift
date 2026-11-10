import XCTest

/// The home screen icon's long-press menu (#3199, `QuickAction`): each item opens its page; a chat opened is on it (#2191,
/// the last two opened, before New Chat and Inbox), and Agents no longer is.
final class QuickActionsTests: XCTestCase {
    private var app: XCUIApplication!
    /// A demo Agent's chat (Persona/Resources/Demo.json), its title (the menu's) and its first message (the open chat's;
    /// its header names the folder, not the title).
    private static let review = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    private static let reviewTitle = "检查 PR #42 的改动"
    private static let reviewMessage = "看一下 PR #42"

    override func setUp() {
        continueAfterFailure = false
    }

    /// `opening`: a chat opened at launch (it goes on the menu), else Persona's.
    @MainActor private func launch(opening session: String? = nil) {
        app = XCUIApplication()
        // not with -newAssistant, which puts Persona in front after -openSession opened the chat
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES"]
            + (session.map { ["-openSession", $0] } ?? ["-newAssistant", "YES", "-dev.openChat", "", "-tab", "assistant"])
        app.launchPinned()
        if session == nil {
            XCTAssertTrue(app.buttons["assistant-sidebar-button"].firstMatch.waitForExistence(timeout: 15), "no shell")
        }
    }

    /// Home, then the icon's menu: `title` tapped; `absent` must not be on the menu.
    @MainActor private func pick(_ title: String, absent: String? = nil) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        XCUIDevice.shared.press(.home)
        let icon = springboard.icons["Persona"].firstMatch
        XCTAssertTrue(icon.waitForExistence(timeout: 10), "no Persona icon on the home screen")
        icon.press(forDuration: 1.5)
        // a recent conversation's item reads "<title>, <computer> · <folder>": its title starts it
        let item = springboard.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", title)).firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no \(title) in the icon's menu")
        if let absent { XCTAssertFalse(springboard.buttons[absent].exists, "\(absent) is still in the icon's menu") }
        let shot = XCTAttachment(screenshot: springboard.screenshot())
        shot.name = "menu"; shot.lifetime = .keepAlways; add(shot)
        item.tap()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 10), "the app did not come back")
    }

    @MainActor func testEachItemOpensItsPage() {
        launch(opening: Self.review)
        XCTAssertTrue(app.staticTexts[Self.reviewMessage].firstMatch.waitForExistence(timeout: 20), "the chat did not open")
        pick(Self.reviewTitle, absent: "Agents")
        XCTAssertTrue(app.staticTexts[Self.reviewMessage].firstMatch.waitForExistence(timeout: 10), "the recent chat did not open")
        pick("收件箱")
        XCTAssertTrue(app.navigationBars["收件箱"].firstMatch.waitForExistence(timeout: 5), "the inbox did not open")
        pick("新对话")
        XCTAssertTrue(app.waitAgentsDrawn(false), "助手 is not in front: \(app.shellTrace)")
        XCTAssertTrue(app.navigationBars["收件箱"].firstMatch.waitForNonExistence(timeout: 5), "the inbox stayed over 助手: \(app.shellTrace)")
        XCTAssertTrue(app.descendants(matching: .any)["shell-composer-field"].firstMatch.waitForExistence(timeout: 5), "no composer")
    }
}
