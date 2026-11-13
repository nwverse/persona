import XCTest

/// The assistant sidebar finds a chat by what was said in it and opens it at that message (#6284), against
/// scripts/mock/mock_hub.py with `MOCK_CHAT_SEARCH=1` (`TEST_RUNNER_PERSONA_MOCK_HUB`): the user's words in a result row,
/// tapping it lands on that message; an early one is paged back for; a thought is never found.
final class AssistantChatSearchUITests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let value = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_CHAT_SEARCH=1 (PERSONA_MOCK_HUB)")
        }
        hub = value
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openWelcome", "NO", "-demo.on", "NO", "-dev.openChat", "", "-tab", "assistant", "-openChatsFresh", "YES"]
        app.launchPinned()
        return app
    }

    @MainActor private func search(_ app: XCUIApplication, _ words: String) {
        let open = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(open.waitForExistence(timeout: 20), "no sidebar button:\n\(app.debugDescription)")
        open.tap()
        // the drawer's search sits behind its button (a split's list column has it open already)
        let reveal = app.buttons["搜索对话"].firstMatch
        if reveal.waitForExistence(timeout: 5), reveal.isHittable { reveal.tap() }
        let field = app.textFields.matching(NSPredicate(format: "placeholderValue == %@ OR label == %@", "搜索对话", "搜索对话")).firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no search field:\n\(app.debugDescription)")
        field.tap()
        field.typeText(words)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testFindsAChatByTheUsersWordsAndOpensItThere() {
        let app = launch()
        search(app, "BlueLane")
        let row = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "我想去青石巷 BlueLane 看看")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no row with the matched sentence:\n\(app.debugDescription)")
        shot(app, "sidebar-search-bluelane")
        row.tap()
        let message = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ AND NOT (label CONTAINS %@)", "我想去青石巷 BlueLane 看看", "琥珀灯塔")).firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 15), "the chat did not open at the message:\n\(app.debugDescription)")
        XCTAssertTrue(message.isHittable, "the message is not on screen")
        shot(app, "sidebar-search-bluelane-landed")
    }

    @MainActor func testPagesBackForAnEarlyMessage() {
        let app = launch()
        search(app, "GlacierPost")
        let row = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "GlacierPost")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no row for early history:\n\(app.debugDescription)")
        row.tap()
        let message = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ AND NOT (label CONTAINS %@)", "冰川邮局 GlacierPost", "琥珀灯塔")).firstMatch
        XCTAssertTrue(message.waitForExistence(timeout: 30), "the early message was not paged back for:\n\(app.debugDescription)")
        shot(app, "sidebar-search-early-landed")
    }

    @MainActor func testNeverFindsAThought() {
        let app = launch()
        search(app, "ThinkWord")
        // the manager answers within a second; no result row may hold the thought
        let row = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "ThinkWord")).firstMatch
        XCTAssertFalse(row.waitForExistence(timeout: 4), "a thought was found:\n\(app.debugDescription)")
    }
}
