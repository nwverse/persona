import XCTest

/// An open Agent chat still gets the new turn when its live items stop (scripts/mock/mock_hub.py, PERSONA_MOCK_HUB).
/// `/mock/say?drop=1` writes a message and its reply into the transcript and moves the session's `updated_at`, but never
/// pushes the items, like a node whose transcript watch ended. The chat notices that the session is newer than its
/// newest line and nothing came over its subscription, asks for what came after it (`AgentSyncPacing.catchUpAt`) and
/// shows the message and the reply.
final class ChatCatchUpTests: XCTestCase {
    private var hub: String!
    private let working = "macbook/a1f3c2d4-0000-4000-8000-000000000001" // the weather widget session, at work

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    @MainActor func testAChatWhoseItemsStopCatchesUp() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // No chat kept on disk from an earlier run: the mock starts its conversations over at each reset, and a cached
        // chat's seqs would hide the lines this test adds.
        app.launchArguments = ["-pairURL", pair, "-showScrollState", "YES", "-dev.openChat", "", "-openChatsFresh", "YES",
                               "-tab", "dev", "-openSession", working]
        app.launchPinned()
        let probe = app.staticTexts.matching(identifier: "chat.scrollState").firstMatch
        XCTAssertTrue(probe.waitForExistence(timeout: 20), "the chat did not open")
        sleep(3) // the backlog is in and the subscription is quiet

        let asked = "补拉测试的新消息", answer = "补拉测试的新回复"
        var c = URLComponents(string: hub + "/mock/say")!
        c.queryItems = [URLQueryItem(name: "text", value: answer), URLQueryItem(name: "user", value: asked),
                        URLQueryItem(name: "drop", value: "1")]
        _ = try Data(contentsOf: c.url!)

        // Asked for after `AgentSyncPacing.staleQuiet` without items, at most every `stalePullEvery`: well within 20 s.
        let message = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", asked)).firstMatch
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", answer)).firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 20), "the new message never came in without its push")
        XCTAssertTrue(reply.waitForExistence(timeout: 5), "the new reply never came in without its push")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "caught-up"
        shot.lifetime = .keepAlways
        add(shot)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = ProcessInfo.processInfo.environment["PERSONA_STYLE"] ?? "light"
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/catch-up-\(style).png"))
        }
    }
}
