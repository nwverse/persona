import XCTest

/// Every state of Move To… and a direction's page (#11516), each checked as it is drawn and kept as a screenshot for
/// the PR: the row with its filed sessions and All ›, the menus, the choices, the toast with Undo, a refusal, the page
/// with the standings, the read still out, the hub out of reach, the read failing, `sessions_unavailable`, an ended and
/// a removed direction, and the Unpin confirmation. Against scripts/mock/mock_hub.py with MOCK_LEADERS=1 and
/// MOCK_DIRECTION_SESSIONS=1 (PERSONA_LEADERS_HUB). Runs only when asked (PERSONA_DIRECTION_SHOTS=1), in the language
/// PERSONA_SHOT_LANG names (en by default); SHOT_DIR keeps the screenshots, SHOT_SUFFIX names the run (appearance,
/// text size, device). The simulator's appearance and text size are the run's (`xcrun simctl ui`).
final class DirectionScreensUITests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!
    private var lang: String { ProcessInfo.processInfo.environment["PERSONA_SHOT_LANG"] ?? "en" }

    override func setUpWithError() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        guard env["PERSONA_DIRECTION_SHOTS"] == "1", let hub = env["PERSONA_LEADERS_HUB"] else {
            throw XCTSkip("screenshots only: PERSONA_DIRECTION_SHOTS=1 with the mock (PERSONA_LEADERS_HUB)")
        }
        self.hub = hub
    }

    // MARK: Helpers

    private final class Answer: @unchecked Sendable { var data: Data? }

    @discardableResult private func get(_ path: String) -> Data? {
        var req = URLRequest(url: URL(string: hub + path)!)
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        let done = expectation(description: path)
        let box = Answer()
        URLSession.shared.dataTask(with: req) { data, _, _ in box.data = data; done.fulfill() }.resume()
        wait(for: [done], timeout: 15)
        return box.data
    }

    private func reset(_ query: String = "") {
        get("/mock/reset")
        get("/mock/direction_sessions?" + query)
    }

    @MainActor private func launch() {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-newAssistant", "YES", "-dev.openChat", "",
                               "-AppleLanguages", "(\(lang))", "-AppleLocale", lang == "en" ? "en_US" : lang == "ja" ? "ja_JP" : "zh_CN"]
        app.launchPinned()
    }

    @MainActor private func shot(_ name: String) {
        let png = app.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["SHOT_DIR"] else { return }
        let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
        try? png.pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/\(name)-\(lang)\(suffix).png"))
    }

    @MainActor private func openSidebar() {
        let todos = app.buttons["sidebar-todos"].firstMatch
        let end = Date().addingTimeInterval(30)
        while Date() < end && !(todos.exists && todos.isHittable) {
            if let menu = app.buttons.matching(identifier: "assistant-sidebar-button").allElementsBoundByIndex.first(where: \.isHittable) {
                menu.tapCenter()
                if todos.waitForExistence(timeout: 5) { break }
            } else {
                usleep(500_000)
            }
        }
        XCTAssertTrue(todos.exists, "the sidebar did not open:\n\(app.debugDescription)")
    }

    /// The first lead (「Persona 产品」), whose name is mock data in every language.
    @MainActor private func firstLeadToggle() -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-direction-toggle")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "Persona 产品")).firstMatch
    }

    @MainActor private func openFirstLead() {
        let toggle = firstLeadToggle()
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), "no fold for Persona 产品:\n\(app.debugDescription)")
        if !app.buttons["sidebar-direction-all"].exists { toggle.tap() }
        XCTAssertTrue(app.buttons["sidebar-direction-all"].firstMatch.waitForExistence(timeout: 10),
                      "no All › under the open row:\n\(app.debugDescription)")
    }

    @MainActor private func openPageFromSidebar() {
        openSidebar()
        openFirstLead()
        app.buttons["sidebar-direction-all"].firstMatch.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 10), "All › did not open the page")
    }

    @MainActor private var page: XCUIElement { app.descendants(matching: .any)["direction-page"].firstMatch }

    @MainActor private func chat(_ prefix: String) -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-conversation").matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch
    }

    // MARK: The row, the page and the standings

    @MainActor func testRowPageAndStandings() {
        reset()
        launch()
        openSidebar()
        openFirstLead()
        XCTAssertGreaterThanOrEqual(app.buttons.matching(identifier: "sidebar-direction-filed").count, 2)
        sleep(1)
        shot("row")
        app.buttons["sidebar-direction-all"].firstMatch.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons.matching(identifier: "direction-page-filed").firstMatch.waitForExistence(timeout: 10))
        sleep(1)
        shot("page")
        let more = app.buttons["direction-page-more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 5), "no Show All N with nine added")
        more.tap()
        sleep(1)
        app.collectionViews.firstMatch.swipeUp(velocity: .slow)
        sleep(1)
        XCTAssertEqual(app.buttons.matching(identifier: "direction-page-filed").count >= 5, true)
        shot("standings")
        app.collectionViews.firstMatch.swipeUp(velocity: .slow)
        sleep(1)
        shot("standings-end")
    }

    @MainActor func testTitleMenuEntry() {
        reset()
        launch()
        openSidebar()
        chat("Persona 产品").tap()
        sleep(2)
        // Assistant tapped again opens the conversation's menu (docs/design/DESIGN_SYSTEM.md 3.5).
        app.buttons["shell-assistant"].firstMatch.tap()
        let item = app.buttons["assistant-direction-page"].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no Tasks and Chats in the lead's title menu:\n\(app.debugDescription)")
        shot("title-menu")
        item.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 10))
        sleep(2)
        shot("page-from-title")
    }

    // MARK: Menus, choices, toast, refusal

    @MainActor func testMenuChoicesAndToast() {
        reset()
        launch()
        openSidebar()
        let row = chat("早报")
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.press(forDuration: 1.2)
        let move = app.buttons["move-to"].firstMatch
        XCTAssertTrue(move.waitForExistence(timeout: 5), "no Move To…:\n\(app.debugDescription)")
        shot("menu")
        move.tap()
        let growth = app.buttons["增长"].firstMatch
        XCTAssertTrue(growth.waitForExistence(timeout: 5))
        sleep(1)
        shot("choices")
        growth.tap()
        XCTAssertTrue(app.buttons["toast-action"].firstMatch.waitForExistence(timeout: 10), "no Undo")
        shot("toast")
    }

    @MainActor func testRefusalSaysTheHubsSentence() {
        reset("refuse=direction_full")
        launch()
        openSidebar()
        let row = chat("早报")
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.press(forDuration: 1.2)
        app.buttons["move-to"].firstMatch.tap()
        app.buttons["增长"].firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["toast"].firstMatch.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["toast-action"].exists, "a refusal offers no Undo")
        shot("refused")
    }

    @MainActor func testAgentMenu() {
        reset()
        launch()
        let agents = app.buttons["shell-agents"].firstMatch
        if agents.waitForExistence(timeout: 10) { agents.tap() }
        let row = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "菜谱网站加搜索")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), "no 菜谱 Agent:\n\(app.debugDescription)")
        row.press(forDuration: 1.2)
        XCTAssertTrue(app.buttons["move-to"].firstMatch.waitForExistence(timeout: 5), "no Move To… on the Agent:\n\(app.debugDescription)")
        shot("agent-menu")
    }

    @MainActor func testUnpinConfirmation() {
        reset()
        launch()
        openSidebar()
        let lead = chat("Persona 产品")
        XCTAssertTrue(lead.waitForExistence(timeout: 10))
        lead.press(forDuration: 1.2)
        let unpin = app.buttons.matching(NSPredicate(format: "label IN %@", ["Unpin", "取消常驻", "常駐をやめる"])).firstMatch
        XCTAssertTrue(unpin.waitForExistence(timeout: 5), "no Unpin:\n\(app.debugDescription)")
        unpin.tap()
        sleep(1)
        shot("unpin")
    }

    // MARK: The page's reads

    @MainActor func testReadStillOut() {
        reset("delay=12")
        launch()
        openPageFromSidebar()
        XCTAssertTrue(app.descendants(matching: .any)["direction-page-reading"].firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.descendants(matching: .any)["direction-page-in-progress"].exists, "In Progress before the read")
        shot("reading")
    }

    @MainActor func testNeverReadAndUnavailable() {
        reset("read=fail")
        launch()
        openPageFromSidebar()
        XCTAssertTrue(app.buttons["direction-page-retry"].firstMatch.waitForExistence(timeout: 10), "no Retry")
        shot("unread")
        reset("sessions_unavailable=1")
        app.buttons["direction-page-retry"].firstMatch.tap()
        sleep(2)
        XCTAssertTrue(app.buttons["direction-page-retry"].firstMatch.exists)
        shot("sessions-unavailable")
    }

    @MainActor func testOfflineAfterAnEarlierRead() {
        reset()
        launch()
        openPageFromSidebar()
        XCTAssertTrue(app.buttons.matching(identifier: "direction-page-filed").firstMatch.waitForExistence(timeout: 10))
        app.navigationBars.buttons.firstMatch.tap()
        sleep(1)
        get("/mock/down?s=90")
        sleep(6)
        openPageFromSidebar()
        sleep(3)
        shot("offline")
        get("/mock/down?s=0")
    }

    @MainActor func testEndedAndGone() {
        reset("ended=lead0000-0000-4000-8000-000000000000")
        launch()
        openPageFromSidebar()
        XCTAssertTrue(app.descendants(matching: .any)["direction-page-ended"].firstMatch.waitForExistence(timeout: 10))
        sleep(1)
        shot("ended")
        reset("read=gone")
        app.navigationBars.buttons.firstMatch.tap()
        sleep(1)
        openPageFromSidebar()
        sleep(3)
        shot("gone")
    }

    // MARK: Nothing new where the change is off

    /// Acceptance 1 and 2: in each case where nothing of #11516 may be drawn, the sidebar (the first lead open), a
    /// conversation's menu, the title menu and an Agent's menu, kept for comparison with the same run of a build of
    /// main. Only identifiers that exist there too, so this file runs unchanged on that build. PERSONA_OFF_CASE:
    /// `none` (a hub with no directions: the mock with MOCK_LEADERS=0), `feature` (no `direction_sessions`), `manager`
    /// (another manager's page) or `missing` (the routes answer 404 without their codes: one Move To… learns it first).
    @MainActor func testOffCasesLookAsBefore() throws {
        guard let off = ProcessInfo.processInfo.environment["PERSONA_OFF_CASE"] else {
            throw XCTSkip("PERSONA_OFF_CASE=none|feature|manager|missing")
        }
        reset(off == "feature" ? "feature=off" : off == "missing" ? "routes=missing" : "")
        launch()
        openSidebar()
        if off == "manager" {
            app.buttons["assistant-switcher"].firstMatch.tap()
            let work = app.buttons["assistant-switcher-row-m-3fa21c"].firstMatch
            XCTAssertTrue(work.waitForExistence(timeout: 10), "no 工作 in the switcher:\n\(app.debugDescription)")
            work.tap()
            sleep(3)
            openSidebar()
        }
        if off == "missing" {
            let row = chat("早报")
            if row.waitForExistence(timeout: 10) {
                row.press(forDuration: 1.2)
                if app.buttons["move-to"].firstMatch.waitForExistence(timeout: 3) {
                    app.buttons["move-to"].firstMatch.tap()
                    app.buttons["增长"].firstMatch.tap()
                    sleep(8) // the toast goes; every entry has hidden
                } else {
                    dismissMenu()
                }
            }
        }
        let toggle = firstLeadToggle()
        if toggle.waitForExistence(timeout: 8), toggle.value as? String != expanded { toggle.tap() }
        sleep(2)
        shot("off-sidebar-\(off)")
        let row = off == "manager" ? app.buttons.matching(identifier: "sidebar-conversation").firstMatch : chat("早报")
        if row.waitForExistence(timeout: 5) {
            row.press(forDuration: 1.2)
            sleep(1)
            shot("off-chat-menu-\(off)")
            dismissMenu()
        }
        let lead = chat("Persona 产品")
        if lead.exists { lead.tap() } else { app.buttons["sidebar-main"].firstMatch.tap() }
        sleep(2)
        app.buttons["shell-assistant"].firstMatch.tap()
        sleep(1)
        shot("off-title-menu-\(off)")
        dismissMenu()
        app.buttons["shell-agents"].firstMatch.tap()
        let agent = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "菜谱网站加搜索")).firstMatch
        XCTAssertTrue(agent.waitForExistence(timeout: 15))
        agent.press(forDuration: 1.2)
        sleep(1)
        shot("off-agent-menu-\(off)")
    }

    /// VoiceOver's Expanded in the run's language.
    private var expanded: String { lang == "en" ? "Expanded" : lang == "ja" ? "展開" : "已展开" }

    @MainActor private func dismissMenu() {
        app.windows.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.04)).tap()
        sleep(1)
    }
}
