import XCTest

/// Walking navigation inside Persona (#6672) on the sample route (`-showWalkNavigation`, DEBUG): the next turn on top,
/// time and distance left with Mute, Back to Chat and End at the bottom; arrived, Done. No hub, location or voice.
/// PERSONA_SHOTS saves the screens there (PERSONA_SHOTS_APPEARANCE names the simulator's appearance);
/// PERSONA_SHOTS_LARGE=1 runs at the largest accessibility text size.
final class WalkNavigationUITests: XCTestCase {
    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    @MainActor private func launch(_ at: String) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-showWalkNavigation", at]
        if ProcessInfo.processInfo.environment["PERSONA_SHOTS_LARGE"] == "1" {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        }
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        let size = env["PERSONA_SHOTS_LARGE"] == "1" ? "-large" : ""
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/walk-nav-\(appearance)\(size)-\(name).png"))
    }

    @MainActor func testAWalkShowsTheNextTurnMutesAndGoesBackToTheChat() throws {
        let app = launch("700")
        let any = app.descendants(matching: .any)
        let banner = any["walk-nav.banner"].firstMatch
        XCTAssertTrue(banner.waitForExistence(timeout: 20), "no navigation screen:\n\(app.debugDescription)")
        // 700 m along the sample walk: the right turn onto Xizang Road is next, about 30 m ahead, then a slight right.
        XCTAssertTrue(banner.label.contains("西藏中路"), "the banner does not show the next turn: \(banner.label)")
        XCTAssertTrue(any["walk-nav.left"].firstMatch.exists, "no time and distance left")
        sleep(2) // the map's tiles
        try shot(app, "guiding")

        let mute = app.buttons["walk-nav.mute"].firstMatch
        let before = mute.label
        mute.tap()
        XCTAssertNotEqual(app.buttons["walk-nav.mute"].firstMatch.label, before, "Mute did not change")
        app.buttons["walk-nav.mute"].firstMatch.tap() // back as it was, for the next run

        app.buttons["walk-nav.back"].firstMatch.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: banner)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 5), .completed, "Back to Chat did not close the screen")
        app.terminate()
    }

    @MainActor func testArrivingSaysSoAndDoneCloses() throws {
        let app = launch("end")
        let done = app.buttons["walk-nav.done"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 20), "no Done after arriving:\n\(app.debugDescription)")
        sleep(2)
        try shot(app, "arrived")
        done.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: done)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 5), .completed, "Done did not close the screen")
        app.terminate()
    }

    @MainActor func testEndClosesTheWalk() throws {
        let app = launch("0")
        let end = app.buttons["walk-nav.end"].firstMatch
        XCTAssertTrue(end.waitForExistence(timeout: 20), "no End:\n\(app.debugDescription)")
        sleep(2)
        try shot(app, "start")
        end.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: end)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 5), .completed, "End did not close the screen")
        app.terminate()
    }
}
