import XCTest

/// A picture sent to Persona shows in the chat, as in an Agents chat: at once in the message being sent, still there once
/// the transcript has it, and after the chat is opened again; a tap opens the image viewer (2026-11-03: it vanished as
/// soon as the transcript's copy came, claude -p writing the picture before the message's text).
///   PORT=18797 MOCK_ECHO_SENT=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/AssistantSentPhotoTests
final class AssistantSentPhotoTests: XCTestCase {
    private var hub = ""
    /// The mock's main Persona conversation.
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_ECHO_SENT=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = false
        // The notifications prompt of a fresh install comes up when it likes, and while it is up nothing under it
        // is in the element tree.
        addUIInterruptionMonitor(withDescription: "notifications") { alert in
            let allow = alert.buttons["Allow"]
            guard allow.exists else { return false }
            allow.tap()
            return true
        }
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/persona-sent-photo-\(name)\(suffix).png"))
    }

    /// The sent picture in the chat (the one being sent, or the transcript's): not the composer's own thumbnail.
    @MainActor private func sentPhoto(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label == %@", "图片 1，点按查看大图")).firstMatch
    }

    @MainActor func testAPictureSentToPersonaShowsAndStays() {
        let app = launch(["-showComposerPhoto", "YES"])
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
        if allow.waitForExistence(timeout: 8) { allow.tap() }
        // The picture in the composer (its label is not always in the tree in time; what counts is what the chat shows).
        _ = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "图片 1")).firstMatch.waitForExistence(timeout: 10)
        if allow.exists { allow.tap() }
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no composer:\n\(app.debugDescription)")
        field.tap()
        field.typeText("看看这张")
        let send = app.buttons["发送"].firstMatch
        XCTAssertTrue(send.waitForExistence(timeout: 5), "Send")
        send.tap()

        // At once, in the message being sent.
        XCTAssertTrue(sentPhoto(app).waitForExistence(timeout: 2), "the picture shows at once")
        shot(app, "sending")
        // The transcript's copy replaces the one being sent (MOCK_ECHO_SENT: claude -p's order, the picture first).
        sleep(4)
        XCTAssertTrue(sentPhoto(app).exists, "the picture stays once the transcript has the message")
        shot(app, "sent")

        // A tap opens the image viewer.
        sentPhoto(app).tap()
        let close = app.buttons["关闭"].firstMatch
        XCTAssertTrue(close.waitForExistence(timeout: 5), "the image viewer")
        close.tap()

        // Opened again: still there.
        app.terminate()
        let again = launch()
        XCTAssertTrue(sentPhoto(again).waitForExistence(timeout: 20), "the picture is there when the chat opens again")
        shot(again, "reopened")
    }
}
