import XCTest

/// The user's report (2026-11-02): after reading an agent's chat and going back, the switch's Agents did not switch to
/// Agents. Taps are a finger's (`ShellFinger`: coordinates, not accessibility elements), at every pace: before the back
/// animation ends, right after, a while later, from a chat that is streaming, and faster than the strip slides. After
/// each, the strip, the switch and the app's tab must agree on the page tapped last, in time (docs/apple/shell.md).
final class ShellRaceTests: XCTestCase {
    private var app: XCUIApplication!
    private var finger: ShellFinger!

    override func setUp() {
        continueAfterFailure = true
    }

    @MainActor private func launch() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-newAssistant", "YES", "-dev.openChat", "",
                               "-tab", "assistant", "-dev.folded", ""]
        app.launchPinned()
        finger = ShellFinger(app)
        sleep(1)
    }

    @MainActor private func attachScreen(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// Demo Mode's agents: the first one waiting on the user, or the first one at work (its chat streams), picked with the
    /// list's own filter (the titles move about with the demo's time).
    private static let chats = ["需要你", "工作中"]

    @MainActor private func openAChat(row: Int) -> Bool {
        let origin = app.windows.firstMatch.coordinate(withNormalizedOffset: .zero)
        let chip = app.buttons["agents.filter." + (row == 0 ? "needsYou" : "working")].firstMatch
        guard chip.waitForExistence(timeout: 5) else { XCTFail("no filter chip for \(Self.chats[row])"); return false }
        origin.withOffset(CGVector(dx: chip.frame.midX, dy: chip.frame.midY)).tap()
        sleep(1)
        let screen = app.windows.firstMatch.frame
        guard let first = app.descendants(matching: .any).matching(identifier: "agents.row").allElementsBoundByIndex
            .filter({ $0.exists && screen.contains(CGPoint(x: $0.frame.midX, y: $0.frame.midY)) })
            .min(by: { $0.frame.minY < $1.frame.minY }) else {
            attachScreen("no row"); XCTFail("no \(Self.chats[row]) row on screen \(app.shellTrace)"); return false
        }
        origin.withOffset(CGVector(dx: first.frame.midX, dy: first.frame.midY)).tap()
        let opened = app.buttons["agents-new"].firstMatch.waitForNonExistence(timeout: 5)
        if !opened { attachScreen("chat not opened"); XCTFail("the chat did not open \(app.shellTrace)") }
        return opened
    }

    /// Back from the chat: the edge swipe, or the back chevron at the top left where a thumb taps it.
    @MainActor private func back(swipe: Bool) {
        let w = app.windows.firstMatch
        if swipe {
            w.coordinate(withNormalizedOffset: CGVector(dx: 0.005, dy: 0.5))
                .press(forDuration: 0.1, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)),
                       withVelocity: .fast, thenHoldForDuration: 0)
        } else {
            w.coordinate(withNormalizedOffset: CGVector(dx: 0.065, dy: 0.094)).tap()
        }
    }

    /// Agents → a chat → back → Assistant → Agents, the switch tapped `delay` seconds after the back. Each round on a fresh
    /// launch: one that goes wrong does not lead the next astray.
    @MainActor private func round(swipe: Bool, delay: Double, row: Int = 0) {
        let what = "\(swipe ? "swiped" : "tapped") back from \(Self.chats[row]), switch after \(delay)s"
        launch()
        defer { app.terminate() }
        guard finger.switchTo(agents: true, "Agents before (\(what))"), openAChat(row: row) else { return }
        sleep(row == 1 ? 3 : 1)
        back(swipe: swipe)
        if delay > 0 { usleep(useconds_t(delay * 1_000_000)) }
        // Tapped while the chat still slides away the tap may land on it (as with a finger); then it is tapped again
        // once the list is back, and must work then.
        if !finger.switchTo(agents: false, "助手 tapped (\(what))", limit: delay < 0.5 ? 2.5 : 1.5) { return }
        finger.switchTo(agents: true, "Agents tapped (\(what))")
        finger.switchTo(agents: false, "助手 again (\(what))")
    }

    @MainActor func testBackThenSwitchAtEveryPace() {
        for swipe in [false, true] {
            for delay in [0.0, 0.15, 0.4, 1.0] {
                round(swipe: swipe, delay: delay)
            }
        }
    }

    /// The working agent's chat streams while it is open (Demo Mode's Working row).
    @MainActor func testBackFromAStreamingChat() {
        for swipe in [false, true] {
            for delay in [0.0, 0.4] {
                round(swipe: swipe, delay: delay, row: 1)
            }
        }
    }

    /// Taps faster than the strip slides: none is lost, it ends on the one tapped last.
    @MainActor func testQuickTapsEndOnTheLast() {
        launch()
        for gap in [0.05, 0.15, 0.3] {
            let g = useconds_t(gap * 1_000_000)
            finger.tap(agents: true); usleep(g); finger.tap(agents: false); usleep(g); finger.tap(agents: true)
            app.expectShell(agents: true, "Agents, 助手, Agents \(gap)s apart")
            finger.tap(agents: false); usleep(g); finger.tap(agents: true); usleep(g); finger.tap(agents: false)
            app.expectShell(agents: false, "助手, Agents, 助手 \(gap)s apart")
        }
        // Two taps: the second while the first still slides.
        for gap in [0.1, 0.2] {
            finger.tap(agents: true); usleep(useconds_t(gap * 1_000_000)); finger.tap(agents: false)
            app.expectShell(agents: false, "Agents then 助手 \(gap)s later")
        }
    }

    /// A drag that takes the strip while a tap's slide is still under way: it ends where the drag settles, and the
    /// switch and the tab say so.
    @MainActor func testSwitchThenDragEndsWhereTheDragSettles() {
        launch()
        finger.tap(agents: true)
        usleep(150_000)
        // A drag to the right from Agents' left edge (on a row elsewhere it is the row's swipe) while it still slides
        // in: back to Assistant.
        let w = app.windows.firstMatch
        w.coordinate(withNormalizedOffset: CGVector(dx: 0.02, dy: 0.6))
            .press(forDuration: 0.05, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.6)),
                   withVelocity: .fast, thenHoldForDuration: 0)
        app.expectShell(agents: false, "Agents tapped, then dragged back while it slid", limit: 2)
        finger.switchTo(agents: true, "Agents after the drag")
    }

    /// A route from elsewhere (the empty chat's agent that needs the user, as a notification does it): Agents comes to
    /// the front with the chat, and the switch works both ways after going back.
    @MainActor func testRouteToAnAgentThenSwitch() {
        for swipe in [false, true] {
            launch()
            let needs = app.buttons["shell-needs-agent"].firstMatch
            XCTAssertTrue(needs.waitForExistence(timeout: 10), "no agent waiting on the empty chat")
            let f = needs.frame
            app.windows.firstMatch.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.midX, dy: f.midY)).tap()
            app.expectShell(agents: true, "an agent opened from the empty chat")
            XCTAssertTrue(app.buttons["agents-new"].firstMatch.waitForNonExistence(timeout: 5), "its chat did not open")
            sleep(1)
            back(swipe: swipe)
            finger.switchTo(agents: false, "助手 after the routed chat (\(swipe ? "swiped" : "tapped") back)", limit: 2.5)
            finger.switchTo(agents: true, "Agents after the routed chat")
            app.terminate()
        }
    }
}
