import XCTest

/// Machines (docs/server/machines.md「App」): the Agents list's machine menu opens every machine on one page, what needs a look
/// first (the offline NAS, then the busy server called out); a card opens the machine's page: its agents on top (one
/// opens the chat), the charts below, the range switches, the system last. Against scripts/mock/mock_hub.py
/// (TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>); TEST_RUNNER_PERSONA_SHOT_DIR=<dir> also saves the screens
/// there as PNG for a look.
final class MachinesPageTests: XCTestCase {
    private func hub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        return hub
    }

    private func launch(_ hub: String, _ args: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-dev.openChat", ""] + args
        app.launchPinned()
        return app
    }

    private func shot(_ name: String) {
        let png = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOT_DIR"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }

    /// A machine's card (in the shell the Agents page under it shows the machine names too).
    private func card(_ app: XCUIApplication, _ name: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", name + "、")).firstMatch
    }

    private func anything(_ app: XCUIApplication, _ label: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", label)).firstMatch
    }

    @MainActor func testListThenMachineThenRange() throws {
        let app = launch(try hub())
        let drawer = app.buttons.matching(NSPredicate(format: "identifier IN %@", ["assistant-sidebar-button", "work-sidebar-button"]))
            .allElementsBoundByIndex.first(where: \.isHittable)
        XCTAssertNotNil(drawer, "no visible drawer button:\n\(app.debugDescription)")
        drawer?.tapCenter()
        XCTAssertFalse(app.buttons["机器监控"].exists, "the old name is gone")
        let item = app.buttons["sidebar-machines"].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), app.debugDescription)
        item.tap()

        let server = card(app, "服务器"), macBook = card(app, "MacBook")
        XCTAssertTrue(server.waitForExistence(timeout: 10), "no machines:\n\(app.debugDescription)")
        XCTAssertTrue(app.navigationBars["机器"].exists, "titled 机器")
        XCTAssertTrue(macBook.exists)
        XCTAssertTrue(app.staticTexts["3/4 台在线"].exists, "the offline NAS should not count")
        XCTAssertTrue(anything(app, "CPU 高").waitForExistence(timeout: 10), "the busy server is not called out")
        XCTAssertTrue(anything(app, "1 个 Agent 需要你").exists, "MacBook's agent waiting is not counted")
        // What needs a look first: offline, then too busy, then an agent waiting.
        let nas = card(app, "NAS")
        XCTAssertTrue(nas.exists)
        XCTAssertLessThan(nas.frame.minY, server.frame.minY, "the offline NAS goes first")
        XCTAssertLessThan(server.frame.minY, macBook.frame.minY, "the busy server before MacBook")
        shot("list")
        // With MOCK_NODE_MISMATCH=1 Mac Studio's card says its personad is not the hub's deploy, with Redeploy
        // (DeployButtonTests taps it); a look at it, then back up.
        let redeploy = app.buttons["重新部署"]
        if redeploy.exists {
            for _ in 0..<3 where !redeploy.isHittable { app.swipeUp() }
            sleep(1)
            shot("list-deploy")
            for _ in 0..<3 where !server.isHittable { app.swipeDown() }
        }

        server.tap()
        let range = app.descendants(matching: .any)["metric-range"].firstMatch
        XCTAssertTrue(range.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.navigationBars["服务器"].exists)
        // Its agent on top, the charts and the system on the same page.
        let agent = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "排查 /orders 接口变慢")).firstMatch
        XCTAssertTrue(agent.exists, "the server's agent")
        XCTAssertLessThan(agent.frame.minY, range.frame.minY, "agents above the charts")
        XCTAssertTrue(app.staticTexts["Ubuntu 24.04.1 LTS"].exists, "the system facts")
        XCTAssertFalse(app.buttons["系统与监控"].exists, "no second page for the charts")
        XCTAssertTrue(range.buttons["1 小时"].isSelected)
        shot("detail")

        app.swipeUp()
        sleep(1)
        shot("detail-1h")
        range.buttons["24 小时"].tap()
        XCTAssertTrue(range.buttons["24 小时"].isSelected)
        let cpu = app.descendants(matching: .any)["chart-CPU"]
        XCTAssertTrue(cpu.waitForExistence(timeout: 10), app.debugDescription)
        sleep(1)
        shot("detail-24h")

        // Hold a chart: the moment's time shows in its header.
        app.swipeUp()
        sleep(1)
        let chart = app.descendants(matching: .any)["chart-下行"]
        XCTAssertTrue(chart.waitForExistence(timeout: 5), app.debugDescription)
        let at = chart.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
        at.press(forDuration: 1.2, thenDragTo: chart.coordinate(withNormalizedOffset: CGVector(dx: 0.45, dy: 0.7)))
        shot("detail-24h-lower")
        app.swipeUp()
        app.swipeUp()
        sleep(1)
        XCTAssertTrue(app.staticTexts["AMD EPYC 9354 32-Core Processor"].waitForExistence(timeout: 5), "no system facts")
        XCTAssertTrue(anything(app, "ssh").exists || anything(app, "hub 所在机器").exists, "how the hub reaches it")
        shot("detail-system")

        // Back up: the agent opens its chat on top.
        for _ in 0..<6 where !agent.isHittable { app.swipeDown() }
        agent.tap()
        XCTAssertTrue((app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch).waitForExistence(timeout: 10),
                      "the agent's chat:\n\(app.debugDescription)")
    }

    /// An offline machine's page: its OS still known, Offline, Reconnect right under its card, history up to when it went away.
    @MainActor func testOfflineMachine() throws {
        let app = launch(try hub(), ["-openMonitor", "nas", "-openMonitorRange", "6h"])
        XCTAssertTrue(app.navigationBars["NAS"].waitForExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(app.staticTexts["离线"].exists)
        let reconnect = app.buttons["重新连接"]
        XCTAssertTrue(reconnect.exists)
        XCTAssertTrue(reconnect.isHittable, "重新连接 on the first screen")
        XCTAssertTrue(anything(app, "机器离线，连上后显示它的 Agent").exists)
        XCTAssertTrue(app.staticTexts["Debian GNU/Linux 12 (bookworm)"].exists, "the system facts")
        shot("offline")
        app.swipeUp()
        sleep(1)
        shot("offline-6h")
    }
}
