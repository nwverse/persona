import XCTest

/// The Sidebar layout on an iPad (#6332, docs/apple/ios.md「Sidebar layout (#6332)」): one column with the main
/// conversation, the pages, Persona's chats and the Agents, no Persona | Agents tabs; a pick in it replaces the page beside
/// it; the app's routes land on the same chat or page as in the Separate layout; Settings › Appearance › Navigation
/// switches back at once. Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB), on an iPad simulator.
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps screenshots (with TEST_RUNNER_PERSONA_SHOTS_APPEARANCE in their names).
final class SidebarLayoutTests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private let agent = "macbook/a1f3c2d4-0000-4000-8000-000000000001"

    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard UIDevice.current.userInterfaceIdiom == .pad else { throw XCTSkip("iPad only") }
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ extra: [String]) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.navigation", "sidebar", "-dev.openChat", ""] + extra
        app.launchPinned()
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    @MainActor private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "system"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("sidebar-layout-\(look)-\(name).png"))
    }

    /// Scrolls the sidebar until `target` can be tapped.
    @MainActor private func reach(_ target: XCUIElement, in column: XCUIElement, _ step: String) {
        for _ in 0..<8 where !(target.exists && target.isHittable) {
            column.swipeUp(velocity: .slow)
        }
        XCTAssertTrue(target.exists && target.isHittable, "\(step): not reached in the sidebar:\n\(app.debugDescription)")
    }

    @MainActor private func waitGone(_ e: XCUIElement, _ step: String) {
        let gone = NSPredicate { _, _ in !e.exists || !e.isHittable }
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: gone, object: NSNull())], timeout: 10), .completed,
                       "\(step):\n\(app.debugDescription)")
    }

    @MainActor func testOneSidebarAndAPickReplacesThePage() {
        launch(["-tab", "assistant"])
        let main = element("sidebar-main"), inbox = element("sidebar-inbox"), chats = element("sidebar-fold-chats")
        XCTAssertTrue(main.waitForExistence(timeout: 20), "no Persona row:\n\(app.debugDescription)")
        XCTAssertTrue(inbox.exists && chats.exists, "the pages or Persona's chats are missing")
        // The web's order: Persona, the pages, Persona's chats; the column on the left.
        XCTAssertLessThan(main.frame.minY, inbox.frame.minY)
        XCTAssertLessThan(inbox.frame.minY, chats.frame.minY)
        XCTAssertLessThan(main.frame.maxX, app.windows.firstMatch.frame.width / 2)
        // No Persona | Agents tabs above it.
        let tabs = app.buttons.matching(NSPredicate(format: "label == 'Agents' OR label == 'Persona'")).allElementsBoundByIndex
            .filter { $0.exists && $0.frame.maxY < main.frame.minY - 20 }
        XCTAssertTrue(tabs.isEmpty, "the tabs are still there: \(tabs.map(\.label))")
        // New Chat and Settings at the foot.
        XCTAssertTrue(element("sidebar-new").exists && element("sidebar-settings").exists)
        shot("home")

        // Inbox replaces the conversation beside the sidebar.
        inbox.tap()
        let more = element("inbox.more")
        XCTAssertTrue(more.waitForExistence(timeout: 10), "no inbox:\n\(app.debugDescription)")
        XCTAssertGreaterThan(more.frame.minX, inbox.frame.maxX, "the inbox is not beside the sidebar")
        XCTAssertTrue(main.isHittable, "the sidebar went away with the inbox")
        shot("inbox")

        // An Agent replaces the inbox: its chat, not the inbox under it.
        let column = element("unified-sidebar")
        let agents = element("sidebar-agents-new")
        reach(agents, in: column, "the Agents' heading")
        let row = app.buttons.matching(identifier: "agents.row").firstMatch
        reach(row, in: column, "an Agent's row")
        row.tap()
        let title = app.buttons["chat.title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 10), "no Agent's chat:\n\(app.debugDescription)")
        waitGone(more, "the inbox is still over the conversation under the Agent's chat")
        shot("agent")

        // Persona's row goes back to the conversation, nothing over it.
        main.tap()
        waitGone(title, "the Agent's chat stayed over the conversation")
        XCTAssertTrue(element("assistant-sidebar-button").waitForExistence(timeout: 10), "not on the conversation:\n\(app.debugDescription)")
    }

    /// An Agent's chat asked for at launch (as a notification, a Live Activity or a link does): beside the sidebar.
    @MainActor func testAnAgentsChatFromOutsideOpensBesideTheSidebar() {
        launch(["-tab", "dev", "-openSession", agent])
        let title = app.buttons["chat.title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 20), "no chat:\n\(app.debugDescription)")
        XCTAssertEqual(title.value as? String, agent)
        XCTAssertTrue(element("sidebar-main").exists, "no sidebar beside the chat")
        XCTAssertGreaterThan(title.frame.minX, element("sidebar-main").frame.maxX)
    }

    /// The inbox asked for at launch (`.mail`, as an Inbox notification does): open beside the sidebar, and it stays.
    @MainActor func testTheInboxFromOutsideStays() {
        launch(["-tab", "mail"])
        let more = element("inbox.more")
        XCTAssertTrue(more.waitForExistence(timeout: 20), "no inbox:\n\(app.debugDescription)")
        sleep(2) // a route back to the conversation would come in this time
        XCTAssertTrue(more.isHittable, "the inbox went away:\n\(app.debugDescription)")
    }

    /// Screenshots for review (TEST_RUNNER_PERSONA_SHOTS, #6332): light and dark, the default text size and xxxLarge (the
    /// interface stops growing there, #6448), landscape and portrait: the column at its top, then the Agents' Needs You reached from the foot's jump with an
    /// Agent open beside it; and the Navigation setting. XCUITest saves landscape screenshots turned; turn them back.
    @MainActor func testShots() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_SHOTS"] != nil else { throw XCTSkip("screenshots only") }
        for look in ["light", "dark"] {
            for size in ["default", "xxxlarge"] {
                for (turn, orientation) in [("landscape", UIDeviceOrientation.landscapeLeft), ("portrait", .portrait)] {
                    XCUIDevice.shared.orientation = orientation
                    var extra = ["-tab", "assistant", "-ui.scheme", look]
                    if size == "xxxlarge" {
                        extra += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryXXXL"]
                    }
                    launch(extra)
                    XCTAssertTrue(element("sidebar-main").waitForExistence(timeout: 20), "\(look) \(size): no sidebar")
                    settle()
                    dismissIconNotice()
                    shot("\(look)-\(size)-\(turn)-home")
                    let jump = element("sidebar-needs-jump")
                    if jump.exists {
                        jump.tap()
                        settle()
                        let row = app.buttons.matching(identifier: "agents.row").firstMatch
                        if row.exists, row.isHittable { row.tap() }
                        settle()
                        shot("\(look)-\(size)-\(turn)-agents")
                    }
                    app.terminate()
                }
            }
            XCUIDevice.shared.orientation = .portrait
            launch(["-tab", "settings", "-openAppearance", "navigation", "-ui.scheme", look])
            XCTAssertTrue(element("navigation-layout-separate").waitForExistence(timeout: 20), "\(look): no Navigation page")
            settle()
            dismissIconNotice()
            shot("\(look)-setting")
            app.terminate()
        }
    }

    @MainActor private func settle() { usleep(1_500_000) }

    /// Changing the look switches the app icon, and the system says so over the screen (`ChatBottomGapTests`).
    @MainActor private func dismissIconNotice() {
        let notice = NSPredicate(format: "label CONTAINS[c] 'icon' OR label CONTAINS '图标'")
        for source in [app!, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let alert = source.alerts.containing(notice).firstMatch
            if alert.waitForExistence(timeout: 2), alert.buttons.firstMatch.exists {
                alert.buttons.firstMatch.tap()
                settle()
                return
            }
        }
    }

    /// Settings › Appearance › Navigation: Separate brings the tabs back at once, Settings still open on its page.
    @MainActor func testTheChoiceAppliesAtOnce() {
        launch(["-tab", "settings", "-openAppearance", "navigation"])
        let separate = element("navigation-layout-separate"), sidebar = element("navigation-layout-sidebar")
        XCTAssertTrue(separate.waitForExistence(timeout: 20), "no Navigation page:\n\(app.debugDescription)")
        XCTAssertTrue(sidebar.isSelected, "Sidebar is not the one chosen")
        shot("setting")
        separate.tap()
        XCTAssertTrue(separate.waitForExistence(timeout: 10), "Settings closed:\n\(app.debugDescription)")
        let chosen = NSPredicate { _, _ in separate.isSelected }
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: chosen, object: NSNull())], timeout: 10), .completed)
        app.buttons["settings-close"].firstMatch.tap()
        XCTAssertTrue(app.buttons["Agents"].firstMatch.waitForExistence(timeout: 10), "the tabs did not come back:\n\(app.debugDescription)")
        XCTAssertFalse(element("unified-sidebar").exists, "the Sidebar layout is still there")
    }
}
