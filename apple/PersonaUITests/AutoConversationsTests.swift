import XCTest

/// Auto Chats in Persona's sidebar (#3264): the conversations Persona opened by itself (Inbox's AI twin, scheduled tasks,
/// Lark /agent…) are not in Recent but in one row, closed at first, that still says one waits for the user; open, a row for
/// each source, which opens All Chats filtered to it. Against scripts/mock/mock_hub.py with
/// MOCK_AUTO_CONVERSATIONS=1 (PERSONA_MOCK_HUB). SHOT_DIR keeps the screenshots (closed, open, the list).
final class AutoConversationsTests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AUTO_CONVERSATIONS=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch() {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-newAssistant", "YES", "-dev.openChat", ""]
        app.launchPinned()
        openSidebar(timeout: 20)
    }

    /// The drawer's button on the phone; the iPad's column is there already.
    @MainActor private func openSidebar(timeout: TimeInterval) {
        let menu = app.buttons["assistant-sidebar-button"].firstMatch
        let group = app.buttons["sidebar-auto"].firstMatch
        let end = Date().addingTimeInterval(timeout)
        while Date() < end && !menu.exists && !group.exists { usleep(250_000) }
        if group.exists && group.isHittable { return }
        XCTAssertTrue(menu.exists, "no sidebar button:\n\(app.debugDescription)")
        sleep(1)
        menu.tapCenter()
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["SHOT_DIR"] {
            let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/auto-\(name)\(suffix).png"))
        }
    }

    @MainActor private func conversation(_ prefix: String) -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-conversation").matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch
    }

    @MainActor func testClosedOpenAndList() {
        launch()
        let group = app.buttons["sidebar-auto"].firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 10), "no 自动对话 row:\n\(app.debugDescription)")
        // The device remembers it open (assistant.autoOpen): an earlier run may have left it so.
        if app.buttons["sidebar-auto-inbox"].exists {
            group.tap()
            XCTAssertTrue(app.buttons["sidebar-auto-inbox"].waitForNonExistence(timeout: 3), "did not close")
        }
        // Closed: the user's own in Recent, Persona's own not; the row says one waits for the user.
        XCTAssertTrue(conversation("帮我约周五").exists, "the user's own conversation is not in 最近")
        XCTAssertFalse(conversation("1105 | 收件箱").exists, "an inbox conversation is still in 最近")
        // Ten with an origin, and the mock's scheduled tasks' own conversations (known the #2838 way).
        XCTAssertTrue(group.label.hasPrefix("自动对话 1"), group.label)
        XCTAssertTrue(group.label.contains("1 个需要你"), group.label)
        XCTAssertFalse(app.buttons["sidebar-auto-inbox"].exists, "open before it was tapped")
        sleep(1)
        shot("closed")

        // Open: a row for each source, and All Chats.
        group.tap()
        let inbox = app.buttons["sidebar-auto-inbox"].firstMatch
        XCTAssertTrue(inbox.waitForExistence(timeout: 3), "no 收件箱 row")
        XCTAssertTrue(inbox.label.hasPrefix("收件箱 6 个，1 个需要你"), inbox.label)
        for id in ["task", "lark_agent", "spawned", "all"] {
            XCTAssertTrue(app.buttons["sidebar-auto-" + id].exists, "no \(id) row")
        }
        sleep(1)
        shot("open")

        // A source's row: All Chats filtered to it.
        inbox.tap()
        let rows = app.buttons.matching(identifier: "auto-conversation")
        XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 5), "the list did not open")
        XCTAssertEqual(rows.count, 6, "收件箱's list")
        XCTAssertTrue(app.buttons["auto-filter-inbox"].isSelected, "收件箱 is not the chosen filter")
        sleep(1)
        shot("list-inbox")
        app.buttons["auto-filter-mine"].firstMatch.tap()
        XCTAssertTrue(app.buttons["auto-filter-mine"].isSelected, "我的 is not the chosen filter")
        XCTAssertTrue(rows.matching(NSPredicate(format: "label BEGINSWITH %@", "帮我约周五")).firstMatch.waitForExistence(timeout: 3), "我的 lacks the user's own")
        XCTAssertFalse(rows.matching(NSPredicate(format: "label CONTAINS %@", "收件箱")).firstMatch.exists, "我的 has an inbox one")
        app.buttons["auto-filter-all"].firstMatch.tap()
        XCTAssertTrue(app.buttons["auto-filter-all"].isSelected, "全部 is not the chosen filter")
        // A list draws only the rows on screen: more than Inbox's 6 is all of them here.
        XCTAssertGreaterThan(rows.count, 6, "全部's list")
        shot("list-all")

        // A row opens its conversation (the sheet and the drawer go).
        rows.matching(NSPredicate(format: "label BEGINSWITH %@", "1105 | 收件箱 | 回复韩梅梅")).firstMatch.tap()
        XCTAssertTrue(rows.firstMatch.waitForNonExistence(timeout: 5), "the list stayed")

        // Back in the sidebar, it closes again.
        openSidebar(timeout: 5)
        XCTAssertTrue(group.waitForExistence(timeout: 5))
        if !app.buttons["sidebar-auto-inbox"].exists { group.tap() }
        XCTAssertTrue(app.buttons["sidebar-auto-inbox"].waitForExistence(timeout: 3))
        group.tap()
        XCTAssertTrue(app.buttons["sidebar-auto-inbox"].waitForNonExistence(timeout: 3), "did not close")
    }

    /// The new shell's drawer: the same group, in its own rows (no marks, rounder).
    @MainActor func testInTheShell() {
        launch()
        let group = app.buttons["sidebar-auto"].firstMatch
        XCTAssertTrue(group.waitForExistence(timeout: 10), "no 自动对话 row in the shell:\n\(app.debugDescription)")
        if !app.buttons["sidebar-auto-inbox"].exists { group.tap() }
        XCTAssertTrue(app.buttons["sidebar-auto-inbox"].waitForExistence(timeout: 3), "did not open")
        sleep(1)
        shot("shell-open")
        group.tap()
        XCTAssertTrue(app.buttons["sidebar-auto-inbox"].waitForNonExistence(timeout: 3), "did not close")
        sleep(1)
        shot("shell-closed")
    }
}
