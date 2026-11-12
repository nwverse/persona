import XCTest

/// The highlights on the iPhone's meeting page (#3862 phase 2b; contract docs/assistant/meeting-minutes.md「Phase 2b:
/// highlights」 and 「The meeting card」, the phone's side docs/apple/recording.md「Highlights (phase 2b)」), end to end
/// against a mock hub (PERSONA_MOCK_HUB): a room meeting started with the highlights shows the room's highlights at the
/// top of its page, earliest first, each time opening the transcript at its minute with those lines lit; Live Transcript
/// opens the newest lines in place; with the keyboard up the card is phase 2's folded Live box; a hub without the flag
/// keeps the Live box; at the largest text the time goes under the words. A made-up meeting stands in for the microphone
/// (`-showMeetingSample room-highlights`, a line in each of fourteen minutes): its lines reach the hub as a real one's do,
/// and once they are there the test sets the highlights as the room's tool would (`POST /mock/meeting_highlights` with
/// each highlight's `at`, which the mock resolves to that line's minute). Every look starts from a fresh mock (`POST
/// /mock/recording_minutes`), with `/mock/meeting_highlights` switching the phase on or off. The app runs in zh-Hans:
/// elements are found by their identifiers. PERSONA_SHOTS=<dir> saves highlights-<look>-<name>.png (`-ui.scheme`).
final class MeetingHighlightsUITests: XCTestCase {
    private let live = "00000000-0000-0000-0000-000000000002"

    /// The highlights the tests set, in the order sent (not the order shown): each `at` is the start of a line of
    /// `RecordingSamples.highlightsLines`, alone in its minute.
    private static let set: [(text: String, at: Double)] = [
        ("张三周五前出两种定价方案的测算。", 1050),
        ("这个版本保留 4 MB 的导出上限。", 225),
        ("待定：招聘计划由谁负责。", 1270),
        ("供应商续约到三季度，下周再核成本。", 620),
    ]
    /// What the page shows, earliest first: each time and words.
    private static let shown: [(clock: String, text: String)] = [
        ("3:45", "这个版本保留 4 MB 的导出上限。"),
        ("10:20", "供应商续约到三季度，下周再核成本。"),
        ("17:30", "张三周五前出两种定价方案的测算。"),
        ("21:10", "待定：招聘计划由谁负责。"),
    ]
    /// The made-up meeting's lines: how many reach the hub, and the newest (Live Transcript and the folded Live box).
    private static let lineCount = 14
    private static let newestLine = "今天先到这里"

    override func setUpWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        try fresh(hub)
    }

    override func tearDownWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "off"])
        try post(hub, "/mock/meeting_live_view", ["on": true])
        try post(hub, "/mock/meeting_highlights", ["on": true])
    }

    /// (a) The room's highlights at the top of the page: all four, earliest first, each with its time; the Persona part
    /// under it as phase 1 has it (the thread's header, the composer), Full Transcript one tap away, and no Live box.
    @MainActor func testTheHighlightsShowAtTheTopInTimeOrder() throws {
        let hub = try mockHub()
        for look in ["light", "dark"] {
            if look != "light" { try fresh(hub) }
            let app = try highlightsPage(hub, look: look)
            let card = element(app, "meeting-highlights")
            let header = element(app, "meeting-thread-header")
            XCTAssertLessThanOrEqual(card.frame.maxY, header.frame.minY + 1, "\(look): the card isn't above the thread")
            XCTAssertTrue(element(app, "meeting-ask-field").exists, "\(look): no composer:\n\(app.debugDescription)")
            XCTAssertFalse(element(app, "meeting-live").exists, "\(look): the Live box beside the highlights")
            let transcript = card.descendants(matching: .any).matching(identifier: "meeting-transcript").firstMatch
            XCTAssertTrue(transcript.exists && transcript.isHittable, "\(look): Full Transcript isn't one tap away:\n\(app.debugDescription)")
            let disclosure = element(app, "meeting-live-transcript")
            XCTAssertEqual(disclosure.value as? String, "已收起", "\(look): the card starts closed")
            XCTAssertEqual(disclosure.frame.midY, transcript.frame.midY, accuracy: 4, "\(look): Live Transcript and Full Transcript aren't one row")
            try shot(app, "card", look: look)
            app.terminate()
        }
    }

    /// (b) A highlight's time pushes the transcript scrolled to its minute, its line lit for a moment; Back returns to the
    /// card. Twice: a later minute, then an earlier one.
    @MainActor func testAHighlightsTimeOpensTheTranscriptAtItsMinute() throws {
        let hub = try mockHub()
        for look in ["light", "dark"] {
            if look != "light" { try fresh(hub) }
            let app = try highlightsPage(hub, look: look)
            for (index, words) in [(3, "招聘计划谁来负责"), (0, "保留 4 MB 的导出上限，发版说明")] {
                let time = rows(app).element(boundBy: index).buttons["meeting-highlight-time"]
                XCTAssertTrue(time.waitForExistence(timeout: 5), "\(look): no time on highlight \(index):\n\(app.debugDescription)")
                time.tap()
                let lit = element(app, "recording-timeline-sentence-lit")
                XCTAssertTrue(lit.waitForExistence(timeout: 3), "\(look): no line lit:\n\(app.debugDescription)")
                XCTAssertTrue(lit.label.contains(words), "\(look): the lit line is \(lit.label), not the minute's \(words)")
                XCTAssertTrue(hittable(lit, within: 2), "\(look): the lit line isn't on the screen:\n\(app.debugDescription)")
                if index == 3 { try shot(app, "transcript-lit", look: look) }
                XCTAssertTrue(lit.waitForNonExistence(timeout: 6), "\(look): the line stays lit")
                app.navigationBars.buttons.element(boundBy: 0).tap()
                XCTAssertTrue(element(app, "meeting-highlights").waitForExistence(timeout: 5), "\(look): Back didn't return:\n\(app.debugDescription)")
            }
            app.terminate()
        }
    }

    /// (c) Live Transcript opens the card in place: the newest highlight alone above the newest lines and how far behind,
    /// Full Transcript under them; it reads Expanded, and closes again to all four.
    @MainActor func testLiveTranscriptOpensAndClosesInPlace() throws {
        let hub = try mockHub()
        for look in ["light", "dark"] {
            if look != "light" { try fresh(hub) }
            let app = try highlightsPage(hub, look: look)
            let disclosure = element(app, "meeting-live-transcript")
            disclosure.tap()
            XCTAssertTrue(waitFor(5) { disclosure.value as? String == "已展开" }, "\(look): not Expanded: \(String(describing: disclosure.value))")
            XCTAssertTrue(waitFor(5) { self.rows(app).count == 1 }, "\(look): open, the card keeps \(rows(app).count) highlights")
            XCTAssertTrue(rows(app).firstMatch.buttons["meeting-highlight-time"].label.contains(Self.shown[3].clock), "\(look): not the newest")
            let card = element(app, "meeting-highlights")
            XCTAssertTrue(text(card, Self.newestLine).waitForExistence(timeout: 5), "\(look): no newest line:\n\(app.debugDescription)")
            let transcript = card.descendants(matching: .any).matching(identifier: "meeting-transcript").firstMatch
            XCTAssertTrue(transcript.exists && transcript.isHittable, "\(look): no Full Transcript in the open card")
            XCTAssertGreaterThan(transcript.frame.minY, text(card, Self.newestLine).frame.minY, "\(look): Full Transcript isn't under the lines")
            XCTAssertTrue(element(app, "meeting-ask-field").exists, "\(look): the composer went")
            try shot(app, "open", look: look)
            disclosure.tap()
            XCTAssertTrue(waitFor(5) { disclosure.value as? String == "已收起" }, "\(look): not Collapsed")
            XCTAssertTrue(waitFor(5) { self.rows(app).count == Self.shown.count }, "\(look): closed, the card has \(rows(app).count) highlights")
            XCTAssertFalse(text(card, Self.newestLine).exists, "\(look): the lines stay")
            app.terminate()
        }
    }

    /// (d) With the keyboard up the card is phase 2's folded Live box ("Live", how far behind, the newest line, no Full
    /// Transcript), and once it goes down the card is as the owner left it (open here).
    @MainActor func testWithTheKeyboardUpTheCardIsTheFoldedLiveBox() throws {
        let hub = try mockHub()
        for look in ["light", "dark"] {
            if look != "light" { try fresh(hub) }
            let app = try highlightsPage(hub, look: look)
            let disclosure = element(app, "meeting-live-transcript")
            disclosure.tap()
            XCTAssertTrue(waitFor(5) { disclosure.value as? String == "已展开" })
            let field = element(app, "meeting-ask-field")
            XCTAssertTrue(waitFor(15) { field.isEnabled }, "\(look): the composer is off: \(app.debugDescription)")
            field.tap()
            XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "\(look): no keyboard")
            let box = element(app, "meeting-live")
            XCTAssertTrue(box.waitForExistence(timeout: 5), "\(look): no folded Live box:\n\(app.debugDescription)")
            XCTAssertTrue(element(app, "meeting-highlights").waitForNonExistence(timeout: 5), "\(look): the highlights stay with the keyboard")
            XCTAssertTrue(text(box, Self.newestLine).exists, "\(look): the folded box hasn't the newest line:\n\(app.debugDescription)")
            XCTAssertFalse(box.descendants(matching: .any).matching(identifier: "meeting-transcript").firstMatch.exists,
                           "\(look): the folded box has Full Transcript")
            try shot(app, "keyboard", look: look)

            // The keyboard down (a drag down on the thread, which dismisses it): the card comes back open.
            let header = element(app, "meeting-thread-header")
            let from = header.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 4))
            from.press(forDuration: 0.1, thenDragTo: from.withOffset(CGVector(dx: 0, dy: 300)))
            if app.keyboards.firstMatch.waitForNonExistence(timeout: 3) {
                XCTAssertTrue(element(app, "meeting-highlights").waitForExistence(timeout: 5), "\(look): the card isn't back:\n\(app.debugDescription)")
                XCTAssertEqual(element(app, "meeting-live-transcript").value as? String, "已展开", "\(look): not as the owner left it")
            }
            app.terminate()
        }
    }

    /// (e) A hub without the highlights (`{"on": false}` before the launch): `GET …/live` carries none, and the page keeps
    /// phase 2's Live box with the phone's own lines.
    @MainActor func testAHubWithoutTheHighlightsKeepsTheLiveBox() throws {
        let hub = try mockHub()
        try post(hub, "/mock/meeting_highlights", ["on": false])
        let app = try meetingPage(hub, look: "light")
        XCTAssertTrue(element(app, "meeting-live").waitForExistence(timeout: 10), app.debugDescription)
        try waitForTheLines(hub)
        sleep(5)
        XCTAssertFalse(element(app, "meeting-highlights").exists, "highlights from a hub without them:\n\(app.debugDescription)")
        XCTAssertTrue(element(app, "meeting-live").exists, "no Live box:\n\(app.debugDescription)")
        try shot(app, "no-highlights", look: "light")
    }

    /// (f) At the largest text size the page scrolls as one: each highlight's time goes under its words, and an open card
    /// keeps every highlight above the lines.
    @MainActor func testLargestTextSizeStacksTheTime() throws {
        let hub = try mockHub()
        let app = try highlightsPage(hub, look: "large")
        let first = rows(app).firstMatch
        let time = first.buttons["meeting-highlight-time"]
        XCTAssertTrue(reveal(time, in: app), "no highlight on the screen:\n\(app.debugDescription)")
        let words = text(first, String(Self.shown[0].text.prefix(6)))
        XCTAssertTrue(words.exists, "\(first.debugDescription)")
        XCTAssertGreaterThanOrEqual(time.frame.minY, words.frame.maxY - 1, "the time isn't under the words")
        try shot(app, "card", look: "large")

        let disclosure = element(app, "meeting-live-transcript")
        XCTAssertTrue(reveal(disclosure, in: app), "no Live Transcript on the screen:\n\(app.debugDescription)")
        disclosure.tap()
        XCTAssertTrue(waitFor(5) { disclosure.value as? String == "已展开" })
        XCTAssertEqual(rows(app).count, Self.shown.count, "open at the largest text, every highlight stays")
        XCTAssertTrue(text(element(app, "meeting-highlights"), Self.newestLine).waitForExistence(timeout: 5), app.debugDescription)
        try shot(app, "open", look: "large")
    }

    // MARK: Helpers: the app

    /// The made-up meeting's page with the highlights set and shown: all four, in time order, each with its time.
    @MainActor private func highlightsPage(_ hub: String, look: String) throws -> XCUIApplication {
        let app = try meetingPage(hub, look: look)
        try waitForTheLines(hub)
        try setHighlights(hub)
        XCTAssertTrue(element(app, "meeting-highlights").waitForExistence(timeout: 20),
                      "\(look): no highlights on the page; the hub has \(String(describing: try? self.highlights(hub))):\n\(app.debugDescription)")
        XCTAssertTrue(waitFor(10) { self.rows(app).count == Self.shown.count }, "\(look): \(rows(app).count) highlights:\n\(app.debugDescription)")
        var above = -CGFloat.infinity
        for (n, expected) in Self.shown.enumerated() {
            let row = rows(app).element(boundBy: n)
            XCTAssertTrue(text(row, expected.text).exists, "\(look): highlight \(n) isn't \(expected.text):\n\(row.debugDescription)")
            let time = row.buttons["meeting-highlight-time"]
            XCTAssertTrue(time.exists && time.label.contains(expected.clock), "\(look): highlight \(n)'s time is \(time.label), not \(expected.clock)")
            XCTAssertGreaterThan(row.frame.minY, above, "\(look): highlight \(n) isn't under the one before")
            above = row.frame.minY
        }
        return app
    }

    @MainActor private func meetingPage(_ hub: String, look: String) throws -> XCUIApplication {
        let app = XCUIApplication()
        // `-openChatsFresh`: the mock makes the room under the same id for the same made-up recording in every test.
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-openChatsFresh", "YES",
                               "-showMeetingSample", "room-highlights", "-ui.scheme", look == "dark" ? "dark" : "light"]
            + (look == "large" ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : [])
        app.launchPinned()
        // The app's icon follows the appearance: switching it, the system says so in an alert over the page.
        _ = waitFor(4) { self.dismissIconNotice(app) }
        if !element(app, "meeting-thread-header").waitForExistence(timeout: 20) {
            // That alert, up while the sheet was being presented, can keep the sheet from showing: once more, the icon
            // settled now.
            app.terminate()
            app.launchPinned()
            _ = waitFor(4) { self.dismissIconNotice(app) }
        }
        XCTAssertTrue(element(app, "meeting-thread-header").waitForExistence(timeout: 20), "no meeting page:\n\(app.debugDescription)")
        return app
    }

    /// Taps away the system's "You have changed the icon" alert, if it is up; true when it was.
    @MainActor @discardableResult private func dismissIconNotice(_ app: XCUIApplication) -> Bool {
        let notice = NSPredicate(format: "label CONTAINS[c] 'icon' OR label CONTAINS '图标'")
        for source in [app, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let alert = source.alerts.containing(notice).firstMatch
            if alert.exists, alert.buttons.firstMatch.exists {
                alert.buttons.firstMatch.tap()
                return true
            }
        }
        return false
    }

    @MainActor private func rows(_ app: XCUIApplication) -> XCUIElementQuery {
        app.descendants(matching: .any).matching(identifier: "meeting-highlight")
    }

    @MainActor private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    /// The element under `parent` whose label holds `words`.
    @MainActor private func text(_ parent: XCUIElement, _ words: String) -> XCUIElement {
        parent.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", words)).firstMatch
    }

    @MainActor private func hittable(_ el: XCUIElement, within seconds: TimeInterval) -> Bool {
        waitFor(seconds) { el.exists && el.isHittable }
    }

    /// At the largest text the page scrolls as one and follows its end: up the page (swipes down) until `el` is on the
    /// screen, then back down.
    @MainActor private func reveal(_ el: XCUIElement, in app: XCUIApplication) -> Bool {
        for _ in 0..<12 where !(el.exists && el.isHittable) { app.swipeDown(velocity: .slow) }
        for _ in 0..<12 where !(el.exists && el.isHittable) { app.swipeUp(velocity: .slow) }
        return el.exists && el.isHittable
    }

    private func waitFor(_ seconds: TimeInterval = 10, _ ok: () -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if ok() { return true }
            usleep(250_000)
        }
        return ok()
    }

    /// A shot in a look this test chose itself (`-ui.scheme`, or the largest text), named by it.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String, look: String) throws {
        if dismissIconNotice(app) { sleep(1) }
        let image = app.screenshot()
        let a = XCTAttachment(screenshot: image)
        a.name = "highlights-\(look)-\(name)"
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try image.pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/highlights-\(look)-\(name).png"))
    }

    // MARK: Helpers: the mock hub

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
    }

    /// A fresh mock with every phase on: no recordings, rooms or highlights from before.
    private func fresh(_ hub: String) throws {
        try post(hub, "/mock/recording_minutes", ["mode": "pending"])
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "participant", "answer": "ok", "kb_read_only": false])
        try post(hub, "/mock/meeting_live_view", ["on": true])
        try post(hub, "/mock/meeting_highlights", ["on": true])
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    private func post(_ hub: String, _ path: String, _ body: [String: Any]) throws {
        var request = URLRequest(url: URL(string: hub + path)!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: request) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    /// `GET /api/v1/recordings/{id}/live` as the hub answers it (`from`: with the live log's lines from there).
    private func liveRead(_ hub: String, from: Int? = nil) throws -> [String: Any] {
        let query = from.map { "?from=\($0)" } ?? ""
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/recordings/\(live)/live\(query)")!)
        return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }

    /// The recording's highlights as the hub has them (`highlights` of `GET …/live`).
    private func highlights(_ hub: String) throws -> [[String: Any]] {
        try liveRead(hub)["highlights"] as? [[String: Any]] ?? []
    }

    /// The made-up meeting's lines are in the hub's live log and its room is up (it made what it makes after the batch).
    private func waitForTheLines(_ hub: String) throws {
        let ready = waitFor(40) {
            guard let read = try? self.liveRead(hub, from: 0) else { return false }
            let lines = (read["lines"] as? [[String: Any]] ?? []).filter { !(($0["text"] as? String) ?? "").isEmpty }
            return (read["room"] as? [String: Any])?["state"] as? String == "ready" && lines.count >= Self.lineCount
        }
        XCTAssertTrue(ready, "the meeting's lines never reached the room: \(String(describing: try? liveRead(hub, from: 0)))")
        sleep(2) // the room's own turn after the batch
    }

    /// The room sets the test's highlights (the mock's tool call, each `at` resolved to its line's minute), again if the
    /// room's own turn replaced them meanwhile.
    private func setHighlights(_ hub: String) throws {
        let body: [String: Any] = ["id": live, "highlights": Self.set.map { ["text": $0.text, "at": $0.at] }]
        let texts = Set(Self.set.map(\.text))
        for _ in 0..<3 {
            try post(hub, "/mock/meeting_highlights", body)
            if waitFor(5, { Set(((try? self.highlights(hub)) ?? []).compactMap { $0["text"] as? String }) == texts }) { return }
        }
        XCTFail("the hub never took the highlights: \(String(describing: try? highlights(hub)))")
    }
}
