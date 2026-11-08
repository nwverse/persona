import XCTest

/// The chat's composer in the liquid glass style is laid out on its own (`LayoutIsland`, a hosting controller of its
/// own): its field, + and send must still be in the accessibility tree, where VoiceOver and the UI tests find them.
/// The island previously showed as an `Other` with nothing under it for both an agent's chat (#995) and Persona's
/// (#1006). Against scripts/mock/mock_hub.py
/// (TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>).
final class ComposerAccessibilityTests: XCTestCase {
    private let widget = "macbook/a1f3c2d4-0000-4000-8000-000000000001"
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = true
    }

    private func pairURL() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        return "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    @MainActor private func attachScreen(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// The composer's controls are in the tree and the field takes typing: + (its menu offers Photos), the field, and
    /// send (Interrupt in its place while the agent works and nothing is typed, Send once something is).
    @MainActor private func expectComposer(_ what: String) {
        // ComposerRow's + reads Add (its symbol's own label); the shell's composer, an agent's too since #1091, says what it adds.
        let plus = app.buttons.matching(NSPredicate(format: "label == '添加' OR label == '添加照片或文件'")).firstMatch
        let send = app.buttons["shell-composer-send"].firstMatch
        let stop = app.buttons["shell-composer-stop"].firstMatch
        let mic = app.buttons["shell-composer-mic"].firstMatch
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        guard plus.waitForExistence(timeout: 15) else {
            attachScreen("no + (\(what))")
            return XCTFail("no + (\(what))\n\(composerTree())")
        }
        attachScreen(what)
        XCTAssertTrue(send.exists || stop.exists || mic.exists, "no send, interrupt or dictation control (\(what))\n\(composerTree())")
        XCTAssertTrue(field.exists, "no input field (\(what))\n\(composerTree())")
        XCTAssertTrue(plus.isHittable, "+ is not hittable (\(what))")

        plus.tap()
        XCTAssertTrue(app.buttons["照片"].waitForExistence(timeout: 5), "the + menu did not open (\(what))")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2)).tap() // closes the menu
        XCTAssertTrue(app.buttons["照片"].waitForNonExistence(timeout: 5), "the + menu stayed open (\(what))")

        guard field.exists else { return }
        // At the end of what is there: a tap in the middle put the cursor before a draft an earlier run left, and the
        // deletes took nothing.
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap()
        clear(field)
        field.typeText("hi")
        XCTAssertTrue(send.waitForExistence(timeout: 3) && send.isEnabled, "发送 is not there once something is typed (\(what))")
        clear(field)
        // Drafts are saved a moment after the last change (`AppModel.saveDrafts`): terminated sooner, the next launch
        // found the typed text still there.
        sleep(2)
    }

    /// Empties the field: as many deletes as it holds (its value is the placeholder while it is empty).
    @MainActor private func clear(_ field: XCUIElement) {
        let value = field.value as? String ?? ""
        let typed = value == field.placeholderValue ? 0 : value.count
        if typed > 0 { field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: typed + 2)) }
        let now = field.value as? String ?? ""
        XCTAssertTrue(now.isEmpty || now == field.placeholderValue, "the field is not empty: \(now)")
    }

    /// The bottom of the tree, where the composer is.
    @MainActor private func composerTree() -> String {
        app.debugDescription.split(separator: "\n").filter { $0.contains("{{") }.suffix(25).joined(separator: "\n")
    }

    /// An agent's chat in the Shell, launched twice (#995).
    @MainActor func testAnAgentsComposerIsInTheTree() throws {
        let pair = try pairURL()
        for round in 1...2 {
            let what = "agent, shell #\(round)"
            app = XCUIApplication()
            app.launchArguments = ["-pairURL", pair, "-openSession", widget]
            app.launchPinned()
            expectComposer(what)
            app.terminate()
        }
    }

    /// Persona's main conversation exposes the same accessible controls as an agent's conversation.
    @MainActor func testTheAssistantsComposerIsInTheTree() throws {
        let pair = try pairURL()
        for round in 1...2 {
            app = XCUIApplication()
            app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant",
                                   "-openAssistant", main]
            app.launchPinned()
            expectComposer("Persona, shell #\(round)")
            app.terminate()
        }
    }
}
