import XCTest

/// Settings › Connectors › GitHub signed in from the app with GitHub's device flow (docs/extensions/connectors.md「Login supports」): straight to
/// the code to enter at github.com/login/device (no client ID to fill in), then signed in on its own (the page asks
/// the hub again every interval); which computer's gh login it uses; what its token lacks. Against
/// scripts/mock/mock_hub.py (PERSONA_MOCK_HUB) with MOCK_GITHUB=device (testDeviceFlow), MOCK_GITHUB_LOGIN=1
/// (testChooseAMachine) or MOCK_GITHUB=scopes (testMissingScopes).
final class GitHubDeviceLoginTests: XCTestCase {
    private func launch(_ app: XCUIApplication) throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openConnectorGitHub", "YES"]
        app.launchPinned()
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "\(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testDeviceFlow() throws {
        let app = XCUIApplication()
        try launch(app)
        let signIn = app.buttons["connector.signIn"]
        guard signIn.waitForExistence(timeout: 20) else {
            shot(app, "github failed")
            XCTFail("no 登录 GitHub (MOCK_GITHUB=device?):\n\(app.debugDescription)")
            return
        }
        sleep(1)
        shot(app, "github signed out")
        signIn.tap()
        let code = app.staticTexts["connector.userCode"]
        XCTAssertTrue(code.waitForExistence(timeout: 10), "no code:\n\(app.debugDescription)")
        XCTAssertFalse(app.textFields["connector.field.client_id"].exists, "a client ID is still asked for")
        XCTAssertEqual(code.label, "ABCD-1234")
        XCTAssertTrue(app.buttons["connector.openVerification"].exists || app.links["connector.openVerification"].exists, "no 打开 GitHub")
        shot(app, "github device code")
        // The page asks again on its own: signed in after the mock's third ask.
        let gone = NSPredicate(format: "exists == false")
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: gone, evaluatedWith: code)], timeout: 15), .completed,
                       "not signed in on its own:\n\(app.debugDescription)")
        for _ in 0..<3 { app.swipeDown() } // the page's top (the list makes rows out of sight only when shown)
        let account = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "example-user")).firstMatch
        XCTAssertTrue(account.waitForExistence(timeout: 5), "the account is not shown:\n\(app.debugDescription)")
        sleep(1)
        shot(app, "github signed in")
    }

    /// Choosing which computer's gh login GitHub uses: MacBook's (signed in), then Mac Studio's (gh not signed in).
    @MainActor func testChooseAMachine() throws {
        let app = XCUIApplication()
        try launch(app)
        let studio = app.buttons["github.source.studio"]
        guard studio.waitForExistence(timeout: 20) else {
            shot(app, "github failed")
            XCTFail("no machines (MOCK_GITHUB_LOGIN=1?):\n\(app.debugDescription)")
            return
        }
        XCTAssertTrue(app.buttons["github.source.mbp"].isSelected, "MacBook is not the one chosen")
        sleep(1)
        shot(app, "github machine")
        app.swipeUp()
        sleep(1)
        shot(app, "github machine bottom")
        app.swipeDown()
        studio.tap()
        let picked = NSPredicate(format: "isSelected == true")
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: picked, evaluatedWith: studio)], timeout: 10), .completed, "Mac Studio not chosen")
        sleep(1)
        shot(app, "github machine studio")
    }

    /// The token in use lacks scopes: the page says which, how to fix it, and what does not work.
    @MainActor func testMissingScopes() throws {
        let app = XCUIApplication()
        try launch(app)
        let warn = app.descendants(matching: .any)["github.missingScopes"]
        for _ in 0..<4 where !warn.exists { app.swipeUp() }
        guard warn.waitForExistence(timeout: 20) else {
            shot(app, "github failed")
            XCTFail("no scope warning (MOCK_GITHUB=scopes?):\n\(app.debugDescription)")
            return
        }
        app.swipeUp() // the whole warning above the tab bar
        sleep(1)
        shot(app, "github scopes")
    }
}
