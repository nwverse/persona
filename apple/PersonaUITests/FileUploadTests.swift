import XCTest

/// Files sent to an agent (docs/protocol/protocol.md「Sending files」), against scripts/mock/mock_hub.py
/// (TEST_RUNNER_PERSONA_UPLOAD_HUB=http://127.0.0.1:<port>; MOCK_UPLOAD_KBPS slows it down enough to see the bar).
final class FileUploadTests: XCTestCase {
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"

    private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_UPLOAD_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_UPLOAD_HUB)")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openSession", "macbook/" + widget] + args
        app.launchPinned()
        return app
    }

    /// How many times the mock was asked for `path` (GET, any query).
    private func hits(_ path: String) -> Int {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_UPLOAD_HUB"],
              let data = try? Data(contentsOf: URL(string: hub + "/mock/hits")!),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return 0 }
        return (obj["hits"] as? [String: Int])?["GET " + path] ?? 0
    }

    /// Opening a chat at launch builds it once (#2061): a push before the list appeared previously
    /// and the chat was built three times, its items read three times and `-openUploadDemo` uploading twice.
    @MainActor func testOpeningAChatAtLaunchReadsItOnce() throws {
        let items = "/api/v1/sessions/macbook/\(widget)/items"
        let before = hits(items)
        let app = try launch([])
        let plus = app.buttons["shell-composer-add"].firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 15), "the chat did not open:\n\(app.debugDescription)")
        sleep(3) // a second and third build came within half a second of the first
        XCTAssertEqual(hits(items) - before, 1, "the chat's items read once")
    }

    /// The + menu offers Files next to Photos and Take Photo.
    @MainActor func testMenuHasFiles() throws {
        let app = try launch([])
        let plus = app.buttons["shell-composer-add"].firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 15), "no + button:\n\(app.debugDescription)")
        plus.tap()
        XCTAssertTrue(app.buttons["文件"].waitForExistence(timeout: 5), "no 文件 item:\n\(app.debugDescription)")
    }

    /// An upload shows its progress with a cancel button, then its path lands in the input box, unsent.
    @MainActor func testUploadInsertsThePath() throws {
        let app = try launch(["-openUploadDemo", "季度报告.pdf"])
        let cancel = app.buttons["取消上传 季度报告.pdf"]
        XCTAssertTrue(cancel.waitForExistence(timeout: 15), "no progress bar:\n\(app.debugDescription)")
        // Typing while it uploads: the path goes after what was typed (at the cursor), nothing is sent.
        let field = app.textFields.firstMatch.exists ? app.textFields.firstMatch : app.textViews.firstMatch
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap() // the end of a draft left by an earlier run
        let left = (field.value as? String ?? "").count
        if left > 0 { field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: left + 2)) }
        field.typeText("look at")
        XCTAssertEqual(field.value as? String, "look at", "typing alone")
        let inserted = NSPredicate { _, _ in ((field.value as? String) ?? "").contains("/tmp/季度报告-") }
        expectation(for: inserted, evaluatedWith: nil)
        waitForExpectations(timeout: 90)
        XCTAssertFalse(cancel.exists, "the bar goes once the file is there")
        let value = field.value as? String ?? ""
        XCTAssertTrue(value.hasPrefix("look at /tmp/季度报告-") && value.hasSuffix(".pdf "), value)
        // Drafts are kept: leave the box empty for the next test on this chat (VideoSendTests sends what is in it),
        // and let the draft be saved before the app goes (`AppModel.saveDrafts` waits a moment).
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: value.count + 2))
        sleep(2)
    }
}
