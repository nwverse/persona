import XCTest

/// `probe.shell` (the app's `ShellShown`, docs/apple/ios-state.md「Probes and assertions」): needs `-probes YES` at launch.
struct ShellShown: Decodable {
    var strip, selected, tab: String
    var position: Double
    var moving, lingering, drawerOpen, chatRoot, workRoot: Bool
    var status: String
    var opened, folded: Int
    var trace: [String]
}

extension XCUIApplication {
    /// The shell's state from `probe.shell` (launched with `-probes YES`).
    /// One query for the one element (there is one shell): `probe(_:as:)` counts every match first, and the tests poll
    /// this every 0.1 s.
    var shellShown: ShellShown? {
        guard let text = descendants(matching: .any)["probe.shell"].firstMatch.value as? String,
              let obj = (try? JSONSerialization.jsonObject(with: Data(text.utf8))) as? [String: Any],
              let v = obj["v"], let data = try? JSONSerialization.data(withJSONObject: v) else { return nil }
        return try? JSONDecoder().decode(ShellShown.self, from: data)
    }

    /// What the shell's strip shows, one line: the page it rests on (`assistant` / `agents`, `moving` while it slides),
    /// the page in front (what the switch shows) and the app's tab, then the rest,
    /// e.g. "agents selected=agents tab=agents position=1.00 drawer=0 chatRoot=1 workRoot=1 status=none …".
    /// Both pages stay built and the hidden one can be back in the accessibility tree (its lists are UIKit), so
    /// `exists` / `isHittable` of a page's controls do not say which page is drawn. This used to be read from the
    /// pixels of the All chip (`agentsDrawn`), which broke when the chip moved into the list (56b2ead).
    var shellPage: String {
        guard let s = shellShown else { return "none" }
        return "\(s.strip) selected=\(s.selected) tab=\(s.tab) position=\(String(format: "%.2f", s.position))"
            + " drawer=\(s.drawerOpen ? 1 : 0) chatRoot=\(s.chatRoot ? 1 : 0) workRoot=\(s.workRoot ? 1 : 0)"
            + " status=\(s.status) opened=\(s.opened) folded=\(s.folded) lingering=\(s.lingering ? 1 : 0)"
    }

    /// One `key=value` of `shellPage` (`status`, `opened`…).
    func shellValue(_ key: String) -> String? {
        shellPage.split(separator: " ").first { $0.hasPrefix(key + "=") }.map { String($0.dropFirst(key.count + 1)) }
    }

    /// The shell's latest events (switch taps, routes, slides, settles), for a failure's message.
    var shellTrace: String {
        "[\(shellPage)] trace: " + (shellShown?.trace.joined(separator: " | ") ?? "")
    }

    /// Whether Agents is what the strip rests on, with the switch and the app's tab on Agents too (TestFlight 70: the
    /// switch said Agents over the empty chat; 2026-11-02: the tab said Agents over Assistant and the switch's Agents did
    /// nothing. The page, the selection and the tab must agree).
    func agentsDrawn() -> Bool { shellPage.hasPrefix("agents selected=agents tab=agents ") }

    /// Whether Assistant is what the strip rests on, with the switch and the app's tab on Assistant.
    func assistantDrawn() -> Bool { shellPage.hasPrefix("assistant selected=assistant tab=assistant ") }

    /// Waits (the pages slide) until the strip rests on Agents (`shown`) or on Assistant.
    func waitAgentsDrawn(_ shown: Bool) -> Bool {
        let met = { shown ? self.agentsDrawn() : self.assistantDrawn() }
        var now = met()
        for _ in 0..<10 where !now {
            usleep(300_000)
            now = met()
        }
        return now
    }
}

/// The shell's switch as a finger uses it (2026-11-02: the user's taps on Agents did nothing): taps at the points of
/// Assistant and Agents, read once from the switch on Assistant at launch. Not the accessibility elements: the tree has offered a
/// hidden page's copy before (`ShellSwitchTests`), and a tap on an element is not where a thumb lands.
@MainActor struct ShellFinger {
    let app: XCUIApplication
    private let assistant: CGPoint
    private let agents: CGPoint

    /// The switch's points: they never move (the hub's link floats, #413).
    init(_ app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
        self.app = app
        let a = app.buttons["shell-assistant"].firstMatch, g = app.buttons["shell-agents"].firstMatch
        XCTAssertTrue(a.waitForExistence(timeout: 15) && g.exists, "no 助手 | Agents switch", file: file, line: line)
        assistant = CGPoint(x: a.frame.midX, y: a.frame.midY)
        agents = CGPoint(x: g.frame.midX, y: g.frame.midY)
    }

    /// Taps the segment, no waiting (quick taps, taps while the strip slides).
    func tap(agents toAgents: Bool) {
        let p = toAgents ? agents : assistant
        app.windows.firstMatch.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: p.x, dy: p.y)).tap()
    }

    /// Taps the segment, then within `limit` seconds the strip must rest on that page with the switch and the app's
    /// tab on it too (docs/apple/shell.md「Consistency and timeliness」: the slide takes about 0.4 s; the rest is the probe's reads).
    @discardableResult
    func switchTo(agents toAgents: Bool, _ what: String, limit: TimeInterval = 1.5,
                  file: StaticString = #filePath, line: UInt = #line) -> Bool {
        tap(agents: toAgents)
        return app.expectShell(agents: toAgents, what, limit: limit, file: file, line: line)
    }
}

extension XCUIApplication {
    /// Within `limit` seconds the strip rests on Agents (or Assistant) with the switch and the app's tab on it too; a failure
    /// says what the probe saw last and the shell's latest events.
    @MainActor @discardableResult
    func expectShell(agents: Bool, _ what: String, limit: TimeInterval = 1.5,
                     file: StaticString = #filePath, line: UInt = #line) -> Bool {
        let start = Date()
        let met = { agents ? self.agentsDrawn() : self.assistantDrawn() }
        var ok = met()
        while !ok, Date().timeIntervalSince(start) < limit {
            usleep(100_000)
            ok = met()
        }
        if !ok {
            let shot = XCTAttachment(screenshot: screenshot())
            shot.name = what
            shot.lifetime = .keepAlways
            XCTContext.runActivity(named: what) { $0.add(shot) }
            XCTFail("\(agents ? "Agents" : "助手") is not in front \(String(format: "%.1f", limit)) s after: \(what) \(shellTrace)",
                    file: file, line: line)
        }
        return ok
    }
}
