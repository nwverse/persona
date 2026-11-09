import XCTest

/// An agent's chat: with the keyboard up, dragging the chat list puts the keyboard away and leaves the draft in the box
/// (#1823). The list is upside down, where `.scrollDismissesKeyboard(.interactively)` alone never did.
///   PORT=18797 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18797 xcodebuild test … -only-testing:PersonaUITests/ChatScrollKeyboardTests
/// Both styles (TEST_RUNNER_PERSONA_UI_STYLE=beautiful).
final class ChatScrollKeyboardTests: XCTestCase {
    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001"
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] != nil else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
    }

    @MainActor func testDraggingTheChatPutsTheKeyboardAway() {
        dragCloses()
    }

    @MainActor private func dragCloses() {
        let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"]!
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", widget]
        app.launchPinned()
        let field = app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 15), "no composer:\n\(app.debugDescription)")
        // At the end of what is there, so the typing lands after a draft an earlier run may have left.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "no keyboard after tapping the composer")
        field.typeText("滑一下")
        sleep(1)
        shot("typing")

        // A drag down the middle of the chat, toward the older messages: the finger never reaches the keyboard.
        let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3))
        from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: 160)), withVelocity: .default, thenHoldForDuration: 0.1)
        XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5), "the keyboard is still up after dragging the chat")
        XCTAssertTrue((field.value as? String ?? "").contains("滑一下"), "the draft went with the keyboard")
        shot("dragged")

        // Clean up the draft for the next run; drafts are saved a moment after the last change.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap()
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 3))
        sleep(2)
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "chat scroll keyboard \(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }
}
