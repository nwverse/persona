import XCTest

final class CalmAttentionShots: XCTestCase {
    @MainActor func testDemoApprovalScenes() throws {
        continueAfterFailure = false
        let app = XCUIApplication()
        let scenes: [(String, String?, Bool)] = [
            ("agents", nil, false),
            ("plan", "studio/a1f3c2d4-0000-4000-8000-000000000005", false),
            ("permission", "macbook/a1f3c2d4-0000-4000-8000-000000000001", true),
        ]
        for (name, session, permission) in scenes {
            app.launchArguments = ["-demo.on", "YES", "-openWelcome", "YES", "-openChatsFresh", "YES",
                                   "-openAgentsFresh", "YES", "-dev.openChat", "", "-tab", "dev",
                                   "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
            if let session { app.launchArguments += ["-openSession", session] }
            if permission { app.launchArguments += ["-showSamplePermission", "YES"] }
            app.launchPinned()
            if session != nil {
                XCTAssertTrue(app.scrollViews["pending-cards"].waitForExistence(timeout: 20))
                XCTAssertTrue(app.staticTexts[name == "plan" ? "计划等你审批" : "需要你授权"].firstMatch.exists)
            } else {
                XCTAssertTrue(app.buttons["agents.filter.all"].waitForExistence(timeout: 20))
            }
            let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons["Allow"]
            if allow.exists { allow.tap() }
            Thread.sleep(forTimeInterval: 2)
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "ios-" + name
            shot.lifetime = .keepAlways
            add(shot)
            app.terminate()
        }
    }
}
