import XCTest

/// Starting a Persona conversation from another page (Inbox › Hand to AI) opens it on the message as sent, not on No messages yet
/// until claude has written its transcript; once it has, the message is there once (#511, #527). Against
/// scripts/mock/mock_hub.py with MOCK_ASSISTANT_TRANSCRIPT_SECONDS=8 (PERSONA_MOCK_HUB): the mock answers the start
/// request at once and writes the first line that much later, as the node does.
final class NewConversationEntryTests: XCTestCase {
    @MainActor func testInboxHandOffOpensOnTheSentMessage() throws {
        try handOff(name: "shell")
    }

    @MainActor private func handOff(name: String) throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "", "-inbox.source", "",
                               "-openInboxItem", "mail:acc1:t1:501", "-showInboxAction", "handoff"]
        app.launchPinned()
        func shot(_ name: String) {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.name = "\(name) \(UITestStyle.name)"
            a.lifetime = .keepAlways
            add(a)
            if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
                try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + ".png"))
            }
        }
        let ask = "总结一下，告诉我要做什么"
        func asked() -> XCUIElementQuery {
            app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", ask))
        }

        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 20), "no 交给 AI sheet:\n\(app.debugDescription)")
        sleep(1)
        send.tap()
        // The conversation opens on the message right away (the transcript comes 8 s after the start).
        let bubble = asked().firstMatch
        let shown = bubble.waitForExistence(timeout: 4)
        shot("handoff-sent-\(name)")
        XCTAssertTrue(shown, "the new conversation did not open on the sent message:\n\(app.debugDescription)")
        XCTAssertFalse(app.staticTexts["还没有消息"].exists, "the new conversation showed 还没有消息")
        // Once claude has written it, the transcript's copy takes the bubble's place: once, not twice.
        sleep(10)
        shot("handoff-transcript-\(name)")
        XCTAssertTrue(bubble.exists, "the message went away")
        let hittable = asked().allElementsBoundByIndex.filter { $0.isHittable }
        XCTAssertEqual(hittable.count, 1, "the message shows \(hittable.count) times:\n\(app.debugDescription)")
    }
}
