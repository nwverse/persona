import XCTest

/// The chat stays at the newest message (scripts/mock/mock_hub.py with MOCK_LONG_HISTORY=1, PERSONA_MOCK_HUB): a long chat
/// opens at the bottom; at the bottom a new reply is followed; scrolled up, ↓ (Jump to Latest) shows and brings you back; a
/// reply that arrives while you read history does not pull you down. On an Agent's chat (working, Unread, and opened
/// where you left off) and on the Persona page. Replies come from `/mock/say`. Where the list is comes from the app's `-showScrollState` probe:
/// XCUITest's frames of rows in the flipped list are wrong (docs/lessons/ios.md).
final class ChatFollowTests: XCTestCase {
    private var hub: String!
    private let working = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // 「给天气 App 加主屏幕小组件」, 15 exchanges in front
    private let persona = "server/b2e4d6f8-0000-4000-8000-000000000004"   // 「整理这个季度的读书笔记」, a long one

    override func setUpWithError() throws {
        continueAfterFailure = false
        if name.contains("RealHub") { return } // its own hub
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_LONG_HISTORY=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    /// The chat the test opened ("<machine>/<session>"): in the shell both pages are alive, the other one's chat
    /// (the Persona conversation behind the Agents page) has a probe too.
    private var opened: String?

    @MainActor private func launch(_ open: [String], keepCache: Bool = false) -> XCUIApplication {
        if let i = open.firstIndex(where: { $0 == "-openSession" || $0 == "-openAssistant" }), i + 1 < open.count { opened = open[i + 1] }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // No chat kept on disk from an earlier run (unless `keepCache`, a cold start of this test's own): the mock starts
        // its conversations over at each reset, and a cached one from another test (its churned steps) took the seqs
        // this test's replies get; they did not show.
        app.launchArguments = ["-pairURL", pair, "-showScrollState", "YES", "-dev.openChat", ""]
            + (keepCache ? [] : ["-openChatsFresh", "YES"]) + open
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = ProcessInfo.processInfo.environment["PERSONA_STYLE"] ?? "light"
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/follow-\(name)-\(style).png"))
        }
    }

    /// The probe's numbers: bottom, follows, frozen, arrow, newestShown, behind (lines below the lowest one on screen),
    /// withheld (items not shown because the list holds still).
    @MainActor private func state(_ app: XCUIApplication) -> [String: Int] {
        let probes = app.staticTexts.matching(identifier: "chat.scrollState")
        let probe = opened.map { probes.matching(NSPredicate(format: "label ENDSWITH %@", "key=" + $0)).firstMatch } ?? probes.firstMatch
        guard probe.exists else { return [:] }
        var out: [String: Int] = [:]
        for pair in probe.label.split(separator: " ") {
            let kv = pair.split(separator: "=")
            if kv.count == 2, let v = Int(kv[1]) { out[String(kv[0])] = v }
        }
        return out
    }

    /// Waits until `ok` holds for the probe; fails with what it last said.
    @MainActor private func expect(_ app: XCUIApplication, _ why: String, timeout: TimeInterval = 10,
                                   _ ok: ([String: Int]) -> Bool) {
        let end = Date().addingTimeInterval(timeout)
        var last: [String: Int] = [:]
        while Date() < end {
            last = state(app)
            if !last.isEmpty, ok(last) { return }
            usleep(250_000)
        }
        shot(app, "failed-\(why.prefix(while: { $0 != ":" }))")
        XCTFail("\(why): \(app.staticTexts["chat.scrollState"].exists ? app.staticTexts["chat.scrollState"].label : "no probe") \(last)")
    }

    /// A reply arrives (after `user`'s message); `stream`: as growing text first, like the Persona assistant's.
    private func say(_ text: String, user: String? = nil, stream: Bool = false) {
        var c = URLComponents(string: hub + "/mock/say")!
        c.queryItems = [URLQueryItem(name: "text", value: text), URLQueryItem(name: "stream", value: stream ? "1" : "0")]
            + (user.map { [URLQueryItem(name: "user", value: $0)] } ?? [])
        _ = try? Data(contentsOf: c.url!)
    }

    /// Fingers dragging the list toward older messages (down on screen), a few times.
    @MainActor private func readOlder(_ app: XCUIApplication, times: Int = 3) {
        for _ in 0..<times {
            let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
            from.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.75)))
        }
        sleep(1)
    }

    /// The whole check on one open chat.
    @MainActor private func followAndReturn(_ app: XCUIApplication, _ name: String, stream: Bool = false) {
        // 1. Opens at the bottom: the newest line on screen, following, no ↓.
        expect(app, "\(name): did not open at the newest", timeout: 20) {
            $0["newestShown"] == 1 && $0["bottom"] == 1 && $0["arrow"] == 0
        }
        sleep(2) // the landing and the chrome have settled
        expect(app, "\(name): left the newest after opening") { $0["newestShown"] == 1 && $0["bottom"] == 1 }
        shot(app, "\(name)-opened")

        // 2. At the bottom, a long reply arrives (taller than the screen): its end is on screen.
        let first = "第一条新回复 \(name)"
        let long = (1...14).map { "第 \($0) 段：这一段把改动讲清楚，好让回复比一屏还长。" }.joined(separator: "\n\n") + "\n\n" + first
        say(long, user: "再看一眼 \(name)", stream: stream)
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", first)).firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 15), "\(name): the reply is not in the chat: \(state(app))")
        // Taller than the screen, the reply never counts as visible: at the bottom with nothing held back is the check.
        expect(app, "\(name): a new reply at the bottom was not followed") {
            $0["withheld"] == 0 && $0["frozen"] == 0 && $0["bottom"] == 1
        }
        shot(app, "\(name)-followed")

        // 3. Up into the history: ↓ shows, nothing newer pulls the list.
        readOlder(app)
        expect(app, "\(name): no ↓ after reading up") { $0["arrow"] == 1 && $0["bottom"] == 0 && $0["newestShown"] == 0 }
        let behind = state(app)["behind"] ?? 0
        let second = "第二条新回复 \(name)"
        say(second)
        sleep(3)
        expect(app, "\(name): a reply pulled the list down while reading history") {
            $0["bottom"] == 0 && $0["newestShown"] == 0 && $0["arrow"] == 1 && ($0["behind"] ?? 0) >= behind
        }
        shot(app, "\(name)-reading")

        // 4. ↓ brings the newest back, the reply that came while reading included.
        XCTAssertTrue(app.buttons["回到最新"].firstMatch.waitForExistence(timeout: 5), "\(name): no 回到最新 button")
        let arrows = app.buttons.matching(identifier: "回到最新").allElementsBoundByIndex // the shell's other page may have one
        (arrows.first { $0.isHittable } ?? arrows[0]).tap()
        // After a jump the list's report of the lines on screen can lag (it once kept the lines read before): the
        // sentinel, nothing held back and the reply's text are what count.
        expect(app, "\(name): ↓ did not go back to the newest") {
            $0["bottom"] == 1 && $0["withheld"] == 0 && $0["arrow"] == 0
        }
        XCTAssertTrue(app.staticTexts[second].firstMatch.waitForExistence(timeout: 5), "\(name): the reply that came while reading is missing")
        shot(app, "\(name)-back")

        // 5. Following again: the next reply shows too.
        let third = "第三条新回复 \(name)"
        say(third)
        expect(app, "\(name): not following after ↓") { $0["withheld"] == 0 && $0["bottom"] == 1 }
        XCTAssertTrue(app.staticTexts[third].firstMatch.waitForExistence(timeout: 5), "\(name): the reply after ↓ is missing")
    }

    @MainActor func testAgentChatFollows() throws {
        followAndReturn(launch(["-tab", "dev", "-openSession", working]), "agent")
    }

    /// Unread opens at the start of the latest answer; a short one is the bottom already, and it must follow from there.
    @MainActor func testUnreadAgentChatFollows() throws {
        followAndReturn(launch(["-tab", "dev", "-openSession", working, "-showAsUnread", "YES"]), "agent-unread")
    }

    /// Where you left off, a line near the end: the landing is the bottom (the list cannot go further), and new replies
    /// must show. They were held back with no ↓, the page stuck at the message you sent (TestFlight 50/51, 2026-10-31).
    /// Named to run first: the other cases add replies under that message (in the app's cache too), and the landing
    /// would then stop short of the bottom.
    @MainActor func testAgentAtASavedPlaceNearTheEndFollows() throws {
        let id = try userMessage(working, startingWith: "给天气 App")
        followAndReturn(launch(["-tab", "dev", "-openSession", working, "-chat.read.\(working)", id]), "agent-saved")
    }

    /// Where you left off far from the end, after a cold start with the chat on disk (#597). The mock pages like the hub
    /// (`/mock/paging`): the cache and the first page hold the last two turns, the saved line is ten turns further up and
    /// takes older pages to reach. The hub answers slowly so the cached chat is on screen before the page, as on a
    /// phone: the page used to be judged on the cached lines, and then nothing looked further up. Then a reply arrives:
    /// the list stays at the saved line (the landing used to turn following back on).
    @MainActor func testAgentAtASavedPlaceFarFromTheEndAfterAColdStart() throws {
        let id = try userMessage(working, startingWith: "第 5 个问题", turns: 40)
        addTeardownBlock { [hub] in
            _ = try? Data(contentsOf: URL(string: hub! + "/mock/delay?s=0")!)
            _ = try? Data(contentsOf: URL(string: hub! + "/mock/paging?on=0")!)
        }
        _ = try Data(contentsOf: URL(string: hub + "/mock/paging?on=1")!)
        let first = launch(["-tab", "dev", "-openSession", working]) // fills the cache
        expect(first, "agent-cold: did not open at the newest", timeout: 20) { $0["bottom"] == 1 }
        sleep(4) // ChatStore writes its cache 2 s after the last change
        first.terminate()

        _ = try Data(contentsOf: URL(string: hub + "/mock/delay?s=3")!)
        let app = launch(["-tab", "dev", "-openSession", working, "-chat.read.\(working)", id], keepCache: true)
        let saved = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "第 5 个问题")).firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 40), "agent-cold: the saved line is not drawn: \(state(app))")
        expect(app, "agent-cold: not held at the saved line (at the newest, or following again)", timeout: 10) {
            $0["bottom"] == 0 && $0["follows"] == 0 && $0["newestShown"] == 0
        }
        shot(app, "agent-cold-landed")

        _ = try Data(contentsOf: URL(string: hub + "/mock/delay?s=0")!)
        say("冷启动之后的新回复")
        sleep(3)
        expect(app, "agent-cold: a reply pulled the list down from the saved line") {
            $0["bottom"] == 0 && $0["newestShown"] == 0 && ($0["withheld"] ?? 0) > 0
        }
        XCTAssertTrue(saved.exists, "agent-cold: the saved line left the screen after a reply: \(state(app))")
    }

    /// The id of the user's last message in a chat of the mock hub that starts with `prefix`.
    private func userMessage(_ chat: String, startingWith prefix: String, turns: Int = 2) throws -> String {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/sessions/\(chat)/items?limit=80&turns=\(turns)")!)
        let items = try XCTUnwrap((try JSONSerialization.jsonObject(with: data) as? [String: Any])?["items"] as? [[String: Any]])
        return try XCTUnwrap(items.last {
            $0["role"] as? String == "user" && $0["kind"] as? String == "text" && ($0["text"] as? String ?? "").hasPrefix(prefix)
        }?["id"] as? String)
    }

    @MainActor func testPersonaChatFollows() throws {
        followAndReturn(launch(["-tab", "assistant", "-openAssistant", persona]), "persona", stream: true)
    }

    /// Persona's conversation reopens where it was left (#1666), as an agent's chat does: read up, leave the app (the
    /// place is saved as it goes to the background), end it, launch it again. It opens at the same place, not at the
    /// newest, with as many lines below it as before (within a line or two: the landing aligns the row's edge).
    @MainActor func testPersonaReopensWhereItWasLeft() throws {
        let open = ["-tab", "assistant", "-openAssistant", persona]
        let first = launch(open)
        expect(first, "persona-reopen: did not open at the newest", timeout: 20) { $0["bottom"] == 1 && $0["newestShown"] == 1 }
        sleep(2)
        readOlder(first, times: 4)
        expect(first, "persona-reopen: did not read up") { $0["bottom"] == 0 && $0["newestShown"] == 0 }
        sleep(1)
        let before = state(first)["behind"] ?? 0
        XCTAssertGreaterThan(before, 0, "persona-reopen: nothing below the place read: \(state(first))")
        shot(first, "persona-reopen-left")
        XCUIDevice.shared.press(.home) // the app goes to the background: the place is saved
        sleep(2)
        first.terminate()

        let app = launch(open, keepCache: true)
        expect(app, "persona-reopen: did not land at the saved place (at the newest, or following)", timeout: 20) {
            $0["bottom"] == 0 && $0["newestShown"] == 0 && $0["follows"] == 0
        }
        sleep(2)
        let after = state(app)["behind"] ?? 0
        XCTAssertLessThanOrEqual(abs(after - before), 2, "persona-reopen: \(before) lines below before, \(after) after")
        shot(app, "persona-reopen-landed")
    }

    // MARK: Against a real hub (screenshots before / after; skipped without PERSONA_REAL_PAIR)

    /// A real hub's long chats: `PERSONA_REAL_PAIR` (a pairing URL), `PERSONA_REAL_AGENT` (machine/session of an agent whose
    /// transcript grows while the test runs), `PERSONA_REAL_SAVED` (a line near its end, the user's case: where it was left;
    /// a work line `w-<message id>` stays put while the agent goes on), `PERSONA_REAL_PERSONA` (a Persona conversation),
    /// `PERSONA_REAL_CASES` (which of agent,saved,persona). Opens each at the bottom, waits for new lines to show, reads
    /// up, ↓. At the bottom with nothing held back is what counts: a reply taller than the screen is never "visible".
    @MainActor func testRealHubChats() throws {
        let env = ProcessInfo.processInfo.environment
        guard let pair = env["PERSONA_REAL_PAIR"], let agent = env["PERSONA_REAL_AGENT"] else {
            throw XCTSkip("needs PERSONA_REAL_PAIR and PERSONA_REAL_AGENT")
        }
        func open(_ args: [String]) -> XCUIApplication {
            let app = XCUIApplication()
            app.launchArguments = ["-pairURL", pair, "-showScrollState", "YES", "-dev.openChat", ""] + args
            app.launchPinned()
            return app
        }
        func real(_ app: XCUIApplication, _ name: String, grows: Bool) {
            continueAfterFailure = true
            expect(app, "\(name): did not open at the newest", timeout: 30) { $0["bottom"] == 1 && $0["withheld"] == 0 }
            sleep(3)
            shot(app, "real-\(name)-opened")
            if grows {
                let start = state(app)["items"] ?? 0
                expect(app, "\(name): no new lines arrived", timeout: 90) { ($0["items"] ?? 0) > start }
                sleep(3)
                expect(app, "\(name): the new lines are not shown", timeout: 20) { $0["withheld"] == 0 && $0["bottom"] == 1 }
                shot(app, "real-\(name)-followed")
            }
            readOlder(app)
            expect(app, "\(name): no ↓ after reading up") { $0["arrow"] == 1 && $0["bottom"] == 0 }
            shot(app, "real-\(name)-reading")
            if app.buttons["回到最新"].exists { app.buttons["回到最新"].tap() }
            expect(app, "\(name): ↓ did not go back to the newest") { $0["bottom"] == 1 && $0["withheld"] == 0 }
            shot(app, "real-\(name)-back")
        }
        let cases = Set((env["PERSONA_REAL_CASES"] ?? "agent,saved,persona").split(separator: ",").map(String.init))
        if cases.contains("agent") {
            real(open(["-tab", "dev", "-openSession", agent]), "agent", grows: true)
        }
        if cases.contains("saved"), let saved = env["PERSONA_REAL_SAVED"] {
            real(open(["-tab", "dev", "-openSession", agent, "-chat.read.\(agent)", saved]), "agent-saved", grows: true)
        }
        if cases.contains("persona"), let persona = env["PERSONA_REAL_PERSONA"] {
            real(open(["-tab", "assistant", "-openAssistant", persona]), "persona", grows: false)
        }
    }
}
