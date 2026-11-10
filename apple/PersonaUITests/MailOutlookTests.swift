import XCTest

/// Add Mail Account › Outlook (docs/extensions/mail.md「Outlook: Sign In with Microsoft」): with no client ID on the hub the field for it and the steps
/// with the redirect URI come first; a value that is no client ID is refused, a good one is saved on the hub and
/// Sign In with Microsoft takes its place. Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB).
final class MailOutlookTests: XCTestCase {
    @MainActor func testTheClientIDComesFirstThenTheSignIn() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-dev.openChat", "", "-addMail", "YES"]
        app.launchPinned()
        func shot(_ name: String) {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.name = "\(name) \(UITestStyle.name)"
            a.lifetime = .keepAlways
            add(a)
        }
        func text(_ s: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
        }

        let outlook = app.buttons["Outlook"]
        XCTAssertTrue(outlook.waitForExistence(timeout: 20), "no Outlook in 添加邮箱:\n\(app.debugDescription)")
        outlook.tap()
        let field = app.textFields["应用程序(客户端) ID"]
        XCTAssertTrue(field.waitForExistence(timeout: 10), "no client ID field:\n\(app.debugDescription)")
        XCTAssertTrue(text("persona-mail-oauth://outlook").exists, "no redirect URI to register")
        XCTAssertTrue(text("允许公共客户端流").exists, "no registration steps")
        shot("outlook client id")

        field.tap()
        field.typeText("not-a-client-id")
        app.buttons["保存 client ID"].tap()
        XCTAssertTrue(text("这不像 client ID").waitForExistence(timeout: 10), "a bad client ID was not refused")

        field.tap()
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 20) + "11111111-2222-3333-4444-555555555555")
        app.buttons["保存 client ID"].tap()
        let signIn = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "使用 Microsoft 账号登录")).firstMatch
        XCTAssertTrue(signIn.waitForExistence(timeout: 10), "no Microsoft sign-in after saving:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["更换 client ID"].exists)
        shot("outlook sign in")
    }
}
