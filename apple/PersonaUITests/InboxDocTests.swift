import XCTest

/// Feishu documents in the inbox (docs/assistant/inbox.md「Docs: mentions in sheets, bases, and documents」): a row's status menu moves the status in the document, and
/// Send on the AI twin's draft makes its proposed move with the comment. Against scripts/mock/mock_hub.py with MOCK_DOCS=1
/// (PERSONA_MOCK_HUB), which records replies at /mock/sent and keeps the rows' status.
final class InboxDocTests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-inbox.kind", "mention", "-inbox.source", ""]
        app.launchPinned()
    }

    private func json(_ path: String) -> [String: Any] {
        guard let data = try? Data(contentsOf: URL(string: hub + path)!),
              let o = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return o
    }

    /// The hub's status of a row (`item`, `ref`).
    private func status(_ item: String, _ ref: String) -> String? {
        let items = json("/api/v1/inbox")["items"] as? [[String: Any]] ?? []
        guard let it = items.first(where: { $0["id"] as? String == item }),
              let rows = (it["doc"] as? [String: Any])?["rows"] as? [[String: Any]],
              let row = rows.first(where: { $0["ref"] as? String == ref }),
              let st = (row["status"] as? [[String: Any]])?.first else { return nil }
        return st["value"] as? String
    }

    private func wait(_ what: String, _ ok: @escaping () -> Bool) {
        let deadline = Date.now.addingTimeInterval(8)
        while !ok(), Date.now < deadline { usleep(250_000) }
        XCTAssertTrue(ok(), what)
    }

    @MainActor func testStatusMenuMovesTheRow() {
        let chip = app.buttons["状态：进行中，点按修改"].firstMatch
        XCTAssertTrue(chip.waitForExistence(timeout: 20), "no status chip:\n\(app.debugDescription)")
        chip.tap()
        let done = app.buttons["已完成"].firstMatch
        XCTAssertTrue(done.waitForExistence(timeout: 5), "the menu did not open")
        done.tap()
        wait("the hub's row did not move") { self.status("doc:n1:1", "tbl1/rec1") == "已完成" }
        XCTAssertTrue(app.buttons["状态：已完成，点按修改"].firstMatch.waitForExistence(timeout: 5), "the chip did not follow")
    }

    @MainActor func testSendingTheDraftMakesItsMove() {
        // The sheet item's draft proposes 「待验收」 → 「已完成」.
        XCTAssertTrue(app.staticTexts["状态：待验收 → 已完成 · 压测报告"].firstMatch.waitForExistence(timeout: 20),
                      "no proposed move on the draft:\n\(app.debugDescription)")
        let send = app.buttons.matching(identifier: "发送 AI 分身草拟的回复").allElementsBoundByIndex
            .first { $0.isHittable && $0.frame.minY > app.staticTexts["状态：待验收 → 已完成 · 压测报告"].firstMatch.frame.minY }
        XCTAssertNotNil(send)
        send?.tap()
        wait("the reply did not carry the move") {
            let sent = self.json("/mock/sent")["sent"] as? [[String: Any]] ?? []
            return sent.contains { ($0["inbox"] as? String) == "doc:n2:1" && (($0["changes"] as? [[String: Any]])?.first?["value"] as? String) == "已完成" }
        }
        XCTAssertEqual(status("doc:n2:1", "s1!4"), "已完成")
    }
}
