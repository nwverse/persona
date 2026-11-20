import XCTest

/// The away recap shown open is the chat's last line, in the flow with the agent's words (#1360): at the newest end it
/// sits whole between the reply and the composer, covering neither; scrolled up, it goes with the messages; a tap folds
/// it back to its one-line chip in place. Against scripts/mock/mock_hub.py with MOCK_RECAP_LONG=1 (PERSONA_MOCK_HUB).
/// With PERSONA_SHOTS set it writes the open and the folded recap, at the newest end and scrolled up, there
/// (PERSONA_SHOTS_APPEARANCE names the simulator's appearance in the file).
final class RecapInFlowTests: XCTestCase {
    private var hub = ""
    private let review = "macbook/a1f3c2d4-0000-4000-8000-000000000006"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_RECAP_LONG=1 (PERSONA_MOCK_HUB)")
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
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/recap-\(appearance)-\(name).png"))
    }

    @MainActor private func composer(_ app: XCUIApplication) -> XCUIElement {
        let placeholder = NSPredicate(format: "placeholderValue == %@", "今天我们做点什么呢~")
        let view = app.textViews.matching(placeholder).firstMatch
        return view.exists ? view : app.textFields.matching(placeholder).firstMatch
    }

    @MainActor private func onScreen(_ app: XCUIApplication, _ e: XCUIElement) -> Bool {
        e.exists && !e.frame.isEmpty && app.windows.firstMatch.frame.intersects(e.frame) && e.isHittable
    }

    @MainActor func testTheOpenRecapScrollsWithTheChatAndFoldsInPlace() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", review, "-openChatsFresh", "YES", "-openRecapsFresh", "YES"]
        app.launchPinned()

        let any = app.descendants(matching: .any)
        let strip = any["chat.recapStrip"].firstMatch
        XCTAssertTrue(strip.waitForExistence(timeout: 20), "no open recap:\n\(app.debugDescription)")
        sleep(2) // the list settles at the newest
        let field = composer(app)
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no composer")
        let reply = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "PR #42 看完了")).firstMatch
        XCTAssertTrue(reply.exists, "no reply before the recap")

        // At the newest end: reply, then the recap whole, then the composer, nothing on top of anything.
        XCTAssertTrue(onScreen(app, strip), "the open recap is not whole on screen at the newest end")
        XCTAssertLessThanOrEqual(reply.frame.maxY, strip.frame.minY + 1, "the recap covers the reply")
        XCTAssertLessThanOrEqual(strip.frame.maxY, field.frame.minY, "the recap runs under the composer")
        try shot(app, "open-bottom")

        // Scrolled up: it goes with the messages; the composer stays.
        let fieldY = field.frame.minY
        for _ in 0..<3 { app.swipeDown(velocity: .fast) }
        sleep(1)
        XCTAssertFalse(onScreen(app, strip), "the recap stayed on screen as the chat scrolled up")
        XCTAssertEqual(field.frame.minY, fieldY, accuracy: 1, "the composer moved")
        try shot(app, "open-scrolled")

        // Back down: it comes back with the reply.
        for _ in 0..<4 { app.swipeUp(velocity: .fast) }
        sleep(1)
        XCTAssertTrue(onScreen(app, strip), "the recap did not come back with the reply")
        XCTAssertLessThanOrEqual(strip.frame.maxY, field.frame.minY, "back at the newest, the recap runs under the composer")

        // A tap folds it to the one-line chip, in the same place.
        strip.tap()
        let gone = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: strip)
        XCTAssertEqual(XCTWaiter.wait(for: [gone], timeout: 5), .completed, "a tap did not close the recap")
        let chip = app.buttons.matching(NSPredicate(format: "label == %@", "离开期间回顾，点击查看")).firstMatch
        XCTAssertTrue(chip.waitForExistence(timeout: 5), "no recap chip after closing")
        sleep(1)
        XCTAssertLessThanOrEqual(reply.frame.maxY, chip.frame.minY + 1, "the chip covers the reply")
        XCTAssertLessThanOrEqual(chip.frame.maxY, field.frame.minY, "the chip runs under the composer")
        try shot(app, "folded-bottom")
        for _ in 0..<3 { app.swipeDown(velocity: .fast) }
        sleep(1)
        try shot(app, "folded-scrolled")

        // Closed stays closed when the chat is opened again.
        app.terminate()
        app.launchArguments = ["-pairURL", pair, "-openSession", review]
        app.launchPinned()
        XCTAssertTrue(chip.waitForExistence(timeout: 20), "no recap chip on reopening")
        XCTAssertFalse(strip.exists, "a closed recap opened again")
        app.terminate()
    }
}
