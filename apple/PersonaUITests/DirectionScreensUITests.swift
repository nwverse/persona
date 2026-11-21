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
        // A semaphore, not an expectation: teardown blocks call this too.
        let done = DispatchSemaphore(value: 0)
        let box = Answer()
        URLSession.shared.dataTask(with: req) { data, _, _ in box.data = data; done.signal() }.resume()
        _ = done.wait(timeout: .now() + 15)
        return box.data
    }

    private func reset(_ query: String = "") {
        get("/mock/down?s=0") // an earlier test's outage never outlives it
        get("/mock/reset")
        get("/mock/direction_sessions?" + query)
    }

    /// Move To…: by its identifier where the menu keeps it (a context menu), by its words where not (a Menu).
    @MainActor private var moveTo: XCUIElement {
        let byID = app.buttons["move-to"].firstMatch
        return byID.exists ? byID : app.buttons[word("Move To…", "移到…", "移動…")].firstMatch
    }

    /// A word of the app in the run's language.
    private func word(_ en: String, _ zh: String, _ ja: String) -> String { lang == "en" ? en : lang == "ja" ? ja : zh }

    /// Launches on the Assistant page, or (`agentChat`, "machine/session") straight into that Agent's chat. The
    /// assistant picked on this device is the hub's default unless `keepPick` (it outlives a launch, `assistant.manager`).
    @MainActor private func launch(agentChat: String? = nil, keepPick: Bool = false) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let place = agentChat.map { ["-app.tab", "chats", "-tab", "dev", "-dev.openChat", $0] }
            ?? ["-tab", "assistant", "-newAssistant", "YES", "-dev.openChat", ""]
        app.launchArguments = ["-pairURL", pair] + place + (keepPick ? [] : ["-assistant.manager", "{}"]) + [
                               // the first lead held open for this launch only (its fold is the device's)
                               "-assistant.direction.open.lead0000-0000-4000-8000-000000000000", "YES",
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
        // The drawer's list is on screen once it is out, however far it was scrolled the last time.
        let head = sidebarList
        let end = Date().addingTimeInterval(30)
        while Date() < end && !(head.exists && head.isHittable) {
            if let menu = app.buttons.matching(identifier: "assistant-sidebar-button").allElementsBoundByIndex.first(where: \.isHittable) {
                menu.tapCenter()
                if head.waitForExistence(timeout: 5) { break }
            } else {
                usleep(500_000)
            }
        }
        XCTAssertTrue(head.exists, "the sidebar did not open:\n\(app.debugDescription)")
    }

    /// Scrolls the sidebar (a lazy list: rows below the fold are not made) until `element` is on screen.
    @MainActor @discardableResult private func reveal(_ element: XCUIElement, rounds: Int = 8) -> Bool {
        // Down the list first, then back up: the drawer keeps where it was scrolled.
        for round in 0..<rounds * 2 {
            if element.exists && element.isHittable { return true }
            if round < rounds { sidebarList.swipeUp(velocity: .slow) } else { sidebarList.swipeDown(velocity: .slow) }
            usleep(400_000)
        }
        return element.exists && element.isHittable
    }

    /// The sidebar's list: on a phone it inherits the drawer's identifier; the iPad's column is the list of its rows.
    @MainActor private var sidebarList: XCUIElement {
        let byID = app.collectionViews.matching(identifier: "assistant-sidebar").firstMatch
        if byID.exists { return byID }
        return app.collectionViews.containing(NSPredicate(format: "identifier BEGINSWITH 'sidebar-'")).firstMatch
    }

    /// The first lead (「Persona 产品」), whose name is mock data in every language.
    @MainActor private func firstLeadToggle() -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-direction-toggle")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "Persona 产品")).firstMatch
    }

    @MainActor private func openFirstLead() {
        let toggle = firstLeadToggle()
        if !toggle.waitForExistence(timeout: 20) || !toggle.isHittable { reveal(toggle) }
        XCTAssertTrue(toggle.exists, "no fold for Persona 产品:\n\(app.debugDescription)")
        if toggle.value as? String != expanded { toggle.tap() }
        XCTAssertTrue(reveal(app.buttons["sidebar-direction-all"].firstMatch),
                      "no All › under the open row:\n\(app.debugDescription)")
    }

    @MainActor private func openPageFromSidebar() {
        openSidebar()
        openFirstLead()
        app.buttons["sidebar-direction-all"].firstMatch.tap()
        XCTAssertTrue(page.waitForExistence(timeout: 10), "All › did not open the page")
    }

    @MainActor private var page: XCUIElement { app.descendants(matching: .any)["direction-page"].firstMatch }

    /// A conversation row by the start of its title, scrolled into view.
    @MainActor private func chat(_ prefix: String) -> XCUIElement {
        let row = app.buttons.matching(identifier: "sidebar-conversation").matching(NSPredicate(format: "label BEGINSWITH %@", prefix)).firstMatch
        if !(row.exists && row.isHittable) { reveal(row) }
        return row
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
        for _ in 0..<8 where !(more.exists && more.isHittable) { app.collectionViews.firstMatch.swipeUp(velocity: .slow) }
        XCTAssertTrue(more.waitForExistence(timeout: 5), "no Show All N with nine added")
        shot("page-more")
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
        // Assistant tapped again opens the conversation's menu on a phone (docs/design/DESIGN_SYSTEM.md 3.5); beside a
        // sidebar column (the iPad) the conversation's title pill over it does (the lead's title in the mock).
        if app.buttons["shell-assistant"].exists {
            app.buttons["shell-assistant"].firstMatch.tap()
        } else {
            app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@ AND NOT (identifier BEGINSWITH 'sidebar')",
                                             "1110 | 探索")).firstMatch.tap()
        }
        // Items of a Menu carry no identifiers: by its words (not the sidebar's All ›, which says the same).
        let item = app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != 'sidebar-direction-all'",
                                                    word("Tasks and Chats", "任务和会话", "タスクとチャット"))).firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no Tasks and Chats in the lead's title menu:\n\(app.debugDescription)")
        shot("title-menu")
        item.tap()
        if !page.waitForExistence(timeout: 10) { shot("page-from-title-missing") }
        XCTAssertTrue(page.exists, "Tasks and Chats did not open the page:\n\(app.debugDescription)")
        sleep(2)
        shot("page-from-title")
    }

    // MARK: Menus, choices, toast, refusal

    @MainActor func testMenuChoicesAndToast() {
        reset()
        launch()
        openSidebar()
        let row = chat("帮我约周五")
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
        let row = chat("帮我约周五")
        XCTAssertTrue(row.waitForExistence(timeout: 10))
        row.press(forDuration: 1.2)
        XCTAssertTrue(app.buttons["move-to"].firstMatch.waitForExistence(timeout: 5))
        app.buttons["move-to"].firstMatch.tap()
        XCTAssertTrue(app.buttons["增长"].firstMatch.waitForExistence(timeout: 5))
        app.buttons["增长"].firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["toast"].firstMatch.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["toast-action"].exists, "a refusal offers no Undo")
        shot("refused")
    }

    /// The Agent menus are one inventory (`AgentActionMenu`, the row's long press and the chat's ⋯): the chat's, on an
    /// Agent filed here, opened from the page.
    @MainActor func testAgentMenu() {
        reset()
        launch()
        openPageFromSidebar()
        let agent = app.buttons.matching(identifier: "direction-page-filed")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "整理首页改版的设计稿")).firstMatch
        XCTAssertTrue(agent.waitForExistence(timeout: 10))
        agent.tap()
        let more = app.buttons["chat.more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 15), "no ⋯ on the Agent's chat:\n\(app.debugDescription)")
        more.tap()
        sleep(1)
        XCTAssertTrue(moveTo.waitForExistence(timeout: 5), "no Move To… on the Agent:\n\(app.debugDescription)")
        shot("agent-menu")
        moveTo.tap()
        sleep(1)
        shot("agent-choices")
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
        addTeardownBlock { [self] in get("/mock/down?s=0") }
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
        guard let off = ProcessInfo.processInfo.environment["PERSONA_OFF_CASE"], !off.isEmpty else {
            throw XCTSkip("PERSONA_OFF_CASE=none|feature|manager|missing")
        }
        reset(off == "feature" ? "feature=off" : off == "missing" ? "routes=missing" : "")
        launch()
        openSidebar()
        if off == "manager" {
            // The drawer's head (the current assistant, 「助手」 in the mock) opens the switcher; on a phone its rows
            // inherit the drawer's identifier, so both are found by their words too.
            let head = app.buttons["assistant-switcher"].exists ? app.buttons["assistant-switcher"].firstMatch
                : app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "助手")).firstMatch
            head.tap()
            let byID = app.buttons["assistant-switcher-row-m-3fa21c"].firstMatch
            let work = byID.waitForExistence(timeout: 3) ? byID
                : app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "工作")).firstMatch
            XCTAssertTrue(work.waitForExistence(timeout: 10), "no 工作 in the switcher:\n\(app.debugDescription)")
            work.tap()
            sleep(3)
            openSidebar()
        }
        if off == "missing" {
            let row = chat("帮我约周五")
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
        let row = off == "manager" ? app.buttons.matching(identifier: "sidebar-conversation").firstMatch : chat("帮我约周五")
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
        // An Agent on no to-do (Move To… would be offered with the feature on): its chat's ⋯, the same inventory as its
        // row's long press (`AgentActionMenu`).
        app.terminate()
        launch(agentChat: "macbook/a1f3c2d4-0000-4000-8000-000000000007", keepPick: off == "manager")
        if off == "manager" { sleep(2) } // the pick of 工作 is kept on the device
        let more = app.buttons["chat.more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 20), "no ⋯ on the Agent's chat:\n\(app.debugDescription)")
        sleep(2)
        if off == "missing" {
            // A launch is a new hello, which forgets the missing routes: one Move To… learns them again (main has none).
            more.tap()
            if moveTo.waitForExistence(timeout: 3) {
                moveTo.tap()
                if app.buttons["增长"].firstMatch.waitForExistence(timeout: 5) { app.buttons["增长"].firstMatch.tap() }
                sleep(8)
            } else {
                dismissMenu(at: CGVector(dx: 0.08, dy: 0.62))
            }
        }
        more.tap()
        sleep(1)
        shot("off-agent-menu-\(off)")
        if off == "manager" { pickDefaultAssistantAgain() }
    }

    /// The pick of 工作 is kept on the device (`assistant.manager`): 助手 again, so the next launch, here or in another
    /// test, starts on the hub's default assistant.
    @MainActor private func pickDefaultAssistantAgain() {
        app.terminate()
        launch(keepPick: true)
        openSidebar()
        let head = app.buttons["assistant-switcher"].exists ? app.buttons["assistant-switcher"].firstMatch
            : app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "工作")).firstMatch
        head.tap()
        let byID = app.buttons["assistant-switcher-row-assistant"].firstMatch
        let main = byID.waitForExistence(timeout: 3) ? byID
            : app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "助手")).firstMatch
        XCTAssertTrue(main.waitForExistence(timeout: 10), "no 助手 in the switcher:\n\(app.debugDescription)")
        main.tap()
        sleep(2)
    }

    /// VoiceOver's Expanded in the run's language.
    private var expanded: String { lang == "en" ? "Expanded" : lang == "ja" ? "展開済み" : "已展开" }

    /// Closes an open menu with a tap outside it: the dimmed page right of the drawer by default.
    @MainActor private func dismissMenu(at point: CGVector = CGVector(dx: 0.92, dy: 0.5)) {
        app.windows.firstMatch.coordinate(withNormalizedOffset: point).tap()
        sleep(1)
    }
}
