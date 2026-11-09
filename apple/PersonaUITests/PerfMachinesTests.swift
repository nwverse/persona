import XCTest

/// The four inherited machine tests from PerfAgentsTests, with explicit page/content assertions and
/// finite push batches, plus the missing idle cases (#5730). This suite never opens an Agents page.
/// The stock mock has only four machines; MOCK_MACHINES adds edge cases, not a long grid. Its current
/// 24h endpoint samples every 240 seconds (~361 points), not the production-shaped mixed-resolution
/// history described in the original map. Larger grids and dense histories remain parent-owned fixtures.
final class PerfMachinesTests: PerfTestCase {
    /// Keep the inherited Settings -> Machines route and launch through PerfTestCase.
    @MainActor private func openMachines(_ extra: [String] = []) {
        launch(["-tab", "settings", "-dev.openChat", "", "-openMachines", "YES"] + extra)
    }

    @MainActor private func serverCard(in scroller: XCUIElement) -> XCUIElement {
        scroller.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "服务器、")).firstMatch
    }

    @MainActor private func machineGrid() -> XCUIElement {
        see(app.navigationBars["机器"], "the Machines page", timeout: 20)
        // Scope the scroller to a machine card: retained shell pages can have other large scrollers.
        let grid = see(app.scrollViews.containing(NSPredicate(format: "label BEGINSWITH %@", "服务器、")).firstMatch,
                       "the machine grid", timeout: 20)
        reveal(serverCard(in: grid), in: grid)
        return grid
    }

    @MainActor private func openDetail(range: String = "24h") -> XCUIElement {
        openMachines(["-openMachineDetail", "server", "-openMonitorRange", range])
        see(app.navigationBars["服务器"], "the server detail page", timeout: 20)
        see(any("metric-range"), "the detail range selector", timeout: 20)
        let detail = see(app.scrollViews.containing(.any, identifier: "metric-range").firstMatch,
                         "the detail scroller")
        see(any("machine.engine"), "real machine detail content")
        let title = range == "1h" ? "1 小时" : "24 小时"
        XCTAssertTrue(any("metric-range").buttons[title].isSelected, "the requested initial range must be selected")
        seeCPUData()
        sleep(2)
        return detail
    }

    /// Both the current reading and the history peak must be numeric, not an empty/loading chart.
    @MainActor private func seeCPUData() {
        let cpu = see(any("chart-CPU"), "the loaded CPU chart", timeout: 20)
        let summary = cpu.value as? String ?? ""
        XCTAssertGreaterThanOrEqual(summary.filter { $0 == "%" }.count, 2,
                                    "the CPU chart needs a current reading and a history peak: \(summary)")
    }

    @MainActor private func fullyVisible(_ element: XCUIElement, in scroller: XCUIElement) -> Bool {
        guard element.exists, element.isHittable else { return false }
        let viewport = scroller.frame.intersection(app.windows.firstMatch.frame).insetBy(dx: 0, dy: 32)
        let frame = element.frame
        return frame.height > 0 && frame.minY >= viewport.minY && frame.maxY <= viewport.maxY
    }

    /// Bounded, small drags avoid flinging past a chart. Failure never turns an interaction into a pass.
    @MainActor private func reveal(_ element: XCUIElement, in scroller: XCUIElement) {
        see(element, "the scroll target")
        for _ in 0..<16 where !fullyVisible(element, in: scroller) {
            let upwards = element.frame.midY > scroller.frame.midY
            let from = scroller.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: upwards ? 0.75 : 0.35))
            let to = scroller.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: upwards ? 0.45 : 0.65))
            from.press(forDuration: 0.05, thenDragTo: to)
        }
        XCTAssertTrue(fullyVisible(element, in: scroller), "the scroll target must be fully visible before interaction")
    }

    /// A ScrollView alone is not scroll coverage. Check settled content movement, outside frame capture,
    /// then restore the anchor before measuring. A short fixture fails rather than reporting bounce as scrolling.
    @MainActor private func proveScrollable(_ scroller: XCUIElement, anchor: XCUIElement) {
        reveal(anchor, in: scroller)
        let before = anchor.frame.minY
        swipe(scroller, .up)
        sleep(1)
        XCTAssertTrue(!anchor.exists || anchor.frame.minY < before - 24,
                      "no real scroll displacement; provide a taller machine fixture, not an empty or short scroller")
        for _ in 0..<8 where !anchor.exists || anchor.frame.minY < before - 12 {
            swipe(scroller, .down)
        }
        sleep(2)
        reveal(anchor, in: scroller)
    }

    private func hits(_ path: String) -> Int {
        (mock("hits")["hits"] as? [String: Int])?["GET " + path] ?? 0
    }

    /// Wait only for this iteration's finite batch; never leave the inherited 200-round churn running
    /// into the next test's reset. The parent must give this suite its own mock and run it serially.
    private func finishPushBatch(_ rounds: Int) {
        let deadline = Date.now.addingTimeInterval(12)
        var completed = mock("churned")["rounds"] as? Int ?? 0
        while completed < rounds, Date.now < deadline {
            usleep(100_000)
            completed = mock("churned")["rounds"] as? Int ?? 0
        }
        XCTAssertEqual(completed, rounds, "the push batch must finish before the next iteration")
    }

    /// Twelve seconds includes the ten-second metric poll. Checking generation does not prove that
    /// every pushed model update was rendered. No accessibility snapshots are taken during this idle.
    @MainActor private func measurePushIdle() {
        measureFrames(metrics()) {
            XCTAssertEqual(mock("churn?n=30&ms=300&steps=0")["ok"] as? Bool, true, "the push batch must start")
            sleep(12)
            finishPushBatch(30)
        }
    }

    /// A real grid glide, measured without accessibility snapshots. Push load has its own idle test.
    @MainActor func testMachinesScroll() throws {
        openMachines()
        let grid = machineGrid()
        proveScrollable(grid, anchor: serverCard(in: grid))
        measureVerifiedGlide()
        see(app.navigationBars["机器"], "Machines after scrolling")
    }

    /// Missing coverage: the machine grid sitting still through pushes and a metric poll.
    @MainActor func testMachinesListIdleUnderPush() throws {
        openMachines()
        _ = machineGrid()
        let before = hits("/api/v1/metrics")
        measurePushIdle()
        XCTAssertGreaterThan(hits("/api/v1/metrics"), before, "the machine overview must poll while in front")
        see(app.navigationBars["机器"], "Machines after the push batches")
    }

    /// Inherited machine detail scroll, with chart/system data and real scroll content asserted first.
    @MainActor func testMachineDetailScroll() throws {
        let detail = openDetail()
        for id in ["chart-CPU", "chart-内存", "chart-下行", "chart-负载"] {
            see(any(id), "loaded detail chart \(id)")
        }
        see(app.staticTexts["AMD EPYC 9354 32-Core Processor"], "the server's system facts")
        proveScrollable(detail, anchor: any("machine.engine"))
        measureVerifiedGlide()
        see(app.navigationBars["服务器"], "the server detail after scrolling")
    }

    /// Missing coverage: pushes while the visible CPU chart receives the ten-second 1h metric poll.
    @MainActor func testMachineDetailIdleUnderPush() throws {
        let detail = openDetail(range: "1h")
        reveal(any("chart-CPU"), in: detail)
        let before = hits("/api/v1/machines/server/metrics")
        measurePushIdle()
        XCTAssertGreaterThan(hits("/api/v1/machines/server/metrics"), before, "detail metrics must poll while in front")
        see(app.navigationBars["服务器"], "the server detail after the push batches")
        seeCPUData()
    }

    /// Inherited 24h CPU drag -> 6h -> 24h round, with visible targets and selected ranges asserted.
    @MainActor func testMachineDetailScrubAndRange() throws {
        let detail = openDetail()
        let chart = any("chart-CPU")
        reveal(chart, in: detail)
        let range = any("metric-range")
        XCTAssertTrue(range.isHittable, "the range selector and CPU chart must be on screen together")
        // The identifier belongs to the plot, not its card/header. Keep both endpoints away from axes.
        let left = chart.coordinate(withNormalizedOffset: CGVector(dx: 0.2, dy: 0.5))
        let right = chart.coordinate(withNormalizedOffset: CGVector(dx: 0.75, dy: 0.5))
        let before = hits("/api/v1/machines/server/metrics")
        measureFrames(metrics()) {
            left.press(forDuration: 0.3, thenDragTo: right)
            for title in ["6 小时", "24 小时"] {
                let choice = range.buttons[title]
                XCTAssertTrue(choice.isHittable, "the range choice must be tappable: \(title)")
                choice.tap()
                XCTAssertTrue(choice.isSelected, "the range tap must change selection: \(title)")
                sleep(2)
            }
        }
        XCTAssertGreaterThan(hits("/api/v1/machines/server/metrics"), before, "range changes must request metrics")
        see(app.navigationBars["服务器"], "the detail page after chart interaction")
        seeCPUData()
        // The existing summary exposes current/peak values, not selection. This measures real input on
        // a loaded, visible plot, but does not assert a selected sample or synchronized cross-chart tooltips.
    }

    /// Inherited card -> detail -> Back round, with entered/returned screens checked in every iteration.
    @MainActor func testMachineDetailPushPop() throws {
        openMachines(["-openMonitorRange", "24h"])
        let grid = machineGrid()
        let card = serverCard(in: grid)
        XCTAssertTrue(card.isHittable, "the server card must be tappable before navigation")
        measureTransition {
            card.tap()
            see(app.navigationBars["服务器"], "entering the server detail", timeout: 20)
            see(any("metric-range"), "the pushed detail's range selector", timeout: 20)
            XCTAssertTrue(any("metric-range").buttons["24 小时"].isSelected, "the pushed detail must use 24h")
            seeCPUData()
            sleep(2)
            back()
            see(app.navigationBars["机器"], "returning to Machines", timeout: 20)
            XCTAssertFalse(app.navigationBars["服务器"].exists, "Back must leave the detail screen")
            XCTAssertTrue(card.isHittable, "the same machine card must be ready for the next iteration")
            sleep(1)
        }
    }
}
