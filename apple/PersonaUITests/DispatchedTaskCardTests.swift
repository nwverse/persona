import XCTest

/// Real iPhone navigation against Fixtures/dispatch_hub.py: each dispatch names only its own tasks. A live Agent is
/// pushed on the Assistant stack, Back returns to the same conversation, and a task still starting opens its own
/// detail. PERSONA_MOCK_HUB supplies the loopback URL; PERSONA_SHOTS / PERSONA_SHOTS_APPEARANCE save light/dark evidence.
final class DispatchedTaskCardTests: XCTestCase {
    private var hub: String!

    override func setUp() async throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs Fixtures/dispatch_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
        try await fixture("reset")
    }

    @MainActor private func launch() -> XCUIApplication {
        let app = XCUIApplication()
        let url = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", url, "-openChatsFresh", "YES", "-holdChromeOpen", "YES", "-ui.scheme", "system", "-tab", "assistant", "-dev.openChat", "",
                               "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001"]
        app.launchEnvironment["PERSONA_UI_TEST_SESSION"] = "1"
        app.launchPinned()
        XCTAssertTrue(app.buttons["action-bar.running"].waitForExistence(timeout: 20), app.debugDescription)
        return app
    }

    /// The icon confirmation is a SpringBoard window, not part of the app's accessibility tree. Dismiss only that
    /// informational confirmation; never automatically answer a permission request for this screenshot fixture.
    @MainActor private func dismissIconConfirmation() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let alert = springboard.alerts.firstMatch
        guard alert.waitForExistence(timeout: 1) else { return }
        let iconText = alert.staticTexts.matching(NSPredicate(format: "label CONTAINS '图标' OR label CONTAINS[c] 'icon'")).firstMatch
        guard iconText.exists else { return }
        for label in ["好", "OK"] where alert.buttons[label].exists {
            alert.buttons[label].tap()
            XCTAssertTrue(alert.waitForNonExistence(timeout: 5), "icon confirmation stayed over the screenshot")
            return
        }
    }

    @MainActor private func attach(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        dismissIconConfirmation()
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let appearance = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/iphone-\(appearance)-\(name).png"))
    }

    /// The first card can sit under the Assistant tabs. `isHittable` is then true while a tap lands on the tab, so
    /// keep scrolling until the whole card is between the tab bar and the dock. Their frames come from the app.
    @MainActor private func link(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        let link = app.buttons["dispatch.task.\(id)"]
        let tabs = app.buttons["shell-assistant"]
        let dock = app.otherElements["action-bar"]
        let deadline = Date.now.addingTimeInterval(30)
        while Date.now < deadline {
            if link.exists {
                let frame = link.frame
                let top = tabs.exists ? tabs.frame.maxY : app.frame.minY
                let bottom = dock.exists ? dock.frame.minY : app.frame.maxY
                if link.isHittable, frame.minY >= top, frame.maxY <= bottom { return link }
                if frame.minY < top { app.swipeDown(velocity: .slow) } else { app.swipeUp(velocity: .slow) }
            } else {
                app.swipeDown(velocity: .slow)
            }
            usleep(400_000)
        }
        XCTFail("no dispatch link for \(id):\n\(app.debugDescription)")
        return link
    }

    @MainActor private func assertPush(_ app: XCUIApplication, title: String, session: String) {
        let header = app.buttons.matching(identifier: "chat.title").firstMatch
        XCTAssertTrue(header.waitForExistence(timeout: 10), app.debugDescription)
        let deadline = Date.now.addingTimeInterval(10)
        while !header.label.hasPrefix(title) && Date.now < deadline { usleep(200_000) }
        XCTAssertTrue(header.label.hasPrefix(title), "opened another Agent: \(header.label)")
        XCTAssertEqual(header.value as? String, session, "the rendered conversation is not the dispatched SessionKey")
        XCTAssertFalse(app.buttons["关闭"].exists, "opened a sheet instead of pushing")
        XCTAssertFalse(app.buttons["task-detail.fullscreen"].exists, "opened the task sheet")
        XCTAssertFalse(app.buttons["action-bar.running"].isHittable, "the Assistant dock shows through the pushed chat")
        XCTAssertFalse(app.otherElements["action-tray.running"].exists, "opened the entire In Progress tray")
    }

    @MainActor private func back(_ app: XCUIApplication) {
        let button = app.buttons.matching(NSPredicate(format: "identifier == 'BackButton' OR label IN %@", ["返回", "Back"])).firstMatch
        XCTAssertTrue(button.waitForExistence(timeout: 5), app.debugDescription)
        button.tap()
        let running = app.buttons["action-bar.running"]
        let deadline = Date.now.addingTimeInterval(10)
        while !running.isHittable && Date.now < deadline { usleep(200_000) }
        XCTAssertTrue(running.isHittable, "Back did not return to the Assistant conversation:\n\(app.debugDescription)")
        XCTAssertFalse(app.buttons["关闭"].exists, "a sheet reopened after Back")
    }

    private func fixture(_ action: String) async throws {
        var request = URLRequest(url: URL(string: hub + "/fixture/handoff/" + action)!)
        request.httpMethod = "POST"
        let (_, response) = try await URLSession.shared.data(for: request)
        XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
    }

    @MainActor func testHandoffNamesOriginAndReturnsToTheVisibleDispatch() async throws {
        try await checkConsecutiveHandoffs(sourceChangesOnFirstVisit: true)
    }

    /// An unchanged source can keep its SwiftUI rows between pushes. Its second hand-off must still
    /// capture the visible message before an update arrives behind the Agent.
    @MainActor func testUnchangedSourceRowsKeepTheirReadingPositionForTheNextHandoff() async throws {
        try await checkConsecutiveHandoffs(sourceChangesOnFirstVisit: false)
    }

    @MainActor private func checkConsecutiveHandoffs(sourceChangesOnFirstVisit: Bool) async throws {
        let app = launch()
        let card = link(app, "todo_task_widget")
        let original = card.frame.minY
        attach("handoff-source")
        card.tap()
        assertPush(app, title: "给天气 App 加主屏幕小组件", session: "macbook/a1f3c2d4-0000-4000-8000-000000000001")
        let origin = app.buttons["task-handoff.return"]
        XCTAssertTrue(origin.waitForExistence(timeout: 5), "The Agent must name its originating conversation")
        XCTAssertTrue(origin.label.contains("今天要处理的事"), origin.label)
        attach("handoff-agent")
        if sourceChangesOnFirstVisit { try await fixture("append") }
        origin.tap()
        XCTAssertTrue(card.waitForExistence(timeout: 10))
        let restored = NSPredicate { _, _ in abs(card.frame.minY - original) <= 4 }
        await fulfillment(of: [XCTNSPredicateExpectation(predicate: restored, object: nil)], timeout: 5)
        XCTAssertEqual(card.frame.minY, original, accuracy: 4)
        attach("handoff-returned")

        card.tap()
        XCTAssertTrue(origin.waitForExistence(timeout: 5))
        app.buttons["task-handoff.dismiss"].tap()
        try await fixture("append")
        back(app)
        await fulfillment(of: [XCTNSPredicateExpectation(predicate: restored, object: nil)], timeout: 5)
        XCTAssertEqual(card.frame.minY, original, accuracy: 4)
        attach("handoff-system-returned")
    }

    @MainActor func testFailedCardOpensEditableTodoWithRetryAndRemove() throws {
        let app = launch()
        link(app, "todo_dispatch_failed").tap()
        let title = app.textFields["todo-detail.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(title.value as? String, "校对测试清单")
        XCTAssertTrue(app.buttons["task-handoff.retry"].isHittable)
        XCTAssertTrue(app.buttons["task-handoff.remove"].isHittable)
        XCTAssertFalse(app.buttons["task-handoff.return"].exists)
        attach("handoff-failed-todo")
        let retry = app.buttons["task-handoff.retry"]
        retry.tap()
        XCTAssertTrue(retry.waitForNonExistence(timeout: 15), app.debugDescription)
        XCTAssertTrue(title.exists)
        back(app)
    }

    @MainActor func testRemoveUnstartedTaskReturnsToSource() {
        let app = launch()
        link(app, "todo_dispatch_failed").tap()
        let remove = app.buttons["task-handoff.remove"]
        XCTAssertTrue(remove.waitForExistence(timeout: 10))
        remove.tap()
        XCTAssertTrue(remove.waitForNonExistence(timeout: 10))
        XCTAssertTrue(app.buttons["action-bar.running"].waitForExistence(timeout: 10))
    }

    @MainActor func testActionBarRowsOpenTheirAgentAndDismissedHintKeepsSystemBack() throws {
        let app = launch()
        for (entry, task, agent, id) in [
            ("needs-you", "补齐网络层的单元测试", "补齐网络层的单元测试", "002"),
            ("running", "修小组件横屏崩溃", "给天气 App 加主屏幕小组件", "001"),
        ] {
            app.openActionTray(entry)
            let row = app.trayTasks.matching(NSPredicate(format: "label CONTAINS %@", task)).firstMatch
            XCTAssertTrue(row.waitForExistence(timeout: 10), app.debugDescription)
            if entry == "needs-you" { attach("handoff-needs-you") }
            row.tap()
            assertPush(app, title: agent, session: "macbook/a1f3c2d4-0000-4000-8000-000000000" + id)
            let origin = app.buttons["task-handoff.return"]
            XCTAssertTrue(origin.waitForExistence(timeout: 5))
            app.buttons["task-handoff.dismiss"].tap()
            XCTAssertFalse(origin.exists)
            back(app)
        }
    }

    @MainActor func testTaskNoticeLineOpensItsOwnAgent() {
        let app = launch()
        let line = app.descendants(matching: .any)["injected-task_notice"].firstMatch
        XCTAssertTrue(line.waitForExistence(timeout: 10))
        line.tap()
        assertPush(app, title: "整理发布说明", session: "macbook/a1f3c2d4-0000-4000-8000-000000000008")
        XCTAssertTrue(app.buttons["task-handoff.return"].exists)
        back(app)
    }

    @MainActor func testTwoMachinesSharingASessionIDKeepTheirOwnCardsAndTheTrayPicksTheUrgentAgent() async throws {
        try await fixture("multi")
        let app = launch()
        let session = "a1f3c2d4-0000-4000-8000-000000000001"
        for (machine, title) in [("macbook", "给天气 App 加主屏幕小组件"), ("studio", "工作室检查小组件")] {
            let key = machine + "/" + session
            let card = link(app, "todo_task_widget." + key)
            XCTAssertTrue(card.label.contains(title), card.label)
            card.tap()
            assertPush(app, title: title, session: key)
            back(app)
        }
        app.openActionTray("needs-you")
        let row = app.trayTasks.matching(NSPredicate(format: "label CONTAINS %@", "修小组件横屏崩溃")).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 10), app.debugDescription)
        row.tap()
        assertPush(app, title: "工作室检查小组件", session: "studio/" + session)
    }

    @MainActor func testDirectAgentHasNoConversationHint() {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-holdChromeOpen", "YES",
                               "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001"]
        app.launchPinned()
        XCTAssertTrue(app.buttons["chat.title"].firstMatch.waitForExistence(timeout: 20), app.debugDescription)
        XCTAssertFalse(app.buttons["task-handoff.return"].exists)
        XCTAssertFalse(app.buttons["task-handoff.dismiss"].exists)
    }

    @MainActor func testStandaloneOpensItsAgentAndBackReturnsToAssistant() throws {
        let app = launch()
        let one = link(app, "todo_task_widget")
        // one card: the title, how the Agent came to it, when it last moved and its state, in one element
        XCTAssertTrue(one.label.contains("给天气 App 加主屏幕小组件"), one.label)
        XCTAssertFalse(one.label.contains("派出"), "the old \"Dispatched\" line is back: \(one.label)")
        try shot(app, "one")
        one.tap()
        assertPush(app, title: "给天气 App 加主屏幕小组件", session: "macbook/a1f3c2d4-0000-4000-8000-000000000001")
        sleep(2)
        try shot(app, "one-opened")
        back(app)
        XCTAssertTrue(app.buttons["dispatch.task.todo_task_widget"].exists)
        try shot(app, "one-back")
    }

    @MainActor func testPlanLinksAreIndependentAndStartingTaskOpensDetail() throws {
        let app = launch()
        let starting = link(app, "todo_dispatch_starting")
        starting.tap()
        let title = app.textFields["todo-detail.title"]
        XCTAssertTrue(title.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(title.value as? String, "检查启动状态", "another task's detail opened")
        XCTAssertFalse(app.buttons["task-detail.fullscreen"].exists)
        XCTAssertFalse(app.otherElements["action-tray.running"].exists)
        try shot(app, "starting")
        back(app)

        let first = link(app, "todo_task_tests")
        XCTAssertTrue(first.label.contains("补齐网络层的单元测试"), first.label)
        XCTAssertEqual(app.buttons.matching(identifier: "dispatch.task.todo_task_tests").count, 1, "duplicate per-task feed link")
        XCTAssertEqual(app.buttons.matching(identifier: "dispatch.task.todo_task_release").count, 1)
        let unstarted = app.descendants(matching: .any)["dispatch.unstarted"].firstMatch
        XCTAssertTrue(unstarted.exists)
        XCTAssertTrue(unstarted.label.contains("未启动的计划任务") && unstarted.label.contains("当前没有可用的电脑"), unstarted.label)
        XCTAssertFalse(app.buttons["dispatch.unstarted"].exists, "failed plan slot has an invented target")
        try shot(app, "many")
        first.tap()
        assertPush(app, title: "补齐网络层的单元测试", session: "macbook/a1f3c2d4-0000-4000-8000-000000000002")
        try shot(app, "many-first-opened")
        back(app)
        link(app, "todo_task_release").tap()
        assertPush(app, title: "整理发布说明", session: "macbook/a1f3c2d4-0000-4000-8000-000000000008")
        try shot(app, "many-second-opened")
        back(app)
    }
}
