import XCTest

/// The user's report (2026-11-03): from the inbox's Needs You, an agent's row opens its chat in Agents; back from it and
/// switched about, Assistant showed the inbox under its own switch and took neither drags nor taps, and the Agents chat had
/// the switch over its title. Every step here: the strip, the switch and the app's tab agree (`ShellFinger`), the page
/// in front is the one drawn, the switch is never over a pushed page, and the page in front scrolls and takes taps.
final class ShellInboxAgentTests: XCTestCase {
    private var app: XCUIApplication!
    private var finger: ShellFinger!

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor private func attachScreen(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// Waits until `e` is on screen and takes taps (a page under the other one exists but is hidden).
    @MainActor private func reachable(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 8) {
        let ok = NSPredicate(format: "exists == true AND hittable == true")
        let met = XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: ok, object: e)], timeout: timeout) == .completed
        if !met {
            attachScreen("not reachable: \(what)")
            print("INBOXTREE-BEGIN \(what)\n\(app.debugDescription)\nINBOXTREE-END")
        }
        XCTAssertTrue(met, "\(what) is not on screen \(app.shellTrace)")
    }

    /// Neither page's switch can be tapped: a pushed page is in front (the switch over its title was the bug).
    @MainActor private func noSwitch(_ what: String) {
        for id in ["shell-assistant", "shell-agents"] {
            let hit = app.buttons.matching(identifier: id).allElementsBoundByIndex.contains { $0.exists && $0.isHittable }
            if hit { attachScreen("switch over \(what)") }
            XCTAssertFalse(hit, "the switch (\(id)) is over \(what) \(app.shellTrace)")
        }
    }

    /// The probe says which page's root is in front: `chatRoot` (Assistant) and `workRoot` (Agents).
    @MainActor private func roots(chat: Bool? = nil, work: Bool? = nil, _ what: String) {
        var ok = false
        for _ in 0..<15 {
            ok = (chat.map { app.shellValue("chatRoot") == ($0 ? "1" : "0") } ?? true)
                && (work.map { app.shellValue("workRoot") == ($0 ? "1" : "0") } ?? true)
            if ok { break }
            usleep(200_000)
        }
        if !ok { attachScreen("roots: \(what)") }
        XCTAssertTrue(ok, "roots are not chat=\(chat.map(String.init) ?? "any") work=\(work.map(String.init) ?? "any"): \(what) \(app.shellTrace)")
    }

    /// Back as a finger does it: the edge swipe, held and slow.
    @MainActor private func swipeBack() {
        sleep(1) // once the push has settled
        let w = app.windows.firstMatch
        w.coordinate(withNormalizedOffset: CGVector(dx: 0.005, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)),
                   withVelocity: .slow, thenHoldForDuration: 0.1)
    }

    /// Demo Mode on Assistant's empty chat; the inbox from the drawer, an agent's row from its Needs You.
    @MainActor private func openAgentFromInbox() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-newAssistant", "YES", "-tab", "assistant",
                               "-dev.openChat", "", "-probes", "YES"]
        app.launchPinned()
        finger = ShellFinger(app)
        app.buttons["assistant-sidebar-button"].firstMatch.tapCenter()
        let inboxEntry = app.buttons["sidebar-inbox"].firstMatch
        XCTAssertTrue(inboxEntry.waitForExistence(timeout: 5), "no 收件箱 in the drawer")
        inboxEntry.tap()
        let inbox = app.navigationBars["收件箱"].firstMatch
        reachable(inbox, "收件箱 pushed on 助手")
        let row = app.buttons["inbox.agentRow"].firstMatch
        reachable(row, "an agent in the inbox's 需要你")
        sleep(1) // the push has settled
        row.tap()
    }

    /// No slide here ended late (`ShellPager.settleLimit`'s backstop): each ended with its animation.
    @MainActor private func noLostSettle(_ what: String) {
        let lost = app.shellTrace.contains("settle lost")
        // `settle late` is the strip ending on time without `.removed`: on this path Assistant's root is out of the window.
        if lost { attachScreen("settle lost: \(what)") }
        XCTAssertFalse(lost, "a slide did not end with its animation: \(what) \(app.shellTrace)")
    }

    /// Inbox › Needs You › an agent: its chat in Agents, nothing of the shell over it; back: the Agents list with its
    /// switch; Assistant: still the inbox, as it was left, with its own bar; back from it: Assistant's root; Agents again.
    @MainActor func testAnAgentFromTheInboxThenBackAndSwitch() {
        openAgentFromInbox()
        app.expectShell(agents: true, "an agent opened from the inbox", limit: 2.5)
        roots(chat: false, work: false, "its chat open in Agents, the inbox still on 助手")
        noSwitch("the agent's chat")
        noLostSettle("Agents slid in for the inbox's agent")
        attachScreen("1 chat from the inbox")

        swipeBack()
        roots(work: true, "back at the Agents list")
        app.expectShell(agents: true, "back from the chat")
        reachable(app.buttons["agents-new"].firstMatch, "the Agents list's 新建会话 after the chat")
        attachScreen("2 agents list")

        finger.switchTo(agents: false, "助手 after the chat opened from the inbox")
        roots(chat: false, "助手 still on the inbox")
        let inbox = app.navigationBars["收件箱"].firstMatch
        reachable(inbox, "the inbox's own bar on 助手")
        noSwitch("the inbox")
        noLostSettle("助手 slid back over the inbox")
        attachScreen("3 inbox on 助手")
        // It scrolls and takes taps: the list moves under a drag, and its back button goes back.
        let row = app.buttons["inbox.agentRow"].firstMatch
        reachable(row, "the inbox's agent row")
        let before = row.frame.minY
        row.swipeUp()
        sleep(1)
        XCTAssertTrue(!row.exists || row.frame.minY < before - 10, "the inbox did not scroll (\(before) → \(row.frame.minY)) \(app.shellTrace)")
        app.swipeDown()
        sleep(1)
        inbox.buttons["BackButton"].firstMatch.tap()
        roots(chat: true, "back at 助手's root from the inbox")
        reachable(app.buttons["assistant-sidebar-button"].firstMatch, "助手's top bar after the inbox")
        app.expectShell(agents: false, "助手's root")

        finger.switchTo(agents: true, "Agents again")
        reachable(app.buttons["agents-new"].firstMatch, "the Agents list again")
        finger.switchTo(agents: false, "助手 again")
        reachable(app.buttons["assistant-sidebar-button"].firstMatch, "助手's top bar again")
        app.terminate()
    }

    /// The same chat, then straight to Assistant with the switch not there: a drag from Agents' left edge goes back in
    /// the chat (not to Assistant), and the list's switch to Assistant lands on the inbox as it was left.
    @MainActor func testAnAgentFromTheInboxThenSwitchFromTheList() {
        openAgentFromInbox()
        app.expectShell(agents: true, "an agent opened from the inbox", limit: 2.5)
        roots(work: false, "its chat open")
        swipeBack()
        roots(work: true, "back at the list")
        finger.switchTo(agents: false, "助手 from the list")
        reachable(app.navigationBars["收件箱"].firstMatch, "the inbox on 助手")
        noSwitch("the inbox")
        // Back to Agents from the inbox: the inbox goes back first, then the switch.
        app.navigationBars["收件箱"].firstMatch.buttons["BackButton"].firstMatch.tap()
        roots(chat: true, "助手's root")
        finger.switchTo(agents: true, "Agents from 助手's root")
        roots(work: true, "the Agents list")
        reachable(app.buttons["agents-new"].firstMatch, "the Agents list")
        noLostSettle("the whole way")
        app.terminate()
    }
}
