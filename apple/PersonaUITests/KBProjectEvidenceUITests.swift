import XCTest

final class KBProjectEvidenceUITests: XCTestCase {
    private var hub: URL!

    override func setUp() async throws {
        continueAfterFailure = false
        guard let address = ProcessInfo.processInfo.environment["PERSONA_PROJECT_HUB"], let url = URL(string: address) else {
            throw XCTSkip("Needs the project evidence mock Hub")
        }
        hub = url
        let (bytes, _) = try await URLSession.shared.data(from: hub.appending(path: "mock/project-evidence/reset"))
        guard (try? JSONDecoder().decode([String: String].self, from: bytes))?["fixture"] == "community-garden" else {
            throw XCTSkip("Needs the community-garden project fixture")
        }
    }

    @MainActor private func launch(_ appearance: String = "light", largest: Bool = false) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.absoluteString.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US", "-pairURL", pair,
                               "-tab", "assistant", "-ui.scheme", appearance]
        if largest { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launchPinned(navigation: false)
        let alert = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch
        if alert.waitForExistence(timeout: 3) {
            let okay = alert.buttons.firstMatch
            if okay.exists { okay.tap(); _ = alert.waitForNonExistence(timeout: 5) }
        }
        let place = app.buttons["sidebar-Knowledge"].firstMatch
        if !place.isHittable {
            let drawer = app.buttons["assistant-sidebar-button"].firstMatch
            XCTAssertTrue(drawer.waitForExistence(timeout: 15), app.debugDescription)
            drawer.tap()
        }
        XCTAssertTrue(place.waitForExistence(timeout: 10), app.debugDescription)
        place.tap()
        return app
    }

    @MainActor private func row(_ app: XCUIApplication, named name: String = "Community garden") -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label CONTAINS %@", name)).firstMatch
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "kb-project-" + name
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testListNamesTheFieldAndItsUncoveredChanges() {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18), app.debugDescription)
        shot(app, "list")
        XCTAssertTrue(project.label.contains("Summary needs review"), project.label)
        XCTAssertTrue(project.label.contains("3 newer"), project.label)
        XCTAssertFalse(project.label.contains("Overview needs review"), project.label)
    }

    @MainActor func testDetailExposesCoverageOnlyWhenOpened() {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        XCTAssertTrue(app.staticTexts["Community garden"].waitForExistence(timeout: 12), app.debugDescription)
        shot(app, "detail")
        let sources = app.buttons["kb.project.evidence"]
        XCTAssertTrue(sources.waitForExistence(timeout: 4), app.debugDescription)
        XCTAssertFalse(label(app, containing: "Timeline bytes 13–87").exists)
        sources.tap()
        let historic = label(app, containing: "Timeline bytes 13–87")
        XCTAssertTrue(historic.exists, app.debugDescription)
        XCTAssertTrue(historic.label.contains("Locked") && historic.label.contains("Timeline bytes 13–87 of 208"))
        XCTAssertTrue(historic.label.contains("Maintained by you"))
        reveal(app, app.buttons["kb.project.edit"])
        let linked = app.buttons.matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@", "Planting plan review", "chats/garden-review")).firstMatch
        reveal(app, label(app, containing: "Source status: Merged"))
        XCTAssertTrue(label(app, containing: "github:example/garden#4").exists)
        reveal(app, linked)
        XCTAssertTrue(label(app, containing: "Source status: Merged").exists, app.debugDescription)
        XCTAssertTrue(label(app, containing: "Environment unverified").exists, app.debugDescription)
        XCTAssertTrue(label(app, containing: "Deployment and acceptance need their own evidence").exists)
        linked.tap()
        XCTAssertTrue(app.staticTexts["Planting plan review"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(label(app, containing: "Source delivery history.").waitForExistence(timeout: 8), app.debugDescription)
    }
}

extension KBProjectEvidenceUITests {
    @MainActor private func label(_ app: XCUIApplication, containing text: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    @MainActor private func reveal(_ app: XCUIApplication, _ element: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        let list = app.collectionViews.firstMatch
        for _ in 0..<8 where !element.isHittable { list.swipeUp(velocity: .slow) }
        XCTAssertTrue(element.isHittable, app.debugDescription, file: file, line: line)
    }

    @MainActor private func editor(_ app: XCUIApplication) -> XCUIElement {
        let edit = app.buttons["kb.project.edit"]
        reveal(app, edit)
        edit.tap()
        let save = app.buttons["kb.project.save"].firstMatch
        XCTAssertTrue(save.waitForExistence(timeout: 8))
        return save
    }

    @MainActor private func clearURLs(_ field: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        for _ in 0..<3 {
            guard let value = field.value as? String, !value.isEmpty, value != "Source references" else { return }
            field.coordinate(withNormalizedOffset: CGVector(dx: 0.98, dy: 0.9)).tap()
            field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: value.count + 2))
        }
        XCTAssertTrue((field.value as? String ?? "").isEmpty || field.value as? String == "Source references",
                      field.debugDescription, file: file, line: line)
    }

    @MainActor private func changePolicy(_ app: XCUIApplication) {
        let row = app.switches["kb.project.reference-only"].firstMatch
        let control = row.switches.firstMatch
        (control.exists ? control : row).tap()
    }

    private func writes() async throws -> [[String: Any]] {
        let (data, _) = try await URLSession.shared.data(from: hub.appending(path: "mock/project-evidence/writes"))
        let result = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        return try XCTUnwrap(result["writes"] as? [[String: Any]])
    }

    @MainActor func testExplicitStorageChoicePointersAndReadOnlyHistory() async throws {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        let evidence = app.buttons["kb.project.evidence"]
        XCTAssertTrue(evidence.waitForExistence(timeout: 10), app.debugDescription)
        evidence.tap()
        var save = editor(app)
        try app.performAccessibilityAudit(for: [.sufficientElementDescription, .trait])
        let toggle = app.switches["kb.project.reference-only"].firstMatch
        let refs = app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch
        XCTAssertFalse(save.isEnabled)
        XCTAssertEqual(refs.value as? String, "https://example.org/garden/planting-plan")
        changePolicy(app)
        XCTAssertEqual(toggle.value as? String, "1")
        shot(app, "storage-reference-only")
        app.buttons["sheet-close"].firstMatch.tap()
        let cancelled = try await writes()
        XCTAssertTrue(cancelled.isEmpty)
        save = editor(app)
        changePolicy(app)
        XCTAssertEqual(toggle.value as? String, "1")
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 8), app.debugDescription)
        let saved = try await writes()
        XCTAssertEqual(saved.count, 1)
        let first = try XCTUnwrap(saved[0]["body"] as? [String: String])
        XCTAssertEqual(first, ["project_mode": "reference-only"])
        XCTAssertFalse(app.buttons["kb.facts.edit"].exists)
        _ = editor(app)
        XCTAssertEqual(toggle.value as? String, "1")
        clearURLs(refs)
        XCTAssertTrue(save.isEnabled, app.debugDescription)
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 8))
        let cleared = try await writes()
        let second = try XCTUnwrap(cleared.last?["body"] as? [String: [String]])
        XCTAssertEqual(second, ["source_urls": []])
        let linked = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "chats/garden-review")).firstMatch
        reveal(app, linked)
        let facts = app.staticTexts["kb.facts"]
        reveal(app, facts)
        XCTAssertTrue(facts.label.contains("Keep the east entrance clear."))
        XCTAssertFalse(app.buttons["kb.facts.edit"].exists)
        app.terminate()

        let again = launch()
        let reopened = row(again)
        XCTAssertTrue(reopened.waitForExistence(timeout: 15))
        reopened.tap()
        let section = again.buttons["kb.project.evidence"]
        XCTAssertTrue(section.waitForExistence(timeout: 10))
        XCTAssertTrue(section.label.contains("References only"), section.label)
        shot(again, "detail-reference-only")
        section.tap()
        let restore = editor(again)
        changePolicy(again)
        XCTAssertTrue(restore.isEnabled)
        restore.tap()
        XCTAssertTrue(restore.waitForNonExistence(timeout: 8))
        let restored = try await writes()
        XCTAssertEqual(restored.last?["body"] as? [String: String], ["project_mode": "full"])
    }

    @MainActor func testRejectedPointerKeepsTheDraftInTheEditor() async throws {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        let disclosure = app.buttons["kb.project.evidence"]
        XCTAssertTrue(disclosure.waitForExistence(timeout: 10))
        disclosure.tap()
        let save = editor(app)
        let pointers = app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch
        clearURLs(pointers)
        pointers.typeText("chats/invalid")
        XCTAssertFalse(save.isEnabled)
        XCTAssertTrue(app.staticTexts["kb.project.invalid-urls"].exists)
        clearURLs(pointers)
        pointers.typeText("https://example.org/rejected")
        XCTAssertEqual(pointers.value as? String, "https://example.org/rejected")
        XCTAssertTrue(save.isEnabled)
        save.tap()
        XCTAssertTrue(app.staticTexts["kb.project.error"].waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue((pointers.value as? String)?.contains("https://example.org/rejected") == true)
        XCTAssertTrue(save.isEnabled)
        let attempts = try await writes()
        XCTAssertTrue(attempts.isEmpty)
    }

    @MainActor func testSharedDeliveryKeepsItsIndependentUnknownCoverageAndOriginalOwnerURL() {
        let app = launch()
        let project = row(app, named: "Garden verification")
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        XCTAssertTrue(project.label.contains("Summary needs review") && project.label.contains("Overview needs review"))
        project.tap()
        let section = app.buttons["kb.project.evidence"]
        XCTAssertTrue(section.waitForExistence(timeout: 10))
        section.tap()
        let receipt = label(app, containing: "Writer unknown")
        XCTAssertTrue(receipt.exists && receipt.label.contains("Coverage unknown") && receipt.label.contains("Maintained by Persona"))
        _ = editor(app)
        let urls = app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch
        XCTAssertEqual(urls.value as? String, "https://github.com/example/garden/issues/4#issuecomment-1")
        app.buttons["sheet-close"].firstMatch.tap()
        reveal(app, label(app, containing: "Environment unverified"))
        XCTAssertTrue(label(app, containing: "Source status: Merged").exists)
        XCTAssertTrue(label(app, containing: "github:example/garden#4").exists)
    }

    @MainActor func testEmptyProjectCanSwitchModesWithoutInventingEvidence() async throws {
        let app = launch()
        let project = row(app, named: "Empty garden")
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        reveal(app, project)
        XCTAssertFalse(project.label.contains("needs review"))
        project.tap()
        let evidence = app.buttons["kb.project.evidence"]
        XCTAssertTrue(evidence.waitForExistence(timeout: 10))
        evidence.tap()
        XCTAssertFalse(label(app, containing: "Writer unknown").exists)
        let save = editor(app)
        XCTAssertFalse(save.isEnabled)
        changePolicy(app)
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 8))
        XCTAssertTrue(evidence.label.contains("References only"))
        _ = editor(app)
        changePolicy(app)
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 8))
        let sent = try await writes()
        XCTAssertEqual(sent.compactMap { $0["body"] as? [String: String] }, [["project_mode": "reference-only"], ["project_mode": "full"]])
    }

    @MainActor func testOlderHubRetainsOriginalProjectControls() {
        let app = launch()
        let project = row(app, named: "Archive index")
        XCTAssertTrue(project.waitForExistence(timeout: 18), app.debugDescription)
        XCTAssertFalse(project.label.contains("needs review"))
        project.tap()
        XCTAssertTrue(app.staticTexts["Archive index"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["kb.project.evidence"].exists)
        reveal(app, app.buttons["kb.facts.edit"])
        XCTAssertFalse(app.buttons["kb.project.edit"].exists)
    }

    @MainActor func testLargestTypeKeepsTheProjectOverviewAndActionsInsideTheHeader() {
        let app = launch(largest: true)
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        let evidence = app.buttons["kb.project.evidence"]
        XCTAssertTrue(evidence.waitForExistence(timeout: 10))
        let overview = app.staticTexts["The garden plan has been reviewed."].firstMatch
        XCTAssertGreaterThanOrEqual(overview.frame.minX, evidence.frame.minX - 1)
        let ask = app.buttons["Ask Persona"].firstMatch
        let start = app.buttons["Start Agent"].firstMatch
        XCTAssertTrue(ask.isHittable && start.isHittable)
        XCTAssertFalse(ask.frame.insetBy(dx: 1, dy: 1).intersects(start.frame))
        for (button, title) in [(ask, "Ask Persona"), (start, "Start Agent")] {
            XCTAssertTrue(button.staticTexts[title].firstMatch.exists, app.debugDescription)
            XCTAssertGreaterThanOrEqual(button.frame.minX, evidence.frame.minX - 1)
            XCTAssertLessThanOrEqual(button.frame.maxX, evidence.frame.maxX + 1)
        }
        shot(app, "header-fit-largest")
    }

    @MainActor func testAppearanceAndAccessibilitySizeOnProjects() {
        for appearance in ["light", "dark"] {
            for largest in [false, true] {
                let app = launch(appearance, largest: largest)
                let project = row(app)
                XCTAssertTrue(project.waitForExistence(timeout: 18), app.debugDescription)
                if !project.isHittable { reveal(app, project) }
                XCTAssertTrue(project.label.contains("Summary needs review"))
                let suffix = appearance + (largest ? "-largest" : "-default")
                shot(app, "list-" + suffix)
                project.tap()
                let evidence = app.buttons["kb.project.evidence"]
                XCTAssertTrue(evidence.waitForExistence(timeout: 10))
                reveal(app, evidence)
                evidence.tap()
                let historic = label(app, containing: "Timeline bytes 13–87")
                reveal(app, historic)
                shot(app, "coverage-" + suffix)
                _ = editor(app)
                XCTAssertTrue(app.switches["kb.project.reference-only"].firstMatch.isHittable)
                XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch.exists)
                shot(app, "storage-" + suffix)
                app.buttons["sheet-close"].firstMatch.tap()
                reveal(app, label(app, containing: "Environment unverified"))
                XCTAssertTrue(label(app, containing: "Source status: Merged").exists)
                shot(app, "sources-" + suffix)
                app.terminate()
            }
        }
    }
}
