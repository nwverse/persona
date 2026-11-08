import XCTest

/// Whole-project linking uses the native group menu and the shared picker, against the isolated mock hub.
final class TodoGroupTests: XCTestCase {
    @MainActor func testCreateLinkAndRemoveAProject() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        continueAfterFailure = false
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-dev.grouping", "space",
                               "-dev.folded", "macbook/w1\nstudio/w2\nstudio/w3\nserver/w1"]
        app.launchPinned()
        let working = app.buttons["agents.filter.working"]
        XCTAssertTrue(working.waitForExistence(timeout: 30))
        working.tap()
        let menu = app.buttons["shop-api 的更多操作"]
        XCTAssertTrue(menu.waitForExistence(timeout: 15))
        menu.tap()
        let attach = app.buttons["挂到待办…"]
        XCTAssertTrue(attach.waitForExistence(timeout: 10))
        attach.tap()
        let picker = app.collectionViews["todo.link.list"]
        XCTAssertTrue(picker.waitForExistence(timeout: 10))
        let create = app.buttons["新建待办…"]
        for _ in 0..<5 where !create.exists || !create.isHittable {
            picker.swipeUp(velocity: .fast)
        }
        XCTAssertTrue(create.waitForExistence(timeout: 10))
        create.tap()
        let title = "Project link " + String(UUID().uuidString.prefix(8))
        let field = app.textFields["输入新待办标题…"]
        XCTAssertTrue(field.waitForExistence(timeout: 10))
        field.tap()
        field.typeText(title)
        app.buttons["创建并挂上"].tap()
        let tag = app.buttons["todo.space.server/w1"]
        XCTAssertTrue(tag.waitForExistence(timeout: 15))
        XCTAssertEqual(tag.label, "待办：" + title)
        XCTAssertTrue(try linkedProject(hub, title: title))
        menu.tap()
        XCTAssertTrue(app.buttons["改挂到其他待办…"].waitForExistence(timeout: 10))
        app.buttons["移出待办"].tap()
        let disappeared = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: tag)
        XCTAssertEqual(XCTWaiter.wait(for: [disappeared], timeout: 15), .completed)
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline, try linkedProject(hub, title: title) { usleep(100_000) }
        XCTAssertFalse(try linkedProject(hub, title: title))
    }

    private func linkedProject(_ hub: String, title: String) throws -> Bool {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/todos")!)
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let todos = try XCTUnwrap(body["todos"] as? [[String: Any]])
        let todo = try XCTUnwrap(todos.first { $0["title"] as? String == title })
        let spaces = todo["spaces"] as? [[String: Any]] ?? []
        return spaces.contains { $0["machine_id"] as? String == "server" && $0["space"] as? String == "shop-api" }
    }
}
