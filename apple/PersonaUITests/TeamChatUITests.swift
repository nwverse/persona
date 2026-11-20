import XCTest
import UIKit

/// Uses the same MOCK_TEAM=1 HTTP/WS fixture as the web. No personal hub or credentials are needed.
final class TeamChatUITests: XCTestCase {
    private let lead = "7ab5e000-0000-4000-8000-000000000001"
    private let tests = "7ab5e000-0000-4000-8000-000000000003"

    override func setUpWithError() throws {
        continueAfterFailure = false
        try XCTSkipIf(ProcessInfo.processInfo.environment["PERSONA_TEAM_HUB"] == nil, "Requires the MOCK_TEAM=1 hub")
    }

    @MainActor private func launch(_ session: String) throws -> XCUIApplication {
        if UIDevice.current.userInterfaceIdiom == .pad { XCUIDevice.shared.orientation = .landscapeLeft }
        let env = ProcessInfo.processInfo.environment
        let hub = try XCTUnwrap(env["PERSONA_TEAM_HUB"])
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-openChatsFresh", "YES",
                               "-tab", "dev", "-openSession", "server/" + session,
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        if let size = env["PERSONA_TEAM_TYPE"] { app.launchArguments += ["-UIPreferredContentSizeCategoryName", size] }
        app.launchPinned()
        let permission = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.buttons
            .matching(NSPredicate(format: "label IN %@", ["Don’t Allow", "Don't Allow", "不允许", "許可しない"])).firstMatch
        if permission.waitForExistence(timeout: 3) { permission.tap() }
        return app
    }

    @MainActor private func text(_ app: XCUIApplication, _ fragment: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", fragment)).firstMatch
    }

    @MainActor private func shot(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    @MainActor func testLeadOffersItsTeam() throws {
        let app = try launch(lead)
        let button = app.buttons["team-entry"].firstMatch
        let close = app.buttons["team-close"].firstMatch
        if !close.waitForExistence(timeout: 3) {
            guard button.waitForExistence(timeout: 25) else {
                shot("lead-without-team")
                XCTFail("The lead's chat did not offer its Team. " + app.debugDescription)
                return
            }
            let source = text(app, "From worker")
            XCTAssertTrue(source.waitForExistence(timeout: 20), app.debugDescription)
            shot("lead-and-worker-request")
            button.tap()
        }
        XCTAssertTrue(close.waitForExistence(timeout: 10), app.debugDescription)
        let pills = app.buttons.matching(identifier: "team-worker")
        XCTAssertTrue(pills.firstMatch.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertEqual(pills.count, 3)
        XCTAssertEqual(pills.element(boundBy: 0).label, "审一下登录重构的改动")
        XCTAssertEqual(pills.element(boundBy: 1).label, "补刷新失败的测试")
        XCTAssertEqual(pills.element(boundBy: 2).label, "更新新中间件的接口文档")
        XCTAssertTrue(pills.element(boundBy: 0).isSelected)
        let pane = app.descendants(matching: .any)["team-worker-chat"].firstMatch
        XCTAssertTrue(pane.waitForExistence(timeout: 12), app.debugDescription)
        let command = pane.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "go test ./auth/")).firstMatch
        XCTAssertTrue(command.waitForExistence(timeout: 12), app.debugDescription)
        shot("team-worker-request")
        let second = pills.element(boundBy: 1)
        second.tap()
        let answer = pane.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "补了 6 个用例")).firstMatch
        XCTAssertTrue(answer.waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertFalse(command.exists)
        XCTAssertTrue(second.isSelected)
        shot("team-worker-transcript")
        close.tap()
        XCTAssertTrue(text(app, "From worker").waitForExistence(timeout: 12), app.debugDescription)
    }

    @MainActor func testReenteringALeadRespectsTheScreenAndItsLastManualClose() throws {
        var app = try launch(lead)
        var close = app.buttons["team-close"].firstMatch
        if !close.waitForExistence(timeout: 3) {
            let open = app.buttons["team-entry"].firstMatch
            XCTAssertTrue(open.waitForExistence(timeout: 30), app.debugDescription)
            open.tap()
        }
        XCTAssertTrue(close.waitForExistence(timeout: 15), app.debugDescription)
        // Leave while open. The next visit should follow the width, with no manual close on record.
        app.terminate()
        app = try launch(lead)
        close = app.buttons["team-close"].firstMatch
        if UIDevice.current.userInterfaceIdiom == .pad {
            XCTAssertTrue(close.waitForExistence(timeout: 30), app.debugDescription)
        } else {
            let open = app.buttons["team-entry"].firstMatch
            XCTAssertTrue(open.waitForExistence(timeout: 30), app.debugDescription)
            XCTAssertFalse(close.exists)
            open.tap()
            XCTAssertTrue(close.waitForExistence(timeout: 10))
        }
        close.tap()
        XCTAssertTrue(app.buttons["team-entry"].firstMatch.waitForExistence(timeout: 10))
        app.terminate()
        app = try launch(lead)
        XCTAssertTrue(app.buttons["team-entry"].firstMatch.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertFalse(app.buttons["team-close"].firstMatch.exists)
    }

    @MainActor func testWorkerLinkOpensItsOwnTranscript() throws {
        let app = try launch(tests)
        let close = app.buttons["team-close"].firstMatch
        XCTAssertTrue(close.waitForExistence(timeout: 35), app.debugDescription)
        XCTAssertTrue(text(app, "补了 6 个用例").waitForExistence(timeout: 20), app.debugDescription)
        let selected = app.buttons.matching(identifier: "team-worker").element(boundBy: 1)
        XCTAssertTrue(selected.isSelected)
        close.tap()
        XCTAssertTrue(text(app, "From worker").waitForExistence(timeout: 10), app.debugDescription)
    }
}
