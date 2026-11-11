import XCTest

/// The section titles of the composer's model menus (#4322): over a running agent's model and effort
/// (`SessionModesMenu`) and over a new conversation's (`StarterModesMenu`). An iOS menu leaves out a section's title over
/// an inline picker in some layouts (docs/lessons/ios.md, "A `Menu` draws no section header over an inline picker"), so
/// each menu is opened, shot, and checked for its titles. Runs on a mock hub:
///   PORT=18931 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18931 TEST_RUNNER_PERSONA_SHOTS=<dir> xcodebuild test …
/// The test runs in zh-Hans: 模型 is "Model", 思考强度 "Thinking Effort".
final class MenuSectionTitleTests: XCTestCase {
    private var hub = ""
    private var app: XCUIApplication!
    /// An idle Claude agent in herdr: its menu has the model list and the effort levels.
    private static let idle = "macbook/a1f3c2d4-0000-4000-8000-000000000006"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = true // every menu is shot even when an earlier one misses a title
    }

    @MainActor func testARunningAgentsMenuTitlesItsSections() {
        open(["-openSession", Self.idle], focus: true)
        openMenu()
        shot("agent")
        assertTitles(["模型", "思考强度"]) // i18n-ignore: the test runs in zh-Hans
    }

    @MainActor func testANewConversationsMenuTitlesItsSections() {
        open(["-tab", "assistant", "-app.tab", "assistant", "-newAssistant", "YES"], focus: false)
        openMenu()
        shot("new")
        assertTitles(["模型", "思考强度"]) // i18n-ignore: the test runs in zh-Hans
    }

    @MainActor private func open(_ extra: [String], focus: Bool) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair] + extra
        app.launchPinned()
        guard focus else { return }
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 20), "no composer:\n\(app.debugDescription)")
        field.tapCenter()
    }

    @MainActor private func openMenu() {
        let menu = app.buttons["shell-composer-modes"].firstMatch
        XCTAssertTrue(menu.waitForExistence(timeout: 20), "no composer model menu:\n\(app.debugDescription)")
        menu.tap()
        sleep(1) // the menu has finished opening
    }

    /// Each title is on screen as its own line, not only as the composer button's or a picker's label.
    @MainActor private func assertTitles(_ titles: [String]) {
        for t in titles {
            let line = app.descendants(matching: .any)
                .matching(NSPredicate(format: "label == %@ AND identifier != %@", t, "shell-composer-modes")).firstMatch
            if !line.waitForExistence(timeout: 3) { XCTFail("the menu's \(t) title; the screen's elements:\n\(app.debugDescription)") }
        }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/menu-titles-\(name).png"))
    }
}
