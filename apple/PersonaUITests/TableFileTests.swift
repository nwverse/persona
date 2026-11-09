import XCTest

/// CSV and xlsx open in the table viewer (#1294): the first row and the row numbers stay put while scrolling, a
/// tapped cell shows whole underneath, a workbook's sheets switch, and the ⋯ menu still has the source / QuickLook.
///   PORT=18785 MOCK_AGENTS=1 MOCK_OFFICE=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18785 xcodebuild test … -only-testing:PersonaUITests/TableFileTests
/// Both styles (TEST_RUNNER_PERSONA_UI_STYLE=beautiful).
final class TableFileTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] != nil else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_OFFICE=1 (PERSONA_MOCK_HUB)")
        }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "table \(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor private func open(_ file: String) {
        let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"]!
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001", "-openFilePath", file]
        app.launchPinned()
        XCTAssertTrue(app.descendants(matching: .any)["file-table"].waitForExistence(timeout: 25), "\(file) did not open as a table")
    }

    @MainActor private func cell(_ label: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", label + " ")).firstMatch
    }

    @MainActor func testCSVScrollsWithFrozenHeader() {
        open("docs/城市观测.csv")
        XCTAssertTrue(cell("A1").waitForExistence(timeout: 10), "no header row")
        sleep(1)
        shot("csv")
        cell("B3").tap()
        XCTAssertTrue(app.staticTexts["B3"].waitForExistence(timeout: 5), "the tapped cell is not shown underneath")
        shot("csv-selected")
        let grid = app.descendants(matching: .any)["file-table"]
        XCTAssertTrue(grid.exists, "no table grid")
        grid.swipeUp(velocity: .fast)
        grid.swipeLeft()
        sleep(2)
        XCTAssertFalse(cell("A2").isHittable && cell("B2").isHittable, "the table did not scroll")
        // A row-1 cell on screen, at the top of the table: the header stayed while the rows went by.
        let screen = app.windows.firstMatch.frame
        let headers = app.descendants(matching: .any).matching(NSPredicate(format: "label MATCHES %@", "^[A-Z]+1 .*")).allElementsBoundByIndex
        XCTAssertTrue(headers.contains { $0.frame.intersects(screen) && $0.frame.minY < screen.height / 4 }, "the header scrolled away")
        shot("csv-scrolled")
        app.buttons["更多"].firstMatch.tap()
        let source = app.buttons["查看源文件"]
        XCTAssertTrue(source.waitForExistence(timeout: 5), "no 查看源文件 for a CSV")
        source.tap()
        XCTAssertTrue(app.descendants(matching: .any)["file-code"].waitForExistence(timeout: 10), "the source did not show")
    }

    @MainActor func testWorkbookSheets() {
        open("docs/逐小时预报.xlsx")
        XCTAssertTrue(cell("A1").waitForExistence(timeout: 10), "no header row")
        sleep(1)
        shot("xlsx")
        app.buttons["城市"].firstMatch.tap()
        XCTAssertTrue(cell("E2").waitForExistence(timeout: 5), "the second sheet did not show")
        cell("E2").tap()
        XCTAssertTrue(app.staticTexts["2026-10-31"].waitForExistence(timeout: 5), "the date cell is not a date")
        shot("xlsx-sheet2")
        app.buttons["更多"].firstMatch.tap()
        let ql = app.buttons["用快速查看打开"]
        XCTAssertTrue(ql.waitForExistence(timeout: 5), "no QuickLook for an xlsx")
        ql.tap()
        XCTAssertTrue(app.descendants(matching: .any)["file-quicklook"].waitForExistence(timeout: 20), "QuickLook did not open")
    }
}
