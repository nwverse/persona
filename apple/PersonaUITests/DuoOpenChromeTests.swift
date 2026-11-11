import XCTest

/// iPhone Duo open, the inner screen, laid out as ChatGPT's (#2623):
/// - the Assistant | Agents switch at the top of the page, with no tab bar down the side;
/// - the button that folds the sidebar at the sidebar's top;
/// - Search at its foot, beside Persona's name and where it runs;
/// - the face's menu (Knowledge, Details, the copies; whose Persona, with several hubs) on the main conversation's long press;
/// - the switch over an agent opened on Agents too, and the iPhone's new conversation with Needs You (#3680).
///
/// A phone at regular width only: the Duo simulator opened in Xcode's Device Hub, on its side
/// (TEST_RUNNER_PERSONA_ORIENTATION=landscape) or upright. The iPad keeps its tab bar (IPadSidebarTests).
/// Taps and long presses only (upright, synthetic drags miss on the open Duo). Against scripts/mock/mock_hub.py
/// (PERSONA_MOCK_HUB).
/// TEST_RUNNER_PERSONA_HOLD=<seconds> holds each state, named in an activity `duo-shot <name>`, for
/// `simctl io --display=3 screenshot`; XCUITest's own screenshot captures the dark outer screen.
final class DuoOpenChromeTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("a phone's tabs only (iPhone Duo open)") }
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        XCUIDevice.shared.orientation = ProcessInfo.processInfo.environment["PERSONA_ORIENTATION"] == "landscape" ? .landscapeLeft : .portrait
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-app.tab", "assistant"]
        app.launchPinned()
        guard app.windows.firstMatch.frame.width > 600 else { throw XCTSkip("regular width only (iPhone Duo open)") }
    }

    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    @MainActor private func shot(_ name: String) {
        XCTContext.runActivity(named: "duo-shot \(name)") { _ in
            if let hold = ProcessInfo.processInfo.environment["PERSONA_HOLD"].flatMap(UInt32.init) { sleep(hold) }
        }
    }

    /// The switch at the top of the page and no tab bar; the fold at the sidebar's top, Search at its foot. Folding the
    /// sidebar and bringing it back from the page; Search opening its field at the top.
    @MainActor func testTheSwitchIsOnTopAndSearchAtTheFoot() {
        let assistant = element("shell-assistant"), agents = element("shell-agents")
        XCTAssertTrue(assistant.waitForExistence(timeout: 20), "no switch on the page:\n\(app.debugDescription)")
        XCTAssertTrue(agents.exists, "no Agents in the switch")
        XCTAssertEqual(app.tabBars.count, 0, "the system's tab bar is still there:\n\(app.debugDescription)")
        let window = app.windows.firstMatch.frame
        XCTAssertLessThan(assistant.frame.minY, window.minY + 100, "the switch is not at the top")
        let collapse = element("assistant-sidebar-collapse"), search = element("assistant-sidebar-search"), main = element("sidebar-main")
        XCTAssertTrue(collapse.exists && search.exists && main.exists, "no fold, Search or main row:\n\(app.debugDescription)")
        XCTAssertLessThan(collapse.frame.maxY, main.frame.minY, "the fold is not at the sidebar's top")
        XCTAssertLessThan(collapse.frame.maxX, assistant.frame.minX, "the fold is not in the sidebar, left of the switch")
        XCTAssertGreaterThan(search.frame.minY, window.maxY - 160, "Search is not at the sidebar's foot")
        let back = element("assistant-sidebar-button")
        XCTAssertFalse(back.exists && back.isHittable, "the page's Chat History shows beside the sidebar")
        shot("chrome")

        collapse.tap()
        XCTAssertTrue(collapse.waitForNonExistence(timeout: 5), "the sidebar did not fold")
        XCTAssertTrue(back.waitForExistence(timeout: 5) && back.isHittable, "nothing on the page brings the sidebar back")
        XCTAssertTrue(assistant.exists, "the switch went with the sidebar")
        shot("folded")
        back.tap()
        XCTAssertTrue(collapse.waitForExistence(timeout: 5), "the sidebar did not come back")

        search.tap()
        let field = app.textFields.matching(NSPredicate(format: "placeholderValue == %@", "搜索对话")).firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "Search opened no field:\n\(app.debugDescription)")
        XCTAssertLessThan(field.frame.maxY, window.minY + 140, "the search field is not at the sidebar's top")
        // The foot's two rows step aside while searching, leaving the results the room above the keyboard (#3680).
        XCTAssertFalse(app.drawerNewChatButton.exists, "the foot's rows ride above the keyboard while searching")
        shot("search")
        app.buttons["取消"].firstMatch.tap()
        XCTAssertTrue(app.drawerNewChatButton.waitForExistence(timeout: 5), "Cancel did not bring the foot back")
    }

    /// The main conversation's long press holds the face's menu: Knowledge, Details and the copies.
    @MainActor func testTheMainRowHoldsTheFacesMenu() {
        let main = element("sidebar-main")
        XCTAssertTrue(main.waitForExistence(timeout: 20), "no main row:\n\(app.debugDescription)")
        main.press(forDuration: 1.2)
        // Not Knowledge: the column has a row of that name too.
        for label in ["详细信息", "拷贝目录", "拷贝信息"] {
            XCTAssertTrue(app.buttons[label].firstMatch.waitForExistence(timeout: 5), "no \(label) in the main row's menu:\n\(app.debugDescription)")
        }
        shot("main-menu")
    }

    /// A new conversation is the iPhone's (#3680): what needs the user over the composer (Agents waiting, the Inbox's
    /// Needs You), as `ShellHome` shows it on every iPhone.
    @MainActor func testTheNewConversationShowsWhatNeedsYou() {
        let new = app.drawerNewChatButton
        XCTAssertTrue(new.waitForExistence(timeout: 20), "no New Chat in the sidebar:\n\(app.debugDescription)")
        new.tap()
        XCTAssertTrue(element("shell-needs-you").waitForExistence(timeout: 10), "no Needs You over the composer:\n\(app.debugDescription)")
        XCTAssertTrue(element("assistant-welcome").exists, "no welcome on the new conversation")
        shot("new-conversation")
    }

    /// A conversation's + opens the iPad's menu, not the phone's photo panel (#3680: its three columns came out about
    /// 296 pt square across the open display, one row of them over the composer).
    @MainActor func testPlusOpensTheMenu() {
        let main = element("sidebar-main")
        XCTAssertTrue(main.waitForExistence(timeout: 20), "no main row:\n\(app.debugDescription)")
        main.tap()
        let add = element("shell-composer-add")
        XCTAssertTrue(add.waitForExistence(timeout: 10), "no + in the composer")
        add.tap()
        // The panel's Files tab has the menu item's name: the panel must not be there at all.
        XCTAssertFalse(element("attach-sheet").waitForExistence(timeout: 2), "+ opened the phone's photo panel")
        XCTAssertTrue(app.buttons["文件"].firstMatch.waitForExistence(timeout: 5), "+ opened no menu:\n\(app.debugDescription)")
        shot("plus")
    }

    /// The switch takes the page to Agents, whose page column has it too, and back.
    @MainActor func testTheSwitchGoesToAgentsAndBack() {
        let agents = element("shell-agents")
        XCTAssertTrue(agents.waitForExistence(timeout: 20), "no switch on the page:\n\(app.debugDescription)")
        agents.tap()
        let row = element("page-tab-switch")
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no switch over Agents' page:\n\(app.debugDescription)")
        XCTAssertEqual(app.tabBars.count, 0, "the tab bar came back on Agents")
        shot("agents")
        let assistant = row.descendants(matching: .any)["shell-assistant"].firstMatch
        XCTAssertTrue(assistant.exists, "no Assistant in Agents' switch")
        assistant.tap()
        XCTAssertTrue(element("sidebar-main").waitForExistence(timeout: 10), "the switch did not go back to Assistant")
    }

    /// An agent opened on Agents keeps the switch over it, and the switch goes back to Assistant (#3680: the row was an
    /// inset of the column's stack, which reached only the empty page; an opened chat had no way back to Assistant).
    @MainActor func testTheSwitchStaysOverAnOpenedAgent() {
        app.terminate()
        app.launchArguments += ["-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000002"]
        app.launchPinned()
        let title = element("chat.title")
        XCTAssertTrue(title.waitForExistence(timeout: 20), "the agent did not open:\n\(app.debugDescription)")
        let row = element("page-tab-switch")
        XCTAssertTrue(row.waitForExistence(timeout: 5) && row.isHittable, "no switch over the opened agent:\n\(app.debugDescription)")
        XCTAssertLessThan(row.frame.minY, title.frame.minY, "the switch is not over the chat")
        XCTAssertEqual(app.descendants(matching: .any).matching(identifier: "page-tab-switch").count, 1, "the switch twice over the chat")
        shot("agent-open")
        let assistant = row.descendants(matching: .any)["shell-assistant"].firstMatch
        XCTAssertTrue(assistant.exists, "no Assistant in the switch over the agent")
        assistant.tap()
        XCTAssertTrue(element("sidebar-main").waitForExistence(timeout: 10), "the switch did not go back to Assistant")
    }
}
