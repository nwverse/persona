import XCTest

/// By Repository's tree (#2531), against scripts/mock/mock_hub.py with MOCK_WORKTREES=1 MOCK_ORCA_CHILDREN=1
/// (PERSONA_WORKTREE_HUB): a repository's header has no ⋯ (its actions are its long-press menu), agent lines name no
/// model, and the screens it scrolls through go to PERSONA_SHOTS.
final class AgentRepoTreeUITests: XCTestCase {
    private var hub = ""

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let h = ProcessInfo.processInfo.environment["PERSONA_WORKTREE_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_WORKTREES=1 MOCK_ORCA_CHILDREN=1 (PERSONA_WORKTREE_HUB)")
        }
        hub = h
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    /// The tree from the top down, a screen at a time.
    @MainActor func testTheTreeScreens() throws {
        let app = launch()
        let all = app.buttons["agents.filter.all"]
        XCTAssertTrue(all.waitForExistence(timeout: 20), "no Agents list")
        sleep(2)
        shot(app, "repo-tree-0")
        let window = app.windows.firstMatch
        let from = window.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: all.frame.midX - window.frame.minX, dy: window.frame.height * 0.75))
        for i in 1...3 {
            from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -window.frame.height * 0.5)),
                       withVelocity: .slow, thenHoldForDuration: 0.3)
            shot(app, "repo-tree-\(i)")
        }
    }

    /// A repository's header holds no ⋯: what it offered (open, pin, mute, rename the space) is its long-press menu.
    @MainActor func testARepositorysActionsAreItsLongPressMenu() throws {
        let app = launch()
        XCTAssertTrue(app.buttons["agents.filter.all"].waitForExistence(timeout: 20), "no Agents list")
        let weather = app.buttons.matching(identifier: "agents.project").matching(NSPredicate(format: "label BEGINSWITH 'weather'")).firstMatch
        bringUp(weather, in: app, column: app.buttons["agents.filter.all"])
        XCTAssertTrue(weather.exists && weather.isHittable, "no weather repository")
        XCTAssertFalse(app.buttons["weather 的更多操作"].exists, "a ⋯ on the repository's header")
        weather.press(forDuration: 1.2)
        let open = app.buttons["打开空间"].firstMatch
        XCTAssertTrue(open.waitForExistence(timeout: 5), "the repository's long-press menu (打开空间)")
        shot(app, "repo-tree-menu")
    }

    /// An agent's line says its state, mark, title and time; the model is in the chat's header, not here.
    @MainActor func testAgentLinesNameNoModel() throws {
        let app = launch()
        XCTAssertTrue(app.buttons["agents.filter.all"].waitForExistence(timeout: 20), "no Agents list")
        let row = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == 'agents.row' AND label CONTAINS %@", "给天气 App 加主屏幕小组件")).firstMatch
        bringUp(row, in: app, column: app.buttons["agents.filter.all"])
        XCTAssertTrue(row.exists, "no widget agent line")
        XCTAssertFalse(app.staticTexts.matching(identifier: "agents.workspaceModel").firstMatch.exists, "a model name on an agent line")
    }

    /// Beside the chat (regular width), the open agent's line is washed in the tree.
    @MainActor func testTheOpenAgentsLineStandsOutBesideTheChat() throws {
        let app = launch()
        XCTAssertTrue(app.buttons["agents.filter.all"].waitForExistence(timeout: 20), "no Agents list")
        guard app.windows.firstMatch.frame.width > 700 else { throw XCTSkip("the list is beside the chat in regular width only") }
        let row = app.descendants(matching: .any)
            .matching(NSPredicate(format: "identifier == 'agents.row' AND label CONTAINS %@", "给天气 App 加主屏幕小组件")).firstMatch
        bringUp(row, in: app, column: app.buttons["agents.filter.all"])
        XCTAssertTrue(row.exists && row.isHittable, "no widget agent line")
        row.tap()
        sleep(3)
        shot(app, "repo-tree-open")
    }

    /// Lifts an element well clear of the window's bottom with short, slow drags in the list's column (on the iPad the
    /// list is the sidebar, and the window's middle the empty detail).
    @MainActor private func bringUp(_ element: XCUIElement, in app: XCUIApplication, column: XCUIElement, tries: Int = 16) {
        let window = app.windows.firstMatch
        let bottom = window.frame.maxY - 120
        let from = window.coordinate(withNormalizedOffset: .zero)
            .withOffset(CGVector(dx: column.frame.midX - window.frame.minX, dy: window.frame.height * 0.6))
        for _ in 0..<tries where !(element.exists && element.isHittable && element.frame.maxY < bottom) {
            from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -200)), withVelocity: .slow, thenHoldForDuration: 0.3)
        }
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-ui.oldLayout", "NO", "-app.tab", "chats", "-tab", "dev", "-dev.openChat", "",
                               "-openAgentsFresh", "YES", "-dev.grouping", "repo"]
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = ProcessInfo.processInfo.environment["PERSONA_STYLE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + style + ".png"))
        }
    }
}
