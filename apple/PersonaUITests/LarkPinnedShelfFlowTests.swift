import XCTest

/// Exercises the real list and socket against an isolated mock, never a Feishu account.
final class LarkPinnedShelfFlowTests: XCTestCase {
    private func fixture(_ change: [String: Any]? = nil) async throws -> [String: Any] {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"],
              let url = URL(string: hub + "/test/lark-pins/state") else { throw XCTSkip("needs pin shelf mock") }
        var request = URLRequest(url: url)
        if let change {
            request.httpMethod = "POST"
            request.httpBody = try JSONSerialization.data(withJSONObject: change)
        }
        let (data, response) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }

    @MainActor private func open(count: Int = 18, large: Bool = false) async throws -> XCUIApplication {
        _ = try await fixture(["count": count, "account": "pin-account-one", "offline": false, "reverse": false, "unread": 3, "newest": false])
        continueAfterFailure = false
        let hub = try XCTUnwrap(ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"])
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "feishu", "-demo.on", "NO",
            "-integrations.lark.enabled", "YES"]
        if large { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        else { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryL"] }
        app.launchPinned()
        XCTAssertTrue(app.buttons["shell-feishu"].firstMatch.waitForExistence(timeout: 20))
        app.buttons["shell-feishu"].firstMatch.tapCenter()
        XCTAssertTrue(chats(app).firstMatch.waitForExistence(timeout: 20))
        return app
    }

    /// Rows of the list under the shelf (the shelf's own buttons are `feishu-pin-…`).
    @MainActor private func chats(_ app: XCUIApplication) -> XCUIElementQuery {
        app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "feishu-chat-"))
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name; attachment.lifetime = .keepAlways; add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }

    @MainActor private func wait(_ element: XCUIElement, _ predicate: String, _ value: String) {
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: NSPredicate(format: predicate, value), object: element)], timeout: 10), .completed)
    }

    @MainActor private func pull(_ app: XCUIApplication) {
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
            .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.82)))
    }

    /// #2823: a search reads on into pages not loaded yet (the fixture has 83 chats, 60 a page: 「会话 75」 is on the
    /// second), and one that matches nothing says so instead of leaving the page blank.
    @MainActor func testSearchReadsOnIntoLaterPagesAndSaysWhenNothingMatches() async throws {
        let app = try await open()
        let field = app.searchFields.firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 10), "no search field")
        field.tap()
        field.typeText("会话 75")
        XCTAssertTrue(app.buttons["feishu-chat-pin-75"].firstMatch.waitForExistence(timeout: 15),
                      "a chat on the second page was not found")
        shot(app, "lark-search-later-page")
        field.buttons.firstMatch.tap() // clear
        field.typeText("没有这个会话")
        XCTAssertTrue(app.staticTexts["没有找到「没有这个会话」"].firstMatch.waitForExistence(timeout: 15), "no empty result")
        XCTAssertEqual(chats(app).count, 0)
        shot(app, "lark-search-nothing")
    }

    @MainActor func testExpandPullCollapseAndOpenSharedChat() async throws {
        let app = try await open()
        let toggle = app.buttons["feishu-pins-toggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        wait(toggle, "label == %@", "展开置顶会话")
        shot(app, "pins-compact")
        XCTAssertLessThanOrEqual(chats(app).firstMatch.frame.height, 66, "normal rows retain the compact 64 pt layout")
        // Pinned chats are on the shelf and in the list too, where their latest message puts them (as in Feishu):
        // the newest chat is a pinned one, not the hub's pin order.
        XCTAssertTrue(app.buttons["feishu-pin-f2"].exists)
        XCTAssertEqual(chats(app).element(boundBy: 0).identifier, "feishu-chat-pin-1")
        XCTAssertEqual(chats(app).element(boundBy: 1).identifier, "feishu-chat-f1")
        toggle.tapCenter()
        wait(toggle, "label == %@", "收起置顶会话")
        shot(app, "pins-expanded")
        let before = try await fixture()["requests"] as? Int
        pull(app)
        wait(toggle, "label == %@", "收起置顶会话")
        let after = try await fixture()["requests"] as? Int
        XCTAssertEqual(after, before, "pulling expanded pins must not refresh")
        XCTAssertFalse(app.staticTexts["松开刷新消息"].exists)
        toggle.tapCenter()
        wait(toggle, "label == %@", "展开置顶会话")
        pull(app)
        wait(toggle, "label == %@", "收起置顶会话")
        // The shelf passes above the viewport; reversing immediately must not leave it expanded.
        app.swipeUp(velocity: .slow)
        app.swipeDown(velocity: .slow)
        for _ in 0..<4 where !toggle.isHittable { app.swipeDown(velocity: .slow) }
        wait(toggle, "label == %@", "展开置顶会话")
        shot(app, "pins-return-collapsed")
        app.buttons["feishu-pin-f2"].tapCenter()
        XCTAssertTrue(app.textFields["lark.box"].waitForExistence(timeout: 15))
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.01, dy: 0.5))
            .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.9, dy: 0.5)))
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        shot(app, "pins-chat-return")
        app.terminate()
    }

    @MainActor func testLiveChangesOfflineCacheAndAccountIsolation() async throws {
        let app = try await open(count: 1)
        let first = app.buttons["feishu-pin-f2"]
        XCTAssertTrue(first.waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["feishu-pins-toggle"].exists)
        shot(app, "pins-single")
        _ = try await fixture(["unread": 8])
        wait(first, "value == %@", "8 条未读")
        _ = try await fixture(["count": 18])
        let toggle = app.buttons["feishu-pins-toggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        _ = try await fixture(["reverse": true])
        XCTAssertTrue(app.buttons["feishu-pin-pin-15"].waitForExistence(timeout: 10))
        shot(app, "pins-live-reordered")
        _ = try await fixture(["offline": true])
        app.terminate(); app.launchPinned()
        XCTAssertTrue(app.buttons["feishu-pin-pin-15"].waitForExistence(timeout: 20))
        shot(app, "pins-offline-cache")
        _ = try await fixture(["offline": false, "account": "pin-account-two"])
        XCTAssertTrue(app.buttons["feishu-pin-other-pin-15"].waitForExistence(timeout: 15))
        XCTAssertFalse(app.buttons["feishu-pin-pin-15"].exists)
        XCTAssertFalse(app.buttons["feishu-chat-f2"].exists)
        shot(app, "pins-new-account")
        _ = try await fixture(["count": 0])
        XCTAssertTrue(toggle.waitForNonExistence(timeout: 10))
        XCTAssertFalse(app.buttons["feishu-pin-other-pin-15"].exists)
        // Unpinned chats go back into the list.
        XCTAssertTrue(app.buttons["feishu-chat-other-f2"].waitForExistence(timeout: 10))
        shot(app, "pins-empty")
        app.terminate()
    }

    @MainActor func testLargeTextAndLongShelf() async throws {
        let app = try await open(count: 80, large: true)
        let toggle = app.buttons["feishu-pins-toggle"]
        XCTAssertTrue(toggle.waitForExistence(timeout: 10))
        shot(app, "pins-large-compact")
        toggle.tapCenter()
        wait(toggle, "label == %@", "收起置顶会话")
        shot(app, "pins-large-expanded")
        // A long grid remains scrollable, and the control remains a single-line label.
        app.swipeUp(velocity: .slow)
        shot(app, "pins-large-scrolled")
        app.terminate()
    }

    @MainActor func testLiveUpdateKeepsTheRowBeingRead() async throws {
        let app = try await open()
        app.swipeUp(velocity: .slow)
        let rows = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH %@", "feishu-chat-")).allElementsBoundByIndex
        let row = try XCTUnwrap(rows.first { $0.frame.midY > app.frame.height * 0.4 && $0.frame.midY < app.frame.height * 0.6 && $0.isHittable })
        let top = row.frame.minY
        shot(app, "pins-reader-before")
        _ = try await fixture(["count": 0, "newest": true])
        wait(row, "label CONTAINS %@", "实时更新")
        // Observe the old row rather than scrolling to the newly arrived one.
        let unchanged = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
            row.exists && abs(row.frame.minY - top) < 2
        }, object: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [unchanged], timeout: 10), .completed)
        shot(app, "pins-reader-after")
        XCTAssertEqual(row.frame.minY, top, accuracy: 2)
        app.terminate()
    }
}
