import XCTest

/// The iPad draws the iPhone's composer (`ShellComposerBar`, #2779): one line, [+] the field [mic], that grows into a
/// card while typing, in an agent's conversation and the assistant's, no wider than the conversation and centered on it.
/// Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB), on an iPad simulator; skipped on iPhone.
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps a screenshot of each.
final class IPadComposerTests: XCTestCase {
    private var app: XCUIApplication!
    private var pair = ""

    /// `ChatView.readableWidth`: the conversation's measure, the composer's too.
    private let readableWidth: CGFloat = 760

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard UIDevice.current.userInterfaceIdiom == .pad else { throw XCTSkip("iPad only") }
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        app = XCUIApplication()
        pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
    }

    override func tearDown() {
        XCUIDevice.shared.orientation = .portrait
        super.tearDown()
    }

    @MainActor private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("ipad-composer-\(name).png"))
    }

    private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any).matching(identifier: id).firstMatch }

    /// The shell's composer, at rest one line with its + and mic; focused, the card's row with the model button.
    /// `geometry`: the frames are checked in portrait only; in landscape XCUITest reports them in the portrait's axes.
    @MainActor private func checkComposer(_ name: String, geometry: Bool = true) {
        let field = element("shell-composer-field")
        XCTAssertTrue(field.waitForExistence(timeout: 30), "\(name): not the shell's composer:\n\(app.debugDescription)")
        let add = element("shell-composer-add"), mic = element("shell-composer-mic")
        XCTAssertTrue(add.exists && mic.exists, "\(name): + and the mic beside the field")
        guard geometry else { return shot("\(name)-rest") }
        XCTAssertEqual(add.frame.midY, field.frame.midY, accuracy: 6, "\(name): one line at rest, field \(String(describing: field.value))")
        // No wider than the conversation, centered in the detail column (the window less the sidebar).
        let width = mic.frame.maxX - add.frame.minX
        XCTAssertLessThanOrEqual(width, readableWidth, "\(name): \(width) wide")
        shot("\(name)-rest")
        field.tap()
        let send = element("shell-composer-send")
        field.typeText("你好")
        XCTAssertTrue(send.waitForExistence(timeout: 5), "\(name): typing shows send")
        XCTAssertGreaterThan(send.frame.minY, field.frame.maxY - 4, "\(name): the buttons' row under the field (the card)")
        shot("\(name)-typing")
        // The draft is kept per conversation: leave none for the next launch (it would open as the card).
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 4))
        XCTAssertTrue(send.waitForNonExistence(timeout: 5), "\(name): the draft is gone")
        sleep(3) // drafts are saved a second after the last change (`AppModel.draftWriter`)
    }

    @MainActor func testAgentConversation() {
        for (name, orientation) in [("portrait", UIDeviceOrientation.portrait), ("landscape", .landscapeLeft)] {
            XCUIDevice.shared.orientation = orientation
            app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001"]
            app.launchPinned()
            checkComposer("agent-\(name)", geometry: orientation == .portrait)
            app.terminate()
        }
    }

    @MainActor func testAssistantConversation() {
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant"]
        app.launchPinned()
        checkComposer("assistant")
        // The modes live in the composer's model button, not in chips over it.
        XCTAssertTrue(element("shell-composer-modes").exists, "the model button in the card's row")
    }
}
