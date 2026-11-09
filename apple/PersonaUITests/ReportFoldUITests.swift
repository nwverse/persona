import XCTest

/// Subagents' reports read as one line until opened, against scripts/mock/mock_hub.py with MOCK_REPORT_FOLD=1: a
/// foreground subagent's result in its card, a background one's notice, and its report again as another session's
/// message (its handback); a real peer's message stays whole. Opened stays open when the chat is opened again.
final class ReportFoldUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private let chat = "macbook/a1f3c2d4-0000-4000-8000-0000000c1e07"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        app = XCUIApplication()
    }

    private func launch(fresh: Bool) {
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", chat, "-openAllWork", "YES"] + (fresh ? ["-openChatsFresh", "YES"] : [])
        app.launchPinned()
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "report-\(name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor @discardableResult
    private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 10) -> XCUIElement {
        XCTAssertTrue(e.waitForExistence(timeout: timeout), "missing: \(what)")
        return e
    }

    private func text(_ s: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    /// "Collapse" under an open report (by its words: in a card, the card's id is on everything in it).
    private var collapse: XCUIElement { app.buttons.matching(NSPredicate(format: "label == %@", "收起")).firstMatch }

    @MainActor func testReportsAreOneLineUntilOpenedAndStayOpen() throws {
        launch(fresh: true)
        // The foreground subagent's card, opened: its report is its heading and "Expand".
        let card = see(app.descendants(matching: .any).matching(identifier: "chat.subagent-card").firstMatch, "the subagent card", timeout: 20)
        card.tap()
        see(text("结算回归：42 个用例全部通过"), "the report's heading")
        XCTAssertFalse(text("用时 1 分 48 秒").exists, "folded: the rest waits for Expand")
        // The background one's report as another session's message: one line; a real peer's stays whole.
        see(text("来自 release-bot"), "the peer's message", timeout: 5)
        XCTAssertTrue(text("发版分支已经切好了").exists)
        XCTAssertFalse(text("改动：").exists, "the handback is folded")
        shot("folded")

        // Expand the card's report: the whole of it, and "Collapse".
        let expand = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "展开")).firstMatch
        see(expand, "Expand").tap()
        see(text("用时 1 分 48 秒"), "the whole report")
        see(collapse, "Collapse")
        shot("open")

        // Opened again: still open.
        app.terminate()
        launch(fresh: false)
        see(app.descendants(matching: .any).matching(identifier: "chat.subagent-card").firstMatch, "the subagent card", timeout: 20).tap()
        see(text("用时 1 分 48 秒"), "still open after the chat is opened again")

        collapse.tap()
        expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: text("用时 1 分 48 秒"))
        waitForExpectations(timeout: 5)
    }

    /// The background subagent's notice, opened: its report folded too.
    @MainActor func testABackgroundSubagentsNoticeFoldsItsReport() throws {
        launch(fresh: true)
        let note = see(app.descendants(matching: .any).matching(identifier: "task-note").firstMatch, "the background notice", timeout: 20)
        note.tap()
        see(text("搜索和筛选的回归跑完了"), "the report's first words")
        XCTAssertFalse(text("改动：").exists, "folded")
        shot("notice-folded")
    }
}
