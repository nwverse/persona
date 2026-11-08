import XCTest

/// Inbox › Hand to AI opens the Persona conversation it went to on the message as sent, not on No messages yet until
/// claude has written its transcript; once it has, the message is there once (#511, #527). The default target is
/// Persona's main conversation, through the inbox hand-off (#1194): the message the hub put in its transcript carries the
/// hand-off's id and takes the bubble's place. Against scripts/mock/mock_hub.py with MOCK_ASSISTANT_TRANSCRIPT_SECONDS=8
/// (PERSONA_MOCK_HUB): the mock answers the start or hand-off request at once and writes the line that much later, as the
/// node does.
final class NewConversationEntryTests: XCTestCase {
    private static let mainConversation = "b2e4d6f8-0000-4000-8000-000000000001"

    @MainActor func testInboxHandOffOpensTheMainChatOnTheSentMessage() throws {
        try handOff(layout: ["-ui.oldLayout", "NO"], name: "main-shell", newChat: false)
    }

    @MainActor func testInboxHandOffOpensTheMainChatOnTheSentMessageInTheOldLayout() throws {
        try handOff(layout: ["-ui.oldLayout", "YES"], name: "main-old", newChat: false)
    }

    @MainActor func testInboxHandOffOpensANewChatOnTheSentMessage() throws {
        try handOff(layout: ["-ui.oldLayout", "NO"], name: "shell", newChat: true)
    }

    @MainActor func testInboxHandOffOpensANewChatOnTheSentMessageInTheOldLayout() throws {
        try handOff(layout: ["-ui.oldLayout", "YES"], name: "old", newChat: true)
    }

    @MainActor private func handOff(layout: [String], name: String, newChat: Bool) throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "", "-inbox.source", "",
                               "-openInboxItem", "mail:acc1:t1:501", "-showInboxAction", "handoff", "-openChatsFresh", "YES"] + layout
        app.launchPinned()
        func shot(_ name: String) {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.name = "\(name) \(UITestStyle.name)"
            a.lifetime = .keepAlways
            add(a)
            if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
                try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + "-\(UITestStyle.name).png"))
            }
        }
        // Every case hands off its own words: the main conversation keeps the earlier cases' hand-offs (in the mock and
        // in the app's chat cache), which would count as a second copy.
        let ask = "Hand-off check \(name) \(UUID().uuidString.prefix(8))"
        func asked() -> XCUIElementQuery {
            app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", ask))
        }
        func row(_ label: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", label)).firstMatch
        }
        let handedBefore = sentTo(hub).count

        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 20), "no 交给 AI sheet:\n\(app.debugDescription)")
        XCTAssertTrue(row("Persona（主对话）").exists, "no main chat target:\n\(app.debugDescription)")
        sleep(1)
        shot("handoff-sheet-\(name)")
        let field = app.descendants(matching: .any).matching(NSPredicate(
            format: "elementType IN %@ AND placeholderValue == %@",
            [XCUIElement.ElementType.textField.rawValue, XCUIElement.ElementType.textView.rawValue], "总结一下，告诉我要做什么，给出建议的回复")).firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no instructions field:\n\(app.debugDescription)")
        field.tap()
        field.typeText(ask)
        if newChat {
            row("Persona（新对话）").tap()
            sleep(1)
        }
        send.tap()
        // The conversation opens on the message right away (the transcript comes 8 s after the request).
        let bubble = asked().firstMatch
        let shown = bubble.waitForExistence(timeout: 4)
        shot("handoff-sent-\(name)")
        XCTAssertTrue(shown, "the conversation did not open on the sent message:\n\(app.debugDescription)")
        XCTAssertFalse(app.staticTexts["还没有消息"].exists, "the conversation showed 还没有消息")
        let handed = sentTo(hub)
        if newChat {
            XCTAssertEqual(handed.count, handedBefore, "a new chat went through the hand-off")
        } else {
            XCTAssertEqual(handed.count, handedBefore + 1, "the main chat target did not go through the hand-off")
            XCTAssertEqual(handed.last?["session_id"] as? String, Self.mainConversation, "the hand-off went elsewhere")
        }
        // Once claude has written it, the transcript's copy takes the bubble's place: once, not twice.
        sleep(10)
        shot("handoff-transcript-\(name)")
        XCTAssertTrue(bubble.exists, "the message went away")
        let hittable = asked().allElementsBoundByIndex.filter { $0.isHittable }
        XCTAssertEqual(hittable.count, 1, "the message shows \(hittable.count) times:\n\(app.debugDescription)")
    }

    /// Where the mock's hand-offs went (`/mock/sent_to`; the main chat's are the ones there with an inbox block).
    private func sentTo(_ hub: String) -> [[String: Any]] {
        guard let url = URL(string: hub + "/mock/sent_to") else { return [] }
        var answer: [[String: Any]] = []
        let done = expectation(description: "sent_to")
        URLSession.shared.dataTask(with: url) { data, _, _ in
            if let data, let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let sent = obj["sent"] as? [[String: Any]] {
                answer = sent.filter { ($0["text"] as? String)?.contains("收件箱事件") == true }
            }
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        return answer
    }
}
