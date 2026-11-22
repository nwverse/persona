import XCTest

/// The hub's line over the Agents list and Settings › hub (docs/operations/hub-update.md「In the App」). Each button on the line takes only
/// its own taps (it sits in a list row that opens the hub page), and an update waiting for busy agents shows them and
/// asks before Update Now. Against scripts/mock/mock_hub.py (any MOCK_HUB_UPDATE; each test sets its own mode):
///   TEST_RUNNER_PERSONA_HUB_UPDATE_HUB=http://127.0.0.1:<port>
final class HubNoticeUITests: XCTestCase {
    private func hub(mode: String) throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_HUB_UPDATE_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_HUB_UPDATE_HUB)")
        }
        _ = try Data(contentsOf: URL(string: hub + "/mock/hub_update?mode=" + mode)!)
        return hub
    }

    private func status(_ hub: String) throws -> [String: Any] {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/hub/update")!)
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    private func launch(_ hub: String, _ args: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", ""] + args
        app.launchPinned()
        return app
    }

    /// OK hides 「hub 会在空闲时自动更新」 and leaves auto-update on; it does not open the page either.
    @MainActor func testSeenKeepsAutoUpdateOn() throws {
        let hub = try hub(mode: "notice")
        let app = launch(hub, ["-tab", "dev"])
        let line = app.descendants(matching: .any)["agents.hubNotice"]
        XCTAssertTrue(line.waitForExistence(timeout: 15), "no hub line:\n\(app.debugDescription)")
        app.buttons["hubNotice.seen"].tap()
        XCTAssertTrue(line.waitForNonExistence(timeout: 5), "the line stayed after 知道了")
        XCTAssertFalse(app.navigationBars["hub"].exists, "知道了 opened the hub page")
        let s = try status(hub)
        XCTAssertEqual(s["auto"] as? Bool, true, "知道了 turned auto-update off")
        XCTAssertEqual(s["auto_notice"] as? Bool, false, "the hub was not told the notice was seen")
    }

    /// Close on the same line turns auto-update off (and counts as seen).
    @MainActor func testOffTurnsAutoUpdateOff() throws {
        let hub = try hub(mode: "notice")
        let app = launch(hub, ["-tab", "dev"])
        let line = app.descendants(matching: .any)["agents.hubNotice"]
        XCTAssertTrue(line.waitForExistence(timeout: 15), "no hub line:\n\(app.debugDescription)")
        app.buttons["hubNotice.autoOff"].tap()
        XCTAssertTrue(line.waitForNonExistence(timeout: 5), "the line stayed after 关闭")
        XCTAssertEqual(try status(hub)["auto"] as? Bool, false, "关闭 left auto-update on")
    }

    /// An update waiting for idle lists what it waits for; Update Now asks first, then the hub restarts.
    @MainActor func testUpdateNowAsksFirst() throws {
        let hub = try hub(mode: "waiting")
        let app = launch(hub, ["-tab", "settings", "-openHubUpdate", "YES"])
        let now = app.buttons["hub.updateNow"]
        XCTAssertTrue(now.waitForExistence(timeout: 15), "no 现在就更新:\n\(app.debugDescription)")
        XCTAssertTrue(app.staticTexts["给天气 App 加主屏幕小组件"].exists, "the busy agent is not shown")
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/hub-waiting.png"))
        now.tap()
        let sure = app.buttons["现在就更新"]
        XCTAssertTrue(sure.waitForExistence(timeout: 5), "no second confirmation:\n\(app.debugDescription)")
        XCTAssertEqual(try status(hub)["state"] as? String, "waiting_idle", "updated before the confirmation")
        sure.tap()
        let progress = app.descendants(matching: .any)["hub.progress"]
        XCTAssertTrue(progress.waitForExistence(timeout: 5))
        let restarting = NSPredicate(format: "label CONTAINS %@", "正在重启")
        XCTAssertTrue(app.staticTexts.matching(restarting).firstMatch.waitForExistence(timeout: 5), "not restarting:\n\(app.debugDescription)")
        XCTAssertEqual(try status(hub)["state"] as? String, "restarting")
    }
}
