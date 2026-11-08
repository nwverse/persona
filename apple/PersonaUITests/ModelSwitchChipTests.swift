import XCTest

/// Switching models in the composer's model menu (#3119): the menu lists Claude Code's models; picking
/// one posts `…/modes {"model": …}`. Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB), which logs the requests at
/// /mock/modes. With PERSONA_SHOTS set it writes the open menu there (PERSONA_SHOTS_APPEARANCE names the appearance).
final class ModelSwitchChipTests: XCTestCase {
    private var hub = ""
    private let idle = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    private let working = "macbook/a1f3c2d4-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
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
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/model-chip-\(appearance)-\(name).png"))
    }

    @MainActor private func open(_ session: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", session]
        app.launchPinned()
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
        field.tapCenter()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard")
        return app
    }

    private func modesLog() throws -> [[String: String]] {
        let data = try Data(contentsOf: URL(string: hub + "/mock/modes")!)
        let obj = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        return (obj?["modes"] as? [[String: Any]] ?? []).map { $0.compactMapValues { $0 as? String } }
    }

    @MainActor func testTheEffortChipSwitchesTheModel() throws {
        let app = open(idle)
        let chip = app.buttons["shell-composer-modes"].firstMatch
        XCTAssertTrue(chip.waitForExistence(timeout: 20), "no effort chip with the model:\n\(app.debugDescription)")
        sleep(1)
        chip.tap()
        let sonnet = app.buttons["Sonnet"].firstMatch
        XCTAssertTrue(sonnet.waitForExistence(timeout: 5), "the chip's menu lists no models:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons["高"].exists, "the effort levels are gone from the chip")
        try shot(app, "open")
        sonnet.tap()
        let id = "a1f3c2d4-0000-4000-8000-000000000006"
        var log: [[String: String]] = []
        for _ in 0..<75 where !log.contains(where: { $0["model"] == "sonnet" }) {
            Thread.sleep(forTimeInterval: 0.2)
            log = try modesLog().filter { $0["session_id"] == id }
        }
        XCTAssertEqual(log.last, ["session_id": id, "model": "sonnet"], "no modes {model: sonnet}, or more than the model was sent: \(log)")
        app.terminate()
    }

    @MainActor func testAWorkingAgentWaitsToSwitch() throws {
        let app = open(working)
        let chip = app.buttons["shell-composer-modes"].firstMatch
        XCTAssertTrue(chip.waitForExistence(timeout: 20), "no effort chip with the model:\n\(app.debugDescription)")
        sleep(1)
        chip.tap()
        let sonnet = app.buttons["Sonnet"].firstMatch
        XCTAssertTrue(sonnet.waitForExistence(timeout: 5), "the chip's menu lists no models")
        XCTAssertFalse(sonnet.isEnabled, "a working agent's model can be picked")
        try shot(app, "working")
        app.terminate()
    }
}
