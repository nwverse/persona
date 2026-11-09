import XCTest

/// The shell's way to the personal agents page on a phone (#140): the Personal Agents pill beside the grouping, under
/// the quick filters. Against scripts/mock/mock_hub.py (TEST_RUNNER_PERSONA_MOCK_HUB); screenshots in PERSONA_SHOTS, with
/// PERSONA_SHOTS_SUFFIX appended to each name.
final class PersonalAgentsEntryTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.lifetime = .keepAlways
            add(a)
        }
        super.tearDown()
    }

    private func shot(_ name: String) {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        try? XCUIScreen.main.screenshot().pngRepresentation
            .write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name)\(env["PERSONA_SHOTS_SUFFIX"] ?? "").png"))
    }

    @MainActor func testThePillOpensPersonalAgents() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-dev.openChat", ""]
        app.launchPinned()
        let pill = app.buttons["agents.personal"]
        XCTAssertTrue(pill.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertTrue(app.buttons["agents.grouping"].exists, "beside the grouping")
        shot("entry")
        pill.tap()
        let title = app.navigationBars.staticTexts.matching(NSPredicate(format: "label == 'Personal Agents' OR label == '个人 Agent'")).firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 10), app.debugDescription)
        shot("page")
    }
}
