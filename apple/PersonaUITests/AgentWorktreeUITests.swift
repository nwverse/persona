import XCTest

/// A new agent's own worktree (#6115), against scripts/mock/mock_hub.py with MOCK_WORKTREES=1 MOCK_AGENT_WORKTREE=1:
/// New Agent offers "In a New Worktree" for a folder in a repository and starts the agent in the checkout it made;
/// closing or deleting an agent in one of Persona's worktrees asks to keep or remove it, removing what loses work only
/// once that is confirmed.
final class AgentWorktreeUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    /// The mock's node actions before this test (/mock/reset keeps them).
    private var before = 0
    /// 「初步调研 WidgetKit API」: idle, in weather/.worktrees/widget (2 changed files, 3 commits main lacks).
    private let research = "a1f3c2d4-0000-4000-8000-0000000000c1"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        _ = try call("/mock/reset")
        guard try call("/api/v1/machines/macbook/worktree/status", body: ["cwd": "/Users/me/code/weather"])["path"] != nil else {
            throw XCTSkip("needs MOCK_AGENT_WORKTREE=1")
        }
        before = ((try call("/mock/node_actions")["actions"] as? [Any]) ?? []).count
        app = XCUIApplication()
    }

    private func launch(_ args: [String]) {
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
    }

    @discardableResult
    private func call(_ path: String, body: [String: Any]? = nil) throws -> [String: Any] {
        var req = URLRequest(url: URL(string: hub + path)!)
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        if let body {
            req.httpMethod = "POST"
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
        }
        var out: Data?
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: req) { d, _, _ in out = d; done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
        return (try? JSONSerialization.jsonObject(with: out ?? Data()) as? [String: Any]) ?? [:]
    }

    private func actions(_ route: String) throws -> [[String: Any]] {
        ((try call("/mock/node_actions")["actions"] as? [[String: Any]]) ?? []).dropFirst(before).filter { $0["route"] as? String == route }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "worktree-\(name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor @discardableResult
    private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 10) -> XCUIElement {
        XCTAssertTrue(e.waitForExistence(timeout: timeout), "missing: \(what)")
        return e
    }

    private func waitFor(_ what: String, timeout: TimeInterval = 10, _ ok: () throws -> Bool) rethrows {
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if try ok() { return }
            usleep(300_000)
        }
        XCTFail("timed out: \(what)")
    }

    private func text(_ s: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    /// Scrolls the form until `e` is on screen (a list only has the rows it shows).
    @MainActor @discardableResult
    private func reveal(_ e: XCUIElement, _ what: String) -> XCUIElement {
        for _ in 0..<6 where !(e.exists && e.isHittable) {
            app.swipeUp()
            _ = e.waitForExistence(timeout: 1.5)
        }
        return see(e, what)
    }

    /// New Agent opens on the first computer; the weather repository is MacBook's.
    @MainActor private func pickMacBook() {
        see(app.buttons["newChat.directory"], "the directory row", timeout: 15)
        see(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "MacBook")).firstMatch, "MacBook card").tap()
    }

    /// A switch in a form: the element that flips it.
    @MainActor private func flip(_ id: String) {
        let s = see(app.switches[id], id)
        (s.switches.firstMatch.exists ? s.switches.firstMatch : s).tap()
    }

    @MainActor func testNewAgentStartsInItsOwnWorktree() throws {
        launch(["-tab", "dev", "-showNewChat", "YES"])
        pickMacBook()
        sleep(2) // the computer is asked about the folder
        reveal(app.switches["newChat.worktree"], "the worktree switch for a folder in a repository")
        shot("new-off")
        flip("newChat.worktree")
        see(text("检出到"), "where it checks out")
        XCTAssertTrue(text(".worktrees/persona-weather").exists, "the folder's name names the branch with no first message")
        // A short drag: the checkout's line clear of the start button, the switch still in view.
        let sw = app.switches["newChat.worktree"].coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.5))
        sw.press(forDuration: 0.1, thenDragTo: sw.withOffset(CGVector(dx: 0, dy: -220)))
        shot("new-on")

        let start = see(app.buttons["开始对话"], "开始对话")
        XCTAssertTrue(start.isEnabled)
        start.tap()
        try waitFor("worktree/add, then the start in it") { try !actions("worktree/add").isEmpty && !((try call("/mock/started")["started"] as? [Any]) ?? []).isEmpty }
        let add = try XCTUnwrap(actions("worktree/add").last)
        XCTAssertEqual(add["cwd"] as? String, "/Users/me/code/weather")
        XCTAssertEqual(add["name_hint"] as? String, "weather")
        XCTAssertNil(add["branch"], "the computer picks the branch when none is typed")
        let started = try XCTUnwrap((try call("/mock/started")["started"] as? [[String: Any]])?.last)
        XCTAssertEqual(started["cwd"] as? String, "/Users/me/code/weather/.worktrees/persona-weather")
    }

    @MainActor func testNewAgentWorktreeFailureStartsNothing() throws {
        launch(["-tab", "dev", "-showNewChat", "YES"])
        pickMacBook()
        sleep(2)
        reveal(app.switches["newChat.worktree"], "the worktree switch")
        flip("newChat.worktree")
        let branch = see(app.textFields["newChat.worktree.branch"], "the branch field")
        branch.tap()
        branch.typeText("persona/taken\n") // return: the keyboard goes, the start button shows
        see(app.buttons["开始对话"], "开始对话").tap()
        let error = see(app.descendants(matching: .any).matching(identifier: "newChat.error").firstMatch, "why it failed")
        XCTAssertTrue(error.label.contains("没能建 worktree"), error.label)
        XCTAssertTrue(error.label.contains("already exists"), error.label)
        shot("new-failed")
        XCTAssertEqual(((try call("/mock/started")["started"] as? [Any]) ?? []).count, 0, "nothing started")
    }

    @MainActor func testClosingRemovesTheWorktreeOnlyOnceTheLossIsConfirmed() throws {
        launch(["-tab", "dev", "-openSession", "macbook/" + research, "-showCloseAgent", "YES"])
        let cleanup = see(app.segmentedControls["worktree.cleanup"], "keep or remove", timeout: 20)
        XCTAssertTrue(cleanup.buttons["保留 worktree"].isSelected, "keeping is the default")
        XCTAssertTrue(text("persona/widget").exists && text(".worktrees/widget").exists)
        shot("close-keep")
        cleanup.buttons["移除"].tap()
        see(text("有 2 个文件的改动没提交"), "what removing it loses")
        let confirm = app.buttons["agent.close.confirm"]
        XCTAssertFalse(confirm.isEnabled, "waits for the confirmation")
        flip("worktree.deleteBranch")
        see(text("分支上有 3 个提交不在 main 上"), "what deleting the branch loses")
        shot("close-remove")
        flip("worktree.confirmLoss")
        XCTAssertTrue(confirm.isEnabled)
        confirm.tap()
        try waitFor("worktree/remove after the close") { try !actions("worktree/remove").isEmpty }
        let remove = try XCTUnwrap(actions("worktree/remove").last)
        XCTAssertEqual(remove["path"] as? String, "/Users/me/code/weather/.worktrees/widget")
        XCTAssertEqual(remove["delete_branch"] as? Bool, true)
        XCTAssertEqual(remove["force"] as? Bool, true, "forced only as confirmed")
        see(text("已移除 worktree 和分支"), "how it went")
        shot("close-done")
    }

    @MainActor func testDeletingKeepsTheWorktreeByDefault() throws {
        try call("/api/v1/sessions/macbook/\(research)/close", body: [:]) // offline, so its chat can be deleted
        launch(["-tab", "dev", "-openSession", "macbook/" + research, "-showDeleteSession", "YES"])
        let cleanup = see(app.segmentedControls["worktree.cleanup"], "keep or remove in Delete Chat", timeout: 40)
        cleanup.buttons["移除"].tap()
        see(text("有 2 个文件的改动没提交"), "what removing it loses")
        shot("delete-remove")
        cleanup.buttons["保留 worktree"].tap()
        let confirm = see(app.buttons["chat.delete.confirm"], "删除")
        XCTAssertTrue(confirm.isEnabled, "keeping loses nothing")
        confirm.tap()
        see(text("已删除对话"), "deleted")
        sleep(2)
        XCTAssertTrue(try actions("worktree/remove").isEmpty, "kept")
    }
}
