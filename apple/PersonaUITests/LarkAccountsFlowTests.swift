import XCTest

/// Several Feishu accounts (docs/extensions/feishu.md「多账号」) against a mock started with MOCK_LARK_ACCOUNTS=1: the Messages page's
/// account chips, a chat of the second account, and its inbox item opening in that account. Never a Feishu account.
final class LarkAccountsFlowTests: XCTestCase {
    @MainActor private func launch(_ extra: [String] = []) throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs a MOCK_LARK_ACCOUNTS mock") }
        continueAfterFailure = false
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-demo.on", "NO", "-integrations.lark.enabled", "YES",
                               "-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryL"] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }

    @MainActor func testSwitchAccountOnTheMessagesPage() throws {
        let app = try launch(["-tab", "feishu"])
        XCTAssertTrue(app.buttons["shell-feishu"].firstMatch.waitForExistence(timeout: 20))
        app.buttons["shell-feishu"].firstMatch.tapCenter()
        let second = app.buttons["feishu-account-2"]
        XCTAssertTrue(second.waitForExistence(timeout: 20))
        app.buttons["feishu-account-1"].tapCenter() // the choice is kept across launches
        XCTAssertTrue(app.buttons["feishu-chat-f2"].waitForExistence(timeout: 20), "the hub's own account")
        XCTAssertFalse(app.buttons["feishu-account-3"].exists, "a signed-out account has no chip")
        shot(app, "accounts-own")
        second.tapCenter()
        // The second account has 「季度规划」 (w1) pinned: it is on the shelf only, not again in the list (#1342).
        XCTAssertTrue(app.buttons["feishu-chat-w2"].waitForExistence(timeout: 20), "the second account's chats")
        XCTAssertTrue(app.buttons["feishu-pin-w1"].waitForExistence(timeout: 10), "the second account's pinned chat")
        XCTAssertFalse(app.buttons["feishu-chat-f2"].exists)
        XCTAssertFalse(app.buttons["feishu-pin-f2"].exists)
        shot(app, "accounts-second")
        app.buttons["feishu-pin-w1"].tapCenter()
        XCTAssertTrue(app.staticTexts["下周一前把排期填一下，表格在群公告里"].firstMatch.waitForExistence(timeout: 20)
                      || app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", "排期")).firstMatch.waitForExistence(timeout: 5))
        shot(app, "accounts-second-chat")
    }

    @MainActor func testFeishuSettingsListsTheAccounts() throws {
        let app = try launch(["-tab", "settings", "-openConnectorFeishu", "YES"])
        let row = app.descendants(matching: .any)["feishu-account-row-2"]
        XCTAssertTrue(row.waitForExistence(timeout: 20))
        XCTAssertTrue(app.descendants(matching: .any)["feishu-account-row-3"].exists)
        shot(app, "accounts-settings")
    }
}
