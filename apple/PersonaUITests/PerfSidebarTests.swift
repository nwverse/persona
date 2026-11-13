import XCTest

/// The one sidebar coming and going beside the page (#4206), on the iPad held on its side: the column slides at its one
/// width, so its rows are not laid out again on every frame. Frames come from the app's `FrameStats` (`PerfTestCase`);
/// run with an optimized build against a mock with many agents (`MOCK_AGENTS=1`), as `scripts/app/perf-test.sh` does.
final class PerfSidebarTests: PerfTestCase {
    override func setUpWithError() throws {
        try super.setUpWithError()
        XCUIDevice.shared.orientation = .landscapeLeft
    }

    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    /// Hide Sidebar, then Show Sidebar, three times.
    @MainActor func testSidebarHideAndShow() throws {
        let app = launch(["-tab", "assistant", "-app.tab", "assistant", "-dev.openChat", "", "-openSplitFresh", "YES"],
                         ready: XCUIApplication().descendants(matching: .any)["sidebar-main"].firstMatch)
        guard app.windows.firstMatch.frame.width > 900 else { throw XCTSkip("the iPad on its side only") }
        let hide = app.descendants(matching: .any)["assistant-sidebar-collapse"].firstMatch
        let show = app.buttons.matching(NSPredicate(format: "identifier == %@ OR label IN %@", "assistant-sidebar-button",
                                                    ["显示边栏", "Show Sidebar"])).firstMatch
        let main = app.descendants(matching: .any)["sidebar-main"].firstMatch
        measureFrames(metrics()) {
            for _ in 0..<3 {
                hide.tap()
                XCTAssertTrue(main.until("exists == false OR isHittable == false"), "the column did not go")
                show.tap()
                XCTAssertTrue(main.until("exists == true AND isHittable == true"), "the column did not come back")
            }
        }
    }
}

private extension XCUIElement {
    func until(_ format: String, timeout: TimeInterval = 5) -> Bool {
        XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: format), object: self)], timeout: timeout) == .completed
    }
}
