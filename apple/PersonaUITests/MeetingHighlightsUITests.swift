import XCTest

/// The highlights on the iPhone's meeting page (#3862 phase 2b; contract docs/assistant/meeting-minutes.md「Phase 2b:
/// highlights」 and 「The meeting card」, the phone's side docs/apple/recording.md「Highlights (phase 2b)」), end to end
/// against a mock hub (PERSONA_MOCK_HUB): a room meeting started with the highlights shows the room's highlights at the
/// top of its page, earliest first, each time opening the transcript at its minute with those lines lit; Live Transcript
/// opens the newest lines in place; with the keyboard up the card is phase 2's folded Live box; a hub without the flag,
/// and a meeting started without it on a hub with it, keep the Live box; at the largest text the time goes under the
/// words. A made-up meeting stands in for the microphone
/// (`-showMeetingSample room-highlights`, a line in each of fourteen minutes): its lines reach the hub as a real one's do,
/// and once they are there and the room made its own highlight from them, the test sets the highlights as the room's
/// tool would (`POST /mock/meeting_highlights` with each highlight's `at`, which the mock resolves to that line's minute).
/// Every look starts from a fresh mock (`POST /mock/recording_minutes`), with `/mock/meeting_highlights` switching the
/// phase on or off; every call to the mock must answer 2xx (a mock without the phase fails loudly). The app runs in zh-Hans:
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
    /// The words the mock's room makes its own highlight of after the batch: the batch's last line (`highlights_made_up`).
    private static let roomsHighlight = "今天先到这里"

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

    /// (b) A highlight's time pushes the transcript scrolled to its minute, its line lit for a moment (and selected for
    /// VoiceOver); Back returns to the card. Twice: a later minute, then an earlier one. The line stays lit two seconds, so
    /// it is read in one snapshot and shot as soon as it is on the screen, before anything else.
    @MainActor func testAHighlightsTimeOpensTheTranscriptAtItsMinute() throws {
        let hub = try mockHub()
        for look in ["light", "dark"] {
            if look != "light" { try fresh(hub) }
            let app = try highlightsPage(hub, look: look)
            for (index, words) in [(3, "招聘计划谁来负责"), (0, "保留 4 MB 的导出上限，发版说明")] {
                let time = rows(app).element(boundBy: index).buttons["meeting-highlight-time"]
                XCTAssertTrue(time.waitForExistence(timeout: 5), "\(look): no time on highlight \(index):\n\(app.debugDescription)")
                let window = app.windows.firstMatch.frame
                time.tap()
                let lit = element(app, "recording-timeline-sentence-lit")
                XCTAssertTrue(lit.waitForExistence(timeout: 3), "\(look): no line lit:\n\(app.debugDescription)")
                // The page scrolls to it as it lights it: the first snapshot with the line inside the window.
                let seen = onScreen(lit, in: window, within: 1.5)
                if index == 3 { try shot(app, "transcript-lit", look: look, quick: true) }
                guard let seen else {
                    XCTFail("\(look): the lit line never was on the screen:\n\(app.debugDescription)")
                    break
                }
                XCTAssertTrue(seen.label.contains(words), "\(look): the lit line is \(seen.label), not the minute's \(words)")
                // Selected while lit: one run in a dozen read the new name a beat before the selected state, so the state
                // is asked again for up to a second, still inside the two seconds the line stays lit.
                let selected = seen.isSelected || waitFor(1) { (try? lit.snapshot())?.isSelected == true }
                XCTAssertTrue(selected, "\(look): the lit line isn't selected for VoiceOver")
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
            // How far behind, on Live Transcript's row at the other side ("About N s behind").
            let behind = text(card, "落后")
            XCTAssertTrue(behind.waitForExistence(timeout: 5), "\(look): the open card doesn't say how far behind:\n\(card.debugDescription)")
            XCTAssertGreaterThan(behind.frame.minX, disclosure.frame.maxX, "\(look): how far behind isn't after Live Transcript")
            XCTAssertTrue(behind.frame.midY > disclosure.frame.minY && behind.frame.midY < disclosure.frame.maxY,
                          "\(look): how far behind isn't on Live Transcript's row: \(behind.frame) beside \(disclosure.frame)")
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

            // The keyboard down: a drag from inside the thread down into the keyboard, which the thread's scroll view
            // dismisses interactively. A thread at its top hands a drag down to the sheet (which hides the page, also when
            // the scroll reaches the top on the way), so the batch's lines are opened first, making the thread taller than
            // its view, and it is scrolled up to them (they open at their place, not the end); the drag, from the bottom
            // of what shows of the thread just into the keyboard, is then shorter than what is above. Then the card comes
            // back open.
            let lines = element(app, "meeting-thread-lines")
            XCTAssertTrue(lines.waitForExistence(timeout: 10), "\(look): no batch of lines in the thread:\n\(app.debugDescription)")
            lines.tap()
            let keyboard = app.keyboards.firstMatch
            XCTAssertTrue(keyboard.exists, "\(look): opening the lines put the keyboard down")
            sleep(1)
            // The thread's scroll view runs under the bar (its inset): what shows of it ends where the bar begins, with
            // Just Me on top while the keyboard is up.
            let thread = app.scrollViews.containing(.any, identifier: "meeting-thread-lines").firstMatch
            let bar = element(app, "meeting-just-me")
            XCTAssertTrue(thread.exists && bar.exists, "\(look): no thread scroll view or bar:\n\(app.debugDescription)")
            let origin = app.coordinate(withNormalizedOffset: .zero)
            for _ in 0..<2 {
                let view = thread.frame
                origin.withOffset(CGVector(dx: view.midX, dy: bar.frame.minY - 20))
                    .press(forDuration: 0.05, thenDragTo: origin.withOffset(CGVector(dx: view.midX, dy: view.minY + 10)),
                           withVelocity: .fast, thenHoldForDuration: 0)
            }
            sleep(1)
            XCTAssertTrue(keyboard.exists, "\(look): scrolling the thread up put the keyboard down")
            let opened = XCTAttachment(screenshot: app.screenshot())
            opened.name = "highlights-\(look)-keyboard-lines-open"
            opened.lifetime = .keepAlways
            add(opened)
            var down = false
            for _ in 0..<3 where !down {
                let view = thread.frame, top = keyboard.frame.minY
                let from = CGPoint(x: view.midX, y: bar.frame.minY - 20), to = CGPoint(x: view.midX, y: top + 24)
                // Never a drag the sheet would take: it would hide the page, and the test would go on without it.
                guard from.y > view.minY, view.minY - lines.frame.minY > to.y - from.y else {
                    XCTFail("\(look): no room for the drag: the thread \(view), its lines \(lines.frame), the bar \(bar.frame)")
                    break
                }
                origin.withOffset(CGVector(dx: from.x, dy: from.y))
                    .press(forDuration: 0.05, thenDragTo: origin.withOffset(CGVector(dx: to.x, dy: to.y)), withVelocity: .fast, thenHoldForDuration: 0)
                down = keyboard.waitForNonExistence(timeout: 3)
            }
            XCTAssertTrue(down, "\(look): dragging the thread didn't put the keyboard down:\n\(app.debugDescription)")
            XCTAssertTrue(element(app, "meeting-highlights").waitForExistence(timeout: 5), "\(look): the card isn't back:\n\(app.debugDescription)")
            XCTAssertEqual(element(app, "meeting-live-transcript").value as? String, "已展开", "\(look): not as the owner left it")
            XCTAssertTrue(waitFor(5) { !self.element(app, "meeting-live").exists }, "\(look): the Live box stays beside the card")
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
        try waitForTheLines(hub, roomsHighlight: false)
        XCTAssertNil(try liveRead(hub)["highlights"], "the hub without the flag sent highlights: \(String(describing: try? liveRead(hub)))")
        // Nothing positive to wait for: the page has its turn to (not) show any.
        sleep(5)
        XCTAssertFalse(element(app, "meeting-highlights").exists, "highlights from a hub without them:\n\(app.debugDescription)")
        XCTAssertTrue(element(app, "meeting-live").exists, "no Live box:\n\(app.debugDescription)")
        try shot(app, "no-highlights", look: "light")
    }

    /// (f) At the largest text size, light and dark, the page scrolls as one: each highlight's time goes under its words, and an open card
    /// keeps every highlight above the lines.
    @MainActor func testLargestTextSizeStacksTheTime() throws {
        let hub = try mockHub()
        for look in ["large", "large-dark"] {
            if look != "large" { try fresh(hub) }
            let app = try highlightsPage(hub, look: look)
            let first = rows(app).firstMatch
            let time = first.buttons["meeting-highlight-time"]
            XCTAssertTrue(reveal(time, in: app), "no highlight on the screen:\n\(app.debugDescription)")
            let words = text(first, String(Self.shown[0].text.prefix(6)))
            XCTAssertTrue(words.exists, "\(first.debugDescription)")
            XCTAssertGreaterThanOrEqual(time.frame.minY, words.frame.maxY - 1, "the time isn't under the words")
            try shot(app, "card", look: look)

            let disclosure = element(app, "meeting-live-transcript")
            XCTAssertTrue(reveal(disclosure, in: app), "no Live Transcript on the screen:\n\(app.debugDescription)")
            disclosure.tap()
            XCTAssertTrue(waitFor(5) { disclosure.value as? String == "已展开" })
            XCTAssertEqual(rows(app).count, Self.shown.count, "open at the largest text, every highlight stays")
            let newest = text(element(app, "meeting-highlights"), Self.newestLine)
            XCTAssertTrue(newest.waitForExistence(timeout: 5), app.debugDescription)
            // Above the lines: the last highlight ends before the lines begin (the page scrolls as one, so the frames compare).
            let last = rows(app).element(boundBy: Self.shown.count - 1)
            XCTAssertLessThanOrEqual(last.frame.maxY, newest.frame.minY + 1,
                                     "open at the largest text, the highlights aren't above the lines: \(last.frame) and \(newest.frame)")
            // The shot with the lines on the screen.
            for _ in 0..<6 where !(newest.exists && newest.isHittable) { app.swipeUp(velocity: .slow) }
            try shot(app, "open", look: look)
            app.terminate()
        }
    }

    /// (g) A meeting started without the highlights keeps the Live box on a hub with them, even once its room made some:
    /// the meeting keeps the flag it started with (`RecordingAttendance.highlights`). `room-taking-part` is started with
    /// the live view only, and the mock's room, up with the highlights' tool, makes a highlight from its batch.
    @MainActor func testAMeetingStartedWithoutTheHighlightsKeepsTheLiveBox() throws {
        let hub = try mockHub()
        let app = try meetingPage(hub, look: "light", sample: "room-taking-part")
        XCTAssertTrue(element(app, "meeting-live").waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(waitFor(40) { !((try? self.highlights(hub)) ?? []).isEmpty },
                      "the room never made a highlight: \(String(describing: try? liveRead(hub)))")
        // The room set its topics just before (the same batch): once the page shows them it has followed the room's turn.
        XCTAssertTrue(element(app, "meeting-topics").waitForExistence(timeout: 20), "the page never followed the room:\n\(app.debugDescription)")
        sleep(3)
        XCTAssertFalse(element(app, "meeting-highlights").exists, "highlights for a meeting started without them:\n\(app.debugDescription)")
        XCTAssertTrue(element(app, "meeting-live").exists, "no Live box:\n\(app.debugDescription)")
        try shot(app, "started-without", look: "light")
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

    /// The made-up meeting's page (`sample`: `-showMeetingSample`, the one started with the highlights unless named).
    @MainActor private func meetingPage(_ hub: String, look: String, sample: String = "room-highlights") throws -> XCUIApplication {
        let app = XCUIApplication()
        // `-openChatsFresh`: the mock makes the room under the same id for the same made-up recording in every test.
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-openChatsFresh", "YES",
                               "-showMeetingSample", sample, "-ui.scheme", look.hasSuffix("dark") ? "dark" : "light"]
            + (look.hasPrefix("large") ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : [])
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

    /// `el` read in one snapshot (its label, frame and state together) as soon as it is inside `window`; nil when it
    /// never was within `seconds`.
    @MainActor private func onScreen(_ el: XCUIElement, in window: CGRect, within seconds: TimeInterval) -> XCUIElementSnapshot? {
        let deadline = Date.now.addingTimeInterval(seconds)
        repeat {
            if let seen = try? el.snapshot(), !seen.frame.isEmpty, window.contains(seen.frame) { return seen }
            usleep(100_000)
        } while Date.now < deadline
        return nil
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

    /// A shot in a look this test chose itself (`-ui.scheme`, or the largest text), named by it. `quick`: at once, without
    /// looking for the icon alert first (dismissed at the launch), for what shows only for a moment.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String, look: String, quick: Bool = false) throws {
        if !quick, dismissIconNotice(app) { sleep(1) }
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

    /// A call to the mock, which must answer 2xx: a mock without the route (404) or that refused it (400) fails here.
    private func post(_ hub: String, _ path: String, _ body: [String: Any]) throws {
        var request = URLRequest(url: URL(string: hub + path)!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: path)
        let answer = MockAnswer()
        URLSession.shared.dataTask(with: request) { data, response, _ in
            answer.status = (response as? HTTPURLResponse)?.statusCode ?? 0
            answer.body = data.map { String(decoding: $0, as: UTF8.self) } ?? ""
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 5)
        XCTAssertTrue((200..<300).contains(answer.status), "POST \(path) answered \(answer.status): \(answer.body)")
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

    /// The made-up meeting's lines are in the hub's live log and its room is up; with the highlights on (`roomsHighlight`),
    /// the room has also had its own turn after the batch, which ends with its highlight from the batch's last line (the
    /// room is ready before it is fed, so ready alone doesn't say that turn is over).
    private func waitForTheLines(_ hub: String, roomsHighlight: Bool = true) throws {
        let ready = waitFor(40) {
            guard let read = try? self.liveRead(hub, from: 0) else { return false }
            let lines = (read["lines"] as? [[String: Any]] ?? []).filter { !(($0["text"] as? String) ?? "").isEmpty }
            return (read["room"] as? [String: Any])?["state"] as? String == "ready" && lines.count >= Self.lineCount
        }
        XCTAssertTrue(ready, "the meeting's lines never reached the room: \(String(describing: try? liveRead(hub, from: 0)))")
        guard roomsHighlight else { return }
        let made = waitFor(20) {
            ((try? self.highlights(hub)) ?? []).contains { ($0["text"] as? String)?.hasPrefix(Self.roomsHighlight) == true }
        }
        XCTAssertTrue(made, "the room never made its own highlight: \(String(describing: try? highlights(hub)))")
    }

    /// The room sets the test's highlights (the mock's tool call, each `at` resolved to its line's minute), after the
    /// room's own turn (`waitForTheLines`), so nothing replaces them.
    private func setHighlights(_ hub: String) throws {
        let body: [String: Any] = ["id": live, "highlights": Self.set.map { ["text": $0.text, "at": $0.at] }]
        let texts = Set(Self.set.map(\.text))
        try post(hub, "/mock/meeting_highlights", body)
        XCTAssertTrue(waitFor(10) { Set(((try? self.highlights(hub)) ?? []).compactMap { $0["text"] as? String }) == texts },
                      "the hub never took the highlights: \(String(describing: try? highlights(hub)))")
    }
}

/// What the mock answered a call (`post`), written from URLSession's queue and read after the wait.
private final class MockAnswer: @unchecked Sendable {
    var status = 0
    var body = ""
}
