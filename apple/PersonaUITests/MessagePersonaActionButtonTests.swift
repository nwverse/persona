import XCTest

/// Message Persona on the Action button (#17789, docs/apple/siri.md "Action Button and Control Center"), as the system
/// lists and runs it, against scripts/mock/mock_hub.py:
///   PORT=<port> python3 scripts/mock/mock_hub.py
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>
///   TEST_RUNNER_PERSONA_SHOTS=<dir>   screenshots of Settings' pickers and of the chat each press opens
/// On a simulator with an Action button (iPhone 15 Pro or later) and a stock boot (Settings and the Shortcuts picker):
/// Settings › Action Button lists Message Persona under Shortcut (the App Shortcut) and under Controls (the control);
/// each is picked and the button pressed with Persona in the background: Persona comes to the front on the main chat
/// with the keyboard up.
final class MessagePersonaActionButtonTests: XCTestCase {
    private var hub: String {
        get throws {
            guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
                throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
            }
            return hub
        }
    }

    @MainActor private var springboard: XCUIApplication { XCUIApplication(bundleIdentifier: "com.apple.springboard") }
    @MainActor private var settings: XCUIApplication { XCUIApplication(bundleIdentifier: "com.apple.Preferences") }

    @MainActor private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? XCUIScreen.main.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir + "/message-persona-\(name).png"))
    }

    private func any(_ app: XCUIApplication, _ label: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label == %@ OR identifier == %@", label, label)).firstMatch
    }

    /// Persona paired with the mock and its main conversation read, left on the Agents tab, then in the background.
    @MainActor private func personaInTheBackground() throws -> XCUIApplication {
        let hub = try hub
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev"]
        app.launchPinned()
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20))
        Thread.sleep(forTimeInterval: 6) // the hub and its main conversation
        XCUIDevice.shared.press(.home)
        XCTAssertTrue(springboard.wait(for: .runningForeground, timeout: 10), "no home screen")
        return app
    }

    /// Settings › Action Button on `mode`'s page (swiped to, the page's title says it).
    @MainActor private func actionButtonPage(_ mode: String) {
        let settings = settings
        settings.terminate()
        settings.launch()
        let row = settings.cells.matching(NSPredicate(format: "label CONTAINS[c] %@", "Action Button")).firstMatch
        for _ in 0..<6 where !row.isHittable { settings.swipeUp() }
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no Action Button row\n\(settings.debugDescription)")
        row.tap()
        let title = settings.staticTexts[mode]
        for _ in 0..<16 where !(title.exists && title.isHittable) {
            settings.swipeLeft()
            Thread.sleep(forTimeInterval: 0.8)
        }
        XCTAssertTrue(title.exists, "no \(mode) page\n\(settings.debugDescription)")
    }

    /// The Action button pressed: Persona comes to the front on the main chat with the keyboard up.
    @MainActor private func pressOpensTheMainChatTyping(_ app: XCUIApplication, shots name: String) {
        XCUIDevice.shared.press(.home)
        _ = springboard.wait(for: .runningForeground, timeout: 10)
        Thread.sleep(forTimeInterval: 1)
        XCUIDevice.shared.press(.action)
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 20), "Persona did not come to the front")
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 15), "no keyboard\n\(app.debugDescription)")
        let field = app.textViews.matching(NSPredicate(format: "hasKeyboardFocus == true")).firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 5), "no focused field\n\(app.debugDescription)")
        Thread.sleep(forTimeInterval: 1)
        shot(name + "-opened")
        print("message-persona: \(name) opened\n\(app.debugDescription)")
    }

    /// Shortcut › Choose a Shortcut… › Persona lists Message Persona with nothing set up (the App Shortcut).
    @MainActor func testShortcutPickerListsMessagePersonaAndItOpensTheMainChatTyping() throws {
        let app = try personaInTheBackground()
        actionButtonPage("Shortcut")
        let settings = settings
        let choose = settings.buttons.matching(NSPredicate(format: "label BEGINSWITH[c] %@ OR label BEGINSWITH[c] %@",
                                                           "Choose a Shortcut", "Shortcut")).firstMatch
        XCTAssertTrue(choose.waitForExistence(timeout: 10), "no Choose a Shortcut\n\(settings.debugDescription)")
        choose.tap()
        Thread.sleep(forTimeInterval: 2)
        let persona = any(settings, "Persona")
        for _ in 0..<12 where !(persona.exists && persona.isHittable) { settings.swipeUp() }
        XCTAssertTrue(persona.exists, "no Persona in the picker\n\(settings.debugDescription)")
        persona.tap()
        let message = any(settings, "Message Persona")
        XCTAssertTrue(message.waitForExistence(timeout: 10), "no Message Persona\n\(settings.debugDescription)")
        Thread.sleep(forTimeInterval: 1)
        shot("shortcut-picker")
        message.tap()
        Thread.sleep(forTimeInterval: 2)
        shot("shortcut-picked")
        pressOpensTheMainChatTyping(app, shots: "shortcut")
    }

    /// Controls › the control picker, searched for Persona, lists Message Persona (the control).
    @MainActor func testControlPickerListsMessagePersonaAndItOpensTheMainChatTyping() throws {
        let app = try personaInTheBackground()
        actionButtonPage("Controls")
        let settings = settings
        let choose = settings.buttons.matching(NSPredicate(format: "label CONTAINS[c] %@ OR label CONTAINS[c] %@",
                                                           "Choose a Control", "Control")).firstMatch
        XCTAssertTrue(choose.waitForExistence(timeout: 10), "no control chooser\n\(settings.debugDescription)")
        choose.tap()
        Thread.sleep(forTimeInterval: 2)
        let owners = [settings, springboard]
        let search = owners.lazy.map { $0.searchFields.firstMatch }.first(where: \.exists)
        if let search {
            search.tap()
            search.typeText("Persona")
            Thread.sleep(forTimeInterval: 2)
        }
        let message = owners.lazy.map { self.any($0, "Message Persona") }.first(where: \.exists)
            ?? any(settings, "Message Persona")
        XCTAssertTrue(message.waitForExistence(timeout: 10),
                      "no Message Persona control\n\(settings.debugDescription)\n\(springboard.debugDescription)")
        shot("control-picker")
        message.tap()
        Thread.sleep(forTimeInterval: 2)
        shot("control-picked")
        pressOpensTheMainChatTyping(app, shots: "control")
    }
}
