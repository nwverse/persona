import XCTest

/// Settings › Appearance's three choices of how the app looks (docs/apple/ios.md「Design language」): Avatar, Theme Color and Interface Style, each on its own page
/// and apart from the others. Against scripts/mock/mock_hub.py started with MOCK_PERSONAS=builtin and PERSONA_MOCK_HUB. Screenshots go to PERSONA_LOOK_SHOTS when set (the appearance is the simulator's).
final class LookPickerTests: XCTestCase {
    private var hub: String!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB) with MOCK_PERSONAS")
        }
        self.hub = hub
    }

    /// Taps write the app's own settings: put back the defaults (Liquid Glass, Match Interface Style, the built-in) for the tests after.
    override func tearDown() {
        if let hub {
            let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
            for (page, id) in [("color", "color-style"), ("style", "style-glass")] {
                let app = XCUIApplication()
                app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", page, "-openPersona", "persona"]
                app.launchPersona()
                _ = app.collectionViews.firstMatch.waitForExistence(timeout: 10)
                let bar = app.windows.firstMatch.frame.maxY - 40
                let e = app.buttons[id]
                func clear() -> Bool { e.exists && e.isHittable && e.frame.maxY < bar }
                for _ in 0..<12 where !clear() { app.collectionViews.firstMatch.swipeUp(velocity: .slow) }
                if clear() { e.tap() }
                sleep(1) // the tree is rebuilt
                app.terminate()
            }
        }
        super.tearDown()
    }

    /// Opens on Settings › Appearance › `page` (color | style | persona).
    @MainActor private func launch(_ page: String, _ extra: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", page] + extra
        app.launchPersona()
        return app
    }

    /// Scrolls the page's list from the top down until `e` can be tapped: on screen and clear of the floating tab
    /// bar (a tap on a card behind it lands on the bar).
    @MainActor @discardableResult private func reveal(_ app: XCUIApplication, _ e: XCUIElement) -> Bool {
        let list = app.collectionViews.firstMatch
        _ = list.waitForExistence(timeout: 10)
        let bar = app.windows.firstMatch.frame.maxY - 40
        func clear() -> Bool { e.exists && e.isHittable && e.frame.maxY < bar }
        if clear() { return true }
        for _ in 0..<3 { list.swipeDown(velocity: .fast) }
        for _ in 0..<12 where !clear() {
            list.swipeUp(velocity: .slow)
        }
        return clear()
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_LOOK_SHOTS"] else { return }
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + ".png"))
    }

    /// After a choice the tree is rebuilt and the list is back at the top: scroll back to the choice.
    @MainActor private func chosen(_ app: XCUIApplication, _ id: String) -> Bool {
        sleep(1)
        return reveal(app, app.buttons[id]) && app.buttons[id].isSelected
    }

    @MainActor private func waitSelected(_ e: XCUIElement, _ why: String) {
        let selected = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: e)
        XCTAssertEqual(XCTWaiter.wait(for: [selected], timeout: 15), .completed, why)
    }

    /// From a page of Appearance back to Settings, where each row says what it is set to.
    @MainActor private func back(_ app: XCUIApplication, from title: String) {
        app.navigationBars[title].buttons["BackButton"].tap()
        XCTAssertTrue(app.buttons["settings-color"].waitForExistence(timeout: 10), "not back on 设置:\n\(app.debugDescription)")
    }

    @MainActor private func row(_ app: XCUIApplication, _ id: String, says value: String) -> Bool {
        app.buttons[id].label.contains(value)
    }

    /// Clay with the persona's colors: Match Avatar is offered; choosing Coral keeps Clay and the persona, choosing Island keeps Coral.
    @MainActor func testTheThreeChoicesStayApart() throws {
        let app = launch("color", ["-ui.style", "clay", "-ui.color", "persona", "-openPersona", "nwverse"])
        let follow = app.buttons["color-persona"]
        XCTAssertTrue(follow.waitForExistence(timeout: 20), "跟随形象 is not offered for a persona with colors:\n\(app.debugDescription)")
        waitSelected(follow, "跟随形象 is not chosen")
        // The persona's package (and its colors) comes from the hub; until then the swatch says the persona brings none.
        let loaded = expectation(for: NSPredicate(format: "NOT (label CONTAINS '没带')"), evaluatedWith: follow)
        XCTAssertEqual(XCTWaiter.wait(for: [loaded], timeout: 20), .completed, "the persona's colors did not arrive: \(follow.label)")
        XCTAssertTrue(app.descendants(matching: .any)["look-sample"].exists, "no sample of the look")
        sleep(3) // the package's pictures land
        shot(app, "settings-1-color")

        // Coral: the tree is rebuilt and still on Theme Color; Clay and the persona stay.
        let coral = app.buttons["color-coral"]
        XCTAssertTrue(reveal(app, coral))
        coral.tap()
        XCTAssertTrue(chosen(app, "color-coral"), "choosing 珊瑚橙 did not take")
        XCTAssertTrue(app.navigationBars["主题色"].exists, "the rebuild left 设置 › 外观 › 主题色")
        back(app, from: "主题色")
        XCTAssertTrue(row(app, "settings-color", says: "珊瑚橙"), "the 主题色 row: \(app.buttons["settings-color"].label)")
        XCTAssertTrue(row(app, "settings-style", says: "黏土"), "choosing a color changed the style: \(app.buttons["settings-style"].label)")
        shot(app, "settings-2-rows")

        // Island: still on Interface Style, and Coral stays.
        app.buttons["settings-style"].tap()
        XCTAssertTrue(reveal(app, app.buttons["style-clay"]))
        XCTAssertTrue(app.buttons["style-clay"].isSelected)
        XCTAssertTrue(reveal(app, app.buttons["style-island"]))
        app.buttons["style-island"].tap()
        XCTAssertTrue(chosen(app, "style-island"), "choosing Island did not take")
        XCTAssertTrue(app.navigationBars["界面风格"].exists, "the rebuild left 设置 › 外观 › 界面风格")
        shot(app, "settings-3-island-coral")
        back(app, from: "界面风格")
        XCTAssertTrue(row(app, "settings-color", says: "珊瑚橙"), "choosing a style changed the color: \(app.buttons["settings-color"].label)")
        XCTAssertTrue(row(app, "settings-style", says: "Island"), "the 界面风格 row: \(app.buttons["settings-style"].label)")

        app.buttons["settings-persona"].tap()
        XCTAssertTrue(app.buttons["persona-nwverse"].waitForExistence(timeout: 10))
        XCTAssertTrue(app.buttons["persona-nwverse"].isSelected, "the persona changed")
    }

    /// The built-in brings no colors: no Match Avatar to choose.
    @MainActor func testNoFollowingAPersonaWithoutColors() throws {
        let app = launch("color", ["-ui.style", "glass", "-ui.color", "style", "-openPersona", "persona"])
        XCTAssertTrue(reveal(app, app.buttons["color-blue"]))
        XCTAssertTrue(app.buttons["color-style"].isSelected)
        XCTAssertFalse(app.buttons["color-persona"].exists, "跟随形象 offered for the built-in")
    }

    /// The iPhone's shell keeps Settings in a sheet: a choice rebuilds the tree, the sheet included, and lands back on Theme Color.
    @MainActor func testShellSheetStaysOnAppearance() throws {
        let app = launch("color", ["-ui.style", "glass", "-ui.color", "style", "-openPersona", "persona"])
        let bar = app.navigationBars["主题色"]
        XCTAssertTrue(bar.waitForExistence(timeout: 20), "the shell's 设置 is not on 主题色")
        XCTAssertFalse(app.tabBars.firstMatch.exists, "not the shell")
        // No tab bar here: between the top bar and the screen's foot is enough. Short drags without a fling, so an
        // element comes into view whole; the sheet covers the page, so a drag on the app scrolls its list.
        func reveal(_ e: XCUIElement) -> Bool {
            let from = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
            for _ in 0..<16 {
                if e.exists && e.frame.minY > bar.frame.maxY && e.frame.maxY < app.frame.maxY { return true }
                let up = e.exists && e.frame.minY <= bar.frame.maxY
                from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: up ? 150 : -150)))
            }
            return false
        }
        let sample = app.descendants(matching: .any)["look-sample"]
        let coral = app.buttons["color-coral"]
        XCTAssertTrue(reveal(sample), "no sample in the shell's 主题色")
        let before = bubble(sample)
        XCTAssertTrue(reveal(coral), "no 珊瑚橙 in the shell's 主题色")
        coral.tap()
        sleep(1)
        XCTAssertTrue(bar.waitForExistence(timeout: 10), "the rebuild left the shell's 设置 › 外观 › 主题色")
        // The sample is drawn with the static tokens: it shows the new color only if the sheet was rebuilt too.
        XCTAssertTrue(reveal(sample))
        let after = bubble(sample)
        XCTAssertEqual([before.count, after.count], [3, 3], "could not read the sample's bubble")
        XCTAssertFalse(zip(before, after).allSatisfy { abs($0 - $1) <= 8 }, "the sheet's sample kept the old color (\(before) → \(after))")
        XCTAssertTrue(reveal(coral) && coral.isSelected, "choosing 珊瑚橙 in the shell did not take")
        shot(app, "shell-coral")
    }

    /// The user's bubble in the sample: inside its right padding, halfway down. Compared with itself only, so the
    /// channel order does not matter.
    @MainActor private func bubble(_ sample: XCUIElement) -> [Int] {
        let image = sample.screenshot().image
        guard let cg = image.cgImage else { return [] }
        let scale = CGFloat(cg.width) / image.size.width
        guard let px = cg.cropping(to: CGRect(x: (image.size.width - 18) * scale, y: 30 * scale, width: 1, height: 1)),
              let data = px.dataProvider?.data, let p = CFDataGetBytePtr(data) else { return [] }
        return [Int(p[0]), Int(p[1]), Int(p[2])]
    }
}
