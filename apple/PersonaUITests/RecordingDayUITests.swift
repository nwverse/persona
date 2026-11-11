import XCTest

/// All-day recording on the phone (docs/assistant/day-recap.md, #4741) against a mock hub (PERSONA_MOCK_HUB, fresh per run,
/// with its `recording_day` block). Made-up days stand in for the watch (`-showDaySample`: yesterday's, ended, and today's,
/// which the watch records or waits on after a call); the phone uploads them as it would a real day, and the mock writes
/// the recap it is told to (`POST /mock/day_recap`). PERSONA_SHOTS=<dir> saves the Today section, the recap ready, being
/// written and failed, and both pages at the largest accessibility text size (PERSONA_SHOTS_APPEARANCE names the
/// appearance).
final class RecordingDayUITests: XCTestCase {
    private let pastDay = "00000000-0000-0000-0000-0000000000da"
    private let todayDay = "00000000-0000-0000-0000-0000000000db"
    private static let largestText = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
    /// The upload, the hub's recap and the page's next look at it (every 30 s while it waits) take this long at most.
    private static let recapTimeout: TimeInterval = 120

    /// The Today section: the switch mirrors the watch (recording: on), today's row opens the day, yesterday is a row of
    /// Recent Recordings and today's is not listed twice; today's text reaches the hub as a day recording.
    @MainActor func testTodaySection() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true, "mode": "pending", "seconds": 0])
        let app = launch(hub, sample: "today")
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(wait(10) { "\(toggle.value ?? "")" == "1" }, "the switch doesn't show the watch recording: \(toggle.debugDescription)")
        let today = app.buttons["allday-today"].firstMatch
        XCTAssertTrue(today.exists, app.debugDescription)
        let past = app.buttons["recording-day-row"].firstMatch
        XCTAssertTrue(past.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(app.buttons.matching(identifier: "recording-day-row").count, 1, "today's day is listed twice")
        XCTAssertTrue(wait(60) { (try self.day(hub, self.todayDay)?["next"] as? Int ?? 0) > 0 }, "today's text didn't reach the hub")
        try shot(app, "today")
        today.tap()
        XCTAssertTrue(app.descendants(matching: .any)["day-title"].firstMatch.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.descendants(matching: .any)["day-state"].firstMatch.exists, app.debugDescription)
    }

    /// Paused by a call on the watch: the mode is still on, and the line says the tap that continues it is on the watch.
    @MainActor func testPausedByACallSaysWhereToContinue() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true, "mode": "pending", "seconds": 0])
        let app = launch(hub, sample: "paused")
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(wait(10) { "\(toggle.value ?? "")" == "1" }, toggle.debugDescription)
        XCTAssertTrue(wait(5) { toggle.label.contains("继续") || toggle.label.contains("Continue") }, toggle.label)
        try shot(app, "today-paused")
    }

    /// The recap on the day's page: its summary, the timeline under the time of day with the meeting the watch marked,
    /// a suggested to-do, and a time that opens the transcript at its line. The day went up whole.
    @MainActor func testRecapReadyJumpsToTheTranscript() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true, "mode": "ready", "seconds": 0])
        let app = launch(hub, sample: "recap", open: true)
        let summary = app.descendants(matching: .any)["dayrecap-summary"].firstMatch
        XCTAssertTrue(summary.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap")
        // Ended, every chunk settled and appended: the phone said the day is complete.
        XCTAssertTrue(wait(60) { try self.day(hub, self.pastDay)?["complete"] as? Bool == true }, "the day never became complete on the hub")
        let entry = app.descendants(matching: .any)["dayrecap-entry"].firstMatch
        reveal(app, entry)
        XCTAssertTrue(entry.exists, app.debugDescription)
        try shot(app, "recap-timeline")
        let action = app.descendants(matching: .any)["dayrecap-action"].firstMatch
        reveal(app, action)
        XCTAssertTrue(action.exists, "no suggested to-do:\n\(app.debugDescription)")
        let time = app.buttons.matching(identifier: "dayrecap-time").firstMatch
        reveal(app, time)
        time.tap()
        let lit = app.descendants(matching: .any)["day-transcript-line-lit"].firstMatch
        XCTAssertTrue(lit.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(lit.isHittable, "the line is not in view")
        try shot(app, "recap-transcript")
    }

    /// Being written: says so, with a spinner.
    @MainActor func testRecapBeingWritten() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true, "mode": "pending", "seconds": 0])
        let app = launch(hub, sample: "pending", open: true)
        let writing = app.descendants(matching: .any)["dayrecap-writing"].firstMatch
        XCTAssertTrue(writing.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap-pending")
    }

    /// Writing it failed: Retry asks the hub again (`POST …/minutes` on the day), and the page follows it to the recap.
    @MainActor func testRecapRetry() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true, "mode": "failed", "seconds": 0])
        let app = launch(hub, sample: "failed", open: true)
        let retry = app.buttons["dayrecap-retry"].firstMatch
        XCTAssertTrue(retry.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap-failed")
        try switchMock(hub, ["mode": "ready", "seconds": 2])
        reveal(app, retry)
        retry.tap()
        XCTAssertTrue(wait(10) { try self.asked(hub).contains(self.pastDay) }, "Retry didn't reach the hub")
        XCTAssertTrue(app.descendants(matching: .any)["dayrecap-summary"].firstMatch.waitForExistence(timeout: 60), app.debugDescription)
    }

    /// The largest accessibility text size: the Today section and the recap still lay out (shots), and their controls
    /// can still be reached.
    @MainActor func testLargestTextSize() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true, "mode": "ready", "seconds": 0])
        var app = launch(hub, sample: "today", extra: Self.largestText)
        let toggle = app.switches["allday-switch"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 20), app.debugDescription)
        reveal(app, toggle)
        XCTAssertTrue(toggle.isHittable, app.debugDescription)
        try shot(app, "today-largest")
        app.terminate()

        app = launch(hub, sample: "recap", open: true, extra: Self.largestText)
        let summary = app.descendants(matching: .any)["dayrecap-summary"].firstMatch
        XCTAssertTrue(summary.waitForExistence(timeout: Self.recapTimeout), app.debugDescription)
        try shot(app, "recap-largest")
        let time = app.buttons.matching(identifier: "dayrecap-time").firstMatch
        reveal(app, time)
        XCTAssertTrue(time.isHittable, app.debugDescription)
    }

    /// Without a day and without a paired watch the Recordings page is as it was: no Today section, no day rows (an
    /// earlier test's made-up days go when the page appears).
    @MainActor func testWithoutAllDayRecordingThePageIsUnchanged() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": true])
        let app = launch(hub, sample: nil)
        XCTAssertTrue(app.buttons["recording-meeting-start"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(wait(10) { !app.switches["allday-switch"].exists && !app.buttons["recording-day-row"].exists },
                      app.debugDescription)
        sleep(2) // nothing brings them back
        XCTAssertFalse(app.switches["allday-switch"].exists, app.debugDescription)
        XCTAssertFalse(app.buttons["recording-day-row"].exists, app.debugDescription)
    }

    /// A hub without `recording_day`: the day is still shown, with a caution that its text stays on the phone.
    @MainActor func testHubWithoutTheFlagSaysTheRecapNeedsAnUpdate() throws {
        let hub = try mockHub()
        try switchMock(hub, ["on": false])
        defer { try? switchMock(hub, ["on": true]) }
        let app = launch(hub, sample: "today")
        let notice = app.descendants(matching: .any)["allday-hub-update"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertTrue(app.switches["allday-switch"].exists, app.debugDescription)
        try shot(app, "hub-update")
    }

    // MARK: Helpers

    @MainActor private func launch(_ hub: String, sample: String?, open: Bool = false, extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair(hub), "-tab", "assistant", "-openShellPage", "recordings"]
            + (sample.map { ["-showDaySample", $0] } ?? [])
            + (open ? ["-openDayRecap", "YES"] : [])
            + extra
        app.launchPinned()
        return app
    }

    /// Swipes until `element` is on screen (rows off screen are not in the tree): up first, then back down.
    @MainActor private func reveal(_ app: XCUIApplication, _ element: XCUIElement) {
        var swipes = 0
        while !(element.exists && element.isHittable), swipes < 6 { app.swipeUp(); swipes += 1 }
        while !(element.exists && element.isHittable), swipes < 12 { app.swipeDown(); swipes += 1 }
    }

    /// `POST /mock/day_recap`: the mock's switches (`mode`, `seconds`, `on`…); only the keys sent change.
    private func switchMock(_ hub: String, _ body: [String: Any]) throws {
        var request = URLRequest(url: URL(string: hub + "/mock/day_recap")!)
        request.httpMethod = "POST"
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let done = expectation(description: "day_recap")
        URLSession.shared.dataTask(with: request) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
        // A mock without the all-day block answers this route with an error, not its switches.
        XCTAssertNotNil(try mockState(hub)["mode"], "the mock hub has no recording_day switches (/mock/day_recap)")
    }

    /// `GET /mock/day_recap`: the switches, the recaps asked for, and each day as the hub holds it.
    private func mockState(_ hub: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/day_recap")!)
        return try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
    }

    /// A day as the hub holds it (`next`, `complete`, `recap_state`…); nil when it has none.
    private func day(_ hub: String, _ id: String) throws -> [String: Any]? {
        (try mockState(hub)["days"] as? [String: Any])?[id] as? [String: Any]
    }

    /// The days a recap was asked for again (`POST …/minutes`), in order.
    private func asked(_ hub: String) throws -> [String] {
        try mockState(hub)["asked"] as? [String] ?? []
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let image = app.screenshot()
        let attachment = XCTAttachment(screenshot: image)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try image.pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/allday-\(appearance)-\(name).png"))
    }

    private func mockHub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs its own mock hub (PERSONA_MOCK_HUB)") }
        return hub
    }

    private func pair(_ hub: String) -> String {
        "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    private func wait(_ seconds: TimeInterval, _ ok: () throws -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if (try? ok()) == true { return true }
            usleep(200_000)
        }
        return false
    }
}
