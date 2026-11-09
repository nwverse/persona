import XCTest

/// Videos picked in Photos go as files (#981, docs/protocol/protocol.md「发送视频」), against scripts/mock/mock_hub.py with
/// `MOCK_SENT_VIDEO=<a video>` (TEST_RUNNER_PERSONA_VIDEO_SEND_HUB=http://127.0.0.1:<port>) and a made-up video in the
/// simulator's library (`xcrun simctl addmedia`). Each runs in the style the environment picks
/// (PERSONA_UI_STYLE); PERSONA_SHOTS saves the screenshots there too.
final class VideoSendTests: XCTestCase {
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"

    private var hub: String {
        get throws {
            guard let hub = ProcessInfo.processInfo.environment["PERSONA_VIDEO_SEND_HUB"] else {
                throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_SENT_VIDEO (PERSONA_VIDEO_SEND_HUB)")
            }
            return hub
        }
    }

    private func launch(_ args: [String]) throws -> XCUIApplication {
        continueAfterFailure = false
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + (try hub).addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        return app
    }

    /// The video goes to the agent's machine and into the message as a card that plays in the system player.
    @MainActor func testAVideoGoesToTheAgentAsAFile() throws {
        let app = try launch(["-openSession", "macbook/" + widget, "-openAgentsFresh", "YES", "-openChatsFresh", "YES"])
        // Wait for the chat: until it opens, the + on screen is the assistant's.
        XCTAssertTrue(remoteCard(app).waitForExistence(timeout: 30), "the chat did not open:\n\(app.debugDescription)")
        try sendAVideo(app, "agent")
    }

    /// The same from a new conversation with the assistant: the video goes to the assistant's machine.
    @MainActor func testAVideoGoesToTheAssistantAsAFile() throws {
        let app = try launch(["-tab", "assistant", "-newAssistant", "YES"])
        try sendAVideo(app, "assistant")
    }

    /// A video sent from elsewhere is only on the machine: a tap downloads it, then the system player opens.
    @MainActor func testAVideoOnlyOnTheMachineDownloadsFirst() throws {
        let app = try launch(["-openSession", "macbook/" + widget, "-openAgentsFresh", "YES", "-openChatsFresh", "YES"])
        let card = remoteCard(app)
        XCTAssertTrue(card.waitForExistence(timeout: 30), "no card for the sent video:\n\(app.debugDescription)")
        XCTAssertTrue((card.value as? String ?? "").contains("0:06"), "\(card.value ?? "")")
        shot(app, "video-card-remote")
        card.tap()
        try closePlayer(app, "video-player-remote", back: card)
    }

    // MARK: -

    /// The card of the video the mock's chat already has (`MOCK_SENT_VIDEO`, sent from elsewhere).
    @MainActor private func remoteCard(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "identifier == %@ AND label == %@", "video-card", "视频 录屏.mov")).firstMatch
    }

    @MainActor private func sendAVideo(_ app: XCUIApplication, _ name: String) throws {
        let hub = try hub
        let sentBefore = try sent(hub).count
        try pickTheVideo(app)
        let busy = app.descendants(matching: .any)["video-attachment-busy"].firstMatch
        if busy.waitForExistence(timeout: 10) { shot(app, "video-\(name)-uploading") }
        let ready = app.descendants(matching: .any)["video-attachment-ready"].firstMatch
        XCTAssertTrue(ready.waitForExistence(timeout: 90), "the video never got to the machine:\n\(app.debugDescription)")
        shot(app, "video-\(name)-ready")
        XCTAssertEqual(try sent(hub).count, sentBefore, "nothing is sent before 发送")

        let send = app.buttons.matching(NSPredicate(format: "label IN %@", ["发送", "Send"])).firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 5), "no send button:\n\(app.debugDescription)")
        send.tapCenter()
        let deadline = Date.now.addingTimeInterval(15)
        while Date.now < deadline, try sent(hub).count == sentBefore { usleep(100_000) }
        let text = try sent(hub).last ?? ""
        XCTAssertTrue(text.contains("<persona-file kind=\"video\""), text)
        XCTAssertTrue(text.contains("path=\"/tmp/"), text)
        XCTAssertTrue(text.contains("duration=\"6.0\""), text)
        XCTAssertTrue(text.hasPrefix("请看视频"), "an empty box sends a short note with it: \(text)")
        XCTAssertFalse(ready.exists, "the card leaves the box once sent")

        // Not the one the chat already had.
        let card = app.buttons.matching(NSPredicate(format: "identifier == %@ AND label != %@", "video-card", "视频 录屏.mov")).firstMatch
        XCTAssertTrue(card.waitForExistence(timeout: 20), "no card in the sent message:\n\(app.debugDescription)")
        XCTAssertTrue((card.value as? String ?? "").contains("0:06"), "\(card.value ?? "")")
        shot(app, "video-\(name)-sent")
        card.tap()
        try closePlayer(app, "video-\(name)-player", back: card)
    }

    /// + › Photos, the made-up video, Add.
    @MainActor private func pickTheVideo(_ app: XCUIApplication) throws {
        // An enabled one: offline, the box's + is there but greyed out.
        let plus = app.buttons.matching(NSPredicate(format: "(identifier == %@ OR label IN %@) AND enabled == true", "shell-composer-add", ["添加", "Add"])).firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 30), "no + button:\n\(app.debugDescription)")
        plus.tap()
        let photos = app.buttons["照片"].firstMatch
        XCTAssertTrue(photos.waitForExistence(timeout: 5), "no 照片 item:\n\(app.debugDescription)")
        photos.tap()
        let video = app.images.matching(NSPredicate(format: "label CONTAINS[c] %@ OR label CONTAINS %@", "video", "视频")).firstMatch
        XCTAssertTrue(video.waitForExistence(timeout: 20), "no video in the picker:\n\(app.debugDescription)")
        shot(app, "video-picker")
        video.tap()
        // The picker's ✓ (out of process; not the app's own + behind it, which reads 「添加」 too).
        let add = app.buttons.matching(identifier: "Add").firstMatch
        XCTAssertTrue(add.waitForExistence(timeout: 5), "no ✓ in the picker:\n\(app.debugDescription)")
        add.tap()
    }

    /// The system player is up: shoot it, then close it with its own button. The button hides with the rest of the
    /// controls a moment into playback: a tap where it is brings them back, the next one closes; done once card is
    /// tappable again.
    @MainActor private func closePlayer(_ app: XCUIApplication, _ name: String, back card: XCUIElement) throws {
        let close = app.buttons.matching(NSPredicate(format: "label IN %@", ["关闭", "Close", "完成", "Done"])).firstMatch
        XCTAssertTrue(close.waitForExistence(timeout: 30), "the system player did not open:\n\(app.debugDescription)")
        let at = close.frame
        shot(app, name)
        for _ in 0..<4 where !card.isHittable {
            app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: at.midX, dy: at.midY)).tap()
            let deadline = Date.now.addingTimeInterval(3)
            while Date.now < deadline, !card.isHittable { usleep(200_000) }
        }
        XCTAssertTrue(card.isHittable, "the player is still up:\n\(app.debugDescription)")
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let layout = "shell"
        let full = "\(name)-\(UITestStyle.name)-\(layout)"
        let png = app.screenshot()
        let attachment = XCTAttachment(screenshot: png)
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(full + ".png"))
        }
    }

    private func sent(_ hub: String) throws -> [String] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/sent")!)
        struct Output: Decodable { struct Message: Decodable { var text: String? }; var sent: [Message] }
        return try JSONDecoder().decode(Output.self, from: data).sent.compactMap(\.text)
    }
}
