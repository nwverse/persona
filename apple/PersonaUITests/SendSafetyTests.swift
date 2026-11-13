import XCTest

/// Answers and sends that must not land in a terminal that changed (docs/protocol/protocol.md「Sending messages」「终端对话框」), against
/// scripts/mock/mock_hub.py started with MOCK_SCREEN_DIALOG=1 MOCK_PROMPT_CHANGED=1 MOCK_SEND_REFUSE=input_draft MOCK_RESTART=1
/// (TEST_RUNNER_PERSONA_SAFETY_HUB=http://127.0.0.1:<port>). MOCK_RESTART=1 keeps receipts: a repeated op id gets its first
/// answer, nothing done again (the hub also answers op_id_reused when the words changed; the mock replays either way).
final class SendSafetyTests: XCTestCase {
    private let review = "a1f3c2d4-0000-4000-8000-000000000006"
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"

    private func hub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_SAFETY_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_SAFETY_HUB)")
        }
        return hub
    }

    private func launch(_ hub: String, _ args: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        return app
    }

    /// GET /mock/<what> until test says it has what it waits for.
    private func poll(_ hub: String, _ what: String, until test: ([[String: Any]]) -> Bool) -> [[String: Any]] {
        let deadline = Date.now.addingTimeInterval(10)
        var list: [[String: Any]] = []
        while Date.now < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/" + what)!),
               let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
                list = obj[what] as? [[String: Any]] ?? []
                if test(list) { break }
            }
            usleep(300_000)
        }
        return list
    }

    /// A dialog the hub read is answered by option, never by guessed keys; a changed dialog says so.
    @MainActor func testScreenDialogAnswersByOption() throws {
        let hub = try hub()
        let app = launch(hub, ["-openSession", "macbook/" + review])
        let yes = app.buttons["Yes"]
        XCTAssertTrue(yes.waitForExistence(timeout: 15), "no dialog card:\n\(app.debugDescription)")
        yes.tap()
        let resolved = poll(hub, "resolved") { $0.contains { ($0["id"] as? String)?.hasPrefix("scr_") == true } }
        let r = try XCTUnwrap(resolved.last { ($0["id"] as? String)?.hasPrefix("scr_") == true }?["resolution"] as? [String: Any])
        XCTAssertEqual(r["option"] as? String, "1")
        XCTAssertNil(r["keys"], "keys are worked out on the computer, from the screen as it is then")
        let toast = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "对话框已经变了")).firstMatch
        XCTAssertTrue(toast.waitForExistence(timeout: 5), "no prompt_changed toast")
    }

    /// The computer's input box has a draft: the alert shows it, and Clear and Send sends the message again with
    /// clear_draft, under a new id: the hub answers the refused one from its receipt (#6425).
    @MainActor func testDraftRefusalClearsAndSends() throws {
        let hub = try hub()
        let text = "把测试也跑一遍 \(Int(Date.now.timeIntervalSince1970) % 100_000)" // its own: the mock keeps every run's sends
        let app = launch(hub, ["-openSession", "macbook/" + widget, "-openSendMessage", text])
        let clear = app.alerts.buttons["清空并发送"]
        XCTAssertTrue(clear.waitForExistence(timeout: 15), "no draft alert:\n\(app.debugDescription)")
        XCTAssertTrue(app.alerts.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "顺便把 README")).firstMatch.exists)
        clear.tap()
        let sent = poll(hub, "sent") { $0.contains { $0["text"] as? String == text && $0["clear_draft"] != nil } }
            .filter { $0["text"] as? String == text }
        let first = try XCTUnwrap(sent.first), retry = try XCTUnwrap(sent.last { $0["clear_draft"] != nil }, "Clear and Send never reached the computer")
        XCTAssertEqual(retry["clear_draft"] as? String, "顺便把 README 也更新一下")
        XCTAssertNotEqual(retry["client_msg_id"] as? String, first["client_msg_id"] as? String, "under the refused id the hub answers its receipt")
    }

    /// Resend after a refusal (the draft dealt with on the computer) asks the computer again, under a new id: under the
    /// old one the hub answered the stored refusal, quoting a draft that was gone (#6425).
    @MainActor func testResendAfterARefusalAsksAgain() throws {
        let hub = try hub()
        // Words of its own: a failed message stays in the outbox for a day, so an earlier run's may be in the chat too.
        let text = "再看一下 CI 的结果 \(Int(Date.now.timeIntervalSince1970) % 100_000)"
        let app = launch(hub, ["-openSession", "macbook/" + widget, "-openSendMessage", text])
        let notNow = app.alerts.buttons["先不发"]
        XCTAssertTrue(notNow.waitForExistence(timeout: 15), "no draft alert:\n\(app.debugDescription)")
        notNow.tap()
        try failedMark(app, nextTo: text).tap()
        let resend = app.buttons["重发"]
        XCTAssertTrue(resend.waitForExistence(timeout: 5), "no Resend:\n\(app.debugDescription)")
        resend.tap()
        let sent = poll(hub, "sent") { $0.filter { $0["text"] as? String == text }.count >= 2 }.filter { $0["text"] as? String == text }
        XCTAssertEqual(sent.count, 2, "the resend never reached the computer")
        XCTAssertNotEqual(sent.first?["client_msg_id"] as? String, sent.last?["client_msg_id"] as? String)
        XCTAssertTrue(app.alerts.buttons["先不发"].waitForExistence(timeout: 10), "no answer from the computer to the resend")
        app.alerts.buttons["先不发"].tap()
        try failedMark(app, nextTo: text).tap() // not left for the next run
        app.buttons["删除"].tap()
    }

    /// The failed mark beside the bubble saying `text`: the one nearest it up and down.
    @MainActor private func failedMark(_ app: XCUIApplication, nextTo text: String) throws -> XCUIElement {
        let bubble = app.staticTexts[text]
        XCTAssertTrue(bubble.waitForExistence(timeout: 5), "no bubble saying \(text):\n\(app.debugDescription)")
        let marks = app.buttons.matching(NSPredicate(format: "label == %@", "发送失败，重发或删除"))
        XCTAssertTrue(marks.firstMatch.waitForExistence(timeout: 5), "no failed mark:\n\(app.debugDescription)")
        let y = bubble.frame.midY
        return try XCTUnwrap(marks.allElementsBoundByIndex.min { abs($0.frame.midY - y) < abs($1.frame.midY - y) })
    }
}
