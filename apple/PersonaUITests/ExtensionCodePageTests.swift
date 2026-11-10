import XCTest

/// The code-hosting page (#2961, docs/extensions/extensions.md「UI version 4: code hosting」) on the schema's shared snapshots
/// (schema/extension/examples/showcase/code-*.json), through the mock's showcase extension: iPad side by side (the file
/// tree beside the diffs, landscape and portrait), the phone a directory at a time with the diffs pushed; a line
/// number opens the inline comment box. Against scripts/mock/mock_hub.py with MOCK_EXTENSIONS=1:
/// `TEST_RUNNER_PERSONA_EXTENSIONS_HUB=http://127.0.0.1:<port>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` keeps the screenshots, named
/// by `TEST_RUNNER_PERSONA_EXTENSIONS_APPEARANCE` and the style.
final class ExtensionCodePageTests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }
    private var pad: Bool { UIDevice.current.userInterfaceIdiom == .pad }

    @MainActor private func launch() throws -> XCUIApplication {
        guard let hub = env["PERSONA_EXTENSIONS_HUB"] else { throw XCTSkip("needs mock_hub.py with MOCK_EXTENSIONS=1 (PERSONA_EXTENSIONS_HUB)") }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        let look = env["PERSONA_EXTENSIONS_APPEARANCE"] ?? "light"
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-ui.scheme", look]
        app.launchPinned()
        acceptIconAlert()
        app.terminate()
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openExtension", "showcase", "-openExtensionPage", "snapshots", "-ui.scheme", look]
        app.launchPinned()
        acceptIconAlert()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["允许", "Allow"] where springboard.buttons[label].waitForExistence(timeout: 3) { springboard.buttons[label].tap() }
        XCTAssertTrue(app.buttons["plugin-ui-action-snapshot-code-review"].waitForExistence(timeout: 40), app.debugDescription)
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = "code-preview-" + name
        attachment.lifetime = .keepAlways
        add(attachment)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let look = env["PERSONA_EXTENSIONS_APPEARANCE"] ?? "light"
        let o = XCUIDevice.shared.orientation.isLandscape ? "-landscape" : ""
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir)
            .appending(path: "code-\(pad ? "ipad" : "iphone")\(o)-\(name)-\(UITestStyle.name)-\(look).png"))
    }

    @MainActor private func open(_ app: XCUIApplication, _ snapshot: String) {
        let row = app.buttons["plugin-ui-action-snapshot-" + snapshot]
        for _ in 0..<8 where !row.isHittable { app.swipeUp() }
        row.tap()
        XCTAssertTrue(app.descendants(matching: .any)["extension-ui-ready"].waitForExistence(timeout: 15), "\(snapshot) did not load")
    }

    @MainActor func testTheReviewPage() throws {
        let orientations: [UIDeviceOrientation] = pad ? [.landscapeLeft, .portrait] : [.portrait]
        for o in orientations {
            XCUIDevice.shared.orientation = o
            let app = try launch()
            open(app, "code-review")
            let tree = app.buttons["code-tree-src/upload/client.go"]
            XCTAssertTrue(app.buttons["code-tab-files"].waitForExistence(timeout: 10), "the tabs:\n\(app.debugDescription)")
            XCTAssertTrue(app.buttons["code-tab-files"].isHittable, "the tabs are on screen")
            // Side by side when the column is wide enough (iPad landscape, or portrait with the settings list folded):
            // the tree and the diffs at once. Otherwise a directory at a time, the diffs pushed.
            if tree.waitForExistence(timeout: 8) {
                XCTAssertTrue(app.staticTexts["src/upload/retry.go"].exists || app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'src/upload/retry.go'")).firstMatch.exists,
                              "the diff beside the tree:\n\(app.debugDescription)")
                shot(app, "review")
                tree.tap() // no tree action in the way: scrolls to client.go's diff (the extension's open_file answers a toast)
                shot(app, "review-file")
            } else {
                let dir = app.buttons["code-tree-src/upload"]
                XCTAssertTrue(dir.waitForExistence(timeout: 10), "the top of the tree:\n\(app.debugDescription)")
                shot(app, "review")
                dir.tap()
                XCTAssertTrue(tree.waitForExistence(timeout: 10), "src/upload's files:\n\(app.debugDescription)")
                shot(app, "review-dir")
                let back = app.buttons["code-back"]
                XCTAssertTrue(back.waitForExistence(timeout: 5), "the way back up:\n\(app.debugDescription)")
                back.tap()
                XCTAssertTrue(dir.waitForExistence(timeout: 5), "back at the top of the tree")
                let all = app.buttons["code-open-main"]
                XCTAssertTrue(all.waitForExistence(timeout: 5), "the way to the diffs:\n\(app.debugDescription)")
                all.tap()
                XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH 'src/upload/retry.go'")).firstMatch.waitForExistence(timeout: 10),
                              "the diffs:\n\(app.debugDescription)")
                shot(app, "review-diffs")
                XCTAssertTrue(app.buttons["code-back"].waitForExistence(timeout: 5), "the way back to the files")
            }
            // A line's number opens the inline comment box under it.
            let line = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH '新增第 3 行'")).firstMatch
            XCTAssertTrue(line.waitForExistence(timeout: 10), "a diff line:\n\(app.debugDescription)")
            // On its number (the gutter's first column).
            line.coordinate(withNormalizedOffset: CGVector(dx: 0, dy: 0.5)).withOffset(CGVector(dx: 14, dy: 0)).tap()
            XCTAssertTrue(app.descendants(matching: .any)["code-inline-comment"].waitForExistence(timeout: 5),
                          "the comment box:\n\(app.debugDescription)")
            shot(app, "review-comment")
            app.terminate()
        }
        XCUIDevice.shared.orientation = .portrait
    }

    @MainActor func testTheOverviewAndBrowsePages() throws {
        XCUIDevice.shared.orientation = pad ? .landscapeLeft : .portrait
        defer { XCUIDevice.shared.orientation = .portrait }
        let app = try launch()
        open(app, "code-overview")
        shot(app, "overview")
        app.swipeUp()
        app.swipeUp()
        shot(app, "overview-bottom")
        app.terminate()
        let browse = try launch()
        open(browse, "code-browse")
        // A narrow column has the tree first and the file one step in.
        let main = browse.buttons["code-open-main"]
        if main.waitForExistence(timeout: 5) {
            shot(browse, "browse")
            main.tap()
        }
        XCTAssertTrue(browse.descendants(matching: .any)["code-file"].waitForExistence(timeout: 10), "the file's code:\n\(browse.debugDescription)")
        shot(browse, "browse-file")
    }
}
