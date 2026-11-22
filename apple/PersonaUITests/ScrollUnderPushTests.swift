import XCTest

/// Scrolling while the hub pushes (2026-11-01, TestFlight 77, the user: 「滚动列表的被迫刷新，影响体验的」). The real hub
/// pushes some machine's agents every two seconds, nearly half of them in a new order (recorded through a read-only
/// proxy, docs/archive/apple/live-updates.md「Forced refreshes while scrolling」). The mock pushes the same kinds at that pace (/mock/churn, one
/// round every 2 s, &real=1: rows that change height, an agent that finishes and starts again, the inbox, a question
/// that comes and goes, new steps in the open chat) while the test drags and flicks the list; the app watches every
/// frame of the scroll (`PerfScroll`, `-perfCounts YES`): what is on screen moves with the finger or the glide only.
///   PORT=18797 MOCK_AGENTS=1 MOCK_CHURN=1 MOCK_LONG_HISTORY=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_CHURN_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/ScrollUnderPushTests
/// Runs in the iPhone's Shell (docs/operations/testing.md).
final class ScrollUnderPushTests: XCTestCase {
    private var hub = ""
    private var app: XCUIApplication!
    private static let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // at work, long with MOCK_LONG_HISTORY
    private static let persona = "server/b2e4d6f8-0000-4000-8000-000000000004"  // a long Persona conversation
    private static let rounds = 10

    override func setUpWithError() throws {
        continueAfterFailure = true
        guard let h = ProcessInfo.processInfo.environment["PERSONA_CHURN_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_CHURN=1 MOCK_LONG_HISTORY=1 (PERSONA_CHURN_HUB)")
        }
        hub = h
        _ = mock("reset")
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

    private func launch(chat: String = "", tab: String = "chats", _ extra: [String] = []) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // Paired with another hub than last time, the app starts over on Assistant: pair first, then open the page.
        app.launchArguments = ["-pairURL", pair]
        app.launchPinned()
        sleep(3)
        app.terminate()
        app.launchArguments = ["-app.tab", tab, "-dev.openChat", chat, "-tab", tab == "chats" ? "dev" : tab,
                               "-openAgentsFresh", "YES", "-openChatsFresh", "YES", "-perfCounts", "YES"] + extra
        app.launchPinned()
    }

    @discardableResult
    private func mock(_ what: String) -> [String: Any] {
        let done = expectation(description: what)
        var out: [String: Any] = [:]
        URLSession.shared.dataTask(with: URL(string: hub + "/mock/" + what)!) { data, _, _ in
            out = (data.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]) ?? [:]
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        return out
    }

    /// The scroll counts (`PerfScroll.summary`).
    private func scroll() -> [String: Any] {
        let probe = app.staticTexts["perf.counts"]
        XCTAssertTrue(probe.waitForExistence(timeout: 5), "perf.counts (launched with -perfCounts YES)")
        let d = (try? JSONSerialization.jsonObject(with: Data(probe.label.utf8))) as? [String: Any] ?? [:]
        return d["scroll"] as? [String: Any] ?? [:]
    }

    /// Drags and flicks, up and down, while the mock pushes `rounds` rounds two seconds apart; then what the scrolling
    /// met, as the difference of the counts.
    private func scrollThroughPushes(_ name: String) -> [String: Int] {
        let w = app.windows.firstMatch
        let high = w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.32))
        let low = w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
        let before = scroll()
        mock("churn?n=\(Self.rounds)&ms=2000&steps=1&inbox=1&waiting=1&real=1")
        let deadline = Date.now.addingTimeInterval(TimeInterval(Self.rounds * 2 + 15))
        var i = 0
        while (mock("churned")["rounds"] as? Int ?? 0) < Self.rounds, Date.now < deadline {
            switch i % 4 {
            case 0: low.press(forDuration: 0.02, thenDragTo: high, withVelocity: 2500, thenHoldForDuration: 0) // a flick: a glide
            case 1: high.press(forDuration: 0.05, thenDragTo: low, withVelocity: 250, thenHoldForDuration: 0.4) // the finger on it
            case 2: low.press(forDuration: 0.05, thenDragTo: high, withVelocity: 250, thenHoldForDuration: 0.4)
            default: high.press(forDuration: 0.02, thenDragTo: low, withVelocity: 2500, thenHoldForDuration: 0)
            }
            usleep(600_000)
            i += 1
        }
        let after = scroll()
        var out: [String: Int] = [:]
        for k in ["frames", "jumps", "big", "maxJump", "moved", "replaced", "stopped", "defaults", "landed"] {
            out[k] = (after[k] as? Int ?? 0) - (k == "maxJump" ? 0 : before[k] as? Int ?? 0)
        }
        let bodies = (after["bodies"] as? [String: Int] ?? [:]).map { k, v in (k, v - ((before["bodies"] as? [String: Int])?[k] ?? 0)) }
            .filter { $0.1 > 0 }.sorted { $0.0 < $1.0 }.map { "\($0.0) \($0.1)" }.joined(separator: ", ")
        let causes = (after["causes"] as? [String: Int] ?? [:]).map { k, v in (k, v - ((before["causes"] as? [String: Int])?[k] ?? 0)) }
            .filter { $0.1 > 0 }.sorted { $0.0 < $1.0 }.map { "\($0.0) \($0.1)" }.joined(separator: ", ")
        let text = "\(name): \(i) drags and flicks through \(Self.rounds) rounds of pushes; " +
            out.sorted { $0.key < $1.key }.map { "\($0.key) \($0.value)" }.joined(separator: ", ") +
            "\n  drawn while scrolling: \(bodies)\n  put down to: \(causes)"
        print("PERF " + text.replacingOccurrences(of: "\n", with: "\nPERF "))
        let att = XCTAttachment(string: text)
        att.name = "scroll " + name
        att.lifetime = .keepAlways
        add(att)
        XCTAssertGreaterThan(out["frames"] ?? 0, 300, "the scrolling was watched")
        return out
    }

    /// Until the chat `key` is open with its messages in (its `chat.scrollState` probe; in the shell both pages have one).
    private func waitForChat(_ key: String) {
        let probe = app.staticTexts.matching(NSPredicate(format: "identifier == 'chat.scrollState' AND label ENDSWITH %@", "key=" + key)).firstMatch
        XCTAssertTrue(probe.waitForExistence(timeout: 30), "the chat \(key) did not open")
        let deadline = Date.now.addingTimeInterval(20)
        func items() -> Int { Int(probe.label.split(separator: " ").first { $0.hasPrefix("items=") }?.dropFirst(6) ?? "") ?? 0 }
        while items() < 20, Date.now < deadline { usleep(500_000) }
        sleep(2)
    }

    private func assertStill(_ r: [String: Int], _ what: String) {
        XCTAssertEqual(r["big"], 0, "\(what): the content jumped (100 pt or more) away from the finger or the glide")
        XCTAssertEqual(r["replaced"], 0, "\(what): rows on screen were taken away and drawn anew (a flash)")
        XCTAssertEqual(r["stopped"], 0, "\(what): a glide was cut short")
        // A write to UserDefaults draws every @AppStorage view again: the list laid itself out anew mid-glide.
        XCTAssertEqual(r["defaults"], 0, "\(what): UserDefaults was written while scrolling")
    }

    // The Agents list: spaces trade places as their agents work, rows grow and shrink with the last line, a question
    // comes and goes at the top; none of it may move the list under the finger.
    func testAgentsListScrollsThroughPushes() {
        launch()
        XCTAssertTrue(app.buttons["agents.filter.all"].waitForExistence(timeout: 15))
        sleep(2)
        let r = scrollThroughPushes("Agents list")
        assertStill(r, "Agents list")
        XCTAssertEqual(r["moved"], 0, "Agents list: rows moved against each other while it was scrolled")
    }

    // An agent's chat at work (a new step every round) scrolled back through.
    func testAgentChatScrollsThroughPushes() {
        launch(chat: Self.widget, ["-showScrollState", "YES"])
        waitForChat(Self.widget)
        assertStill(scrollThroughPushes("Agent chat"), "Agent chat")
    }

    // The Persona chat, while the agents and the inbox are pushed around it.
    func testPersonaChatScrollsThroughPushes() {
        launch(tab: "assistant", ["-openAssistant", Self.persona, "-showScrollState", "YES"])
        waitForChat(Self.persona)
        assertStill(scrollThroughPushes("Persona chat"), "Persona chat")
    }
}
