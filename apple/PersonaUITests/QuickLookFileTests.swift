import XCTest

/// Office and RTF files open in QuickLook inside the file page, and the Files tab shows their thumbnails (#1293).
///   PORT=18785 MOCK_AGENTS=1 MOCK_OFFICE=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18785 xcodebuild test … -only-testing:PersonaUITests/QuickLookFileTests
/// Both styles (TEST_RUNNER_PERSONA_UI_STYLE=beautiful).
final class QuickLookFileTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] != nil else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_AGENTS=1 MOCK_OFFICE=1 (PERSONA_MOCK_HUB)")
        }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "quicklook \(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor private func row(_ name: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", name)).firstMatch
    }

    @MainActor private func hittable(_ element: XCUIElement, timeout: TimeInterval) -> Bool {
        let deadline = Date.now.addingTimeInterval(timeout)
        while Date.now < deadline {
            if element.exists && element.isHittable { return true }
            usleep(200_000)
        }
        return false
    }

    @MainActor func testDocumentsPreviewInQuickLook() {
        let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"]!
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001", "-openPane", "files"]
        app.launchPinned()
        let docs = row("docs")
        XCTAssertTrue(docs.waitForExistence(timeout: 20), "the files did not open")
        docs.tap()
        XCTAssertTrue(row("季度报告.docx").waitForExistence(timeout: 10), "no documents in docs/")
        sleep(4) // the thumbnails: fetched, then drawn by QuickLook
        shot("list")
        for (name, tag) in [("季度报告.docx", "docx"), ("路线图.pptx", "pptx"), ("发布说明.rtf", "rtf")] {
            row(name).tap()
            let preview = app.descendants(matching: .any)["file-quicklook"]
            XCTAssertTrue(preview.waitForExistence(timeout: 20), "\(name) did not open in QuickLook")
            // QuickLook draws in its own process, a page at a time, and shows nothing we can wait on: seconds on a busy
            // simulator, the first document of a launch the slowest.
            sleep(name == "季度报告.docx" ? 12 : 8)
            shot(tag)
            if tag == "docx" {
                app.buttons["更多"].firstMatch.tap()
                XCTAssertTrue(app.buttons["分享"].waitForExistence(timeout: 5), "no 分享 in the file's menu")
                shot("menu")
                app.buttons["分享"].tap()
                sleep(2)
                shot("share")
                app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.9)).tap() // below the share sheet: it closes
                XCTAssertTrue(hittable(app.buttons["file-close"], timeout: 5), "the share sheet did not close")
            }
            app.buttons["file-close"].tap()
            XCTAssertTrue(preview.waitForNonExistence(timeout: 10), "\(name) did not close")
            XCTAssertTrue(row(name).waitForExistence(timeout: 10))
        }
    }
}
