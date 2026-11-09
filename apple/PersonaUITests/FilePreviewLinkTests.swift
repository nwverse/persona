import XCTest

final class FilePreviewLinkTests: XCTestCase {
    private var hub: String!
    private let session = "macbook/a1f3c2d4-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_FILE_LINK_HUB"] else {
            throw XCTSkip("需要 MOCK_FILES=1 MOCK_FILE_LINKS=1 MOCK_PORTS=1 的 mock hub")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ extra: [String] = []) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", session, "-openAgentsFresh", "YES", "-openChatsFresh", "YES"] + extra
        app.launchPinned()
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        guard let directory = env["PERSONA_SHOTS"] else { return }
        let style = env["PERSONA_UI_STYLE"] ?? "glass"
        let appearance = env["PERSONA_FILE_LINK_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: directory + "/file-links-\(style)-\(appearance)-\(name).png"))
    }

    @MainActor private func link(_ name: String, in app: XCUIApplication) -> XCUIElement {
        let element = app.links[name].firstMatch
        let file = app.scrollViews["file-markdown"]
        XCTAssertTrue(file.waitForExistence(timeout: 15), app.debugDescription)
        for _ in 0..<6 {
            if element.exists && element.isHittable { return element }
            file.swipeUp()
        }
        XCTFail("找不到可点击的 Markdown 链接：\n\(app.debugDescription)")
        return element
    }

    @MainActor private func assertReturned(_ app: XCUIApplication, y: CGFloat, name: String) throws {
        let close = app.buttons["file-close"]
        XCTAssertTrue(close.waitForExistence(timeout: 10))
        XCTAssertTrue(close.isHittable, "关闭链接后应回到文件预览")
        let original = app.links[name].firstMatch
        XCTAssertTrue(original.exists && original.isHittable, "返回后原文件位置不再可见")
        XCTAssertEqual(original.frame.minY, y, accuracy: 3, "返回后文件滚动位置改变")
        XCTAssertFalse(app.buttons["link-close"].exists)
    }

    @MainActor func testConversationFileLinkOpensAbovePreview() throws {
        let app = launch(["-tab", "dev"])
        let path = app.links["/Users/me/code/weather/README.md"].firstMatch // linked in the message's text (#1706)
        XCTAssertTrue(path.waitForExistence(timeout: 20), app.debugDescription)
        path.tap()
        XCTAssertTrue(app.buttons["file-close"].waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertTrue(app.scrollViews["file-markdown"].waitForExistence(timeout: 15))
        app.scrollViews["file-markdown"].swipeUp()
        let webLink = link("示例网页", in: app)
        let originalY = webLink.frame.minY
        try shot(app, "file")
        webLink.tap()
        let closeLink = app.buttons["link-close"]
        XCTAssertTrue(closeLink.waitForExistence(timeout: 10), "没有立即显示链接抽屉：\n\(app.debugDescription)")
        XCTAssertTrue(closeLink.isHittable, "链接抽屉被文件预览挡住")
        XCTAssertTrue(app.webViews.firstMatch.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["file-close"].isHittable)
        try shot(app, "web")
        closeLink.tap()
        try assertReturned(app, y: originalY, name: "示例网页")
        try shot(app, "returned")
        app.buttons["file-close"].tap()
        XCTAssertFalse(app.buttons["file-close"].waitForExistence(timeout: 2))
        XCTAssertFalse(app.buttons["link-close"].waitForExistence(timeout: 2), "关闭文件后又补弹了旧链接")
    }

    @MainActor func testFilesPaneLinkLoadsForwardedPageAndReturns() throws {
        let app = launch(["-openPane", "files"])
        let readme = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "README.md")).firstMatch
        XCTAssertTrue(readme.waitForExistence(timeout: 20), app.debugDescription)
        readme.tap()
        XCTAssertTrue(app.buttons["file-close"].waitForExistence(timeout: 15))
        XCTAssertTrue(app.scrollViews["file-markdown"].waitForExistence(timeout: 15))
        app.scrollViews["file-markdown"].swipeUp()
        let preview = link("开发预览", in: app)
        let originalY = preview.frame.minY
        preview.tap()
        let closeLink = app.buttons["link-close"]
        XCTAssertTrue(closeLink.waitForExistence(timeout: 10))
        XCTAssertTrue(closeLink.isHittable)
        XCTAssertTrue(app.webViews.staticTexts["明天有雨"].waitForExistence(timeout: 20), "开发预览未通过 hub 加载：\n\(app.debugDescription)")
        try shot(app, "forwarded")
        closeLink.tap()
        try assertReturned(app, y: originalY, name: "开发预览")
        preview.tap()
        XCTAssertTrue(app.buttons["link-close"].waitForExistence(timeout: 10), "同一链接无法再次打开")
        app.buttons["link-close"].tap()
        try assertReturned(app, y: originalY, name: "开发预览")
        app.buttons["file-close"].tap()
        XCTAssertTrue(readme.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["link-close"].waitForExistence(timeout: 2))
    }
}
