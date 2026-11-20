import XCTest

/// The Assistant composer's calm default (#7770): one collapsed queue summary, the next request inside the action row,
/// and no reading-position jump when streaming or an explicit disclosure changes the footer's height.
/// Run with MOCK_PERSONA_QUEUE=1 MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_ASK_LONG=swipe.
final class CalmComposerUITests: XCTestCase {
    private var hub = ""
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs the combined Persona queue/tasks mock (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-dev.openChat", "",
                               "-openAssistant", main, "-openChatsFresh", "YES", "-openDraftsFresh", "YES", "-showScrollState", "YES", "-probes", "YES"]
        app.launchPinned()
        return app
    }

    @MainActor private func state(_ app: XCUIApplication) -> [String: Int] {
        let probe = app.staticTexts.matching(identifier: "chat.scrollState")
            .matching(NSPredicate(format: "label ENDSWITH %@", "key=" + main)).firstMatch
        guard probe.exists else { return [:] }
        var out: [String: Int] = [:]
        for pair in probe.label.split(separator: " ") {
            let parts = pair.split(separator: "=")
            if parts.count == 2, let value = Int(parts[1]) { out[String(parts[0])] = value }
        }
        return out
    }

    @MainActor private func readingID(_ app: XCUIApplication) -> String? {
        let probe = app.staticTexts.matching(identifier: "chat.scrollState")
            .matching(NSPredicate(format: "label ENDSWITH %@", "key=" + main)).firstMatch
        guard probe.exists else { return nil }
        return probe.label.split(separator: " ").first(where: { $0.hasPrefix("reading=") }).map { String($0.dropFirst("reading=".count)) }
    }

    @MainActor private func waitUntil(_ timeout: TimeInterval = 10, samples: Int = 1, _ condition: () -> Bool) -> Bool {
        let end = Date().addingTimeInterval(timeout)
        var matched = 0
        while Date() < end {
            matched = condition() ? matched + 1 : 0
            if matched >= samples { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.2))
        }
        return false
    }

    private func mock(_ path: String, query: [URLQueryItem] = []) {
        var components = URLComponents(string: hub + "/mock/" + path)!
        components.queryItems = query
        _ = try? Data(contentsOf: components.url!)
    }

    private func stream(upto: Int? = nil) {
        var query = [URLQueryItem(name: "preset", value: "repair"), URLQueryItem(name: "stream", value: "1"),
                     URLQueryItem(name: "steps", value: "1"), URLQueryItem(name: "ms", value: "0")]
        if let upto { query.append(URLQueryItem(name: "upto", value: String(upto))) }
        mock("say", query: query)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/\(name).png"))
        }
    }

    @MainActor func testQueueAndCombinedRowCollapseDuringStreaming() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "排队中 3 条")).firstMatch.exists)
        XCTAssertEqual(app.buttons.matching(identifier: "queue-item").count, 0, "a fresh queue must start collapsed")
        XCTAssertFalse(app.buttons["queue-relayed-summary"].exists)

        let needs = app.buttons["action-bar.needs-you"]
        let running = app.buttons["action-bar.running"]
        XCTAssertTrue(running.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(needs.exists)
        XCTAssertTrue(app.buttons["选项 1"].waitForExistence(timeout: 10), app.debugDescription)
        app.buttons["选项 1"].tap()
        let next = app.buttons["chat.nextNeedsYou"]
        XCTAssertTrue(waitUntil(30) { next.exists }, "the answered card did not offer the next conversation: \(app.debugDescription)")
        XCTAssertTrue(waitUntil(5) { abs(next.frame.midY - needs.frame.midY) <= 0.5 && abs(next.frame.midY - running.frame.midY) <= 0.5 },
                      "the next request is not in the action row")
        XCTAssertTrue(waitUntil { self.state(app)["bottom"] == 1 && self.state(app)["newestShown"] == 1 }, "not following before streaming: \(self.state(app))")
        shot(app, "calm-before-stream")

        stream(upto: 175)
        let bar = app.descendants(matching: .any)["action-bar"].firstMatch
        XCTAssertTrue(waitUntil { !bar.exists && !next.exists }, "streaming did not collapse the action row: \(app.debugDescription)")
        XCTAssertTrue(strip.exists, "the collapsed queue must remain available")
        XCTAssertEqual(app.buttons.matching(identifier: "queue-item").count, 0)
        XCTAssertTrue(waitUntil { self.state(app)["bottom"] == 1 && self.state(app)["newestShown"] == 1 }, "the footer jump broke following: \(self.state(app))")
        shot(app, "calm-during-stream")

        stream()
        XCTAssertTrue(waitUntil { bar.exists && next.exists }, "the action row did not return after streaming: \(app.debugDescription)")
        XCTAssertTrue(waitUntil { self.state(app)["bottom"] == 1 && self.state(app)["newestShown"] == 1 }, "the restored footer broke following: \(self.state(app))")
        shot(app, "calm-after-stream")
    }

    @MainActor func testFooterChangesKeepTheReadingPosition() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(waitUntil(30) { (self.state(app)["items"] ?? 0) > 0 }, "the initial transcript never arrived: \(self.state(app))")
        let beforeItems = state(app)["items"] ?? 0
        let history = (1...18).map { "第 \($0) 段历史内容，用来把当前阅读位置移离最新回答。" }.joined(separator: "\n\n")
        mock("say", query: [URLQueryItem(name: "text", value: history)])
        XCTAssertTrue(waitUntil(30) { (self.state(app)["items"] ?? 0) > beforeItems && self.state(app)["bottom"] == 1 }, "the long fixture did not arrive: \(self.state(app))")

        for _ in 0..<3 {
            let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
            from.press(forDuration: 0.05, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.75)))
        }
        XCTAssertTrue(waitUntil { self.state(app)["bottom"] == 0 && self.state(app)["follows"] == 0 && self.state(app)["arrow"] == 1 },
                      "did not enter manual reading: \(self.state(app))")
        XCTAssertTrue(waitUntil { self.state(app)["readingY"] != nil && self.state(app)["readingY"] != -1 && self.readingID(app) != "none" },
                      "no historical row position after scrolling: \(self.state(app))")
        let rowY = try XCTUnwrap(state(app)["readingY"], "the scroll probe has no historical row position")
        XCTAssertNotEqual(rowY, -1, "the historical row has no measured screen position")
        let rowID = try XCTUnwrap(readingID(app), "the scroll probe has no historical row id")
        let stripHeight = strip.frame.height
        let stable = {
            abs((self.state(app)["readingY"] ?? rowY + 1000) - rowY) <= 2 && self.readingID(app) == rowID
        }

        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        XCTAssertTrue(app.buttons.matching(identifier: "queue-item").firstMatch.waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertEqual(strip.frame.height, stripHeight, accuracy: 0.5, "expanded queue rows must float rather than grow the footer")
        XCTAssertEqual(app.buttons.matching(identifier: "queue-item").count, 2, "only the owner's two rows may open")
        XCTAssertFalse(app.buttons["queue-relayed-summary"].exists)
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "来自「早报")).firstMatch.exists)
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "对话空闲后送达")).firstMatch.exists)
        XCTAssertTrue(waitUntil(5, samples: 4, stable), "opening the queue moved the historical row: before \(rowY), now \(self.state(app))")

        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        XCTAssertTrue(waitUntil { app.buttons.matching(identifier: "queue-item").count == 0 }, "the queue did not collapse")
        XCTAssertTrue(waitUntil(5, samples: 4, stable), "closing the queue moved the historical row: before \(rowY), now \(self.state(app))")

        stream(upto: 175)
        let bar = app.descendants(matching: .any)["action-bar"].firstMatch
        XCTAssertTrue(waitUntil { !bar.exists }, "streaming did not collapse the action row")
        XCTAssertTrue(waitUntil(5, samples: 4, stable), "starting the stream moved the historical row: before \(rowY), now \(self.state(app))")
        stream()
        XCTAssertTrue(waitUntil { bar.exists }, "the action row did not return")
        XCTAssertTrue(waitUntil(5, samples: 4, stable), "ending the stream moved the historical row: before \(rowY), now \(self.state(app))")
    }
}
