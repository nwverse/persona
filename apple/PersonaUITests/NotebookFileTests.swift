import XCTest

/// A Jupyter notebook opens as cells with their outputs (#1296), and the ⋯ menu shows its source.
///   PORT=18785 MOCK_AGENTS=1 MOCK_OFFICE=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18785 xcodebuild test … -only-testing:PersonaUITests/NotebookFileTests
/// Both styles (TEST_RUNNER_PERSONA_UI_STYLE=beautiful).
final class NotebookFileTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] != nil else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_OFFICE=1 (PERSONA_MOCK_HUB)")
        }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "notebook \(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testNotebookCellsAndOutputs() {
        let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"]!
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001",
                               "-openFilePath", "docs/分析.ipynb"]
        app.launchPinned()
        XCTAssertTrue(app.descendants(matching: .any)["file-notebook"].waitForExistence(timeout: 25), "the notebook did not open")
        XCTAssertTrue(app.staticTexts["In [1]"].waitForExistence(timeout: 5), "no run number on the first code cell")
        sleep(2)
        shot("top")
        XCTAssertTrue(app.descendants(matching: .any)["notebook-html"].waitForExistence(timeout: 10), "the HTML result is not rendered")
        app.descendants(matching: .any)["file-notebook"].swipeUp()
        XCTAssertTrue(app.staticTexts["KeyError: '风速'"].waitForExistence(timeout: 5), "the error output is missing")
        sleep(1)
        shot("outputs")
        app.buttons["更多"].firstMatch.tap()
        let source = app.buttons["查看源文件"]
        XCTAssertTrue(source.waitForExistence(timeout: 5), "no 查看源文件 for a notebook")
        source.tap()
        XCTAssertTrue(app.descendants(matching: .any)["file-code"].waitForExistence(timeout: 10), "the source did not show")
    }
}
