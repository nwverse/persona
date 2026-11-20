import XCTest

/// A Persona chat's queued messages (#4475): the user's own rows and actions; relayed messages only add to the total (#7770).
/// The mock (`MOCK_PERSONA_QUEUE=1`, `PERSONA_MOCK_HUB`) has two of the user's messages, one with a photo, and one relayed.
final class PersonaQueueUITests: XCTestCase {
    private var hub: String!
    private struct QueueEdits: Decodable { var edits: [[String: [Int]]] }

    private func queueEdits() throws -> [[String: [Int]]] {
        try JSONDecoder().decode(QueueEdits.self, from: Data(contentsOf: URL(string: hub + "/mock/queue_edits")!)).edits
    }

    /// The sessions the mock got a `…/queue/send-now` for, in order (`GET /mock/send_nows`).
    private func sendNows() throws -> [String] {
        struct Log: Decodable { var send_nows: [String] }
        return try JSONDecoder().decode(Log.self, from: Data(contentsOf: URL(string: hub + "/mock/send_nows")!)).send_nows
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_PERSONA_QUEUE=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        resetQueue()
    }

    private func resetQueue(photos: Int = 1) {
        let done = expectation(description: "reset persona queue")
        URLSession.shared.dataTask(with: URL(string: hub + "/mock/reset?photos=\(photos)")!) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 5)
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001", "-openChatsFresh", "YES",
                               "-openDraftsFresh", "YES"] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String, appearance: String? = nil) throws {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = appearance ?? env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/persona-queue-\(appearance)-\(name).png"))
    }

    /// PERSONA_SHOTS: this step in light and dark through the system's appearance (a `-ui.scheme` launch changes the app
    /// icon, and its notice covers the shot), then back to the run's appearance.
    @MainActor private func shotLightAndDark(_ app: XCUIApplication, _ name: String) throws {
        guard ProcessInfo.processInfo.environment["PERSONA_SHOTS"] != nil else { return }
        let look = XCUIDevice.shared.appearance
        for (each, word) in [(XCUIDevice.Appearance.light, "light"), (.dark, "dark")] {
            XCUIDevice.shared.appearance = each
            sleep(1)
            try shot(app, name, appearance: word)
        }
        XCUIDevice.shared.appearance = look
        sleep(1)
    }

    @MainActor func testPersonaChatShowsTheQueueStrip() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), "no queue strip in the Persona chat:\n\(app.debugDescription)")
        try shot(app, "strip")
    }

    /// The strip names the assistant, and Send Now sends at one tap (#7169): its Persona wording, which a confirmation
    /// used to show, is the button's VoiceOver hint (`QueueStripHintTests`), never drawn over the chat.
    @MainActor func testPersonaSendNowSendsAtOneTap() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        // The assistant by the name shown everywhere else (#6489: the roster's, 「助手」 in the mock), never Claude.
        let named = { (name: String) in strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", name)).firstMatch }
        XCTAssertTrue(named("助手").exists, "the queue does not name the assistant:\n\(strip.debugDescription)")
        XCTAssertFalse(named("Claude").exists, "the queue names Claude, not the assistant:\n\(strip.debugDescription)")
        let sendNow = app.buttons["queue-send-now"]
        XCTAssertTrue(sendNow.waitForExistence(timeout: 5), "no Send Now for the user's own queued messages:\n\(app.debugDescription)")
        let before = try sendNows().count
        sendNow.tap()
        XCTAssertTrue(sendNow.waitForNonExistence(timeout: 10), "one tap on Send Now did not send:\n\(app.debugDescription)")
        XCTAssertEqual(Array(try sendNows().dropFirst(before)), ["server/b2e4d6f8-0000-4000-8000-000000000001"], "one tap, one send-now")
        let wording = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "不用等到下一步")).firstMatch
        XCTAssertFalse(wording.exists, "a confirmation came up:\n\(app.debugDescription)")
        try shot(app, "sent")
    }

    @MainActor func testRelayedMessagesOnlyCountTowardTheCollapsedSummary() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "排队中 3 条")).firstMatch.exists)
        XCTAssertFalse(app.buttons["queue-relayed-summary"].exists, "another conversation gets no insertion row above the composer")
        XCTAssertEqual(app.buttons.matching(identifier: "queue-item").count, 0, "a fresh queue is collapsed")
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        XCTAssertEqual(app.buttons.matching(identifier: "queue-item").count, 2, "only the owner's two messages open")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "来自「早报")).firstMatch.exists)
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "对话空闲后送达")).firstMatch.exists)
        try shotLightAndDark(app, "relayed-count-only")
    }

    @MainActor func testTakeBackRestoresWordsAndPhotos() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let photoMessage = app.buttons.matching(identifier: "queue-item").element(boundBy: 1)
        XCTAssertTrue(photoMessage.waitForExistence(timeout: 5), app.debugDescription)
        photoMessage.tap()
        let takeBack = app.buttons["撤回编辑"]
        XCTAssertTrue(takeBack.waitForExistence(timeout: 5), app.debugDescription)
        takeBack.tap()

        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertFalse((field.value as? String)?.contains("请看图片") == true,
                       "the photo-only stand-in is not restored as the user's words")
        XCTAssertTrue(app.buttons["移除图片 1"].waitForExistence(timeout: 10),
                      "the queued photo returns to the composer too:\n\(app.debugDescription)")
        let edits = try queueEdits()
        XCTAssertEqual(edits.last?["keep"], [0], "only the other own message is re-queued")
        try shot(app, "restored")
    }

    @MainActor func testOverflowTakeBackCannotBeSentAway() throws {
        resetQueue(photos: 4)
        let app = launch(["-showComposerPhoto", "YES"])
        let words = "撤回后保留 \(UUID().uuidString.prefix(8))"
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        field.tap()
        field.typeText(words)
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let photoMessage = app.buttons.matching(identifier: "queue-item").element(boundBy: 1)
        XCTAssertTrue(photoMessage.waitForExistence(timeout: 5), app.debugDescription)
        photoMessage.tap()
        let takeBack = app.buttons["撤回编辑"]
        XCTAssertTrue(takeBack.waitForExistence(timeout: 5), app.debugDescription)
        takeBack.tap()

        let fifth = app.buttons["移除图片 5"]
        XCTAssertTrue(fifth.waitForExistence(timeout: 10), "one picked plus four restored photos stay in the composer:\n\(app.debugDescription)")
        let send = app.buttons["发送"]
        XCTAssertTrue(send.waitForExistence(timeout: 5), app.debugDescription)
        send.tap()
        XCTAssertTrue(app.staticTexts["一次最多 4 张"].waitForExistence(timeout: 5), app.debugDescription)
        XCTAssertTrue(fifth.exists, "a refused over-limit send must not clear the restored photos:\n\(app.debugDescription)")
        XCTAssertTrue((field.value as? String)?.contains(words) == true,
                      "a refused over-limit send must not clear the words either:\n\(app.debugDescription)")
        try shot(app, "overflow-send")
    }

    @MainActor func testOwnMessagesMoveDeleteAndSendNow() throws {
        let app = launch()
        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let items = app.buttons.matching(identifier: "queue-item")
        XCTAssertTrue(items.element(boundBy: 0).waitForExistence(timeout: 5), app.debugDescription)
        items.element(boundBy: 0).tap()
        let down = app.buttons["queue-move-down"]
        XCTAssertTrue(down.waitForExistence(timeout: 5), app.debugDescription)
        down.tap()
        XCTAssertTrue(items.element(boundBy: 1).label.contains("下周三"), "the first own message moved down")

        var edits = try queueEdits()
        XCTAssertEqual(edits.last?["keep"], [1, 0], "the move re-queues only the user's own messages")

        items.element(boundBy: 0).tap()
        let delete = app.buttons["删除"]
        XCTAssertTrue(delete.waitForExistence(timeout: 5), app.debugDescription)
        delete.tap()
        XCTAssertTrue(items.element(boundBy: 0).label.contains("下周三"), "the other own message remains")
        edits = try queueEdits()
        XCTAssertEqual(edits.last?["keep"], [1], "delete leaves the other own message in place")

        let sendNow = app.buttons["queue-send-now"]
        XCTAssertTrue(sendNow.waitForExistence(timeout: 5), app.debugDescription)
        sendNow.tap() // at one tap (#7169)
        XCTAssertTrue(sendNow.waitForNonExistence(timeout: 10), "one tap on Send Now did not send:\n\(app.debugDescription)")
        XCTAssertTrue(strip.exists, "the relayed message still counts toward the queue summary")
        XCTAssertTrue(strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "排队中 1 条")).firstMatch.exists)
        XCTAssertFalse(app.buttons["queue-relayed-summary"].exists)
        XCTAssertEqual(items.count, 0, "a relayed-only queue has no rows to expand")
        XCTAssertNotEqual(strip.elementType, .button, "a relayed-only summary must not expose a no-op disclosure")
        XCTAssertEqual(strip.buttons.count, 0, "a relayed-only summary has no Send Now ghost or disclosure button")
        XCTAssertFalse(app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "来自「早报")).firstMatch.exists)

        let assistant = try JSONSerialization.jsonObject(with: Data(contentsOf: URL(string: hub + "/api/v1/assistant")!)) as? [String: Any]
        let conversations = assistant?["conversations"] as? [[String: Any]]
        let main = conversations?.first { $0["session_id"] as? String == "b2e4d6f8-0000-4000-8000-000000000001" }
        XCTAssertEqual(main?["queued_relayed"] as? Int, 1)
        XCTAssertEqual((main?["queued"] as? [String])?.count, 1)
        XCTAssertTrue((main?["queued"] as? [String])?.first?.contains("来自「早报") == true)
        try shot(app, "relayed-only")

        // A new owner message later must not reopen the panel left by the old expanded state (#7770).
        let collapsedHeight = strip.frame.height
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        field.tap()
        field.typeText("后来新增的主人消息")
        app.buttons["发送"].tap()
        XCTAssertTrue(strip.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "排队中 2 条")).firstMatch.waitForExistence(timeout: 5))
        XCTAssertEqual(items.count, 0, "the new owner message stays collapsed until the user taps again")
        XCTAssertEqual(app.buttons.matching(identifier: "queue-sending-item").count, 0, "a pending send stays collapsed too")
        XCTAssertEqual(strip.frame.height, collapsedHeight, accuracy: 0.5, "the count alone must not change the collapsed height")
    }

    @MainActor func testPhotoMarkForMessageSentFromThisDevice() throws {
        let app = launch(["-showComposerPhoto", "YES"])
        let words = "本机刚发的照片 \(UUID().uuidString.prefix(8))"
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), app.debugDescription)
        field.tap()
        field.typeText(words)
        let send = app.buttons["发送"]
        XCTAssertTrue(send.waitForExistence(timeout: 5), app.debugDescription)
        send.tap()

        let strip = app.descendants(matching: .any)["queue-strip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 10), app.debugDescription)
        if strip.elementType == .button { strip.tap() } else { strip.buttons.firstMatch.tap() }
        let row = app.buttons.matching(NSPredicate(format: "identifier == %@ AND label CONTAINS %@", "queue-item", words)).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "the new own message joined the queue:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["1 张图片"].waitForExistence(timeout: 5),
                      "the message sent from here carries a photo mark:\n\(app.debugDescription)")
        try shot(app, "photo-mark")
    }
}
