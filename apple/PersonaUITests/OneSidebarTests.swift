import XCTest

/// One sidebar at regular width (#4206): iPad and the open iPhone Duo, in both postures, show Persona's places, Pinned
/// and All Chats (as every client lists Persona's chats, #4610) and the Agents list in one column, with Persona's page or
/// the agent's chat beside it, and no Assistant | Agents switch.
///
/// - the column's header (Persona's mark and name, the fold) and foot (New Chat; search and Settings);
/// - More opens inline to Messages (when on), Recordings, Health and Machines;
/// - Pinned: the directions under their names with their work; All Chats over the window;
/// - the grouping's segments under the Agents heading, one tap each, the same choice as the ⋯ menu's;
/// - an agent opened from the list beside Persona's page, its row the selected one; a Persona row closes it;
/// - upright on the open Duo the column is over the page, and a pick that changes the page slides it away while the
///   filters and groupings leave it (#4852); ‹ on an agent opened from it goes back to Persona's page (#3641).
///
/// Against scripts/mock/mock_hub.py started with MOCK_LEADERS=1 (PERSONA_MOCK_HUB). On the iPad simulator, or the Duo simulator opened in Xcode's
/// Device Hub, on its side (TEST_RUNNER_PERSONA_ORIENTATION=landscape) or upright; the closed Duo and other iPhones skip.
/// Taps and long presses only (upright, synthetic drags miss on the open Duo). TEST_RUNNER_PERSONA_HOLD=<seconds> holds
/// each state, named in an activity `one-sidebar <name>`, for `simctl io screenshot` (`--display=3` on the Duo).
final class OneSidebarTests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        XCUIDevice.shared.orientation = ProcessInfo.processInfo.environment["PERSONA_ORIENTATION"] == "landscape" ? .landscapeLeft : .portrait
        app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-app.tab", "assistant", "-dev.openChat", "",
                               "-dev.grouping", "repo", "-agents.list.closed", "NO"]
    }

    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    private var pair: String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    /// Launches and waits for the column; skips where the layout is not the one sidebar (a phone at compact width).
    /// Upright on the open Duo the column may start away over the page: brought out from the page's button.
    @MainActor private func launch(_ extra: [String] = []) throws {
        app.launchArguments += extra
        app.launchPinned()
        let main = element("sidebar-main"), back = element("assistant-sidebar-button")
        XCTAssertTrue(main.waitForExistence(timeout: 20) || back.waitForExistence(timeout: 5), "no main row:\n\(app.debugDescription)")
        guard app.windows.firstMatch.frame.width > 600 else { throw XCTSkip("regular width only (iPad, iPhone Duo open)") }
        bringColumnOut()
    }

    /// The column out, from the page's button if it is away.
    @MainActor private func bringColumnOut() {
        let main = element("sidebar-main")
        // The column can take a moment to take touches after a launch (seen on the iPad).
        if main.waitForHittable(timeout: 5) { return }
        // Persona's page and Messages have their own button; a page pushed in Persona's stack (the Inbox) the system's.
        let back = app.buttons.matching(NSPredicate(format: "identifier IN %@ OR label IN %@", ["assistant-sidebar-button", "feishu-sidebar-button"],
                                                    ["显示边栏", "Show Sidebar"])).firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 5) && back.isHittable, "neither the column nor its button:\n\(app.debugDescription)")
        back.tap()
        XCTAssertTrue(main.waitForHittable(timeout: 5), "the column did not come out:\n\(app.debugDescription)")
    }

    /// The column slid away over the page (upright): its rows out of reach, the page's button back.
    @MainActor private func assertColumnAway(_ after: String, file: StaticString = #filePath, line: UInt = #line) {
        XCTAssertTrue(element("sidebar-main").waitForNotHittable(timeout: 5), "the column stayed after \(after):\n\(app.debugDescription)",
                      file: file, line: line)
    }

    /// The column still out a moment after a tap that changes no page.
    @MainActor private func assertColumnStays(_ after: String, file: StaticString = #filePath, line: UInt = #line) {
        sleep(1) // longer than the slide (`Theme.Motion.layout`)
        XCTAssertTrue(element("sidebar-main").isHittable, "the column went after \(after)", file: file, line: line)
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// The column's width: its list, the collection view at the window's leading edge (as SplitListTests reads it).
    @MainActor private func columnWidth() -> CGFloat {
        let window = app.windows.firstMatch.frame
        let list = app.collectionViews.allElementsBoundByIndex.first { $0.frame.minX < window.minX + 40 && $0.frame.width < window.width * 0.7 }
        return list?.frame.width ?? 0
    }

    /// The buttons in the window's top leading corner that fold the column or bring it back, the page's own or the
    /// system's, that can be tapped there.
    @MainActor private func columnButtons() -> [XCUIElement] {
        let window = app.windows.firstMatch.frame
        return app.buttons.matching(NSPredicate(format: "identifier IN %@ OR label IN %@",
                                                ["assistant-sidebar-button", "assistant-sidebar-collapse", "feishu-sidebar-button"],
                                                ["显示边栏", "隐藏边栏", "Show Sidebar", "Hide Sidebar"]))
            .allElementsBoundByIndex
            .filter { $0.isHittable && $0.frame.minX >= window.minX - 1 && $0.frame.midX < window.midX && $0.frame.midY < window.minY + 200 }
    }

    /// One button for the column at the top corner, a moment after the column moved.
    @MainActor private func assertOneColumnButton(_ state: String, file: StaticString = #filePath, line: UInt = #line) {
        sleep(1) // longer than the slide (`Theme.Motion.layout`)
        let found = columnButtons()
        XCTAssertEqual(found.count, 1, "\(state): not one button for the column at the top corner: "
                       + "\(found.map { "\($0.identifier) “\($0.label)” \($0.frame)" })\n\(app.debugDescription)", file: file, line: line)
    }

    /// The open Duo held on its side: the system's bars down the side.
    @MainActor private var duoOnItsSide: Bool {
        let w = app.windows.firstMatch.frame
        return UIDevice.current.userInterfaceIdiom == .phone && w.width > w.height
    }

    /// The open Duo held upright: the column slides in over the page (#4852).
    @MainActor private var duoUpright: Bool {
        let w = app.windows.firstMatch.frame
        return UIDevice.current.userInterfaceIdiom == .phone && w.height > w.width
    }

    @MainActor private func shot(_ name: String) {
        XCTContext.runActivity(named: "one-sidebar \(name)") { _ in
            if let hold = ProcessInfo.processInfo.environment["PERSONA_HOLD"].flatMap(UInt32.init) { sleep(hold) }
        }
    }

    /// The column top to bottom (header, the places, More, Pinned and All Chats, the Agents heading and its segments, the
    /// list) and the foot; no switch and no tab bar anywhere; no Recent (#4610).
    @MainActor func testTheColumnHasThePlacesPinnedAndAgents() throws {
        try launch()
        let window = app.windows.firstMatch.frame
        XCTAssertEqual(app.tabBars.count, 0, "a tab bar is still there:\n\(app.debugDescription)")
        XCTAssertFalse(element("shell-assistant").exists || element("page-tab-switch").exists, "the Assistant | Agents switch is still there")
        let collapse = element("assistant-sidebar-collapse"), main = element("sidebar-main"), inbox = element("sidebar-inbox")
        let more = element("sidebar-more"), pinned = element("sidebar-pinned-heading"), all = element("sidebar-all-chats")
        let heading = element("sidebar-agents-heading")
        for (name, e) in [("fold", collapse), ("Inbox", inbox), ("More", more), ("Pinned", pinned), ("All Chats", all), ("Agents heading", heading)] {
            XCTAssertTrue(e.exists, "no \(name) in the column:\n\(app.debugDescription)")
        }
        XCTAssertLessThan(collapse.frame.maxY, main.frame.minY + 1, "the fold is not over the places")
        XCTAssertLessThan(main.frame.minY, inbox.frame.minY, "Main Chat is not first")
        XCTAssertLessThan(inbox.frame.minY, more.frame.minY)
        XCTAssertLessThan(more.frame.minY, pinned.frame.minY, "Pinned is not under the places")
        XCTAssertLessThan(pinned.frame.minY, all.frame.minY, "All Chats is not under Pinned")
        XCTAssertLessThan(all.frame.minY, heading.frame.minY, "the Agents are not under All Chats")
        XCTAssertFalse(element("sidebar-recent-fold").exists, "Recent is still there (#4610)")
        // The directions under their names, with their work (the direction tree's counts).
        let leads = app.buttons.matching(identifier: "sidebar-pinned")
        XCTAssertEqual(leads.count, 4, "not the mock hub's four directions (MOCK_LEADERS=1):\n\(app.debugDescription)")
        XCTAssertTrue(leads.element(boundBy: 0).label.hasPrefix("Persona 产品"), "a direction not under its name: \(leads.element(boundBy: 0).label)")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "identifier == %@ AND label CONTAINS %@", "sidebar-pinned", "等你")).firstMatch.exists,
                      "no direction shows its work")
        XCTAssertLessThan(main.frame.maxX, window.midX + 100, "the column is not on the left")
        // As wide as ChatGPT's on the open Duo, 320 pt in both postures (`Theme.Size.sidebarPhone`); the iPad's is wider.
        let width = columnWidth()
        if duoUpright || duoOnItsSide {
            XCTAssertEqual(width, 320, accuracy: 4, "the open Duo's column is not 320 pt wide")
        } else {
            XCTAssertGreaterThanOrEqual(width, 320, "the iPad's column is narrower than the Duo's")
        }
        // Beside the page on the iPad and the open Duo on its side, the fold and the page's top on one line (2026-11-11,
        // the owner). Upright on the open Duo the column is over the page instead (#4852): the page keeps its full width.
        let pageNew = element("assistant-new")
        XCTAssertTrue(pageNew.exists, "no New Chat on the page")
        if duoUpright {
            XCTAssertGreaterThan(pageNew.frame.maxX, window.maxX - 80, "the page is not its full width under the column")
        } else {
            let face = app.buttons.matching(NSPredicate(format: "identifier IN %@", ["assistant-menu", "persona-avatar"])).firstMatch
            XCTAssertTrue(face.exists, "no face on the page:\n\(app.debugDescription)")
            XCTAssertGreaterThan(face.frame.midX, collapse.frame.maxX + 100, "the page is under the column, not beside it")
            XCTAssertEqual(face.frame.minY, collapse.frame.minY, accuracy: 2, "the fold and the page's face do not start on one line")
            if duoOnItsSide {
                // #4853: nothing above the columns under side bars, so a margin keeps the mark clear of the rounded corner.
                XCTAssertGreaterThanOrEqual(collapse.frame.minY, window.minY + 16, "the column's header is at the display's edge: \(collapse.frame)")
                // #4854: New Chat in the system's side column at the window's trailing edge, under its status items.
                XCTAssertGreaterThan(pageNew.frame.minX, face.frame.maxX + 100, "New Chat is not at the trailing edge: \(pageNew.frame) in \(window)")
                XCTAssertGreaterThan(pageNew.frame.minY, collapse.frame.maxY, "New Chat is not under the side column's status items: \(pageNew.frame)")
            } else {
                XCTAssertEqual(pageNew.frame.midY, collapse.frame.midY, accuracy: 2, "the fold and the page's New Chat are not on one line")
            }
        }
        // The rarer places wait under More.
        XCTAssertFalse(element("sidebar-machines").exists, "Machines shows before More is opened")
        // The foot: New Chat on the left, search and Settings on the right, in one row at the column's foot.
        let new = element("sidebar-new"), search = element("sidebar-search"), settings = element("sidebar-settings")
        XCTAssertTrue(new.exists && search.exists && settings.exists, "no New Chat, search or Settings at the foot")
        XCTAssertEqual(new.frame.midY, settings.frame.midY, accuracy: 4, "New Chat and Settings not in one row")
        XCTAssertLessThan(new.frame.maxX, search.frame.minX)
        XCTAssertGreaterThan(new.frame.minY, window.maxY - 160, "the foot is not at the column's foot")
        shot("home")
        // Folding the column and bringing it back from the page's button: its own, or the system's under side bars.
        collapse.tap()
        let back = app.buttons.matching(NSPredicate(format: "identifier == %@ OR label IN %@", "assistant-sidebar-button",
                                                    ["显示边栏", "Show Sidebar"])).firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 5) && back.isHittable, "nothing on the page brings the column back")
        shot("folded")
        back.tap()
        XCTAssertTrue(collapse.waitForExistence(timeout: 5), "the column did not come back")
    }

    /// One button for the column at a time, out or folded, on Persona's page and on Messages (2026-11-12, the owner: on
    /// the open Duo on its side the system's Show Sidebar and the page's own both showed, one over the other).
    @MainActor func testOneButtonForTheColumnAtATime() throws {
        try launch(["-integrations.lark.enabled", "YES"])
        let collapse = element("assistant-sidebar-collapse"), main = element("sidebar-main")
        assertOneColumnButton("Persona's page, the column out")
        shot("one-button-out")
        collapse.tap()
        XCTAssertTrue(main.waitForNotHittable(timeout: 5), "the column did not fold")
        assertOneColumnButton("Persona's page, the column folded")
        shot("one-button-folded")
        bringColumnOut()
        assertOneColumnButton("Persona's page, the column out again")
        // Messages has a button of its own for the column (`OneSidebarHome`).
        element("sidebar-more").tap()
        let messages = element("sidebar-messages")
        XCTAssertTrue(messages.waitForExistence(timeout: 10), "no Messages under More:\n\(app.debugDescription)")
        messages.tap()
        XCTAssertTrue(element("feishu-conversations").waitForExistence(timeout: 10), "Messages did not open:\n\(app.debugDescription)")
        if !duoUpright { // upright the pick slid the column away already (#4852)
            assertOneColumnButton("Messages, the column out")
            collapse.tap()
            XCTAssertTrue(main.waitForNotHittable(timeout: 5), "the column did not fold")
        }
        assertOneColumnButton("Messages, the column folded")
    }

    /// More opens inline to Recordings, Health and Machines, and folds again; a place opens beside the column.
    @MainActor func testMoreOpensInlineAndAPlaceOpensBeside() throws {
        try launch()
        let more = element("sidebar-more")
        more.tap()
        let machines = element("sidebar-machines"), health = element("sidebar-health"), recordings = element("sidebar-recordings")
        XCTAssertTrue(machines.waitForExistence(timeout: 5) && health.exists && recordings.exists, "More did not open:\n\(app.debugDescription)")
        XCTAssertGreaterThan(recordings.frame.minY, more.frame.minY, "More's places are not under it")
        shot("more")
        more.tap()
        XCTAssertTrue(machines.waitForNonExistence(timeout: 5), "More did not fold")
        let inbox = element("sidebar-inbox")
        inbox.tap()
        let page = element("inbox.more")
        XCTAssertTrue(page.waitForExistence(timeout: 10), "the Inbox did not open:\n\(app.debugDescription)")
        if duoUpright {
            assertColumnAway("the Inbox") // over the page, it slides away (#4852)
        } else {
            XCTAssertGreaterThan(page.frame.minX, inbox.frame.maxX - 1, "the Inbox is not beside the column")
            XCTAssertTrue(inbox.isHittable, "the column went with the Inbox")
        }
    }

    /// The segments under the Agents heading pick the grouping, as the ⋯ menu does (`dev.grouping`): By Repository is
    /// the one lit at launch, and Machine lights when tapped. In a column too narrow for the five (the open Duo on its
    /// side) the phone's grouping menu stands in, saying By Repository.
    @MainActor func testTheSegmentsPickTheGrouping() throws {
        try launch()
        let segments = element("agents.grouping.segments"), pill = element("agents.grouping")
        // On its side the column is short: the grouping is under Pinned, below its first screen, and not built until
        // scrolled to. A short drag, so the heading above it stays in.
        let column = app.collectionViews.firstMatch
        for _ in 0..<3 where !(segments.waitForExistence(timeout: 3) || pill.exists) {
            let from = column.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.8))
            from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -200)))
        }
        if pill.exists && !segments.exists {
            XCTAssertGreaterThan(pill.frame.minY, element("sidebar-agents-heading").frame.minY, "the grouping is not under the heading")
            XCTAssertTrue(pill.label.contains("按仓库"), "the grouping menu does not say By Repository: \(pill.label)")
            shot("segments")
            return
        }
        XCTAssertTrue(segments.waitForExistence(timeout: 10), "no grouping segments:\n\(app.debugDescription)")
        XCTAssertGreaterThan(segments.frame.minY, element("sidebar-agents-heading").frame.minY, "the segments are not under the heading")
        XCTAssertFalse(pill.exists, "the phone's grouping pill shows beside the segments")
        let repo = segments.buttons["仓库"].firstMatch, machine = segments.buttons["机器"].firstMatch
        XCTAssertTrue(repo.exists && machine.exists, "no 仓库 / 机器 segment:\n\(segments.debugDescription)")
        XCTAssertTrue(repo.isSelected, "仓库 is not the one lit")
        shot("segments")
        machine.tap()
        XCTAssertTrue(machine.waitForSelected(), "机器 did not light")
        XCTAssertFalse(repo.isSelected, "仓库 stayed lit")
    }

    /// An agent opened from the list shows beside the column with its row selected and Main Chat not; Main Chat closes it
    /// and is the selected row again.
    @MainActor func testAnOpenedAgentIsSelectedAndPersonaClosesIt() throws {
        // One of the current assistant's agents (the list shows only its computers since #5587: the mock hub's MacBook is
        // another assistant's), waiting for the user so it never folds into Idle Repositories with the time of day.
        try launch(["-openSession", "studio/a1f3c2d4-0000-4000-8000-000000000005"])
        // Upright the column is over the page and goes with a pick: `testUprightBackFromAnAgentGoesToPersonasPage`.
        guard !duoUpright else { throw XCTSkip("the column beside the page only (iPad, the open Duo on its side)") }
        // The open header's title, or the slim one it folds to (a chat that opens scrolled to its end).
        let title = app.descendants(matching: .any).matching(NSPredicate(format: "identifier IN %@", ["chat.title", "chat.compactTitle"])).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 20), "the agent did not open:\n\(app.debugDescription)")
        let main = element("sidebar-main")
        XCTAssertGreaterThan(title.frame.minX, main.frame.maxX - 1, "the agent is not beside the column")
        XCTAssertFalse(main.isSelected, "Main Chat is selected while an agent is open")
        let selected = app.descendants(matching: .any).matching(NSPredicate(format: "(identifier == %@ OR identifier == %@) AND isSelected == true",
                                                                             "agents.row", "agents.compactRow"))
        // Its row is under the Needs You cards and the other groups: down the column until it is built.
        let column = app.collectionViews.firstMatch
        var swipes = 0
        while selected.count == 0 && swipes < 8 { column.swipeUp(); swipes += 1 }
        XCTAssertEqual(selected.count, 1, "the open agent's row is not the one selected:\n\(app.debugDescription)")
        shot("agent")
        // Back to the column's top: a swipe's momentum can stop short, leaving the row under the header.
        let header = element("assistant-sidebar-collapse")
        for _ in 0..<(swipes + 3) where !main.isHittable || main.frame.minY < header.frame.maxY { column.swipeDown() }
        main.tap()
        XCTAssertTrue(title.waitForNonExistence(timeout: 10), "Main Chat did not close the agent")
        XCTAssertTrue(main.waitForSelected(), "Main Chat is not the selected row")
        for _ in 0..<swipes { column.swipeUp() } // back where the agent's row was
        XCTAssertEqual(selected.count, 0, "an agent row stays selected:\n\(app.debugDescription)")
    }

    /// Upright on the open Duo (#4852): the column lies over the page. Needs You / All and the grouping's segments only
    /// change the list, so it stays; a pick that changes the page slides it away: a direction, a place, Main Chat, a
    /// conversation from All Chats.
    @MainActor func testUprightAPickSlidesTheColumnAwayAndFiltersDoNot() throws {
        try launch()
        guard duoUpright else { throw XCTSkip("the open Duo upright only (the column beside the page elsewhere)") }
        let needsYou = element("agents.filter.needsYou"), all = element("agents.filter.all")
        XCTAssertTrue(needsYou.waitForExistence(timeout: 10), "no Needs You filter:\n\(app.debugDescription)")
        needsYou.tap()
        assertColumnStays("Needs You")
        all.tap()
        assertColumnStays("All")
        // The segments, or the grouping menu standing in for them in a column too narrow for the five.
        let segments = element("agents.grouping.segments"), pill = element("agents.grouping")
        for (segment, item) in [("机器", "按机器"), ("仓库", "按仓库")] {
            if segments.exists {
                segments.buttons[segment].firstMatch.tap()
            } else {
                XCTAssertTrue(pill.waitForExistence(timeout: 10), "no grouping segments or menu:\n\(app.debugDescription)")
                pill.tap()
                let pick = app.buttons[item].firstMatch
                XCTAssertTrue(pick.waitForExistence(timeout: 10), "the grouping menu has no \(item):\n\(app.debugDescription)")
                pick.tap()
                XCTAssertTrue(pick.waitForNonExistence(timeout: 10), "the grouping menu stayed open")
            }
            assertColumnStays("a grouping")
        }
        shot("upright-filters")

        // A direction: its conversation on the page, the column away.
        app.buttons.matching(identifier: "sidebar-pinned").firstMatch.tap()
        assertColumnAway("a direction")
        shot("upright-direction")
        // A place.
        bringColumnOut()
        element("sidebar-inbox").tap()
        assertColumnAway("the Inbox")
        XCTAssertTrue(element("inbox.more").waitForExistence(timeout: 10), "the Inbox did not open:\n\(app.debugDescription)")
        // Main Chat.
        bringColumnOut()
        element("sidebar-main").tap()
        assertColumnAway("Main Chat")
        // A conversation from All Chats: the list over the window, the conversation on the page, the column away.
        bringColumnOut()
        element("sidebar-all-chats").tap()
        let row = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "auto-conversation")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "All Chats lists nothing:\n\(app.debugDescription)")
        row.tap()
        XCTAssertTrue(row.waitForNonExistence(timeout: 5), "a conversation did not close All Chats")
        assertColumnAway("a conversation from All Chats")
    }

    /// Upright on the open Duo: an agent picked in the column slides it away (#4852), and ‹ on that agent goes back to
    /// Persona's page, not the column out again (#3641).
    @MainActor func testUprightBackFromAnAgentGoesToPersonasPage() throws {
        // The column at its default 320 pt: in the system's old one (about 200 pt upright) the Pinned rows took two lines,
        // the filter chips wrapped under the foot and the first Needs You card started below the first screen.
        try launch()
        guard duoUpright else { throw XCTSkip("the open Duo upright only") }
        // The agents are under the first screen upright, and synthetic drags miss there: the first Needs You card's
        // place, at the column's foot, is tapped (a tap scrolls it in), which opens its agent as its row would.
        let card = app.staticTexts.matching(identifier: "agents.interaction").firstMatch
        let edge = element("sidebar-main").frame.maxX // the column's edge, read before it scrolls Main Chat away
        if !card.waitForExistence(timeout: 3) {
            // Not built yet: the column's Needs You heading (its lowest, under the filter chips) tapped scrolls the cards in.
            let heading = app.staticTexts.matching(NSPredicate(format: "label == %@", "需要你")).allElementsBoundByIndex
                .filter { $0.frame.maxX < edge }.max { $0.frame.minY < $1.frame.minY }
            heading?.tap()
        }
        XCTAssertTrue(card.waitForExistence(timeout: 10), "no Needs You card in the column:\n\(app.debugDescription)")
        XCTAssertLessThan(card.frame.maxX, edge + 1, "the card is not the column's")
        card.tap()
        let title = app.descendants(matching: .any).matching(NSPredicate(format: "identifier IN %@", ["chat.title", "chat.compactTitle"])).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 20), "the agent did not open:\n\(app.debugDescription)")
        assertColumnAway("an agent")
        shot("upright-agent")
        let back = app.buttons.matching(NSPredicate(format: "label IN %@", ["返回", "Back"])).firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 5) && back.isHittable, "no ‹ on the agent:\n\(app.debugDescription)")
        back.tap()
        XCTAssertTrue(title.waitForNonExistence(timeout: 10), "‹ did not leave the agent:\n\(app.debugDescription)")
        XCTAssertTrue(element("assistant-new").waitForHittable(timeout: 5), "Persona's page is not back:\n\(app.debugDescription)")
        XCTAssertFalse(element("sidebar-main").isHittable, "‹ slid the column out instead of going back")
        shot("upright-back")
    }

    /// Messages is under More while it is switched on and the hub has Feishu chats; it opens beside the column, its row
    /// selected, and a Persona row takes the column back.
    @MainActor func testMessagesIsUnderMore() throws {
        try launch(["-integrations.lark.enabled", "YES"])
        element("sidebar-more").tap()
        let messages = element("sidebar-messages")
        XCTAssertTrue(messages.waitForExistence(timeout: 10), "no Messages under More:\n\(app.debugDescription)")
        messages.tap()
        let list = element("feishu-conversations")
        XCTAssertTrue(list.waitForExistence(timeout: 10), "Messages did not open:\n\(app.debugDescription)")
        if duoUpright {
            // Over the page the column slides away (#4852); out again, Messages is the row picked.
            assertColumnAway("Messages")
            shot("messages-away")
            bringColumnOut()
            XCTAssertTrue(messages.waitForSelected(), "the Messages row is not selected")
            shot("messages")
            element("sidebar-main").tap()
            XCTAssertTrue(list.waitForNonExistence(timeout: 10), "Main Chat did not take the column back from Messages")
            return
        }
        // The list scrolls under the floating column (its frame starts at the window's edge): its rows' text is beside it.
        let text = list.staticTexts.firstMatch
        XCTAssertTrue(text.waitForExistence(timeout: 10), "Messages shows no chat:\n\(list.debugDescription)")
        XCTAssertGreaterThan(text.frame.minX, messages.frame.maxX - 1, "Messages is not beside the column")
        XCTAssertTrue(messages.waitForSelected(), "the Messages row is not selected")
        XCTAssertFalse(element("sidebar-main").isSelected, "Main Chat stays selected beside Messages")
        shot("messages")
        element("sidebar-main").tap()
        XCTAssertTrue(list.waitForNonExistence(timeout: 10), "Main Chat did not take the column back from Messages")
    }

    /// Settings: over the window on the open Duo, a sheet on the iPad; its × closes it and the column is back.
    @MainActor func testSettingsOpensAndCloses() throws {
        try launch()
        let settings = element("sidebar-settings")
        settings.tap()
        let close = app.buttons["settings-close"].firstMatch
        XCTAssertTrue(close.waitForExistence(timeout: 10), "Settings has no ×:\n\(app.debugDescription)")
        shot("settings")
        close.tap()
        XCTAssertTrue(close.waitForNonExistence(timeout: 5), "the × did not close Settings")
        XCTAssertTrue(settings.waitForExistence(timeout: 5) && settings.isHittable, "the column did not come back")
    }

    /// Search covers the window (#3705): its field takes the keyboard, a row opens its conversation beside the column.
    @MainActor func testSearchOpensAConversation() throws {
        try launch()
        element("sidebar-search").tap()
        let field = element("full-search-field")
        XCTAssertTrue(field.waitForExistence(timeout: 5), "search opened no field:\n\(app.debugDescription)")
        let rows = app.buttons.matching(identifier: "full-search-row")
        XCTAssertGreaterThan(rows.count, 0, "no conversations in the search:\n\(app.debugDescription)")
        rows.firstMatch.tap()
        XCTAssertTrue(field.waitForNonExistence(timeout: 5), "a row did not close the search")
        if duoUpright {
            assertColumnAway("a conversation from the search") // over the page, it slides away (#4852)
        } else {
            XCTAssertTrue(element("sidebar-main").waitForHittable(timeout: 5), "the column is not back")
        }
    }
}

private extension XCUIElement {
    /// Waits until it can be tapped where it is (on screen, nothing over it).
    func waitForHittable(timeout: TimeInterval = 5) -> Bool {
        XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == true AND isHittable == true"), object: self)],
                       timeout: timeout) == .completed
    }

    /// Waits until it is gone or out of reach (slid off screen).
    func waitForNotHittable(timeout: TimeInterval = 5) -> Bool {
        XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false OR isHittable == false"), object: self)],
                       timeout: timeout) == .completed
    }

    /// Waits for VoiceOver's selected trait (the row the detail column shows).
    func waitForSelected(timeout: TimeInterval = 5) -> Bool {
        XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: "isSelected == true"), object: self)],
                       timeout: timeout) == .completed
    }
}
