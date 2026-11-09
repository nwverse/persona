import XCTest

/// A sealed message on a messaging source (#2138, docs/extensions/extension-messaging.md「Composer modes」) against scripts/mock/mock_hub.py with
/// MOCK_MESSAGING=1 MOCK_LARK_NATIVE_OLK=1 (PERSONA_MOCK_HUB): the app asks the source's `compose` what it needs and shows
/// exactly that (unlock the vault, then the cover as a choice), then sends with the token it gave; the sealed chunks
/// reach the vendor, and the message shows as sealed. No password or text goes anywhere but the mock.
final class MessagingSealedFlowTests: XCTestCase {
    @MainActor private func open(_ hub: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "feishu", "-demo.on", "NO",                                "-integrations.lark.enabled", "YES", "-showLarkSendFresh", "YES"]
        app.launchPinned()
        let messages = app.buttons.matching(identifier: "shell-feishu").firstMatch
        XCTAssertTrue(messages.waitForExistence(timeout: 15))
        messages.tapCenter()
        let chat = app.buttons["feishu-chat-f2"]
        XCTAssertTrue(chat.waitForExistence(timeout: 20)); chat.tapCenter()
        XCTAssertTrue(app.textFields["lark.box"].waitForExistence(timeout: 15))
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }

    private func post(_ hub: String, _ path: String, _ body: [String: Any]) async throws {
        var req = URLRequest(url: URL(string: hub + path)!)
        req.httpMethod = "POST"
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        _ = try await URLSession.shared.data(for: req)
    }

    @MainActor func testComposeAsksThenSendsSealed() async throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"], ProcessInfo.processInfo.environment["PERSONA_MESSAGING"] == "1" else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_MESSAGING=1 MOCK_LARK_NATIVE_OLK=1 (PERSONA_MOCK_HUB, PERSONA_MESSAGING=1)")
        }
        continueAfterFailure = false
        try await post(hub, "/test/lark-encryption/reset", ["locked": true])
        let app = open(hub)
        let mode = app.buttons["lark.encryption.mode"]
        XCTAssertTrue(mode.waitForExistence(timeout: 8))
        mode.tapCenter(); app.buttons["lark.encryption.encrypted"].tapCenter()
        XCTAssertTrue(app.staticTexts["发送时确认对外展示"].waitForExistence(timeout: 5), "the cover is asked for when sending, not here")
        let field = app.textFields["lark.box"]
        field.tapCenter()
        field.typeText("会议室改到三楼")
        app.buttons["lark.send"].tapCenter()

        // The source asks to unlock first: its own view, a secret field.
        let password = app.secureTextFields["messaging.compose.password"]
        XCTAssertTrue(password.waitForExistence(timeout: 10), "the unlock the source asks for")
        shot(app, "messaging-sealed-unlock")
        password.tapCenter(); password.typeText("wrong-synthetic")
        app.buttons["messaging.compose.submit"].tapCenter()
        XCTAssertTrue(app.staticTexts["密码不正确，请重试"].waitForExistence(timeout: 10))
        password.tapCenter(); password.typeText("synthetic-password")
        app.buttons["messaging.compose.submit"].tapCenter()

        // Then the cover: a choice of earlier lines.
        XCTAssertTrue(app.staticTexts["选一段历史消息，作为无法解密时看到的内容。"].waitForExistence(timeout: 10), "the cover the source asks for")
        shot(app, "messaging-sealed-cover")
        app.buttons["messaging.compose.submit"].tapCenter()

        // Sent with the token: the box empties, the message is there sealed, the vendor got the sealed chunks.
        XCTAssertTrue(app.buttons["messaging.compose.submit"].waitForNonExistence(timeout: 10))
        XCTAssertTrue(app.staticTexts["会议室改到三楼"].firstMatch.waitForExistence(timeout: 15))
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "lark.outgoing.sending").firstMatch.waitForNonExistence(timeout: 20))
        let (data, _) = try await URLSession.shared.data(from: URL(string: hub + "/api/v1/lark/conversations/f2/messages")!)
        let messages = (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["messages"] as? [[String: Any]] ?? []
        let mine = messages.last { $0["is_mine"] as? Bool == true }
        XCTAssertNotNil(mine?["olk"], "went out sealed: \(String(describing: mine))")
        shot(app, "messaging-sealed-sent")
        app.terminate()
    }
}
