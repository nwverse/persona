import XCTest

/// The composer's + (#3225). On the iPhone a tap opens the photo / file panel and touch and hold opens a menu of what the
/// panel's More tab lists, in the same order: Voice Chat, Take Photo, Mention a File or Session, an Agent's Quick
/// Commands in a submenu, then extension prompts. The iPad's + is that menu, with Photos and Files first. Against
/// scripts/mock/mock_hub.py (PERSONA_MOCK_HUB); `-showCameraRows YES` lists Take Photo, since the simulator has no camera.
/// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps screenshots, in light and dark (`-ui.scheme`).
final class AttachMenuTests: XCTestCase {
    private var hub = ""
    private static let persona = ["-tab", "assistant", "-app.tab", "assistant"]
    private static let agent = ["-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000002"] // weather › tests: working, Claude in herdr
    private var isPad: Bool { UIDevice.current.userInterfaceIdiom == .pad }
    /// Both looks when screenshots are kept; light alone otherwise.
    private var schemes: [String] { ProcessInfo.processInfo.environment["PERSONA_SHOTS"] == nil ? ["light"] : ["light", "dark"] }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)") }
        hub = h
    }

    /// Persona's conversation: the calls, the camera and Mention, no Quick Commands; Mention types "@" and opens the list.
    @MainActor func testPersonaMenu() {
        for scheme in schemes {
            let app = launch(Self.persona, scheme: scheme)
            openMenu(app)
            for id in ["composer-menu-voice", "composer-menu-camera", "composer-menu-mention"] {
                XCTAssertTrue(item(app, id).waitForExistence(timeout: 5), "no \(id) in the menu:\n\(app.debugDescription)")
            }
            XCTAssertFalse(commands(app).exists, "Quick Commands in Persona's conversation")
            if isPad {
                XCTAssertTrue(app.buttons["照片"].exists && app.buttons["文件"].exists, "the iPad's menu lost Photos or Files")
            } else {
                XCTAssertFalse(app.buttons["照片"].exists, "Photos in the touch-and-hold menu: the panel's job")
            }
            shot("attach-menu-persona", scheme)
            item(app, "composer-menu-mention").tap()
            let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
            XCTAssertTrue(app.descendants(matching: .any)["mention-menu"].firstMatch.waitForExistence(timeout: 5),
                          "Mention did not open the @ list:\n\(app.debugDescription)")
            XCTAssertTrue((field.value as? String)?.hasSuffix("@") == true, "Mention did not type @: \(String(describing: field.value))")
            app.terminate()
        }
    }

    /// An Agent's conversation: Quick Commands fold into a submenu, with /clear dimmed while the Agent works.
    @MainActor func testAgentMenu() {
        for scheme in schemes {
            let app = launch(Self.agent, scheme: scheme)
            openMenu(app)
            let quick = commands(app)
            XCTAssertTrue(quick.waitForExistence(timeout: 5), "no Quick Commands:\n\(app.debugDescription)")
            XCTAssertTrue(item(app, "composer-menu-voice").exists && item(app, "composer-menu-mention").exists)
            XCTAssertFalse(item(app, "attach-clear").exists, "the commands are not folded")
            shot("attach-menu-agent", scheme)
            quick.tap()
            let clear = item(app, "attach-clear")
            XCTAssertTrue(clear.waitForExistence(timeout: 5), "no /clear in Quick Commands:\n\(app.debugDescription)")
            XCTAssertFalse(clear.isEnabled, "/clear while the Agent works")
            XCTAssertTrue(item(app, "attach-command-汇报进展").exists, "no Report Progress")
            shot("attach-menu-agent-commands", scheme)
            app.terminate()
        }
    }

    /// The iPhone's tap is unchanged: the panel, whose More tab lists the menu's rows (an Agent's commands under them).
    @MainActor func testATapStillOpensThePanel() throws {
        guard !isPad else { throw XCTSkip("the iPad's + is the menu") }
        for scheme in schemes {
            for (name, args) in [("persona", Self.persona), ("agent", Self.agent)] {
                let app = launch(args, scheme: scheme)
                let plus = plusButton(app)
                sleep(2)
                plus.tap()
                let more = app.buttons["attach-tab-more"]
                XCTAssertTrue(more.waitForExistence(timeout: 5), "a tap did not open the panel:\n\(app.debugDescription)")
                more.tap()
                for id in ["composer-menu-voice", "composer-menu-camera", "composer-menu-mention"] {
                    XCTAssertTrue(app.buttons[id].waitForExistence(timeout: 5), "no \(id) in More:\n\(app.debugDescription)")
                }
                XCTAssertEqual(app.buttons["attach-clear"].exists, name == "agent", "the commands in More")
                shot("attach-panel-more-\(name)", scheme)
                app.terminate()
            }
        }
    }

    /// The largest accessibility text size: the menu and the Quick Commands submenu still read (screenshots only).
    @MainActor func testTheMenuAtTheLargestTextSize() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_SHOTS"] != nil else { throw XCTSkip("screenshots only") }
        let largest = ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"]
        let app = launch(Self.persona + largest, scheme: "light")
        openMenu(app)
        XCTAssertTrue(item(app, "composer-menu-mention").waitForExistence(timeout: 5), "no menu:\n\(app.debugDescription)")
        shot("attach-menu-persona-largest-text", "light")
        app.terminate()
        let agent = launch(Self.agent + largest, scheme: "light")
        openMenu(agent)
        let quick = commands(agent)
        XCTAssertTrue(quick.waitForExistence(timeout: 5), "no Quick Commands:\n\(agent.debugDescription)")
        quick.tap()
        shot("attach-menu-agent-commands-largest-text", "light")
        agent.terminate()
    }

    @MainActor private func plusButton(_ app: XCUIApplication) -> XCUIElement {
        let plus = app.descendants(matching: .any).matching(identifier: "shell-composer-add").firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 40), "no + button:\n\(app.debugDescription)")
        return plus
    }

    /// The iPad's + opens its menu on a tap, the iPhone's on touch and hold.
    @MainActor private func openMenu(_ app: XCUIApplication) {
        let plus = plusButton(app)
        sleep(2) // the conversation settles
        if isPad { plus.tap() } else { plus.press(forDuration: 1.0) }
    }

    @MainActor private func item(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    /// The Quick Commands submenu: by its title too, as a submenu may not carry its identifier through UIKit's menu.
    @MainActor private func commands(_ app: XCUIApplication) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "identifier == %@ OR label == %@", "attach-menu-commands", "快捷指令")).firstMatch
    }

    @MainActor private func launch(_ extra: [String], scheme: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-showCameraRows", "YES", "-ui.scheme", scheme] + extra
        app.launchPinned()
        return app
    }

    /// The whole screen: a menu is drawn in a window of its own.
    @MainActor private func shot(_ name: String, _ scheme: String) {
        sleep(1)
        let s = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name + "-" + scheme
        a.lifetime = .keepAlways
        add(a)
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + (isPad ? "-ipad" : "") + "-" + scheme + ".png"))
    }
}
