import XCTest

final class PermissionInputUITests: XCTestCase {
    @MainActor func testSecureInputIsRequiredBeforeApproval() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs MOCK_SETTINGS_INPUT=1 scripts/mock/mock_hub.py")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-app.tab", "chats", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000006", "-ui.style", "glass"]
        app.launch()
        let field = app.secureTextFields["interaction.field.password"]
        XCTAssertTrue(field.waitForExistence(timeout: 20), app.debugDescription)
        let allow = app.buttons["填写并允许"]
        XCTAssertFalse(allow.isEnabled)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "permission-input.png"))
        }
        field.tap()
        field.typeText("example-password")
        XCTAssertTrue(allow.isEnabled)
        allow.tap()
        let deadline = Date().addingTimeInterval(5)
        while Date() < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
               let response = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let rows = response["resolved"] as? [[String: Any]],
               let row = rows.first(where: { $0["id"] as? String == "ix_settings_input" }) {
                let resolution = row["resolution"] as? [String: Any]
                XCTAssertEqual(resolution?["allow"] as? Bool, true)
                XCTAssertEqual((resolution?["values"] as? [String: String])?["password"], "example-password")
                // The card goes once answered: iOS must not offer to save the value as a password (#1598).
                PasswordPrompt.assertNone(app, "a permission form's secret field")
                return
            }
            Thread.sleep(forTimeInterval: 0.1)
        }
        XCTFail("mock did not receive the form approval")
    }
}
