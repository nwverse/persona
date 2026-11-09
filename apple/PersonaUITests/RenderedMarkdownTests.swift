import XCTest

/// Formulas (KaTeX) and diagrams (Mermaid) in Markdown (#1295): a reply in the chat and a Markdown file draw them as
/// pictures; a formula KaTeX cannot read shows its source.
///   PORT=18785 MOCK_AGENTS=1 MOCK_RENDER=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18785 xcodebuild test … -only-testing:PersonaUITests/RenderedMarkdownTests
/// Both styles (TEST_RUNNER_PERSONA_UI_STYLE=beautiful).
final class RenderedMarkdownTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] != nil else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_RENDER=1 (PERSONA_MOCK_HUB)")
        }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "render \(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor private func launch(_ extra: [String], session: String = "a1f3c2d4-0000-4000-8000-000000000001") {
        let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"]!
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/" + session] + extra
        app.launchPinned()
    }

    @MainActor func testChatDrawsFormulasAndDiagram() {
        launch([], session: "a1f3c2d4-0000-4000-8000-000000000008") // 「整理发布说明」: its last turn
        let diagram = app.descendants(matching: .any)["rendered-mermaid"]
        let drawn = diagram.waitForExistence(timeout: 30)
        shot("chat-first")
        XCTAssertTrue(drawn, "the diagram was not drawn")
        XCTAssertTrue(app.descendants(matching: .any)["rendered-math"].waitForExistence(timeout: 10), "the formula was not drawn")
        sleep(2)
        shot("chat")
        diagram.tap()
        sleep(2)
        shot("diagram")
    }

    @MainActor func testMarkdownFileDrawsThem() {
        launch(["-openFilePath", "docs/算法.md"])
        XCTAssertTrue(app.descendants(matching: .any)["rendered-math"].waitForExistence(timeout: 30), "the formula was not drawn")
        sleep(2)
        shot("file")
        app.swipeUp()
        XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "rendered-mermaid").element(boundBy: 1).waitForExistence(timeout: 20),
                      "the sequence diagram was not drawn")
        sleep(2)
        shot("file-diagram")
    }
}
