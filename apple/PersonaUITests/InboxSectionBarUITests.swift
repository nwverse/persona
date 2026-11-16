import XCTest

/// The Inbox's section bar (#9053, docs/assistant/inbox.md "Section bar"): it opens on Needs You and shows that section
/// alone; a choice shows its own section with its title as a heading that keeps its action and doesn't fold; the choice
/// stays across an item opened and closed; All is the stacked page, folding as before; Done shows its history with Jump
/// To and no grouping; a search shows All and clearing it brings the choice back; the Inbox entered again opens on Needs
/// You. Against scripts/mock/mock_hub.py with MOCK_MANAGERS=0 (one assistant, so the FYI group chat 「周末徒步群」 is
/// in its Inbox):
///   PORT=<port> MOCK_MANAGERS=0 python3 scripts/mock/mock_hub.py
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps the screens; TEST_RUNNER_PERSONA_UI_SCHEME=dark runs it dark.
final class InboxSectionBarUITests: XCTestCase {
    private var app: XCUIApplication!
    private var pair = ""
    /// A Needs You row and an FYI row of the mock's Inbox.
    private static let needsYou = "韩梅梅"
    private static let fyi = "周末徒步群"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_MANAGERS=0 (PERSONA_MOCK_HUB)")
        }
        pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.name = "failure"
            a.lifetime = .keepAlways
            add(a)
        }
        super.tearDown()
    }

    @MainActor func testAChoiceShowsItsSectionAloneAndStaysAcrossAnItem() throws {
        launch()
        let needs = choice("action"), fyi = choice("fyi"), all = choice("all"), done = choice("done")
        XCTAssertTrue(needs.isSelected, "the Inbox does not open on Needs You:\n\(app.debugDescription)")
        XCTAssertTrue(row(Self.needsYou).waitForExistence(timeout: 15), "no Needs You row:\n\(app.debugDescription)")
        XCTAssertFalse(row(Self.fyi).exists, "an FYI row on Needs You")
        XCTAssertTrue(app.buttons["inbox.grouping"].firstMatch.exists, "no grouping at the bar's end")
        XCTAssertFalse(app.segmentedControls.firstMatch.exists, "the old grouping row is still there")
        XCTAssertFalse(app.buttons["inbox.sectionTitle"].firstMatch.exists, "Needs You chosen alone still folds")
        shot("needs-you")

        // FYI alone: its heading keeps Mark All Seen and does not fold.
        tap(fyi)
        XCTAssertTrue(row(Self.fyi).waitForExistence(timeout: 5), "FYI's rows did not show:\n\(app.debugDescription)")
        XCTAssertTrue(fyi.isSelected, "FYI is not the choice")
        XCTAssertFalse(row(Self.needsYou).exists, "a Needs You row under FYI")
        XCTAssertTrue(app.buttons["inbox.clear"].firstMatch.exists, "FYI's heading lost Mark All Seen")
        XCTAssertTrue(element("inbox.sectionHeading").exists, "FYI's title is not a heading")
        XCTAssertFalse(app.buttons["inbox.sectionTitle"].firstMatch.exists, "FYI chosen alone folds")
        shot("fyi")

        // An item opened and closed: the list is where it was.
        row(Self.fyi).tapCenter()
        XCTAssertTrue(app.buttons["inbox.detail.more"].firstMatch.waitForExistence(timeout: 15), "the item did not open")
        back()
        XCTAssertTrue(row(Self.fyi).waitForExistence(timeout: 10), "Back did not return to FYI:\n\(app.debugDescription)")
        XCTAssertTrue(fyi.isSelected, "the choice did not survive the item")

        // All: the stacked page, its sections folding from their titles.
        tap(all)
        XCTAssertTrue(row(Self.needsYou).waitForExistence(timeout: 5), "All does not list Needs You")
        XCTAssertTrue(app.buttons["inbox.sectionTitle"].firstMatch.exists, "All's sections do not fold from their titles")
        shot("all")

        // Done: its history, Jump To on its heading, nothing to group.
        XCTAssertTrue(done.waitForExistence(timeout: 10), "no Done on the bar (the mock's day counts):\n\(app.debugDescription)")
        tap(done)
        XCTAssertTrue(element("inbox.doneHeading").waitForExistence(timeout: 10), "Done's heading did not show:\n\(app.debugDescription)")
        XCTAssertFalse(row(Self.needsYou).exists, "a Needs You row under Done")
        XCTAssertTrue(app.buttons["inbox.jumpTo"].firstMatch.waitForExistence(timeout: 5), "Done lists no Jump To")
        XCTAssertFalse(app.buttons["inbox.grouping"].firstMatch.isEnabled, "the grouping is offered under Done")
        shot("done")
    }

    @MainActor func testASearchShowsAllAndClearingItBringsTheChoiceBack() throws {
        launch()
        XCTAssertTrue(row(Self.needsYou).waitForExistence(timeout: 15), "no Needs You row:\n\(app.debugDescription)")
        let search = app.searchFields.firstMatch
        if !search.waitForExistence(timeout: 3) {
            // a wide screen keeps the field behind the navigation bar's search button
            let button = app.navigationBars.buttons.matching(NSPredicate(format: "label IN %@", ["搜索", "Search"])).firstMatch
            if button.exists { button.tap() } else { app.swipeDown() }
        }
        XCTAssertTrue(search.waitForExistence(timeout: 5), "no search field:\n\(app.debugDescription)")
        search.tap()
        search.typeText("徒步")
        XCTAssertTrue(row(Self.fyi).waitForExistence(timeout: 5), "the search did not find the FYI row:\n\(app.debugDescription)")
        XCTAssertTrue(wait { self.choice("all").isSelected }, "a search does not show All")
        shot("search")

        search.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 2))
        XCTAssertTrue(wait(5) { self.choice("action").isSelected }, "clearing the search did not bring Needs You back:\n\(app.debugDescription)")
        XCTAssertTrue(wait(5) { !self.row(Self.fyi).exists }, "the FYI row stayed on Needs You")
    }

    /// The Inbox chosen again while it is in front (its sidebar row; a widget and a quick action route the same way)
    /// opens on Needs You, though its page stays up (`AppModel.inboxEntries`): a choice left on FYI never hides new
    /// requests.
    @MainActor func testEnteringTheInboxAgainOpensOnNeedsYou() throws {
        launch()
        XCTAssertTrue(row(Self.needsYou).waitForExistence(timeout: 15), "no Needs You row:\n\(app.debugDescription)")
        tap(choice("fyi"))
        XCTAssertTrue(row(Self.fyi).waitForExistence(timeout: 5), "FYI's rows did not show:\n\(app.debugDescription)")
        XCTAssertTrue(choice("fyi").isSelected, "FYI is not the choice")

        let inbox = element("sidebar-inbox")
        if !(inbox.exists && inbox.isHittable) {
            // a phone: the drawer over the place
            let drawer = app.buttons["shell-place-sidebar"].firstMatch
            XCTAssertTrue(drawer.waitForExistence(timeout: 5), "no drawer button on the Inbox:\n\(app.debugDescription)")
            drawer.tap()
        }
        XCTAssertTrue(inbox.waitForExistence(timeout: 5), "no Inbox in the sidebar:\n\(app.debugDescription)")
        inbox.tap()
        XCTAssertTrue(wait(5) { self.choice("action").isSelected }, "the Inbox entered again kept FYI:\n\(app.debugDescription)")
        XCTAssertTrue(row(Self.needsYou).waitForExistence(timeout: 5), "no Needs You row after entering again")
        XCTAssertFalse(row(Self.fyi).exists, "an FYI row after entering again")
        shot("entered-again")
    }

    /// An item the Inbox place kept open (#6599) comes back over a list beneath it; Back lands on the item's choice, not
    /// on Needs You (docs/assistant/inbox.md "Section bar": an item opened from outside the list). Wide screens only:
    /// there the sidebar leaves a place with an item open, and picking it again brings the item back.
    @MainActor func testAnItemBroughtBackLandsTheListOnItsChoice() throws {
        launch()
        guard app.windows.firstMatch.frame.width > 600 else { throw XCTSkip("needs a wide screen: the sidebar beside the Inbox") }
        // The Inbox as its sidebar row opens it, so the place keeps what is pushed on it.
        let machines = element("sidebar-machines"), inbox = element("sidebar-inbox")
        XCTAssertTrue(inbox.waitForExistence(timeout: 5), "no Inbox in the sidebar:\n\(app.debugDescription)")
        inbox.tap()
        XCTAssertTrue(choice("fyi").waitForExistence(timeout: 10), "the Inbox did not open from the sidebar")
        tap(choice("fyi"))
        XCTAssertTrue(row(Self.fyi).waitForExistence(timeout: 5), "FYI's rows did not show:\n\(app.debugDescription)")
        row(Self.fyi).tapCenter()
        XCTAssertTrue(app.buttons["inbox.detail.more"].firstMatch.waitForExistence(timeout: 15), "the item did not open")

        // Another place, then the Inbox again: its item, a new list put beneath it.
        XCTAssertTrue(machines.waitForExistence(timeout: 5), "no Machines in the sidebar:\n\(app.debugDescription)")
        machines.tap()
        XCTAssertTrue(app.buttons["inbox.detail.more"].firstMatch.waitForNonExistence(timeout: 10), "Machines did not replace the item")
        inbox.tap()
        XCTAssertTrue(app.buttons["inbox.detail.more"].firstMatch.waitForExistence(timeout: 15),
                      "the Inbox did not bring its item back:\n\(app.debugDescription)")
        shot("kept-item")
        back()
        XCTAssertTrue(choice("fyi").waitForExistence(timeout: 10), "Back did not return to the list:\n\(app.debugDescription)")
        XCTAssertTrue(wait(5) { self.choice("fyi").isSelected }, "the list beneath the item is not on its choice (FYI)")
        XCTAssertTrue(row(Self.fyi).waitForExistence(timeout: 5), "the item's row is not in sight after Back")
        shot("kept-item-back")
    }

    // MARK: -

    @MainActor private func launch() {
        app = XCUIApplication()
        let env = ProcessInfo.processInfo.environment
        app.launchArguments = ["-pairURL", pair, "-ui.scheme", env["PERSONA_UI_SCHEME"] ?? "light", "-tab", "mail",
                               "-inbox.kind", "", "-inbox.source", "", "-inbox.grouping", ""]
        // the first launch after pairing may open on the Assistant instead: once more, paired
        for attempt in 0..<2 {
            if attempt > 0 { app.terminate() }
            app.launchPinned()
            acceptIconAlert(timeout: 2)
            if choice("action").waitForExistence(timeout: 20) { break }
        }
        XCTAssertTrue(choice("action").exists, "no section bar:\n\(app.debugDescription)")
        sleep(2) // the day counts that put Done on the bar
    }

    @MainActor private func choice(_ id: String) -> XCUIElement { app.buttons["inbox.bar." + id].firstMatch }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// A row's title in the list.
    @MainActor private func row(_ title: String) -> XCUIElement { app.staticTexts[title].firstMatch }

    /// A choice on the bar, the strip scrolled to it when it is past its end.
    @MainActor private func tap(_ choice: XCUIElement) {
        let strip = element("inbox.bar")
        for _ in 0..<3 where !choice.isHittable { strip.swipeLeft() }
        choice.tap()
        sleep(1)
    }

    @MainActor private func back() {
        let system = app.navigationBars.buttons.matching(identifier: "BackButton").allElementsBoundByIndex.first(where: \.isHittable)
        if let system { system.tap(); return }
        let window = app.windows.firstMatch
        window.coordinate(withNormalizedOffset: CGVector(dx: 0.0, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.6, dy: 0.5)),
                   withVelocity: .slow, thenHoldForDuration: 0.1)
    }

    @MainActor private func wait(_ timeout: TimeInterval = 3, _ condition: @escaping () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if condition() { return true }
            usleep(200_000)
        }
        return condition()
    }

    @MainActor private func shot(_ name: String) {
        let image = app.screenshot()
        let a = XCTAttachment(screenshot: image)
        a.name = "inbox bar \(name)"
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let scheme = ProcessInfo.processInfo.environment["PERSONA_UI_SCHEME"] ?? "light"
        try? image.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("inbox-bar-\(name)-\(scheme).png"))
    }
}
