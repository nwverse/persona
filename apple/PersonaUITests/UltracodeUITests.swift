import UIKit
import XCTest

/// Claude Code's ultracode switch on the phone (#1307): the switch under the thinking effort in the composer's model menu
/// and the workflow mark after the effort and beside an agent row's title, the line
/// after a resume turned it off, the node's reason, and New Agent's switch (off each time, grayed where Claude Code is
/// too old). Against scripts/mock/mock_hub.py with `MOCK_ULTRACODE=1` (`TEST_RUNNER_PERSONA_ULTRACODE_HUB`; GET
/// /mock/ultracode lists the requests, `?reset=1` puts the states back). With PERSONA_SHOTS set the screenshots are
/// written there too (PERSONA_SHOTS_APPEARANCE names the simulator's appearance in the file names).
final class UltracodeUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""

    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // working, on
    private let tests = "macbook/a1f3c2d4-0000-4000-8000-000000000002" // a change queued (another device's)
    private let blog = "studio/a1f3c2d4-0000-4000-8000-000000000003" // off, at rest
    private let review = "macbook/a1f3c2d4-0000-4000-8000-000000000006" // turned off by a resume
    private let release = "macbook/a1f3c2d4-0000-4000-8000-000000000008" // the node's reason (an old Claude Code)
    private let orders = "server/a1f3c2d4-0000-4000-8000-000000000004" // Codex at "ultra"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_ULTRACODE_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_ULTRACODE=1 (TEST_RUNNER_PERSONA_ULTRACODE_HUB)")
        }
        hub = h
        continueAfterFailure = false
        _ = try Data(contentsOf: URL(string: hub + "/mock/ultracode?reset=1")!)
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.lifetime = .keepAlways
            add(a)
            let t = XCTAttachment(string: app.debugDescription)
            t.lifetime = .keepAlways
            add(t)
        }
        super.tearDown()
    }

    // MARK: Helpers

    @MainActor private func launch(_ args: [String]) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "",
                               "-newChat.machine", "macbook", "-launcher.macbook", ""] + args
        app.launchPinned()
    }

    @MainActor private func shot(_ name: String) {
        let env = ProcessInfo.processInfo.environment
        let image = app.screenshot()
        let a = XCTAttachment(screenshot: image)
        a.name = "\(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone"
            let file = "ultracode-\(device)-\(name)-\(env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown").png"
            try? image.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: file))
        }
    }

    @discardableResult
    private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 10) -> XCUIElement {
        XCTAssertTrue(e.waitForExistence(timeout: timeout), "\(what) not found:\n\(app.debugDescription)")
        return e
    }

    private func any(_ id: String) -> XCUIElement { app.descendants(matching: .any).matching(identifier: id).firstMatch }

    private func labelled(_ text: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    /// The menu's switch: a menu item labelled ultracode (its line after it).
    private var menuSwitch: XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "identifier == 'ultracode.switch' OR label BEGINSWITH 'ultracode'"))
            .matching(NSPredicate(format: "elementType == %d OR elementType == %d OR elementType == %d",
                                  XCUIElement.ElementType.button.rawValue, XCUIElement.ElementType.switch.rawValue,
                                  XCUIElement.ElementType.menuItem.rawValue)).firstMatch
    }

    /// The menu's switch.
    @MainActor private func switchInMenu() -> XCUIElement { see(menuSwitch, "the ultracode switch in the menu") }

    private var composerField: XCUIElement {
        app.textViews["shell-composer-field"].firstMatch.exists ? app.textViews["shell-composer-field"].firstMatch
            : app.textFields["shell-composer-field"].firstMatch
    }

    /// The shell composer opened with a short draft and the keyboard put away (a menu over the keyboard has room for
    /// only part of its items, and shows the rest on a drag that a test can't do without picking one), and its model
    /// button. `clearDraft()` takes the draft back out.
    @MainActor private func modesButton() -> XCUIElement {
        see(composerField, "the composer", timeout: 20)
        for _ in 0..<3 where !app.keyboards.firstMatch.exists {
            composerField.tap()
            _ = app.keyboards.firstMatch.waitForExistence(timeout: 4)
        }
        // A draft an earlier run left (drafts are kept) goes first.
        composerField.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 12) + "ok")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)).tap()
        _ = app.keyboards.firstMatch.waitForNonExistence(timeout: 5)
        return see(app.buttons["shell-composer-modes"], "the model button")
    }

    /// Takes the draft `modesButton()` typed back out, so the Agents list marks no row as a draft.
    @MainActor private func clearDraft() {
        guard composerField.waitForExistence(timeout: 5) else { return }
        for _ in 0..<3 where !app.keyboards.firstMatch.exists {
            composerField.tap()
            _ = app.keyboards.firstMatch.waitForExistence(timeout: 4)
        }
        composerField.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 12))
        sleep(2) // drafts are saved a moment later: not cut off by the app going away
    }

    /// Closes an open menu without picking anything.
    @MainActor private func closeMenu() { app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.1)).tap() }

    /// The requests the mock took (`ultracode` / `ultracode_dismiss_lost`), for one session.
    private func requests(_ session: String) throws -> [[String: Any]] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/ultracode")!)
        let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        let id = String(session.split(separator: "/").last!)
        return (obj?["requests"] as? [[String: Any]] ?? []).filter { $0["session_id"] as? String == id }
    }

    private func waitForRequest(_ session: String, _ key: String, _ value: Bool) throws -> Bool {
        for _ in 0..<50 {
            if try requests(session).contains(where: { $0[key] as? Bool == value }) { return true }
            Thread.sleep(forTimeInterval: 0.2)
        }
        return false
    }

    // MARK: The chat

    /// On: the model button carries the mark (read out as 「… ultracode 开」) and its menu has the switch on, with what
    /// it does under it and the Claude quota as its section's header (words, not a grayed item).
    @MainActor func testTheModelMenuHasTheSwitch() throws {
        launch(["-openSession", widget])
        let button = modesButton()
        XCTAssertTrue(button.value.map { "\($0)" }?.hasSuffix("ultracode 开") == true, "the button does not say it: \(String(describing: button.value))")
        XCTAssertTrue(any("ultracode.mark").exists || button.value != nil, "no mark on the model button")
        shot("chip-on")
        button.tap()
        let toggle = switchInMenu()
        XCTAssertTrue(toggle.label.contains("每个任务都编排多 Agent workflow") || labelled("每个任务都编排多 Agent workflow").exists,
                      "what it does: \(toggle.label)")
        XCTAssertTrue(labelled("Claude 额度").exists, "the Claude quota over it: \(toggle.label)")
        XCTAssertFalse(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Claude 额度")).firstMatch.exists,
                       "the quota drawn as a (disabled) item")
        shot("menu-on")
        closeMenu()
        clearDraft()
    }

    /// At rest: turning it on goes alone ({"ultracode": true}), and the mark comes once the node says it is on.
    @MainActor func testTurningItOnAtRest() throws {
        launch(["-openSession", blog])
        let button = modesButton()
        XCTAssertFalse(button.value.map { "\($0)" }?.contains("ultracode") == true, "a mark while it is off")
        button.tap()
        switchInMenu().tap()
        let sent = try waitForRequest(blog, "ultracode", true)
        XCTAssertTrue(sent, "no {ultracode: true}: \(String(describing: try? requests(blog)))")
        XCTAssertEqual(try requests(blog).count, 1, "more than one request")
        let on = NSPredicate(format: "value ENDSWITH %@", "ultracode 开")
        expectation(for: on, evaluatedWith: app.buttons["shell-composer-modes"])
        waitForExpectations(timeout: 10)
        clearDraft()
    }

    /// A change queued elsewhere says it waits, not "Applies next turn" beside a switch it did not move.
    @MainActor func testAQueuedChangeSaysItWaits() throws {
        launch(["-openSession", tests])
        modesButton().tap()
        let toggle = switchInMenu()
        XCTAssertTrue(toggle.label.contains("有一次切换在排队") || labelled("有一次切换在排队").exists, "the queue's line: \(toggle.label)")
        shot("menu-queued")
        closeMenu()
        clearDraft()
    }

    /// The node's reason replaces the line; turning it on there is refused (409) and said once.
    @MainActor func testTheNodesReasonAndARefusal() throws {
        launch(["-openSession", release])
        modesButton().tap()
        let toggle = switchInMenu()
        XCTAssertTrue(toggle.label.contains("请升级到 Claude Code 2.1.284") || labelled("请升级到 Claude Code 2.1.284").exists,
                      "the node's reason as the line: \(toggle.label)")
        shot("menu-reason")
        toggle.tap()
        see(labelled("ultracode 没有切换成"), "the refusal, said")
        clearDraft()
    }

    /// After a resume turned it off: one line with Turn Back On ({"ultracode": true}), which goes once it is on.
    @MainActor func testTheResumeNoticeTurnsItBackOn() throws {
        launch(["-openSession", review])
        let note = see(any("ultracode.lost"), "the resume notice", timeout: 20)
        XCTAssertTrue(labelled("恢复后 ultracode 已关").exists)
        let reopen = see(app.buttons["ultracode.reopen"], "重新打开")
        XCTAssertLessThan(note.frame.height, 60, "not one line")
        shot("lost")
        reopen.tap()
        let sent = try waitForRequest(review, "ultracode", true)
        XCTAssertTrue(sent, "no {ultracode: true}: \(String(describing: try? requests(review)))")
        XCTAssertTrue(note.waitForNonExistence(timeout: 10), "the notice stays once it is on")
    }

    /// × takes the notice away ({"ultracode_dismiss_lost": true}) without touching the switch.
    @MainActor func testTheResumeNoticeCanBeDismissed() throws {
        launch(["-openSession", review])
        let note = see(any("ultracode.lost"), "the resume notice", timeout: 20)
        see(app.buttons["ultracode.dismissLost"], "×").tap()
        XCTAssertTrue(note.waitForNonExistence(timeout: 5), "still there")
        let sent = try waitForRequest(review, "ultracode_dismiss_lost", true)
        XCTAssertTrue(sent, "no dismiss request: \(String(describing: try? requests(review)))")
        XCTAssertFalse(try requests(review).contains { $0["ultracode"] != nil }, "dismissing switched it")
    }

    /// A Codex thread at "ultra" carries the same mark; it has no switch.
    @MainActor func testCodexUltraHasTheMarkNoSwitch() throws {
        launch(["-openSession", orders])
        let button = modesButton()
        XCTAssertFalse(button.value.map { "\($0)" }?.contains("ultracode") == true)
        shot("codex-ultra")
        button.tap()
        XCTAssertFalse(menuSwitch.waitForExistence(timeout: 2), "a switch for Codex")
        closeMenu()
        clearDraft()
    }

    // MARK: Lists and New Agent

    /// The Agents list: the mark beside a working row's title, none on the offline one the node still remembers on.
    @MainActor func testTheListMarksRowsThatHaveItOn() throws {
        launch(["-app.tab", "chats", "-tab", "dev", "-openAgentsFresh", "YES", "-dev.grouping", "recent", "-dev.folded", ""])
        // Working only: the cards that need you fill the first screen.
        see(app.buttons["agents.filter.working"], "the Working filter", timeout: 30).tap()
        let rows = app.buttons.matching(identifier: "agents.row")
        see(rows.firstMatch, "the Agents list")
        let widgetRow = rows.matching(NSPredicate(format: "label CONTAINS %@", "给天气 App 加主屏幕小组件")).firstMatch
        see(widgetRow, "the widget row")
        XCTAssertTrue(widgetRow.label.contains("ultracode 开"), "no mark on the row: \(widgetRow.label)")
        let blogRow = rows.matching(NSPredicate(format: "label CONTAINS %@", "博客迁移到 Astro")).firstMatch
        if blogRow.exists { XCTAssertFalse(blogRow.label.contains("ultracode"), "a mark while it is off: \(blogRow.label)") }
        shot("list")
        // Every terminal agent: the offline one the node still remembers on has no mark.
        app.buttons["agents.filter.terminal"].tap()
        let recipe = rows.matching(NSPredicate(format: "label CONTAINS %@", "菜谱网站加搜索")).firstMatch
        for _ in 0..<4 where !recipe.exists { app.swipeUp() }
        if recipe.exists { XCTAssertFalse(recipe.label.contains("ultracode"), "a mark on an offline row: \(recipe.label)") }
    }

    /// New Agent: off each time, sent only when turned on; grayed with why where Claude Code is too old.
    @MainActor func testNewAgentStartsOff() throws {
        launch(["-tab", "dev", "-showNewChat", "YES", "-newChat.machine", "macbook"])
        see(app.navigationBars["新对话"], "New Agent", timeout: 20)
        let toggle = app.switches["newChat.ultracode"]
        for _ in 0..<6 where !(toggle.exists && toggle.isHittable) { app.swipeUp() }
        see(toggle, "the ultracode switch")
        XCTAssertEqual(toggle.value as? String, "0", "on when the sheet opens")
        XCTAssertTrue(toggle.isEnabled)
        shot("new-chat")
        toggle.switches.firstMatch.tap()
        XCTAssertEqual(toggle.value as? String, "1")
    }

    @MainActor func testNewAgentGraysAnOldClaudeCode() throws {
        // Mac Studio's Claude Code is 2.1.250.
        launch(["-tab", "dev", "-showNewChat", "YES", "-newChat.machine", "studio", "-launcher.studio", ""])
        see(app.navigationBars["新对话"], "New Agent", timeout: 20)
        let toggle = app.switches["newChat.ultracode"]
        for _ in 0..<6 where !(toggle.exists && toggle.isHittable) { app.swipeUp() }
        see(toggle, "the ultracode switch")
        XCTAssertTrue(toggle.label.contains("要 2.1.284 或更新才能开 ultracode"), "why it is grayed: \(toggle.label)")
        XCTAssertFalse(toggle.isEnabled, "usable on a too-old Claude Code")
        XCTAssertEqual(toggle.value as? String, "0")
        shot("new-chat-old")
    }
}
