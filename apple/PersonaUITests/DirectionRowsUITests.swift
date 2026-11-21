import XCTest

/// A direction row's one activity mark (#6626): a standing (「常驻」) lead spins exactly while something under it runs
/// (the hub's `running`, not its working count) and says in words only what waits on you (「等你 N」), never 「在做 N」;
/// the To-dos page's standing sections end the same way. Against scripts/mock/mock_hub.py with MOCK_LEADERS=1
/// (PERSONA_LEADERS_HUB), whose tree has a lead with an Agent at work (「基础设施」) and one counting a working to-do whose
/// Agent has stopped (「增长」).
/// With the mock started with MOCK_DIRECTION_SESSIONS=1 too (#11516), the tests below turn its `direction_sessions` on
/// and off (`/mock/direction_sessions?feature=`) before each launch: off, a lead's open row and the menus are as before;
/// on, the row lists its live filed sessions and All ›, which opens the page, and Move To… with Undo restores the book.
/// SHOT_DIR keeps the screenshots.
final class DirectionRowsUITests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_LEADERS_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_LEADERS=1 (PERSONA_LEADERS_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ extra: [String] = []) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-newAssistant", "YES", "-dev.openChat", ""] + extra
        app.launchPinned()
    }

    /// The drawer's button on the phone (the hittable one of the pager's pages; tapped again while a busy simulator has
    /// not opened the drawer yet); the iPad's column is there already. Open once its To-dos entry shows: with four
    /// leads the All Chats row is below the fold, and the lazy list does not make it.
    @MainActor private func openSidebar(timeout: TimeInterval = 30) {
        let all = app.buttons["sidebar-todos"].firstMatch
        let end = Date().addingTimeInterval(timeout)
        while Date() < end && !(all.exists && all.isHittable) {
            if let menu = app.buttons.matching(identifier: "assistant-sidebar-button").allElementsBoundByIndex.first(where: \.isHittable) {
                menu.tapCenter()
                if all.waitForExistence(timeout: 5) { break }
            } else {
                usleep(500_000)
            }
        }
        XCTAssertTrue(all.exists, "the sidebar did not open:\n\(app.debugDescription)")
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["SHOT_DIR"] {
            let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/directions-\(name)\(suffix).png"))
        }
    }

    /// A lead's row, by its direction's name (the row's label starts with it).
    @MainActor private func lead(_ name: String) -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-conversation").matching(NSPredicate(format: "label BEGINSWITH %@", name)).firstMatch
    }

    @MainActor func testALeadSpinsOnWhatRunsUnderItAndSaysOnlyWhatWaitsOnYou() {
        launch()
        openSidebar()
        let infra = lead("基础设施")
        XCTAssertTrue(infra.waitForExistence(timeout: 10), "no 基础设施 lead:\n\(app.debugDescription)")
        sleep(2) // the tree's read lands after the conversations
        shot("sidebar") // before the checks, so a failing run still shows what it drew
        // An Agent of one of its to-dos is at work: the row spins, and VoiceOver hears the spinner's word.
        XCTAssertTrue(infra.label.contains("工作中"), "基础设施 has an Agent at work but does not spin: \(infra.label)")
        XCTAssertTrue(infra.label.contains("等你 1"), "基础设施's to-do waiting on you is not said: \(infra.label)")
        // A working to-do whose Agent has stopped: nothing runs, so no spinner (the working count used to spin it).
        let growth = lead("增长")
        XCTAssertTrue(growth.exists, "no 增长 lead")
        XCTAssertFalse(growth.label.contains("工作中"), "增长 spins with nothing running under it: \(growth.label)")
        // One meaning, one expression: no lead says 「在做 N」 beside its spinner.
        let leads = app.buttons.matching(identifier: "sidebar-conversation").allElementsBoundByIndex
        for row in leads where ["基础设施", "增长", "支付对账", "Persona 产品"].contains(where: { row.label.hasPrefix($0) }) {
            XCTAssertFalse(row.label.contains("在做"), "a lead still says 在做: \(row.label)")
        }
    }

    // MARK: Move To… and the page (#11516)

    /// Asks the mock (`scripts/mock/direction_sessions_mock.py`) to answer differently; nil when it does not file sessions.
    @discardableResult private func mock(_ query: String) throws -> [String: Any] {
        let url = URL(string: hub + "/mock/direction_sessions?" + query)!
        var req = URLRequest(url: url)
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        let done = expectation(description: "mock")
        let box = MockAnswer()
        URLSession.shared.dataTask(with: req) { data, response, _ in
            if (response as? HTTPURLResponse)?.statusCode == 200 { box.data = data }
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        guard let data = box.data, let out = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            throw XCTSkip("needs the mock with MOCK_DIRECTION_SESSIONS=1 (scripts/mock/direction_sessions_mock.py)")
        }
        return out
    }

    /// The mock's answer, handed from URLSession's queue to the test.
    private final class MockAnswer: @unchecked Sendable { var data: Data? }

    /// Resets the mock's book and switches, then sets `query`.
    private func mockReset(_ query: String = "") throws {
        let done = expectation(description: "reset")
        var req = URLRequest(url: URL(string: hub + "/mock/reset")!)
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        URLSession.shared.dataTask(with: req) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
        try mock(query)
    }

    /// Opens the first lead's row (「Persona 产品」), which the mock files sessions under.
    @MainActor private func openFirstLead() {
        let toggle = app.buttons.matching(identifier: "sidebar-direction-toggle")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "Persona 产品")).firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no fold for Persona 产品:\n\(app.debugDescription)")
        if toggle.value as? String != "已展开" { toggle.tap() }
        sleep(1)
    }

    @MainActor func testWithoutTheFeatureTheRowAndTheMenusAreAsBefore() throws {
        try mockReset("feature=off")
        launch()
        openSidebar()
        openFirstLead()
        shot("off-row")
        XCTAssertFalse(app.buttons["sidebar-direction-all"].exists, "All › without direction_sessions")
        XCTAssertFalse(app.buttons["sidebar-direction-filed"].exists, "filed rows without direction_sessions")
        let chat = app.buttons.matching(identifier: "sidebar-conversation")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "帮我约周五")).firstMatch
        XCTAssertTrue(chat.waitForExistence(timeout: 10))
        chat.press(forDuration: 1.2)
        XCTAssertTrue(app.buttons["Copy Diagnostics"].waitForExistence(timeout: 5) || app.buttons["复制诊断信息"].exists)
        shot("off-menu")
        XCTAssertFalse(app.buttons["move-to"].exists, "Move To… without direction_sessions")
    }

    @MainActor func testTheRowListsItsFiledSessionsAndAllOpensThePage() throws {
        try mockReset()
        launch()
        openSidebar()
        openFirstLead()
        let all = app.buttons["sidebar-direction-all"].firstMatch
        XCTAssertTrue(all.waitForExistence(timeout: 10), "no All › under Persona 产品:\n\(app.debugDescription)")
        XCTAssertGreaterThanOrEqual(app.buttons.matching(identifier: "sidebar-direction-filed").count, 2,
                                    "the live filed sessions are listed")
        shot("on-row")
        all.tap()
        let page = app.descendants(matching: .any)["direction-page"].firstMatch
        XCTAssertTrue(page.waitForExistence(timeout: 10), "All › did not open the page")
        XCTAssertTrue(app.navigationBars["Persona 产品"].waitForExistence(timeout: 5), "the page is titled by its name")
        XCTAssertTrue(app.descendants(matching: .any)["direction-page-in-progress"].waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons.matching(identifier: "direction-page-filed").firstMatch.waitForExistence(timeout: 10))
        shot("page")
        // ‹ goes back to the lead conversation, not to the drawer
        app.navigationBars.buttons.firstMatch.tap()
        XCTAssertTrue(page.waitForNonExistence(timeout: 5))
        XCTAssertFalse(app.buttons["sidebar-direction-all"].isHittable, "back at the conversation, the drawer closed")
    }

    @MainActor func testMoveToThenUndoRestoresTheBook() throws {
        try mockReset()
        let key = "server/b2e4d6f8-0000-4000-8000-000000000002" // 「早报 · 周三」, in no direction
        let before = (try mock("")["book"] as? [String: Any])?[key]
        XCTAssertNil(before, "the chat starts in no direction")
        launch()
        openSidebar()
        let chat = app.buttons.matching(identifier: "sidebar-conversation")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "早报")).firstMatch
        XCTAssertTrue(chat.waitForExistence(timeout: 10), "no 早报 chat:\n\(app.debugDescription)")
        chat.press(forDuration: 1.2)
        let move = app.buttons["move-to"].firstMatch
        XCTAssertTrue(move.waitForExistence(timeout: 5), "no Move To…:\n\(app.debugDescription)")
        shot("menu")
        move.tap()
        let choice = app.buttons["增长"].firstMatch
        XCTAssertTrue(choice.waitForExistence(timeout: 5))
        shot("choices")
        choice.tap()
        let undo = app.buttons["toast-action"].firstMatch
        XCTAssertTrue(undo.waitForExistence(timeout: 10), "no Undo on the toast")
        shot("toast")
        let filed = (try mock("")["book"] as? [String: [String: Any]])?[key]?["direction"] as? String
        XCTAssertEqual(filed, "lead0002-0000-4000-8000-000000000002")
        undo.tap()
        sleep(2)
        XCTAssertNil((try mock("")["book"] as? [String: Any])?[key], "Undo of a first filing takes the entry out (null)")
    }

    @MainActor func testTheToDosPinnedSectionsEndAsTheirRowsDo() {
        // The list view whatever an earlier test left (the calendar keeps itself per device).
        launch(["-todos.grouping", "direction", "-todos.view", "list"])
        openSidebar()
        app.buttons["sidebar-todos"].firstMatch.tap()
        let header = app.descendants(matching: .any).matching(identifier: "todos.direction").firstMatch
        XCTAssertTrue(header.waitForExistence(timeout: 15), "the To-dos page is not grouped by 常驻:\n\(app.debugDescription)")
        sleep(2)
        shot("todos")
        // A list builds a row, and its section's header, only once it is on screen, and 「增长」 sits below the fold on a
        // phone: read the headers while scrolling down to it.
        var labels: [String] = []
        let list = app.collectionViews.firstMatch
        for round in 0..<8 {
            for h in app.descendants(matching: .any).matching(identifier: "todos.direction").allElementsBoundByIndex where !labels.contains(h.label) {
                labels.append(h.label)
            }
            if labels.contains(where: { $0.hasPrefix("增长") }) || round == 7 { break }
            list.swipeUp(velocity: .slow)
        }
        XCTAssertFalse(labels.isEmpty)
        for label in labels {
            XCTAssertFalse(label.contains("在做"), "a 常驻 section still says 在做: \(label)")
        }
        // the spinner exactly where something runs: 「基础设施」 has an Agent at work, 「增长」's working to-do has none
        let infra = labels.first { $0.hasPrefix("基础设施") }
        XCTAssertTrue(infra?.contains("工作中") == true, "基础设施's section does not spin: \(infra ?? "missing")")
        let growth = labels.first { $0.hasPrefix("增长") }
        XCTAssertTrue(growth.map { !$0.contains("工作中") } ?? false, "增长's section spins or is missing: \(growth ?? "missing")")
    }
}
