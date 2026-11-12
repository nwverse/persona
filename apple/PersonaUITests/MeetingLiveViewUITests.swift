import XCTest

/// The live view on the iPhone (#3862 phase 2; contract docs/assistant/meeting-minutes.md「Phase 2: live view」, the phone's
/// side docs/apple/recording.md「Live view (phase 2)」), end to end against a mock hub (PERSONA_MOCK_HUB): a room meeting
/// started with the live view shows the room's topics on its page once the room set them after a batch of lines, and its
/// Live Activity says the current topic; a hub without the live view shows none. A made-up meeting stands in for the
/// microphone (`-showMeetingSample room-taking-part`): its lines reach the hub as a real one's do, and the mock's room sets
/// made-up topics after each batch (`meeting_topics_set`). Every test starts from a fresh mock (`POST
/// /mock/recording_minutes`), with `/mock/meeting_live_view` switching the phase on or off. The app runs in zh-Hans:
/// elements are found by their identifiers. PERSONA_SHOTS=<dir> saves live-<appearance>-<name>.png.
final class MeetingLiveViewUITests: XCTestCase {
    private let live = "00000000-0000-0000-0000-000000000002"

    override func setUpWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        try post(hub, "/mock/recording_minutes", ["mode": "pending"])
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "participant", "answer": "ok", "kb_read_only": false])
        try post(hub, "/mock/meeting_live_view", ["on": true])
    }

    override func tearDownWithError() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { return }
        try post(hub, "/mock/meeting_conversation", ["on": true, "room": "ok", "end_seconds": 180])
        try post(hub, "/mock/meeting_attendance", ["on": true, "default": "off"])
        try post(hub, "/mock/meeting_live_view", ["on": true])
    }

    /// The room sets topics after a batch of the meeting's lines: the page shows them as chips under its header, the
    /// current one among them, as the hub has them; the Live Activity says "Now: <topic>" on the Lock Screen.
    @MainActor func testTopicsShowOnThePageAndTheLockScreen() throws {
        let hub = try mockHub()
        let app = try meetingPage(hub)
        let topics = element(app, "meeting-topics")
        XCTAssertTrue(topics.waitForExistence(timeout: 40),
                      "no topics on the page; the hub has \(String(describing: try? self.topics(hub, self.live))):\n\(app.debugDescription)")
        let stored = try self.topics(hub, live)
        XCTAssertFalse(stored.isEmpty, "the page shows topics the hub doesn't have")
        let current = stored.first { $0["state"] as? String == "current" }
        if current != nil {
            XCTAssertTrue(element(app, "meeting-topic-current").waitForExistence(timeout: 10), "no current topic:\n\(app.debugDescription)")
        }
        XCTAssertTrue(element(app, "meeting-live").exists, "the Live box stays under the topics")
        try shot(app, "topics")

        // The Lock Screen: the current topic under the name (the activity follows the page's read of the topics).
        guard let title = current?["title"] as? String else { return }
        XCUIDevice.shared.press(.home)
        sleep(3)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        springboard.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: 201, dy: 37)).press(forDuration: 1.5)
        sleep(2)
        try screen("island-expanded")
        XCUIDevice.shared.press(.home)
        sleep(1)
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(2)
        XCUIDevice.shared.perform(NSSelectorFromString("pressLockButton"))
        sleep(3)
        for label in ["Allow", "允许", "Always Allow", "始终允许"] {
            let b = springboard.buttons[label].firstMatch
            if b.exists { b.tap(); sleep(2) }
        }
        let topic = springboard.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", title)).firstMatch
        let found = topic.waitForExistence(timeout: 10)
        try screen("lock")
        // Unlock (swipe up; no passcode on the simulator): a locked simulator hangs the next test run.
        let bottom = springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.99))
        bottom.press(forDuration: 0.1, thenDragTo: springboard.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)))
        sleep(1)
        XCTAssertTrue(found, "the Lock Screen doesn't say the current topic \(title):\n\(springboard.debugDescription)")
    }

    /// A hub without the live view (`{"on": false}`): `GET …/live` carries no topics, and the page shows none, only the
    /// phone's own lines in the Live box.
    @MainActor func testAHubWithoutTheLiveViewShowsNoTopics() throws {
        let hub = try mockHub()
        try post(hub, "/mock/meeting_live_view", ["on": false])
        let app = try meetingPage(hub)
        XCTAssertTrue(element(app, "meeting-live").waitForExistence(timeout: 10), app.debugDescription)
        // Its lines reached the room (a batch went), and still no topics.
        XCTAssertTrue(waitFor(20) { ((try? self.liveRead(hub, self.live))?["room"] as? [String: Any])?["state"] as? String == "ready" })
        sleep(5)
        XCTAssertFalse(element(app, "meeting-topics").exists, "topics from a hub without the live view:\n\(app.debugDescription)")
        try shot(app, "no-topics")
    }

    // MARK: Helpers: the app

    @MainActor private func meetingPage(_ hub: String) throws -> XCUIApplication {
        let app = XCUIApplication()
        // `-openChatsFresh`: the mock makes the room under the same id for the same made-up recording in every test.
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings", "-openChatsFresh", "YES",
                               "-showMeetingSample", "room-taking-part"]
        app.launchPinned()
        XCTAssertTrue(element(app, "meeting-thread-header").waitForExistence(timeout: 20), "no meeting page:\n\(app.debugDescription)")
        return app
    }

    @MainActor private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    private func waitFor(_ seconds: TimeInterval = 10, _ ok: () -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if ok() { return true }
            usleep(250_000)
        }
        return ok()
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        try save(app.screenshot().pngRepresentation, name)
    }

    @MainActor private func screen(_ name: String) throws {
        let image = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: image)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        try save(image.pngRepresentation, name)
    }

    private func save(_ png: Data, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try png.write(to: URL(fileURLWithPath: "\(dir)/live-\(appearance)-\(name).png"))
    }

    // MARK: Helpers: the mock hub

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("需要独立 mock hub") }
        return hub
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

    /// `GET /api/v1/recordings/{id}/live` as the hub answers it.
    private func liveRead(_ hub: String, _ id: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/recordings/\(id)/live")!)
        return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }

    /// The recording's topics as the hub has them (`topics` of `GET …/live`).
    private func topics(_ hub: String, _ id: String) throws -> [[String: Any]] {
        try liveRead(hub, id)["topics"] as? [[String: Any]] ?? []
    }
}
