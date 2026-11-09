import Darwin
import XCTest

/// #1941: the new shell's Agents page left with only a chat's large title over a black page (TestFlight, never
/// reproduced). Presses the moments a push and a pop can meet: a chat opened from outside the list (a notification, the
/// runtime CLI's `navigate … keep`, which sets `model.openChat` the same way) while Back is on its way, during a back
/// swipe let go halfway or carried through, while the list reorders under a busy hub, while the shell changes page, and
/// while the tree is rebuilt (the background snapshot's width change). After each step: the list or a chat, never neither.
/// Against scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_CHURN=1 (PERSONA_MOCK_HUB); PERSONA_RACE_ROUNDS (default 1).
final class AgentsPushPopRaceTests: XCTestCase {
    private static let a = "macbook/a1f3c2d4-0000-4000-8000-000000000007" // 「菜谱网站加搜索」
    private static let b = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // 「给天气 App 加主屏幕小组件」
    private static let port = 18958
    private static let token = UUID().uuidString
    private var hub = ""
    private var blanks: [String] = []

    @MainActor func testPushPopRaces() throws {
        let app = try launchForRaces()
        let rounds = Self.rounds
        let gaps: [UInt32] = [0, 60, 120, 200, 300, 450]
        var unread = 0 // the next Unread (finished, not seen) agent of MOCK_AGENTS (platform's 4…38, mobile's 101…105)
        for round in 0..<rounds {
            for gap in gaps {
                let tag = "r\(round)-\(gap)ms"
                // Back, and a chat opened from outside while the pop is on its way.
                open(app, Self.a)
                back(app); after(gap) { Self.ctl("navigate", ["scene": "chat", "session": Self.b, "keep": true]) }
                check(app, "back-open-other-\(tag)")
                open(app, Self.a)
                back(app); after(gap) { Self.ctl("navigate", ["scene": "chat", "session": Self.a, "keep": true]) }
                check(app, "back-open-same-\(tag)")
                // An Unread agent opened (held in its place, #2509), and Back: it moves as the pop lands (the last ones of
                // a space fold it into Idle Spaces), with a chat opened from outside meanwhile.
                unread += 1
                let held = unread % 7 == 0 ? "macbook/e5f7a9b1-0000-4000-8000-%012d" : "studio/e5f7a9b1-0000-4000-8000-%012d"
                open(app, String(format: held, unread % 7 == 0 ? 100 + unread / 7 : 3 + unread))
                back(app); after(gap) { Self.ctl("navigate", ["scene": "chat", "session": Self.b, "keep": true]) }
                check(app, "held-back-open-\(tag)")
                // Opened from outside, and Back before the push has landed.
                toList(app)
                Self.ctl("navigate", ["scene": "chat", "session": Self.a, "keep": true]); usleep(gap * 1000)
                back(app, wait: false)
                check(app, "open-back-\(tag)")
                // A back swipe let go halfway, and one carried through, with a chat opened meanwhile.
                open(app, Self.a)
                after(gap) { Self.ctl("navigate", ["scene": "chat", "session": Self.b, "keep": true]) }
                edgeSwipe(app, to: 0.35)
                check(app, "half-swipe-open-\(tag)")
                open(app, Self.a)
                after(gap) { Self.ctl("navigate", ["scene": "chat", "session": Self.b, "keep": true]) }
                edgeSwipe(app, to: 0.9)
                check(app, "full-swipe-open-\(tag)")
                // The shell changes page while a push or a pop is on its way.
                toList(app)
                Self.ctl("navigate", ["scene": "chat", "session": Self.a, "keep": true]); usleep(gap * 1000)
                Self.ctl("navigate", ["scene": "assistant", "keep": true]); usleep(300_000)
                Self.ctl("navigate", ["scene": "agents", "keep": true])
                check(app, "open-switch-\(tag)")
                back(app); usleep(gap * 1000)
                Self.ctl("navigate", ["scene": "assistant", "keep": true]); usleep(300_000)
                Self.ctl("navigate", ["scene": "agents", "keep": true])
                check(app, "back-switch-\(tag)")
                // A new tree (the width class swapped for a background snapshot) while a push or a pop is on its way.
                toList(app)
                Self.ctl("navigate", ["scene": "chat", "session": Self.a, "keep": true]); usleep(gap * 1000)
                Self.ctl("reset", [:])
                check(app, "open-rebuild-\(tag)")
                open(app, Self.a)
                back(app); usleep(gap * 1000)
                Self.ctl("reset", [:])
                check(app, "back-rebuild-\(tag)")
            }
        }
        XCTAssertTrue(blanks.isEmpty, "a page with nothing on it after: \(blanks.joined(separator: ", "))")
    }

    /// The one that left the page blank (BeautifulUI, 1 in 18): a back swipe carried through, and a chat opened from
    /// outside while the pop finishes. Repeated round the moment it hit, the edge where the open stops being dropped and lands (PERSONA_RACE_ROUNDS × 26 tries).
    @MainActor func testBackSwipeMeetsAnOpen() throws {
        let app = try launchForRaces()
        let rounds = Self.rounds
        for round in 0..<rounds {
            for gap in stride(from: 280, through: 380, by: 4) as StrideThrough<UInt32> {
                open(app, Self.a)
                after(gap) { Self.ctl("navigate", ["scene": "chat", "session": Self.b, "keep": true]) }
                edgeSwipe(app, to: 0.9)
                check(app, "swipe-open-r\(round)-\(gap)ms")
            }
        }
        XCTAssertTrue(blanks.isEmpty, "a page with nothing on it after: \(blanks.joined(separator: ", "))")
    }

    private static var rounds: Int { Int(ProcessInfo.processInfo.environment["PERSONA_RACE_ROUNDS"] ?? "") ?? 1 }

    @MainActor private func launchForRaces() throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-holdChromeOpen", "YES",
                               "-runtimeCLI", "\(Self.port)", "-runtimeCLIToken", Self.token]
        app.launchPinned()
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.buttons["允许"]
        if allow.waitForExistence(timeout: 2) { allow.tap() }
        // A chat the last run left open comes back (`dev.openChat`): from the list.
        _ = app.buttons["agents.filter.all"].waitForExistence(timeout: 20)
        toList(app)
        XCTAssertTrue(list(app), "the Agents list:\n\(app.debugDescription)")
        mock("churn?n=2000&ms=150&inbox=1&waiting=1&real=1") // the list moving under every step
        return app
    }

    // MARK: Steps

    /// The chat on screen (opened from outside the list).
    @MainActor private func open(_ app: XCUIApplication, _ key: String) {
        Self.ctl("navigate", ["scene": "chat", "session": key, "keep": true])
        _ = backButton(app).waitForExistence(timeout: 5)
    }

    /// At the list: Back while a chat is open.
    @MainActor private func toList(_ app: XCUIApplication) {
        for _ in 0..<3 where !list(app) { back(app); sleep(1) }
    }

    @MainActor private func back(_ app: XCUIApplication, wait: Bool = true) {
        let b = backButton(app)
        if wait ? b.waitForExistence(timeout: 3) : b.exists, b.isHittable { b.tap() }
    }

    /// From the left edge, let go at `to` of the width.
    @MainActor private func edgeSwipe(_ app: XCUIApplication, to: CGFloat) {
        let start = app.coordinate(withNormalizedOffset: CGVector(dx: 0.005, dy: 0.5))
        start.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: to, dy: 0.5)),
                    withVelocity: .slow, thenHoldForDuration: 0.05)
    }

    // MARK: Looking

    @MainActor private func backButton(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label == '返回'")).firstMatch // the chat's own, by its title
    }

    /// The list at the root: its chips there (under the shell's bar when scrolled), no page pushed over it.
    @MainActor private func list(_ app: XCUIApplication) -> Bool {
        app.buttons["agents.filter.all"].exists && !app.navigationBars.buttons["BackButton"].exists
    }

    /// Settled, the list or a chat is on screen; a second look a while later, so a page still sliding is not counted.
    @MainActor private func check(_ app: XCUIApplication, _ step: String) {
        usleep(1_200_000)
        let shown = list(app) ? "list" : backButton(app).exists ? "chat" : "neither"
        print("RACE step \(step): \(shown)")
        if shown != "neither" { return }
        sleep(3)
        if list(app) || backButton(app).exists { return }
        // Seen (#1941): the stack has a page pushed (its system back button, 「Agents」) with nothing on it, under the
        // shell's bar, which only shows at the root.
        blanks.append(step)
        let bars = app.navigationBars.allElementsBoundByIndex.map { "\($0.identifier)" }
        print("RACE-BLANK \(step) navbars=\(bars)\n\(app.debugDescription)")
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = "blank-\(step)"
        shot.lifetime = .keepAlways
        add(shot)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? XCUIScreen.main.screenshot().pngRepresentation
                .write(to: URL(fileURLWithPath: dir).appendingPathComponent("race-blank-\(step).png"))
        }
        // Out of it for the next step: a new tree at the list.
        Self.ctl("navigate", ["scene": "agents"])
        _ = app.buttons["agents.filter.all"].waitForExistence(timeout: 10)
    }

    // MARK: Plumbing

    private func after(_ ms: UInt32, _ run: @escaping @Sendable () -> Void) {
        DispatchQueue.global().asyncAfter(deadline: .now() + .milliseconds(Int(ms)), execute: run)
    }

    /// One runtime CLI command (docs/operations/runtime-cli.md): a JSON line to the app's loopback port, a JSON line back.
    @discardableResult nonisolated private static func ctl(_ cmd: String, _ args: [String: Any]) -> [String: Any] {
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { return [:] }
        defer { close(fd) }
        var addr = sockaddr_in()
        addr.sin_family = sa_family_t(AF_INET)
        addr.sin_port = in_port_t(UInt16(port).bigEndian)
        addr.sin_addr.s_addr = inet_addr("127.0.0.1")
        let ok = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        guard ok == 0, var line = try? JSONSerialization.data(withJSONObject: ["token": token, "cmd": cmd, "args": args]) else {
            print("RACE ctl \(cmd): no connection")
            return [:]
        }
        line.append(0x0A)
        _ = line.withUnsafeBytes { send(fd, $0.baseAddress, line.count, 0) }
        var reply = Data()
        var buf = [UInt8](repeating: 0, count: 1 << 16)
        while reply.last != 0x0A {
            let n = recv(fd, &buf, buf.count, 0)
            if n <= 0 { break }
            reply.append(contentsOf: buf[0..<n])
        }
        let r = (try? JSONSerialization.jsonObject(with: reply)) as? [String: Any] ?? [:]
        if r["ok"] as? Bool != true { print("RACE ctl \(cmd): \(r["error"] ?? "no answer")") }
        return r
    }

    @discardableResult private func mock(_ path: String) -> [String: Any] {
        guard let url = URL(string: hub + "/mock/" + path), let data = try? Data(contentsOf: url) else { return [:] }
        return (try? JSONSerialization.jsonObject(with: data)) as? [String: Any] ?? [:]
    }
}
