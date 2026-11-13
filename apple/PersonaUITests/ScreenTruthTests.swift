import XCTest

/// What the screen shows is the hub's truth, soon enough (docs/apple/ios-state.md「Truth and timeliness」; the user: holding back
/// updates must not leave the screen saying something that is no longer so). The Agents list exports what it shows
/// (`probe.agents-list`: each agent's state and last line, the questions), the mock says what it holds (`/mock/truth`)
/// and changes one thing at a time (`/mock/bump`):
/// - consistent: at rest the list shows what the hub holds;
/// - timely: what needs the user shows within 1 s even while a finger is on the list; anything else within 0.5 s of
///   the finger lifting, and within 5 s however long the scrolling goes on;
/// - converging: after any mix of pushes and gestures (pages, chats, the background, the network gone), the screen is
///   the hub's again within a bound;
/// - no lost taps: a row tapped opens the agent it showed;
/// - stale is visible: with the hub out of reach the title says so.
///   PORT=18797 MOCK_AGENTS=1 MOCK_CHURN=1 MOCK_LONG_HISTORY=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_CHURN_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/ScreenTruthTests
/// Both styles (TEST_RUNNER_PERSONA_UI_STYLE=beautiful).
final class ScreenTruthTests: XCTestCase {
    private var hub = ""
    private var app: XCUIApplication!

    /// What `probe.agents-list` and `/mock/truth` hold.
    private struct Shown: Decodable {
        var states: [String: String]
        var last: [String: String]
        var asks: [String]
        var titles: [String: String]?
    }

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

    /// The Agents list, paired with the mock (pairing a new hub starts the app over on Assistant: pair, then open it).
    private func launch() {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair]
        app.launchPinned()
        sleep(3)
        app.terminate()
        app.launchArguments = ["-app.tab", "chats", "-tab", "dev", "-dev.openChat", "", "-openAgentsFresh", "YES",
                               "-probes", "YES", "-perfCounts", "YES", "-showScrollState", "YES"]
        app.launchPinned()
        XCTAssertTrue(app.buttons["agents.filter.all"].firstMatch.waitForExistence(timeout: 15), "the Agents list")
    }

    @discardableResult
    private func mock(_ what: String) -> [String: Any] {
        var out: [String: Any] = [:]
        let done = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: URL(string: hub + "/mock/" + what)!) { data, _, _ in
            out = (data.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]) ?? [:]
            done.signal()
        }.resume()
        _ = done.wait(timeout: .now() + 10)
        return out
    }

    /// `what` sent `after` seconds from now, off this thread (the test's own thread is busy with a gesture then); the
    /// moment it went out lands in the box.
    private func bumpLater(_ what: String, after: TimeInterval, sent: Box) {
        DispatchQueue.global().asyncAfter(deadline: .now() + after) { [hub] in
            sent.at = .now
            let done = DispatchSemaphore(value: 0)
            URLSession.shared.dataTask(with: URL(string: hub + "/mock/bump?what=" + what)!) { data, _, _ in
                sent.reply = (data.flatMap { try? JSONSerialization.jsonObject(with: $0) } as? [String: Any]) ?? [:]
                done.signal()
            }.resume()
            _ = done.wait(timeout: .now() + 10)
        }
    }

    private final class Box: @unchecked Sendable {
        var at: Date?
        var reply: [String: Any] = [:]
    }

    private var page: String? { "agents" }

    /// Back on the Agents list, by the controls a user would use: the chat's back button (or a drag from the left edge,
    /// only while a chat is open), the drawer's close, a drag to the left from Assistant in the shell. (A drag from the left
    /// edge with no chat open is not it: in the shell it pages to Assistant, and there it opens the drawer.)
    private func backToList() {
        // The filter bar scrolls away with the list: a row or a card that needs you in reach says it as well. Any of its
        // chips or cards that need you keep the rows out of sight; scrolled into those cards,
        // neither chips nor rows are on screen (#836, #926).
        func onList() -> Bool {
            app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "agents.filter.")).allElementsBoundByIndex.contains { $0.isHittable }
                || app.descendants(matching: .any).matching(identifier: "agents.interaction").allElementsBoundByIndex.contains { $0.isHittable }
                || app.buttons.matching(identifier: "agents.row").allElementsBoundByIndex.contains { $0.isHittable }
        }
        let w = app.windows.firstMatch
        for attempt in 0..<4 {
            if onList() { return }
            // The chat has two: its header's and the folded header's (in the status bar's band, a tap there does not go
            // back). The lowest first, then the next.
            let backs = app.buttons.matching(NSPredicate(format: "label IN %@", ["返回", "Back"])).allElementsBoundByIndex
                .filter { $0.isHittable }.sorted { $0.frame.minY > $1.frame.minY }
            // BeautifulUI's folded header sits in the status bar's band: no back button in reach then.
            let chatOpen = app.probeSeq("chat", page: "agents") != nil
            if !backs.isEmpty {
                backs[attempt % backs.count].tap()
            } else if chatOpen {
                w.coordinate(withNormalizedOffset: CGVector(dx: 0.01, dy: 0.5))
                    .press(forDuration: 0.05, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.8, dy: 0.5)))
            } else if app.descendants(matching: .any)["assistant-drawer-close"].firstMatch.exists {
                app.descendants(matching: .any)["assistant-drawer-close"].firstMatch.tap()
            } else {
                w.coordinate(withNormalizedOffset: CGVector(dx: 0.85, dy: 0.45))
                    .press(forDuration: 0.05, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.1, dy: 0.45)))
            }
            sleep(1)
        }
        if !onList() {
            for (name, a) in [("not back on the list", XCTAttachment(screenshot: app.screenshot())),
                              ("not back on the list: tree", XCTAttachment(string: app.debugDescription))] {
                a.name = name
                a.lifetime = .keepAlways
                add(a)
            }
        }
        XCTAssertTrue(onList(), "back on the Agents list")
    }

    private func shown() -> Shown? { app.probe("agents-list", as: Shown.self, page: page) }
    private func shownAt() -> Date? { app.probeChanged("agents-list", page: page) }

    private func truth() -> Shown? {
        guard let data = try? JSONSerialization.data(withJSONObject: mock("truth")) else { return nil }
        return try? JSONDecoder().decode(Shown.self, from: data)
    }

    /// Where the screen and the hub differ; `last` (the agents' last lines) left out when it may lag.
    private func differs(last: Bool = true) -> [String] {
        guard let s = shown() else { return ["no probe.agents-list (launched with -probes YES?)"] }
        guard let t = truth() else { return ["no /mock/truth"] }
        var out = differences(shown: s.states, truth: t.states, label: "state ")
        if last { out += differences(shown: s.last, truth: t.last, label: "last ") }
        if s.asks != t.asks { out.append("asks: shown \(s.asks), hub \(t.asks)") }
        return out
    }

    /// A finger on the list for `seconds`: dragged a little, then held (the list is being scrolled all along).
    private func holdFinger(_ seconds: TimeInterval) {
        let w = app.windows.firstMatch
        w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6))
            .press(forDuration: 0.05, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)),
                   withVelocity: 200, thenHoldForDuration: seconds)
    }

    // Consistent: at rest the list says what the hub holds.
    func testTheListIsTheHubs() {
        launch()
        eventually("the list shows what the hub holds", within: 5) { differs() }
    }

    // Timely, what matters: an agent starting to need the user, a question coming, and a card read off a terminal
    // (with its agent turning blocked, #751) show while the finger is on the list, within 1 s; nothing waits for the
    // finger to lift.
    func testWhatNeedsTheUserShowsUnderTheFinger() {
        launch()
        eventually("at rest", within: 5) { differs() }
        for what in ["needs", "ask", "screen"] {
            let sent = Box()
            bumpLater(what, after: 0.8, sent: sent)
            holdFinger(3)
            let lifted = Date.now
            guard let at = sent.at else { return XCTFail("\(what) was not sent") }
            eventually("\(what) shows", within: 2) { differs(last: false) }
            guard let shownAt = shownAt() else { return XCTFail("no change time") }
            let delay = shownAt.timeIntervalSince(at)
            print(String(format: "TRUTH %@: shown %.2f s after the push, %.2f s before the finger lifted", what, delay, lifted.timeIntervalSince(shownAt)))
            XCTAssertLessThanOrEqual(delay, 1.0, "\(what): shown \(delay) s after the push")
            XCTAssertLessThan(shownAt, lifted, "\(what): waited for the finger to lift")
        }
    }

    // Timely, the rest: a new last line waits while the finger is down, and lands within 0.5 s of it lifting.
    func testQuietChangesLandWhenTheFingerLifts() {
        launch()
        eventually("at rest", within: 5) { differs() }
        let sent = Box()
        bumpLater("last", after: 0.8, sent: sent)
        holdFinger(3)
        let lifted = Date.now
        eventually("the last line lands", within: 2) { differs() }
        guard let shownAt = shownAt() else { return XCTFail("no change time") }
        let after = shownAt.timeIntervalSince(lifted)
        print(String(format: "TRUTH last line: landed %.2f s after the finger lifted", after))
        XCTAssertGreaterThan(after, -0.2, "landed under the finger")
        XCTAssertLessThanOrEqual(after, 0.8, "landed \(after) s after the finger lifted (0.5 s and the probe's reading)")
    }

    // Timely, however long the scrolling: a change waits at most 5 s.
    func testALongScrollStillLandsWithinTheCap() {
        launch()
        eventually("at rest", within: 5) { differs() }
        let sent = Box()
        bumpLater("last", after: 1.0, sent: sent)
        holdFinger(9)
        let lifted = Date.now
        eventually("the last line lands", within: 2) { differs() }
        guard let at = sent.at, let shownAt = shownAt() else { return XCTFail("no times") }
        let delay = shownAt.timeIntervalSince(at)
        print(String(format: "TRUTH long scroll: landed %.2f s after the push, %.2f s before the finger lifted", delay, lifted.timeIntervalSince(shownAt)))
        XCTAssertLessThanOrEqual(delay, 5.5, "waited \(delay) s")
        XCTAssertLessThan(shownAt, lifted, "waited for the finger to lift")
    }

    // Converging: a seeded random mix of pushes and what the user does; then the screen is the hub's within 8 s
    // (a reconnect included). The seed is printed: a failure replays with TEST_RUNNER_PERSONA_SEED.
    func testAnyMixConverges() {
        launch()
        let seed = UInt64(ProcessInfo.processInfo.environment["PERSONA_SEED"] ?? "") ?? 7
        var rng = SplitMix(seed)
        print("TRUTH seed \(seed)")
        let w = app.windows.firstMatch
        let high = w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.32))
        let low = w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
        var log: [String] = []
        for _ in 0..<18 {
            let op = Int(rng.next() % 11)
            switch op {
            case 0: low.press(forDuration: 0.02, thenDragTo: high, withVelocity: 2500, thenHoldForDuration: 0); log.append("flick up")
            case 1: high.press(forDuration: 0.02, thenDragTo: low, withVelocity: 2500, thenHoldForDuration: 0); log.append("flick down")
            case 2: low.press(forDuration: 0.05, thenDragTo: high, withVelocity: 250, thenHoldForDuration: 0.5); log.append("drag")
            case 3: mock("bump?what=last"); log.append("push last")
            case 4: mock("bump?what=needs"); log.append("push needs")
            case 5: mock("bump?what=ask"); log.append("push ask")
            case 6: mock("bump?what=resolve"); log.append("push resolve")
            case 7: mock("churn?n=3&ms=300&inbox=1&waiting=1&real=1"); sleep(1); log.append("churn")
            case 8:
                // Into a chat and back.
                let row = app.buttons.matching(identifier: "agents.row").allElementsBoundByIndex.first { $0.isHittable }
                if let row {
                    row.tap()
                    sleep(1)
                    backToList()
                    log.append("chat and back")
                }
            case 9:
                XCUIDevice.shared.press(.home)
                mock("bump?what=last")
                sleep(1)
                app.activate()
                log.append("background, push, back")
            default:
                mock("down?s=3")
                mock("bump?what=needs")
                mock("bump?what=last")
                log.append("network gone, pushes missed")
            }
            usleep(300_000)
        }
        print("TRUTH ops " + log.joined(separator: ", "))
        backToList() // if a step left it elsewhere
        let took = eventually("converged after: " + log.joined(separator: ", "), within: 8) { differs() }
        if let took { print(String(format: "TRUTH converged %.2f s after the last step", took)) }
    }

    // No lost taps: right after a drag (the list still holding what it showed), with pushes reordering it, a tapped row
    // opens the agent it showed. (A tap during a glide only stops the glide: no row is tapped then.)
    func testATapOpensTheRowShown() {
        launch()
        mock("churn?n=12&ms=500&inbox=1&real=1")
        let w = app.windows.firstMatch
        for i in 0..<4 {
            w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: i % 2 == 0 ? 0.7 : 0.35))
                .press(forDuration: 0.05, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: i % 2 == 0 ? 0.4 : 0.65)),
                       withVelocity: 200, thenHoldForDuration: 0.3)
            // The row nearest the middle of the screen (the top ones sit under the shell's floating bar), by its frame
            // (a List's rows off the screen cannot even say whether they are hittable).
            let mid = app.frame.midY
            let rows = app.buttons.matching(identifier: "agents.row").allElementsBoundByIndex
                .map { (label: $0.label, frame: $0.frame) }
                .filter { $0.frame.minY > 160 && $0.frame.maxY < app.frame.height - 120 }
            guard let row = rows.min(by: { abs($0.frame.midY - mid) < abs($1.frame.midY - mid) }) else { continue }
            let label = row.label
            w.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: row.frame.midX, dy: row.frame.midY)).tap()
            struct Chat: Decodable { var key: String }
            var opened: String?
            let until = Date.now.addingTimeInterval(8)
            while opened == nil, Date.now < until {
                opened = app.probe("chat", as: Chat.self, page: "agents")?.key
                if opened == nil { usleep(300_000) }
            }
            guard let opened,
                  let title = truth()?.titles?[opened] else { XCTFail("no chat opened from \(label)"); continue }
            XCTAssertTrue(label.contains(title), "tapped「\(label)」, opened \(opened)「\(title)」")
            w.coordinate(withNormalizedOffset: CGVector(dx: 0.01, dy: 0.5))
                .press(forDuration: 0.05, thenDragTo: w.coordinate(withNormalizedOffset: CGVector(dx: 0.8, dy: 0.5)))
            sleep(1)
        }
    }

    // Stale is visible: the hub out of reach, the title says so right after the drop's grace (8 s: a hub restart or a
    // short drop shows nothing, docs/operations/hub-restart.md), and says so until the link is back: within 25 s of the hub coming
    // back (the reconnect backs off, 1 s doubling up to 20 s: after 14 s down the next try is at about 15–23 s).
    func testStaleShows() {
        launch()
        struct Conn: Decodable { var shown: String }
        mock("down?s=14")
        eventually("the title shows the link is down", within: 11) {
            let s = app.probe("connection", as: Conn.self)?.shown ?? "?"
            return s == "online" ? ["shown: online"] : []
        }
        eventually("the title is clear again", within: 25) {
            let s = app.probe("connection", as: Conn.self)?.shown ?? "?"
            return s == "online" ? [] : ["shown: \(s)"]
        }
        eventually("and the list is the hub's", within: 5) { differs() }
    }
}

/// A small seeded generator (SplitMix64): the same seed, the same steps.
struct SplitMix {
    private var state: UInt64
    init(_ seed: UInt64) { state = seed }
    mutating func next() -> UInt64 {
        state &+= 0x9E37_79B9_7F4A_7C15
        var z = state
        z = (z ^ (z >> 30)) &* 0xBF58_476D_1CE4_E5B9
        z = (z ^ (z >> 27)) &* 0x94D0_49BB_1331_11EB
        return z ^ (z >> 31)
    }
}
