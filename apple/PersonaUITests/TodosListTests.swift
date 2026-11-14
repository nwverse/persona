import XCTest

/// The To-dos list's top (#3712) and its filter and sort (#3740) against the mock hub (`MOCK_TODO_LIST=1`): a merged row's
/// "N related" opens the related to-dos under it, and a touch on it never opens the to-do; the filter button's menu picks
/// This Week, which shows as a chip that clears it, with a line for what waits on the user outside the week.
/// `TEST_RUNNER_PERSONA_TODOS_HUB=<mock URL>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>` also saves the screens.
final class TodosListTests: XCTestCase {
    private var app: XCUIApplication!

    @MainActor func testRelatedOpensInPlaceAndTheFilterChipClears() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_TODOS_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_LIST=1 (PERSONA_TODOS_HUB)")
        }
        continueAfterFailure = false
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos", "-todos.view", "list",
                               "-todos.filter", "all", "-todos.sort", "recent"]
        app.launchPinned()

        let filter = app.buttons["todos.filter"]
        XCTAssertTrue(filter.waitForExistence(timeout: 60))
        let member = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "补回归测试")).firstMatch
        XCTAssertFalse(member.exists, "the related to-do shows before its row is opened")
        let related = app.buttons["todos.related.tl_push_fix"]
        if !related.isHittable { app.swipeUp() }
        XCTAssertTrue(related.waitForExistence(timeout: 10))
        related.tap()
        XCTAssertTrue(member.waitForExistence(timeout: 10), "N related did not open its row")
        XCTAssertTrue(filter.exists, "the touch on N related opened the to-do")
        shot("todos-related-open")

        filter.tap()
        let week = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "本周")).firstMatch
        XCTAssertTrue(week.waitForExistence(timeout: 10))
        week.tap()
        let chip = app.buttons["todos.filter.clear"]
        XCTAssertTrue(chip.waitForExistence(timeout: 10))
        // the cache decision has no due date: it waits on the user outside This Week, and the line says so. The line sits
        // over the rows, and the list stays where the swipe to N related left it; a List loads no row off screen (#5039).
        let line = app.descendants(matching: .any)["todos.filter.hiddenWaiting"]
        for _ in 0..<3 where !line.waitForExistence(timeout: 2) {
            app.collectionViews.firstMatch.swipeDown(velocity: .slow)
        }
        XCTAssertTrue(line.waitForExistence(timeout: 10), "the line for what This Week leaves out of Needs you")
        shot("todos-filter-week")
        chip.tap()
        XCTAssertTrue(chip.waitForNonExistence(timeout: 10))
    }

    /// Open concerns keep their own labelled rows in Working / Other open; accepted ones keep their open state and a
    /// reported one is labelled in Just finished. `TEST_RUNNER_PERSONA_CONCERN_MATRIX=1` runs en / zh-Hans × light / dark
    /// plus the largest text size and saves top / accepted / bottom screenshots. The default checks the first case only.
    @MainActor func testConcernRowsStayVisibleAndLabelled() throws {
        let environment = ProcessInfo.processInfo.environment
        guard let hub = environment["PERSONA_TODOS_HUB"] else {
            throw XCTSkip("needs MOCK_MANAGERS=0 MOCK_TODO_LIST=1 MOCK_TODO_CONCERNS=1 (PERSONA_TODOS_HUB)")
        }
        continueAfterFailure = false
        let all: [(language: String, scheme: String, largest: Bool, tag: String, openState: String, accepted: String, args: [String])] = [
            ("zh-Hans", "light", false, "关注点", "未开始", "已验收", ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]),
            ("zh-Hans", "dark", false, "关注点", "未开始", "已验收", ["-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]),
            ("en", "light", false, "Concern", "Not started", "Accepted", ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]),
            ("en", "dark", false, "Concern", "Not started", "Accepted", ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]),
            ("en", "light", true, "Concern", "Not started", "Accepted", ["-AppleLanguages", "(en)", "-AppleLocale", "en_US"]),
        ]
        let cases = environment["PERSONA_CONCERN_MATRIX"] == "1" ? all : Array(all.prefix(1))
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        var first = true
        for item in cases {
            app?.terminate()
            app = XCUIApplication()
            let size = item.largest ? ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] : []
            app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos",
                                   "-todos.view", "list", "-todos.filter", "all", "-todos.sort", "recent",
                                   "-ui.scheme", item.scheme] + item.args + size
            app.launchPinned()
            let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
            for label in ["允许", "Allow"] where first ? board.buttons[label].waitForExistence(timeout: 3) : board.buttons[label].exists {
                board.buttons[label].tap()
            }
            first = false

            let working = reveal("concern_working")
            XCTAssertTrue(working.waitForExistence(timeout: 60))
            XCTAssertTrue(app.descendants(matching: .any)["todos.section.working"].exists, "the linked working agent puts the concern in Working")
            check(working, tag: item.tag)
            if board.alerts.firstMatch.waitForExistence(timeout: 2) {
                for label in ["OK", "好"] where board.alerts.buttons[label].exists { // i18n-ignore: the system's buttons
                    board.alerts.buttons[label].tap()
                }
            }
            concernShot("todos-concerns-\(item.language)-\(item.scheme)-\(item.largest ? "largest" : "default")-top")

            XCTAssertTrue(app.descendants(matching: .any)["todos.section.other"].exists, "an unanswered concern stays in Other open")
            let ordinary = reveal("concern_control")
            XCTAssertFalse(ordinary.label.contains(item.tag), "an ordinary to-do keeps its row unchanged: \(ordinary.label)")
            XCTAssertFalse(app.buttons["todos.related.concern_control"].exists, "an open concern must not fold into a task")
            let accepted = reveal("concern_accepted")
            check(accepted, tag: item.tag)
            XCTAssertTrue(accepted.label.contains(item.openState), "an accepted concern keeps its open state: \(accepted.label)")
            XCTAssertFalse(accepted.label.contains(item.accepted), "an accepted concern has no Accepted tag: \(accepted.label)")
            concernShot("todos-concerns-\(item.language)-\(item.scheme)-\(item.largest ? "largest" : "default")-accepted")
            let open = reveal("concern_open")
            check(open, tag: item.tag)
            XCTAssertFalse(app.buttons["todos.related.concern_open"].exists, "an open concern must not fold related work")
            let closed = reveal("concern_closed")
            XCTAssertTrue(app.descendants(matching: .any)["todos.section.just_done"].exists, "the report closes the concern into Just finished")
            check(closed, tag: item.tag)
            concernShot("todos-concerns-\(item.language)-\(item.scheme)-\(item.largest ? "largest" : "default")-bottom")
            let unseen = reveal("concern_unseen")
            check(unseen, tag: item.tag)
            let seen = app.buttons["todos.seen.concern_unseen"]
            XCTAssertTrue(seen.waitForExistence(timeout: 10))
            let otherOpen = item.language == "en" ? "Other open" : "其余没完成"
            let done = item.language == "en" ? "Done" : "已完成"
            XCTAssertTrue(seen.label.contains(otherOpen), "Got It says where an open concern goes: \(seen.label)")
            XCTAssertFalse(seen.label.contains(done), "Got It must not promise Done for an open concern: \(seen.label)")
        }
        app?.terminate()
    }

    @MainActor private func reveal(_ id: String) -> XCUIElement {
        let row = app.descendants(matching: .any)["todos.row.\(id)"].firstMatch
        let window = app.windows.firstMatch.frame
        for _ in 0..<12 where !row.exists || !row.isHittable || !window.contains(row.frame) {
            if !row.waitForExistence(timeout: 2) || !row.isHittable || !window.contains(row.frame) {
                app.collectionViews.firstMatch.swipeUp(velocity: .slow)
            }
        }
        XCTAssertTrue(row.isHittable && window.contains(row.frame), "missing or not fully visible \(id):\n\(app.debugDescription)")
        return row
    }

    @MainActor private func check(_ row: XCUIElement, tag: String) {
        XCTAssertEqual(row.label.components(separatedBy: tag).count - 1, 1,
                       "\(row.identifier) must say \(tag) exactly once and keep its state: \(row.label)")
        let window = app.windows.firstMatch.frame
        XCTAssertGreaterThanOrEqual(row.frame.minX, window.minX - 0.5)
        XCTAssertLessThanOrEqual(row.frame.maxX, window.maxX + 0.5)
    }

    @MainActor private func concernShot(_ name: String) {
        var last = XCUIScreen.main.screenshot()
        for _ in 0..<8 {
            usleep(300_000)
            let next = XCUIScreen.main.screenshot()
            if next.pngRepresentation == last.pngRepresentation { break }
            last = next
        }
        let attachment = XCTAttachment(screenshot: last)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            try? last.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + ".png"))
        }
    }

    private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + look + ".png"))
        }
    }
}
