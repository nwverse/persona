import XCTest

/// Place maps in the main conversation (#5543): "where am I" and a nearby search each draw a map card under the turn's
/// work line, with the tool calls still folded; tapping a map opens the full map with the same places, and Done closes
/// it. A route (#5544) draws a route card with Start in Maps, and its map opens the same way. Against scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_PLACE_MAP=1 MOCK_TASKS_NO_ASKS=1 (PERSONA_MOCK_HUB).
/// PERSONA_SHOTS saves the cards and the sheet there (PERSONA_SHOTS_APPEARANCE names the simulator's appearance);
/// PERSONA_SHOTS_LARGE=1 runs at the largest accessibility text size.
final class PlaceMapUITests: XCTestCase {
    private var hub = ""
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_PLACE_MAP=1 MOCK_TASKS_NO_ASKS=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = false
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        let size = env["PERSONA_SHOTS_LARGE"] == "1" ? "-large" : ""
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/place-map-\(appearance)\(size)-\(name).png"))
    }

    /// A tenth of a screen toward older or newer lines: a swipe flies past a card at large text sizes.
    @MainActor private func nudge(_ app: XCUIApplication, older: Bool) {
        let top = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.45))
        let bottom = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.55))
        if older { top.press(forDuration: 0.05, thenDragTo: bottom) } else { bottom.press(forDuration: 0.05, thenDragTo: top) }
    }

    /// The map is on screen below the floating header, where a tap reaches it.
    @MainActor private func clear(_ map: XCUIElement, in app: XCUIApplication) -> Bool {
        map.exists && map.isHittable && map.frame.minY > app.frame.height * 0.15 && map.frame.maxY < app.frame.height * 0.8
    }

    @MainActor func testPlaceMapsShowAndOpenTheFullMap() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main]
        if ProcessInfo.processInfo.environment["PERSONA_SHOTS_LARGE"] == "1" {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        }
        app.launchPinned()

        let any = app.descendants(matching: .any)
        let sheet = any["place-map-sheet.map"].firstMatch
        let done = app.buttons.matching(NSPredicate(format: "label IN %@", ["完成", "Done"])).firstMatch

        let route = any.matching(NSPredicate(format: "identifier BEGINSWITH %@", "chat.route-map.")).firstMatch
        XCTAssertTrue(route.waitForExistence(timeout: 20), "no route card:\n\(app.debugDescription)")
        for _ in 0..<40 where !clear(route, in: app) { nudge(app, older: true) }
        XCTAssertTrue(any["route-map.start"].firstMatch.exists, "the route card has no Start in Maps")
        sleep(2)
        try shot(app, "route")
        route.tap()
        let routeSheet = any["route-map-sheet.map"].firstMatch
        XCTAssertTrue(routeSheet.waitForExistence(timeout: 5), "tapping the route did not open its map")
        sleep(2)
        try shot(app, "route-sheet")
        done.tap()
        let routeGone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: routeSheet)
        XCTAssertEqual(XCTWaiter.wait(for: [routeGone], timeout: 5), .completed, "Done did not close the route's map")

        let maps = any.matching(NSPredicate(format: "identifier BEGINSWITH %@", "chat.place-map."))
        let nearby = maps.matching(NSPredicate(format: "label CONTAINS %@", "街角咖啡")).firstMatch
        XCTAssertTrue(nearby.waitForExistence(timeout: 20), "no map for the nearby search:\n\(app.debugDescription)")
        for _ in 0..<40 where !nearby.isHittable { nudge(app, older: true) } // at large text sizes it is above the answer
        sleep(2) // the snapshot comes in
        try shot(app, "nearby")

        let here = maps.matching(NSPredicate(format: "label CONTAINS %@", "人民大道")).firstMatch
        for _ in 0..<60 where !clear(here, in: app) { nudge(app, older: true) }
        XCTAssertTrue(here.exists, "no map for where the phone is")
        sleep(2)
        try shot(app, "here")

        here.tap()
        XCTAssertTrue(sheet.waitForExistence(timeout: 5), "tapping the map did not open the full map")
        sleep(2)
        try shot(app, "here-sheet")
        XCTAssertTrue(done.exists, "the full map has no Done")
        done.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: sheet)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 5), .completed, "Done did not close the full map")

        for _ in 0..<60 where !clear(nearby, in: app) { nudge(app, older: false) }
        nearby.tap()
        XCTAssertTrue(sheet.waitForExistence(timeout: 5), "the nearby map did not open")
        for name in ["街角咖啡", "安静书房咖啡", "老弄堂咖啡馆", "河边烘焙咖啡"] {
            XCTAssertTrue(any.matching(NSPredicate(format: "label CONTAINS %@", name)).firstMatch.exists, "the full map lacks \(name)")
        }
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "河边烘焙咖啡")).firstMatch.exists,
                      "the fourth place has no Directions in the full map")
        sleep(2)
        try shot(app, "nearby-sheet")
        app.terminate()
    }
}
