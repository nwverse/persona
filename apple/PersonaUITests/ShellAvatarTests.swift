import XCTest

/// The shell's top-left button is the persona's face (docs/apple/ios.md「New shell」): it opens the drawer on Assistant and on Agents,
/// and carries the inbox's dot while the inbox has something for the user (Demo Mode's has).
/// The empty-inbox case needs scripts/mock/mock_hub.py started with
/// MOCK_INBOX=empty (PERSONA_EMPTY_INBOX_HUB).
final class ShellAvatarTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor private func launch(_ arguments: [String]) {
        app = XCUIApplication()
        // probe.shell; no chat restored from an earlier run: Agents opened on it, with no face to tap (2026-11-03).
        app.launchArguments = arguments + ["-probes", "YES", "-dev.openChat", ""]
        app.launchPinned()
        // A fresh install asks for notifications first; while the alert is up the app stays inactive and the inbox
        // is not fetched, so the face would carry no dot.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        if allow.waitForExistence(timeout: 3) { allow.tap() }
    }

    @MainActor private func value(_ e: XCUIElement) -> String { e.value as? String ?? "" }

    /// The drawer is open when its rows are there; a tap on the page's card beside it closes it.
    @MainActor private func openAndCloseDrawer(_ face: XCUIElement, _ page: String) {
        face.tapCenter()
        XCTAssertTrue(app.buttons["sidebar-inbox"].waitForExistence(timeout: 5), "the face on \(page) did not open the drawer")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap()
        XCTAssertTrue(app.buttons["sidebar-inbox"].waitForNonExistence(timeout: 5), "the drawer stayed open on \(page)")
    }

    @MainActor func testFaceOpensTheDrawerOnBothPagesWithTheInboxMark() {
        launch(["-demo.on", "YES", "-newAssistant", "YES"])
        let face = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(face.waitForExistence(timeout: 15), "no face on 助手")
        XCTAssertEqual(face.label, "历史对话")
        // The face's state first, then the inbox's mark: Demo Mode's inbox has things that need the user.
        XCTAssertTrue(value(face).hasPrefix("Persona，"), "the face's state: \(value(face))")
        XCTAssertTrue(value(face).contains("收件箱有事等你"), "no inbox mark: \(value(face))")
        openAndCloseDrawer(face, "助手")

        ShellFinger(app).switchTo(agents: true, "Agents tapped after the drawer on 助手")
        let agentsFace = app.buttons["work-sidebar-button"].firstMatch
        XCTAssertTrue(agentsFace.waitForExistence(timeout: 10), "no face on Agents")
        XCTAssertTrue(value(agentsFace).contains("收件箱有事等你"), "no inbox mark on Agents: \(value(agentsFace))")
        openAndCloseDrawer(agentsFace, "Agents")
    }

    /// Nothing in the inbox: the face alone.
    @MainActor func testNoMarkWithAnEmptyInbox() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_EMPTY_INBOX_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_INBOX=empty (PERSONA_EMPTY_INBOX_HUB)")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        launch(["-pairURL", pair, "-newAssistant", "YES"])
        let face = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(face.waitForExistence(timeout: 15), "no face on 助手")
        sleep(3) // the inbox has been fetched
        XCTAssertFalse(value(face).contains("收件箱"), "an inbox mark with nothing in the inbox: \(value(face))")
        openAndCloseDrawer(face, "助手")
    }

}
