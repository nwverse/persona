import XCTest

/// An assistant row's text in the Inbox (#9041), on scripts/mock/mock_hub.py with MOCK_ASSISTANT_INBOX=1
/// (PERSONA_MOCK_HUB): a scheduled task's briefing opens to its text drawn as Markdown, folded with Expand while long,
/// and no line of the detail shows a Markdown mark; a row from an older hub (`/mock/assistant_inbox?mode=before`: no
/// `body`, the Markdown run into one line) draws its summary as Markdown and its Timeline message without marks. It sets
/// the mode itself and puts it back. Light and dark are attached, and kept in TEST_RUNNER_PERSONA_SHOTS=<dir> when set
/// (TEST_RUNNER_PERSONA_PHASE names them).
final class InboxAssistantMarkdownUITests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor func testBriefingReadsAsMarkdown() throws {
        let env = ProcessInfo.processInfo.environment
        guard let hub = env["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_ASSISTANT_INBOX=1 (PERSONA_MOCK_HUB)")
        }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        defer { _ = try? Data(contentsOf: URL(string: hub + "/mock/assistant_inbox?mode=1")!) }
        for (mode, name) in [("1", "hub"), ("before", "older-hub")] {
            _ = try Data(contentsOf: URL(string: hub + "/mock/assistant_inbox?mode=" + mode)!)
            for scheme in ["light", "dark"] {
                let app = XCUIApplication()
                app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "", "-inbox.source", "",
                                       "-openInboxItem", "assistant:s3", "-ui.scheme", scheme]
                app.launchPinned()
                func text(_ s: String) -> XCUIElement {
                    app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
                }
                let marks = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "**")).firstMatch
                let body = app.descendants(matching: .any)["inbox.body"].firstMatch
                XCTAssertTrue(body.waitForExistence(timeout: 20), "\(name) \(scheme): no Markdown text\n\(app.debugDescription)")
                // Opened is kept for the row (an earlier run opened it): folded again first, as a new one shows.
                let collapse = app.buttons["message.collapse"].firstMatch
                if collapse.exists {
                    collapse.tap()
                    XCTAssertTrue(app.buttons["message.expand"].firstMatch.waitForExistence(timeout: 5), "\(name) \(scheme): Collapse did nothing")
                }
                XCTAssertTrue(text("今天的课").exists, "\(name) \(scheme): the briefing's words are not shown")
                XCTAssertFalse(marks.exists, "\(name) \(scheme): a Markdown mark is shown: \(marks.label)")
                shot(app, "assistant-body-\(env["PERSONA_PHASE"] ?? "after")-\(name)-\(scheme)", env)

                if mode == "1" {
                    // The whole briefing runs past its first lines: folded, Expand shows the rest.
                    let expand = app.buttons["message.expand"].firstMatch
                    XCTAssertTrue(expand.exists, "\(scheme): a long briefing is not folded")
                    expand.tap()
                    XCTAssertTrue(app.buttons["message.collapse"].firstMatch.waitForExistence(timeout: 5), "\(scheme): Expand did nothing")
                    app.swipeUp()
                    XCTAssertTrue(text("校园卡余额").waitForExistence(timeout: 5), "\(scheme): the rest did not show")
                    shot(app, "assistant-body-\(env["PERSONA_PHASE"] ?? "after")-\(name)-\(scheme)-expanded", env)
                }

                // The Timeline's received entry opens to its message: words, not marks.
                let chain = app.descendants(matching: .any)["inbox.chain"].firstMatch
                XCTAssertTrue(chain.waitForExistence(timeout: 5), "\(name) \(scheme): no Timeline")
                let open = chain.buttons.matching(NSPredicate(format: "label == %@", "展开")).firstMatch
                XCTAssertTrue(open.exists, "\(name) \(scheme): the received entry cannot open\n\(app.debugDescription)")
                open.tap()
                XCTAssertTrue(text("数据结构辅导课").waitForExistence(timeout: 5), "\(name) \(scheme): the message did not open")
                app.swipeUp() // the opened message above the bottom bar
                XCTAssertFalse(marks.exists, "\(name) \(scheme): a Markdown mark is shown: \(marks.label)")
                shot(app, "assistant-body-\(env["PERSONA_PHASE"] ?? "after")-\(name)-\(scheme)-timeline", env)
                app.terminate()
            }
        }
    }

    @MainActor private func shot(_ app: XCUIApplication, _ file: String, _ env: [String: String]) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = file
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(file + ".png"))
        }
    }
}
