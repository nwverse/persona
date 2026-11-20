import XCTest

/// The two ordinary composers show the shared prompt even before the field has focus.
final class ComposerPromptsTests: XCTestCase {
    @MainActor private func open(_ arguments: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-showSameIcon", "YES", "-dev.openChat", "", "-AppleLanguages", "(en)", "-AppleLocale", "en_US"] + arguments
        if ProcessInfo.processInfo.environment["PERSONA_COMPOSER_LARGEST"] == "1" {
            app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        }
        if let look = ProcessInfo.processInfo.environment["PERSONA_COMPOSER_APPEARANCE"] {
            app.launchArguments += ["-ui.scheme", look]
        }
        app.launchPinned()
        return app
    }

    @MainActor private func check(_ app: XCUIApplication, prompt: String, scene: String) {
        let field = app.descendants(matching: .any).matching(NSPredicate(format: "label == %@ OR placeholderValue == %@", prompt, prompt)).firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 12), "Missing empty composer prompt: \(prompt)")
        let picture = XCTAttachment(screenshot: app.screenshot())
        picture.name = scene
        picture.lifetime = .keepAlways
        add(picture)
    }

    @MainActor func testOrdinaryAgentPrompt() {
        let app = open(["-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000006", "-holdChromeOpen", "YES"])
        check(app, prompt: "What shall we work on today?", scene: "agent")
    }

    @MainActor func testAgentMenuShowsTheNamedModes() {
        let app = open(["-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000006", "-holdChromeOpen", "YES"])
        let mode = app.buttons["chat.permissionMode"]
        XCTAssertTrue(mode.waitForExistence(timeout: 12))
        mode.tap()
        for title in ["Default permissions", "Auto accept edits", "Plan mode", "Auto-review", "Full access"] {
            let option = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS[c] %@", title)).firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 3), title)
        }
        let picture = XCTAttachment(screenshot: app.screenshot())
        picture.name = "mode-menu"
        picture.lifetime = .keepAlways
        add(picture)
    }

    @MainActor func testLegacyPlanKeepsItsTwoApprovalChoices() {
        let app = open(["-tab", "dev", "-openSession", "studio/a1f3c2d4-0000-4000-8000-000000000005", "-holdChromeOpen", "YES"])
        let cards = app.scrollViews["pending-cards"]
        XCTAssertTrue(cards.waitForExistence(timeout: 12))
        for title in ["Approve & Auto-Run", "Auto-Accept Edits"] {
            let option = cards.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS[c] %@", title)).firstMatch
            XCTAssertTrue(option.waitForExistence(timeout: 5), title)
        }
        let picture = XCTAttachment(screenshot: app.screenshot())
        picture.name = "legacy-plan"
        picture.lifetime = .keepAlways
        add(picture)
    }

    @MainActor func testNewPersonaConversationPrompt() {
        let app = open(["-app.tab", "assistant", "-newAssistant", "YES", "-showHomeState", "empty"])
        check(app, prompt: "What can Persona do for you today?", scene: "persona")
    }
}
