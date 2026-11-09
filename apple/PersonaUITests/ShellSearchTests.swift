import XCTest

/// Searching Agents in the ChatGPT-style shell (docs/apple/ios.md「新外壳」): the shell's top bar (☰, Assistant | Agents, New Session)
/// floats over the navigation bar, and the active search field rises into that bar. Drawn together they covered each
/// other (TestFlight 70): while searching the top bar makes way, and comes back on Cancel.
final class ShellSearchTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor @discardableResult private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 10) -> XCUIElement {
        let found = e.waitForExistence(timeout: timeout)
        if !found { attachScreen("missing") }
        XCTAssertTrue(found, "no \(what)")
        return e
    }

    @MainActor private func attachScreen(_ name: String) {
        let screenshot = app.screenshot()
        if let directory = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? FileManager.default.createDirectory(atPath: directory, withIntermediateDirectories: true)
            try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: directory).appendingPathComponent(name + ".png"))
        }
        let shot = XCTAttachment(screenshot: screenshot)
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// The shell's controls on Agents' top bar.
    @MainActor private var topBar: [(String, XCUIElement)] {
        [("☰", app.buttons["work-sidebar-button"].firstMatch), ("助手", app.buttons["shell-assistant"].firstMatch),
         ("Agents", app.buttons["shell-agents"].firstMatch), ("新建会话", app.buttons["agents-new"].firstMatch)]
    }

    /// No top-bar control that is on screen overlaps the search field, and nothing at all of the bar does (any element
    /// in it: the face's inbox mark lay over the field, 2026-11-02).
    @MainActor private func assertClear(of field: XCUIElement, _ when: String) {
        let f = field.frame
        for (name, e) in topBar where e.exists && e.isHittable {
            XCTAssertFalse(e.frame.intersects(f), "\(name) \(e.frame) over the search field \(f) \(when)")
        }
        let bar = app.descendants(matching: .any)["agents-top-bar"].firstMatch
        guard bar.exists else { return } // made way for the search
        for e in bar.descendants(matching: .any).allElementsBoundByIndex where e.exists && !e.frame.isEmpty {
            XCTAssertFalse(e.frame.intersects(f), "\(e.identifier.isEmpty ? e.label : e.identifier) \(e.frame) of the top bar over the search field \(f) \(when)")
        }
    }

    @MainActor func testSearchTakesTheTopBarAndGivesItBack() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-app.tab", "chats", "-dev.openChat", ""]
        app.launchPinned()
        // Agents (a stored tab from another test, Settings, would open its sheet over the shell).
        see(app.buttons["agents-new"].firstMatch, "新建会话 on Agents", timeout: 15)
        let field = app.revealAgentsSearch()
        assertClear(of: field, "before searching")
        attachScreen("before searching")

        field.tapCenter()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard for the search")
        field.typeText("zqxj")
        XCTAssertEqual(field.value as? String, "zqxj", "the search field takes what is typed")
        // No match says so (it used to show Start Your First Session and its New Session).
        see(app.staticTexts["没有找到「zqxj」"].firstMatch, "the no-match state")
        // The field has risen into the bar; the shell's controls must not be drawn over it (or under it).
        sleep(1)
        attachScreen("searching")
        assertClear(of: field, "while searching")
        XCTAssertFalse(app.buttons["agents-new"].firstMatch.isHittable, "新建会话 still over the bar while searching")

        let cancel = app.navigationBars.buttons.matching(
            NSPredicate(format: "label IN {'取消', 'Cancel', '关闭', 'Close'} OR identifier == 'Cancel'")).firstMatch
        see(cancel, "取消 beside the search field").tap()
        // The top bar is back, every control on it reachable, and the field back under it.
        for (name, e) in topBar {
            XCTAssertTrue(e.waitForExistence(timeout: 5) && e.isHittable, "\(name) not back after 取消")
        }
        XCTAssertFalse(app.keyboards.firstMatch.exists, "the keyboard still up after 取消")
        sleep(1)
        attachScreen("after cancel")
        // Closed, the field may fold away with the list (UIKit's): pulled out again, nothing of the bar is over it.
        assertClear(of: app.revealAgentsSearch(), "after 取消")
    }
    /// The expanded search field stays clear of the top bar at small, standard and large text sizes, including the avatar's badge that sticks out of its button.
    @MainActor func testExpandedSearchClearsTheTopBarAtEveryTextSize() {
        for size in ["small", "standard", "large"] {
            app = XCUIApplication()
            app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-app.tab", "chats",
                                   "-dev.openChat", "", "-ui.textSize", size]
            app.launchPinned()
            see(app.buttons["agents-new"].firstMatch, "Agents 顶栏", timeout: 15)
            let field = app.revealAgentsSearch()
            attachScreen("expanded-" + size)
            assertClear(of: field, "展开搜索框，字号 " + size)
            let face = app.buttons["work-sidebar-button"].firstMatch
            XCTAssertGreaterThanOrEqual(field.frame.minY - face.frame.maxY, 8,
                                        "搜索框要与头像及收件箱角标留出间距，字号 " + size)
            app.terminate()
        }
    }

    /// A result pushing a chat does not keep the list's top spacing; coming back restores the top bar, the search text and a layout without overlap.
    @MainActor func testSearchResultReturnsToAnUncoveredSearchField() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-app.tab", "chats", "-dev.openChat", ""]
        app.launchPinned()
        see(app.buttons["agents-new"].firstMatch, "Agents 顶栏", timeout: 15)
        let field = app.revealAgentsSearch()
        field.tapCenter()
        field.typeText("天气")
        let result = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "给天气 App")).firstMatch
        see(result, "搜索结果").tapCenter()
        see(app.buttons["更多"].firstMatch, "对话")
        attachScreen("result-chat")
        XCTAssertFalse(app.navigationBars.firstMatch.isHittable, "对话不应带上列表的系统导航栏")
        let window = app.windows.firstMatch
        window.coordinate(withNormalizedOffset: CGVector(dx: 0.005, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)),
                   withVelocity: .slow, thenHoldForDuration: 0.1)
        see(app.buttons["agents-new"].firstMatch, "返回后的 Agents 顶栏")
        let returned = app.revealAgentsSearch()
        XCTAssertEqual(returned.value as? String, "天气")
        attachScreen("result-returned")
        assertClear(of: returned, "从搜索结果返回")
    }

    /// After a search on Agents is cancelled the switch works where a finger taps it, both ways (#174's bar coming
    /// back must take the taps).
    @MainActor func testSwitchAfterASearchIsCancelled() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-tab", "assistant", "-dev.openChat", ""]
        app.launchPinned()
        let finger = ShellFinger(app)
        finger.switchTo(agents: true, "Agents tapped")
        let field = app.revealAgentsSearch()
        field.tapCenter()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard for the search")
        field.typeText("zqxj")
        let cancel = app.navigationBars.buttons.matching(
            NSPredicate(format: "label IN {'取消', 'Cancel', '关闭', 'Close'} OR identifier == 'Cancel'")).firstMatch
        see(cancel, "取消 beside the search field").tap()
        XCTAssertTrue(app.buttons["agents-new"].firstMatch.waitForExistence(timeout: 5), "the bar is not back after 取消")
        finger.switchTo(agents: false, "助手 tapped after 取消")
        finger.switchTo(agents: true, "Agents tapped after 取消")
    }
    /// Inbox and Knowledge, pushed on Assistant, search in their own navigation bar: the shell's top bar is not over them.
    @MainActor func testSearchOnPagesPushedOnTheAssistant() {
        for (page, title) in [("inbox", "收件箱"), ("knowledge", "知识库")] {
            app = XCUIApplication()
            app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-newAssistant", "YES", "-openShellPage", page]
            app.launchPinned()
            see(app.navigationBars[title], "\(title) pushed on 助手", timeout: 15)
            let field = see(app.searchFields.firstMatch, "\(title)'s search")
            field.tapCenter()
            XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard for \(title)'s search")
            field.typeText("zqxj")
            XCTAssertEqual(field.value as? String, "zqxj", "\(title)'s search takes what is typed")
            sleep(1)
            attachScreen("\(page) searching")
            for id in ["assistant-sidebar-button", "assistant-new", "shell-assistant", "shell-agents"] {
                let e = app.buttons[id].firstMatch
                XCTAssertFalse(e.exists && e.isHittable && e.frame.intersects(field.frame), "\(id) over \(title)'s search field")
            }
            app.terminate()
        }
    }
}
