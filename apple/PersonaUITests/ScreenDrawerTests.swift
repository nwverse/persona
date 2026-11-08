import XCTest

/// The live screen as a drawer (#1343): while a chat's model operates a Mac its running work says so
/// (「正在操作 MacBook · 点 AC → 9 → × → 8」) and opens the screen like the terminal's drawer, over it the step, dragged down it
/// closes; the ··· menus have VNC whether or not the model is at it. Against scripts/mock/mock_hub.py with
/// MOCK_COMPUTER=ready MOCK_COMPUTER_OPERATING=1 MOCK_SCREEN=fake MOCK_WEB=web/dist (`TEST_RUNNER_PERSONA_SCREEN_HUB`):
/// without MOCK_SCREEN=fake the screen is this Mac's own Screen Sharing, and with it off the drawer only says the hub
/// would not connect. PERSONA_SHOTS keeps the screenshots, named by `TEST_RUNNER_PERSONA_LOOK`.
final class ScreenDrawerTests: XCTestCase {
    private let calc = "server/b2e4d6f8-0000-4000-8000-0000000000c1"
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_SCREEN_HUB"] else {
            throw XCTSkip("needs mock_hub.py with MOCK_COMPUTER=ready MOCK_COMPUTER_OPERATING=1 MOCK_SCREEN=fake (PERSONA_SCREEN_HUB)")
        }
        continueAfterFailure = false
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=" + UITestToken.device
        app.launchArguments = ["-pairURL", pair, "-openScreenFresh", "YES"] + args // no Mac sign-in kept from another test
        app.launchPinned()
        return app
    }

    @MainActor func testTheAssistantsOperationOpensTheScreen() throws {
        let app = try launch(["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", calc])
        let pill = app.descendants(matching: .any)["computer-activity"].firstMatch
        XCTAssertTrue(pill.waitForExistence(timeout: 30), "no running work for the Mac:\n\(app.debugDescription)")
        XCTAssertTrue(pill.label.contains("正在操作 MacBook") && pill.label.contains("点 AC → 9 → × → 8"), pill.label)
        shot(app, "screen-pill-assistant")
        pill.tap()

        let step = app.descendants(matching: .any)["screen-step"].firstMatch
        XCTAssertTrue(step.waitForExistence(timeout: 10), "no drawer:\n\(app.debugDescription)")
        XCTAssertTrue(step.label.contains("正在：点 AC → 9 → × → 8"), step.label)
        let signIn = app.staticTexts["登录这台 Mac 的屏幕共享"].firstMatch
        XCTAssertTrue(signIn.waitForExistence(timeout: 30), "the screen did not come up:\n\(app.debugDescription)")
        XCTAssertTrue(app.descendants(matching: .any)["screen-phase"].firstMatch.label.contains("要登录"))
        shot(app, "screen-drawer")

        // × closes it.
        app.descendants(matching: .any)["screen-close"].firstMatch.tap()
        XCTAssertTrue(waitGone(step), "closed, still up")
    }

    @MainActor func testAnAgentsOperationShows() throws {
        let app = try launch(["-openSession", "macbook/" + widget, "-openAgentsFresh", "YES", "-openChatsFresh", "YES"])
        let pill = app.descendants(matching: .any)["computer-activity"].firstMatch
        XCTAssertTrue(pill.waitForExistence(timeout: 30), "no running work for the Mac:\n\(app.debugDescription)")
        XCTAssertTrue(pill.label.contains("正在操作 MacBook") && pill.label.contains("点 小组件"), pill.label)
        shot(app, "screen-pill-agent")
    }

    /// From a machine page pushed in the new shell's pages (#1604): the screen came up nowhere there (a cover from the
    /// root was swallowed while a page was presented); in a window of its own it comes up over it, and closes back to it.
    @MainActor func testVNCFromTheShellsMachinePage() throws {
        let app = try launch(["-newAssistant", "YES", "-openShellPage", "machines"])
        let row = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "MacBook")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 30), "no MacBook on the machines page:\n\(app.debugDescription)")
        row.tap()
        let vnc = app.buttons["computer-screen"].firstMatch
        XCTAssertTrue(vnc.waitForExistence(timeout: 20), "no VNC on its page:\n\(app.debugDescription)")
        for _ in 0..<6 where !vnc.isHittable { app.swipeUp() }
        XCTAssertTrue(vnc.label.contains("VNC"), vnc.label)
        vnc.tap()
        let drawer = app.descendants(matching: .any)["screen-drawer"].firstMatch
        XCTAssertTrue(drawer.waitForExistence(timeout: 10), "the screen over the machine page:\n\(app.debugDescription)")
        shot(app, "screen-from-machine-page")
        app.descendants(matching: .any)["screen-close"].firstMatch.tap()
        XCTAssertTrue(waitGone(drawer), "closed")
        XCTAssertTrue(vnc.waitForExistence(timeout: 5), "back on the machine page")
    }

    @MainActor func testTheMenusHaveTheScreen() throws {
        let app = try launch(["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", calc])
        let face = app.buttons["shell-assistant"].firstMatch
        XCTAssertTrue(face.waitForExistence(timeout: 30))
        face.tap()
        let item = app.buttons["VNC · MacBook"].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no VNC in the menu:\n\(app.debugDescription)")
        shot(app, "screen-menu")
        item.tap()
        XCTAssertTrue(app.descendants(matching: .any)["screen-drawer"].firstMatch.waitForExistence(timeout: 10), "the menu's VNC opens it")
    }

    private func waitGone(_ e: XCUIElement) -> Bool {
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline { if !e.exists { return true }; usleep(200_000) }
        return false
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let look = ProcessInfo.processInfo.environment["PERSONA_LOOK"].map { "-" + $0 } ?? ""
        let full = "\(name)-\(UITestStyle.name)\(look)"
        let png = app.screenshot()
        let attachment = XCTAttachment(screenshot: png)
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(full + ".png"))
        }
    }
}
