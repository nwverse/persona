import XCTest

/// Uses only scripts/mock/turn_folding_hub.py and a fresh synthetic pairing.
final class AppleTurnFoldingTests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private let finalText = "示例变更已核对完成。最终结果保留在这里，所有工具和中间说明都可以展开查看。"
    private let liveText = "正在运行最后一项检查，工具过程已收起。"

    override func setUpWithError() throws {
        guard let url = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("requires the isolated synthetic turn-folding mock")
        }
        hub = url
        continueAfterFailure = false
    }

    private func launch(_ suffix: String, dev: Bool = false, fresh: Bool = true) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-openAllWork", "NO"]
        if fresh { app.launchArguments += ["-openChatsFresh", "YES"] }
        if dev {
            app.launchArguments += ["-app.tab", "chats", "-tab", "dev", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000006"]
        } else {
            app.launchArguments += ["-app.tab", "assistant", "-tab", "assistant", "-openAssistant", "server/b2e4d6f8-0000-4000-8000-" + suffix]
        }
        app.launchPinned()
        dismissNotifications(wait: true)
    }

    private func dismissNotifications(wait: Bool = false) {
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons
            .matching(NSPredicate(format: "label IN %@", ["Allow", "允许"])).firstMatch
        if wait ? allow.waitForExistence(timeout: 3) : allow.exists { allow.tap() }
    }

    private func text(_ value: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", value)).firstMatch
    }

    private func shot(_ name: String) throws {
        dismissNotifications()
        XCTAssertFalse(XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch.exists,
                       "system prompts must not cover screenshot evidence")
        let image = app.screenshot()
        let attachment = XCTAttachment(screenshot: image)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"] ?? "local"
        try image.pngRepresentation.write(to: URL(fileURLWithPath: "/tmp/persona-2705-\(name)-\(suffix).png"))
    }

    private func assertFolded(_ narration: String) {
        XCTAssertTrue(text(narration).waitForExistence(timeout: 25))
        dismissNotifications()
        let work = app.buttons["work-head"].firstMatch
        XCTAssertTrue(work.waitForExistence(timeout: 10))
        XCTAssertTrue(work.isHittable, "the summary is visible and not covered by a system prompt")
        XCTAssertTrue(text(narration).isHittable)
        XCTAssertEqual(work.label, "展开过程")
        XCTAssertFalse(app.buttons["compact-divider"].exists)
        for tool in ["github read", "github diff", "task review", "supervision finish"] {
            XCTAssertFalse(text(tool).exists, "no tool row escapes the collapsed summary: \(tool)")
        }
        XCTAssertFalse(text("先读取示例仓库").exists)
        XCTAssertFalse(text("已读取仓库，正在核对示例差异").exists)
    }

    func testRunningAndCompletedDefaultToCollapsedInAssistantAndDev() throws {
        launch("000000000001")
        assertFolded(liveText)
        try shot("running")
        app.buttons["work-head"].firstMatch.tap()
        XCTAssertTrue(text("github read").waitForExistence(timeout: 5))
        app.buttons["work-head"].firstMatch.tap()
        assertFolded(liveText)
        launch("000000000002")
        assertFolded(finalText)
        try shot("completed")
        launch("", dev: true)
        assertFolded(liveText)
        try shot("dev-running")
    }

    func testCompactedAndFailedToolsCanBeExpandedCollapsedAndReloaded() throws {
        launch("000000000003")
        assertFolded(finalText)
        try shot("compacted-failed-collapsed")
        let work = app.buttons["work-head"].firstMatch
        work.tap()
        XCTAssertTrue(text("github read").waitForExistence(timeout: 5))
        XCTAssertTrue(app.buttons["compact-divider"].waitForExistence(timeout: 5))
        // The lazy list may need scrolling before the post-compaction rows are realized.
        for _ in 0..<5 where !text("supervision finish").exists { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(text("task review").exists)
        XCTAssertTrue(text("supervision finish").exists)
        try shot("compacted-failed-expanded")
        let bottom = app.buttons["work-fold-bottom"].firstMatch
        for _ in 0..<5 where !bottom.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(bottom.isHittable)
        bottom.tap()
        assertFolded(finalText)
        try shot("compacted-failed-recollapsed")
        app.terminate()
        launch("000000000003", fresh: false)
        assertFolded(finalText)
        app.buttons["work-head"].firstMatch.tap()
        XCTAssertTrue(app.buttons["compact-divider"].waitForExistence(timeout: 5))
    }
}
