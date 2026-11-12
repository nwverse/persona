import XCTest

/// A connector's items in the inbox (GitHub…, docs/plans/plan-connectors.md「App side」): the generic row with the
/// connector's tag, mark and thread, a filter for the source, the detail (a pull request as GitHub's thread) and
/// Open in GitHub, and AI Draft Reply whose Send comments on the thread through the hub. Against scripts/mock/mock_hub.py with
/// MOCK_GITHUB_INBOX=1 (PERSONA_MOCK_HUB), which lists them only to apps asking ?sources=all and records replies at
/// /mock/sent.
final class InboxConnectorTests: XCTestCase {
    @MainActor func testAGitHubItemReadsAndReplies() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "", "-inbox.source", ""]
        app.launchPinned()
        func shot(_ name: String) {
            let a = XCTAttachment(screenshot: app.screenshot())
            a.name = "\(name) \(UITestStyle.name)"
            a.lifetime = .keepAlways
            add(a)
        }
        func text(_ s: String) -> XCUIElement {
            app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
        }

        let row = text("Add retries to the example uploader")
        guard row.waitForExistence(timeout: 20) else {
            XCTFail("no GitHub item (does the app ask for ?sources=all?):\n\(app.debugDescription)")
            return
        }
        XCTAssertTrue(text("请你 review").exists, "no connector tag")
        XCTAssertTrue(text("example-org/example-repo#12 · alice：请看一下重试的退避时间").exists, "no thread line")
        XCTAssertTrue(app.buttons["GitHub"].exists || text("GitHub").exists, "no GitHub filter")
        shot("inbox github row")

        row.tap()
        XCTAssertTrue(app.buttons["在 GitHub 打开"].waitForExistence(timeout: 10), "no 在 GitHub 打开:\n\(app.debugDescription)")
        XCTAssertTrue(text("上传失败时按指数退避重试三次").waitForExistence(timeout: 10), "the thread's text did not load")
        // A pull request opens as GitHub's thread (#1055, GitHubThreadTests), with its own box at the bottom.
        XCTAssertTrue(app.descendants(matching: .any)["thread.box"].exists, "no box to comment")
        shot("inbox github detail")

        let draft = app.buttons["AI 草拟回复"].firstMatch
        XCTAssertTrue(draft.waitForExistence(timeout: 5))
        draft.tap()
        XCTAssertTrue(app.navigationBars["评论 example-repo#12"].waitForExistence(timeout: 10), "no reply sheet:\n\(app.debugDescription)")
        let editor = app.textViews.firstMatch
        let drafted = NSPredicate(format: "value CONTAINS %@", "收到，我看一下") // the mock's inbox_draft
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: drafted, evaluatedWith: editor)], timeout: 10), .completed, "no draft")
        shot("inbox github reply")
        app.navigationBars.buttons["发送"].tap()

        let deadline = Date.now.addingTimeInterval(8)
        var sent = ""
        while Date.now < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/sent")!), let s = String(data: data, encoding: .utf8),
               s.contains("github:review12") {
                sent = s
                break
            }
            usleep(250_000)
        }
        XCTAssertTrue(sent.contains("收到，我看一下"), "the reply did not reach the hub: \(sent)")
    }
}
