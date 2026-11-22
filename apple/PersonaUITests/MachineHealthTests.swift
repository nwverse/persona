import XCTest

/// What This Computer Can Do on a machine's page (#3018, docs/server/machines.md「Doctor: what this computer can do」), against scripts/mock/mock_hub.py started with
/// MOCK_DOCTOR=stale (hooks and MCP files out of date; Repair puts them right):
///   TEST_RUNNER_PERSONA_DOCTOR_HUB=http://127.0.0.1:<port>
/// TEST_RUNNER_PERSONA_SHOTS=<dir> also saves a screenshot of each step there.
final class MachineHealthTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_DOCTOR_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_DOCTOR=stale (PERSONA_DOCTOR_HUB)")
        }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook"]
        app.launchPinned()
    }

    private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + look + ".png"))
        }
    }

    /// Scrolls the page until the element is on screen (the section sits below the agents).
    private func reveal(_ e: XCUIElement) {
        for _ in 0..<8 where !(e.exists && e.isHittable) { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(e.waitForExistence(timeout: 5) && e.isHittable)
    }

    func testStaleFilesAreFixedAfterAConfirmation() {
        let summary = app.descendants(matching: .any)["machine.health.summary"]
        XCTAssertTrue(summary.waitForExistence(timeout: 40), "the self-check never came")
        let fix = app.buttons["machine.health.fix"]
        reveal(fix)
        XCTAssertTrue(summary.label.contains("需要处理"), summary.label)
        shot("health-stale")

        fix.tap()
        let confirm = app.buttons.matching(NSPredicate(format: "label == %@ AND identifier != %@", "修复", "machine.health.fix")).firstMatch
        XCTAssertTrue(app.staticTexts["重新生成 Persona 的文件"].waitForExistence(timeout: 5))
        shot("health-confirm")
        confirm.tap()

        let deadline = Date.now.addingTimeInterval(15)
        while !summary.label.contains("一切正常") && Date.now < deadline { RunLoop.current.run(until: .now + 0.3) }
        XCTAssertTrue(summary.label.contains("一切正常"), summary.label)
        XCTAssertFalse(app.buttons["machine.health.fix"].exists)

        let all = app.buttons["machine.health.all"]
        reveal(all)
        all.tap()
        reveal(app.descendants(matching: .any)["machine.health.check.node.sock"])
        shot("health-all")
    }
}
