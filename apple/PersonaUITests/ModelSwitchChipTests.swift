import XCTest

/// The Shell composer changes a Claude pane's model by sending a /model command, not by writing its mode settings.
/// The mock hub records the exact destination and payload at /mock/sent_to.
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
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let directory = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(directory)/model-chip-\(appearance)-\(name).png"))
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

    private func sends() async throws -> [[String: String]] {
        let (data, _) = try await URLSession.shared.data(from: URL(string: hub + "/mock/sent_to")!)
        let value = try JSONSerialization.jsonObject(with: data) as? [String: Any]
        return (value?["sent"] as? [[String: Any]] ?? []).map { $0.compactMapValues { $0 as? String } }
    }

    @MainActor private func checkModelCommand(_ session: String, name: String) async throws {
        let app = open(session)
        defer { app.terminate() }
        let before = try await sends().count
        let menu = app.buttons["shell-composer-modes"].firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 20), "no composer model menu:\n\(app.debugDescription)")
        menu.tap()
        let sonnet = app.buttons["Sonnet"].firstMatch
        XCTAssertTrue(sonnet.waitForExistence(timeout: 5), "the menu lists no models")
        XCTAssertTrue(sonnet.isEnabled, "the Shell command picker must remain available")
        XCTAssertTrue(app.buttons["高"].exists, "the effort levels are gone from the menu")
        try shot(app, name)
        sonnet.tap()

        let parts = session.split(separator: "/").map(String.init)
        let expected = ["machine_id": parts[0], "session_id": parts[1], "text": "/model sonnet"]
        var received: [[String: String]] = []
        for _ in 0..<75 {
            received = Array(try await sends().dropFirst(before))
            if received.contains(where: { record in expected.allSatisfy { record[$0.key] == $0.value } }) { break }
            try await Task.sleep(for: .milliseconds(200))
        }
        XCTAssertTrue(received.contains(where: { record in expected.allSatisfy { record[$0.key] == $0.value } }),
                      "the model command did not reach its conversation: \(received)")
    }

    @MainActor func testTheComposersModelMenuSendsTheModelCommand() async throws {
        try await checkModelCommand(idle, name: "idle")
    }

    @MainActor func testAWorkingAgentUsesTheModelCommand() async throws {
        try await checkModelCommand(working, name: "working")
    }
}
