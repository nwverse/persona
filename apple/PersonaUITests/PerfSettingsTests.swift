import XCTest

/// Settings and what it pushes: Extensions, the extension market, Persona's settings, scheduled tasks (#5730).
/// Mock (`scripts/app/perf-test.sh`, suite `settings`): MOCK_EXTENSIONS=1 MOCK_SCHEDULE=1 MOCK_AGENTS=1.
final class PerfSettingsTests: PerfTestCase {
    @MainActor private func openSettings(_ extra: [String] = []) {
        launch(["-dev.openChat", "", "-tab", "settings"] + extra)
        sleep(1)
    }

    /// Settings scrolled.
    @MainActor func testSettingsScroll() throws {
        openSettings()
        see(any("settings-list"), "Settings", timeout: 20)
        measureScroll(mainScroller(), flicks: 2)
    }

    @MainActor private func settingsRow(_ id: String, direction: Direction) -> XCUIElement {
        let list = see(any("settings-list"), "the Settings list", timeout: 20)
        let row = app.buttons[id].firstMatch
        for _ in 0..<10 {
            let viewport = list.frame.intersection(app.windows.firstMatch.frame).insetBy(dx: 0, dy: 24)
            if row.exists, row.isHittable, row.frame.minY >= viewport.minY, row.frame.maxY <= viewport.maxY { return row }
            let move = row.exists ? (row.frame.midY > viewport.midY ? Direction.up : .down) : direction
            let from = list.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: move == .up ? 0.75 : 0.35))
            let to = list.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: move == .up ? 0.45 : 0.65))
            from.press(forDuration: 0.05, thenDragTo: to)
        }
        XCTFail("Settings row did not become visible: \(id)")
        return row
    }

    /// Settings → Extensions → back, then Settings → Persona → back.
    @MainActor func testSettingsPushChain() throws {
        openSettings()
        _ = settingsRow("settings-plugins", direction: .up)
        measureTransition {
            let plugins = settingsRow("settings-plugins", direction: .up)
            plugins.tap()
            wait(any("extensions-list"))
            sleep(1)
            back()
            wait(plugins)
            let assistant = settingsRow("settings-assistant", direction: .down)
            assistant.tap()
            sleep(2)
            back()
            _ = settingsRow("settings-plugins", direction: .up)
            sleep(1)
        }
    }

    /// The extensions list scrolled.
    @MainActor func testExtensionsListScroll() throws {
        openSettings(["-openExtensions", "YES"])
        see(any("extensions-list"), "the extensions", timeout: 20)
        measureScroll(mainScroller(), flicks: 2)
    }

    /// Extensions → the market → back.
    @MainActor func testExtensionMarketPushPop() throws {
        openSettings(["-openExtensions", "YES"])
        let market = see(app.buttons["plugins-market"].firstMatch, "the market entry", timeout: 20)
        measureTransition {
            market.tap()
            wait(app.buttons["market-filter-all"])
            sleep(2)
            back()
            wait(market)
            sleep(1)
        }
    }

    /// The market's catalog scrolled.
    @MainActor func testExtensionMarketScroll() throws {
        openSettings(["-openExtensionMarket", "YES"])
        see(app.buttons["market-filter-all"], "the market", timeout: 20)
        sleep(2)
        measureScroll(mainScroller(), flicks: 2)
    }

    /// Typing into the market's search (the catalog reloaded from the hub after each pause).
    @MainActor func testExtensionMarketSearchTyping() throws {
        openSettings(["-openExtensionMarket", "YES"])
        see(app.buttons["market-filter-all"], "the market", timeout: 20)
        let search = app.searchFields.firstMatch
        if !search.waitForExistence(timeout: 3) { mainScroller().swipeDown() }
        measureTyping(see(search, "the market's search"), text: "example gateway")
    }

    /// An extension's detail page (its settings form) scrolled.
    @MainActor func testExtensionDetailScroll() throws {
        openSettings(["-openExtension", "example-gateway"])
        sleep(3)
        measureScroll(mainScroller(), flicks: 2)
    }

    /// Scheduled tasks scrolled.
    @MainActor func testRoutinesScroll() throws {
        launch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant"]
               + ["-openShellPage", "routines"])
        sleep(3)
        measureScroll(mainScroller(), flicks: 2)
    }

    /// The run history scrolled.
    @MainActor func testRunsScroll() throws {
        launch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant", "-openAssistantPage", "runs"]
               + ["-openShellPage", "profile"])
        sleep(3)
        measureScroll(mainScroller(), flicks: 2)
    }
}
