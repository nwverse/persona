import XCTest

/// A message card about a task (docs/assistant/unified-entry.md「Message cards link to tasks」) once its message was answered (#4797): handled
/// elsewhere it has no Send and says the draft was not sent; sent from the card it says it was replied. Against
/// scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_AT=11 (the card at the bottom) and
/// MOCK_TASKS_MESSAGE=filed / sent / unset (PERSONA_MOCK_HUB, PERSONA_MOCK_MESSAGE says which). With PERSONA_SHOTS set it
/// writes the card there (PERSONA_SHOTS_APPEARANCE names the appearance).
final class MessageCardStateUITests: XCTestCase {
    private var hub = ""
    private var message = ""
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        let env = ProcessInfo.processInfo.environment
        guard let h = env["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASKS_AT=11 (PERSONA_MOCK_HUB)")
        }
        hub = h
        message = env["PERSONA_MOCK_MESSAGE"] ?? ""
        continueAfterFailure = false
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/message-card-\(appearance)-\(name).png"))
    }

    @MainActor func testTheCardSaysWhereItsReplyStands() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main]
        app.launchPinned()
        // The notification question from SpringBoard covers the card on a fresh install.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let allow = springboard.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        if allow.waitForExistence(timeout: 3) { allow.tap() }

        let reply = app.descendants(matching: .any)["messageCard.reply"].firstMatch
        XCTAssertTrue(reply.waitForExistence(timeout: 20), "no message card:\n\(app.debugDescription)")
        let send = app.buttons["messageCard.send"] // The composer has its own send button.
        let filedLine = app.descendants(matching: .any)["messageCard.filed"].firstMatch
        switch message {
        case "filed":
            XCTAssertTrue(app.staticTexts["已处理，未发送的草稿"].exists, app.debugDescription)
            XCTAssertTrue(filedLine.exists, "no line saying the draft was not sent")
            XCTAssertFalse(send.exists, "a handled card still offers Send")
        case "sent":
            XCTAssertTrue(app.staticTexts["已以你的名义回复"].exists, app.debugDescription)
            XCTAssertFalse(send.exists, "a sent card still offers Send")
            XCTAssertFalse(filedLine.exists)
        default:
            XCTAssertTrue(app.staticTexts["按任务进度起草的回复"].exists, app.debugDescription)
            XCTAssertTrue(send.exists, "an open card has no Send")
            XCTAssertFalse(filedLine.exists)
        }
        try shot(app, message.isEmpty ? "open" : message)
    }
}
