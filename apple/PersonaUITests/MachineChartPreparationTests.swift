import XCTest

final class MachineChartPreparationTests: XCTestCase {
    private var hub = ""
    private var app: XCUIApplication!

    @MainActor func testDetailRangesAndScrubInLightAndDark() throws {
        continueAfterFailure = false
        guard let value = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("requires the mock hub (PERSONA_MOCK_HUB)")
        }
        hub = value
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", ""]
        app.launchPinned()
        acceptIconAlert()
        app.terminate()
        for scheme in ["light", "dark"] {
            app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-tab", "settings", "-openMachines", "YES",
                                   "-openMachineDetail", "server", "-openMonitorRange", "24h",
                                   "-ui.scheme", scheme]
            app.launchPinned()
            acceptIconAlert()
            XCTAssertTrue(app.navigationBars["服务器"].waitForExistence(timeout: 30))
            let detail = app.scrollViews.containing(.any, identifier: "metric-range").firstMatch
            XCTAssertTrue(detail.waitForExistence(timeout: 20))
            let range = app.descendants(matching: .any).matching(identifier: "metric-range").firstMatch
            let cpu = app.descendants(matching: .any).matching(identifier: "chart-CPU").firstMatch
            waitForCPU(cpu)
            acceptIconAlert()
            XCTAssertFalse(XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch.exists)
            for title in ["6 小时", "1 小时", "24 小时"] {
                reveal(range, in: detail)
                let before = metricsHits()
                let choice = range.buttons[title]
                XCTAssertTrue(choice.isHittable)
                choice.tap()
                XCTAssertTrue(choice.isSelected)
                let deadline = Date.now.addingTimeInterval(15)
                while metricsHits() <= before, Date.now < deadline { usleep(100_000) }
                XCTAssertGreaterThan(metricsHits(), before)
                waitForCPU(cpu)
                sleep(1)
            }
            reveal(cpu, in: detail)
            let from = cpu.coordinate(withNormalizedOffset: CGVector(dx: 0.25, dy: 0.5))
            let to = cpu.coordinate(withNormalizedOffset: CGVector(dx: 0.7, dy: 0.5))
            from.press(forDuration: 0.3, thenDragTo: to)
            waitForCPU(cpu)
            XCTAssertTrue(range.buttons["24 小时"].isSelected)
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "machine-chart-preparation-" + scheme
            shot.lifetime = .keepAlways
            add(shot)
            app.terminate()
        }
    }

    @MainActor private func waitForCPU(_ cpu: XCUIElement) {
        XCTAssertTrue(cpu.waitForExistence(timeout: 20))
        let deadline = Date.now.addingTimeInterval(15)
        while (cpu.value as? String ?? "").filter({ $0 == "%" }).count < 2, Date.now < deadline {
            usleep(100_000)
        }
        XCTAssertGreaterThanOrEqual((cpu.value as? String ?? "").filter { $0 == "%" }.count, 2)
    }

    @MainActor private func reveal(_ element: XCUIElement, in scroller: XCUIElement) {
        XCTAssertTrue(element.exists)
        for _ in 0..<16 {
            let viewport = scroller.frame.intersection(app.windows.firstMatch.frame).insetBy(dx: 0, dy: 24)
            if element.isHittable, element.frame.minY >= viewport.minY, element.frame.maxY <= viewport.maxY { return }
            let upwards = element.frame.midY > viewport.midY
            scroller.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: upwards ? 0.75 : 0.35))
                .press(forDuration: 0.05, thenDragTo: scroller.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: upwards ? 0.45 : 0.65)))
        }
        XCTFail("the chart or range selector is not fully visible")
    }

    private func metricsHits() -> Int {
        guard let url = URL(string: hub + "/mock/hits"), let data = try? Data(contentsOf: url),
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let hits = json["hits"] as? [String: Int] else { return 0 }
        return hits["GET /api/v1/machines/server/metrics"] ?? 0
    }
}
