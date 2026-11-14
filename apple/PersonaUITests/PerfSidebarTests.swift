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
        let app = launch(["-tab", "assistant", "-app.tab", "assistant", "-dev.openChat", "", "-openSplitFresh", "YES", "-sidebar.mode", "merged"],
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

extension PerfSidebarTests {
    /// The column slid by a finger (#4206): from the line beside it out of the way, then from the screen's edge back,
    /// three times. Only the column's layer moves and the page's blur advances; compare with the width drag this replaced
    /// (28.8 fps, 54 ms of main-thread work per frame at p95 on the same simulator).
    @MainActor func testSidebarDrag() throws {
        let app = launch(["-tab", "assistant", "-app.tab", "assistant", "-dev.openChat", "", "-openSplitFresh", "YES", "-sidebar.mode", "merged"],
                         ready: XCUIApplication().descendants(matching: .any)["sidebar-main"].firstMatch)
        guard app.windows.firstMatch.frame.width > 900 else { throw XCTSkip("the iPad on its side only") }
        let main = app.descendants(matching: .any)["sidebar-main"].firstMatch
        let window = app.windows.firstMatch.frame
        let width = main.frame.maxX + 8
        func drag(from x: CGFloat, by dx: CGFloat) {
            let start = app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: window.minX + x, dy: window.midY))
            start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: dx, dy: 0)), withVelocity: .slow, thenHoldForDuration: 0.05)
        }
        measureFrames(metrics()) {
            for _ in 0..<3 {
                drag(from: width, by: -(width * 0.8))
                XCTAssertTrue(main.until("exists == false OR isHittable == false"), "the column did not slide away")
                drag(from: 2, by: width * 0.8)
                XCTAssertTrue(main.until("exists == true AND isHittable == true"), "the column did not slide back")
            }
        }
    }
}

private extension XCUIElement {
    func until(_ format: String, timeout: TimeInterval = 5) -> Bool {
        XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: format), object: self)], timeout: timeout) == .completed
    }
}
