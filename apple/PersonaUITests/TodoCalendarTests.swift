import XCTest

/// The to-dos' calendar (#2029) against the mock hub, whose to-dos are due today at 10:00, 16:00 and two at 18:00, on a
/// day in two days and yesterday, and one has no due date: the day, the week and the month show them; a block opens
/// its to-do; a day of the week opens that day; "+" makes a to-do due on the day shown; the bar under the calendar
/// goes back to the list; a to-do dragged to another time, the All Day row, another day of the week or the month is
/// saved with Undo on its toast (#2045). TEST_RUNNER_PERSONA_SHOTS=<dir> also saves the screens
/// (TEST_RUNNER_PERSONA_SHOTS_APPEARANCE names them light / dark).
final class TodoCalendarTests: XCTestCase {
    private var app: XCUIApplication!

    @MainActor func testCalendarShowsOpensAndAdds() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        continueAfterFailure = false
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // The view and the mode are switched here, not by launch arguments: a value in the argument domain could not be
        // changed by the taps below.
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openAssistantPage", "todos",
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        // An iPad on its side: the calendar beside the sidebar is wide enough for the full week (Theme.CalendarGrid.wide).
        let pad = UIDevice.current.userInterfaceIdiom == .pad
        if pad { XCUIDevice.shared.orientation = .landscapeLeft }
        defer { if pad { XCUIDevice.shared.orientation = .portrait } }
        app.launchPinned()
        let calendarSwitch = app.buttons["日历"]
        XCTAssertTrue(calendarSwitch.waitForExistence(timeout: 60))
        calendarSwitch.tap()
        app.buttons["日"].tap()

        // The day: today's to-dos on the axis, the two at 18:00 side by side.
        let weather = entry("天气 App 发 1.3 版")
        XCTAssertTrue(weather.waitForExistence(timeout: 60))
        XCTAssertTrue(entry("博客迁移到 Astro").exists)
        XCTAssertTrue(app.descendants(matching: .any)["todo.calendar.strip"].exists)
        XCTAssertTrue(app.descendants(matching: .any)["todo.calendar.undated"].exists)
        shot("calendar-day")

        // The week, then a day of it.
        app.buttons["周"].tap()
        XCTAssertTrue(app.descendants(matching: .any)["todo.calendar.dayHeader"].firstMatch.waitForExistence(timeout: 10))
        shot("calendar-week")
        // The month.
        app.buttons["月"].tap()
        XCTAssertTrue(app.descendants(matching: .any)["todo.calendar.monthDay"].firstMatch.waitForExistence(timeout: 10))
        shot("calendar-month")
        // Back to the week; its first day opens as a day.
        app.buttons["周"].tap()
        let header = app.descendants(matching: .any)["todo.calendar.dayHeader"].firstMatch
        XCTAssertTrue(header.waitForExistence(timeout: 10))
        header.tap()
        XCTAssertTrue(app.descendants(matching: .any)["todo.calendar.strip"].waitForExistence(timeout: 10))
        app.buttons["todo.calendar.today"].tap()

        // A block opens its to-do.
        XCTAssertTrue(weather.waitForExistence(timeout: 10))
        weather.tap()
        XCTAssertTrue(app.switches["todo.plan.due"].waitForExistence(timeout: 15))
        app.navigationBars.buttons.element(boundBy: 0).tap()
        XCTAssertTrue(weather.waitForExistence(timeout: 10))

        // "+" makes a to-do due on the day shown, all day.
        app.buttons["新建待办"].tap()
        let title = app.textFields["todo.calendar.newTitle"]
        XCTAssertTrue(title.waitForExistence(timeout: 10))
        title.typeText("日历里记的事")
        shot("calendar-add")
        app.buttons["todo.calendar.newAdd"].tap()
        XCTAssertTrue(wait { try self.todos(hub).contains { $0["title"] as? String == "日历里记的事" && $0["due_date"] is String } })
        XCTAssertTrue(entry("日历里记的事").waitForExistence(timeout: 10))
        // the mock as it was, for the tests after this one (a to-do due today, all day, would be today's first line)
        for t in try todos(hub) where t["title"] as? String == "日历里记的事" {
            try send(hub, "DELETE", "/api/v1/todos/\(t["id"] as? String ?? "")")
        }

        // The bar under the calendar goes back to the list.
        app.descendants(matching: .any)["todo.calendar.undated"].tap()
        XCTAssertTrue(app.textFields["搜索待办"].waitForExistence(timeout: 10))
    }

    /// Moving a to-do (#2045): held and dragged on the day's axis two hours later, onto the All Day row, to the next
    /// day's column of the week and to the next day of the month; each saved at once with a toast whose Undo puts it
    /// back. The mock hub's "检查 PR #42" is due today at 10:00. PERSONA_BEFORE (the app built from main, for the
    /// before / after recordings) makes the same gestures and checks nothing; PERSONA_APPEARANCE dark / light.
    @MainActor func testDragMovesAToDo() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        let env = ProcessInfo.processInfo.environment
        let before = env["PERSONA_BEFORE"] != nil
        continueAfterFailure = before
        switch env["PERSONA_APPEARANCE"] {
        case "dark": XCUIDevice.shared.appearance = .dark
        case "light": XCUIDevice.shared.appearance = .light
        default: break
        }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openAssistantPage", "todos",
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        let pad = UIDevice.current.userInterfaceIdiom == .pad
        if pad { XCUIDevice.shared.orientation = .landscapeLeft }
        defer { if pad { XCUIDevice.shared.orientation = .portrait } }
        app.launchPinned()
        let calendarSwitch = app.buttons["日历"]
        XCTAssertTrue(calendarSwitch.waitForExistence(timeout: 60))
        calendarSwitch.tap()
        app.buttons["日"].tap()
        let cal = Calendar.current
        let ten = cal.date(bySettingHour: 10, minute: 0, second: 0, of: .now)!
        try resetReview(hub, to: ten)
        let review = entry("检查 PR #42")
        XCTAssertTrue(review.waitForExistence(timeout: 60))
        /// Checks the mock's to-do (not before), then Undo on the toast puts it back at 10:00 today.
        func expectSaved(_ what: String, at: Date? = nil, day: String? = nil, visible: Bool = true) {
            guard !before else { return }
            XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: at, day: day) }, "\(what): \((try? self.due(hub, "todo_review")).map { "\($0)" } ?? "?")")
            let undo = app.buttons["toast-action"]
            XCTAssertTrue(undo.waitForExistence(timeout: 10), "\(what): no Undo on the toast")
            shot("drag-\(what)-toast")
            undo.tap()
            XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: ten, day: nil) }, "\(what): Undo didn't put it back")
            if visible { XCTAssertTrue(review.waitForExistence(timeout: 10)) }
        }

        // The day: two hours later (the block's top edge moves with the finger, snapped to the quarter hour).
        drag(review, to: review.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).withOffset(CGVector(dx: 0, dy: 2 * Self.hour)))
        expectSaved("day", at: cal.date(byAdding: .hour, value: 2, to: ten))

        // The All Day row: due today, all day.
        let allDay = app.staticTexts["全天"].firstMatch
        XCTAssertTrue(allDay.exists)
        let row = review.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .withOffset(CGVector(dx: 0, dy: allDay.frame.midY - review.frame.midY))
        drag(review, to: row)
        expectSaved("all-day", day: dayKey(ten))

        // Held near the axis's bottom edge it scrolls on: it lands well after the hour under the finger. Last on the day:
        // the week opens scrolled to its morning again.
        if !before {
            let bar = app.descendants(matching: .any)["todo.calendar.undated"]
            XCTAssertTrue(bar.exists)
            let edge = bar.frame.minY - Self.hour / 3, y0 = review.frame.midY
            let from = review.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            from.press(forDuration: 0.6, thenDragTo: from.withOffset(CGVector(dx: 0, dy: edge - y0)), withVelocity: .slow, thenHoldForDuration: 1.5)
            let underFinger = ten.addingTimeInterval(Double((edge - y0) / Self.hour) * 3600)
            XCTAssertTrue(wait { (try self.due(hub, "todo_review").at ?? .distantPast) > underFinger.addingTimeInterval(3600) },
                          "autoscroll: \((try? self.due(hub, "todo_review")).map { "\($0)" } ?? "?")")
            let undo = app.buttons["toast-action"]
            XCTAssertTrue(undo.waitForExistence(timeout: 10))
            undo.tap()
            XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: ten, day: nil) })
        }

        // The week: the next day's column, at the same time (the day before on the week's last day).
        app.buttons["周"].tap()
        let headers = app.descendants(matching: .any).matching(identifier: "todo.calendar.dayHeader")
        XCTAssertTrue(headers.firstMatch.waitForExistence(timeout: 10))
        XCTAssertTrue(review.waitForExistence(timeout: 10))
        let todayColumn = (0..<headers.count).first { abs(headers.element(boundBy: $0).frame.midX - review.frame.midX) < headers.element(boundBy: $0).frame.width / 2 } ?? 0
        let step = todayColumn + 1 < headers.count ? 1 : -1
        let next = headers.element(boundBy: todayColumn + step)
        drag(review, to: review.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).withOffset(CGVector(dx: next.frame.midX - review.frame.midX, dy: 0)))
        expectSaved("week", at: cal.date(byAdding: .day, value: step, to: ten))

        // The month: the next day, at the same time. Today's first line is 10:00's (its chip, under the date). A phone's
        // month day is one element, its chips not apart: they are found by where they are.
        app.buttons["月"].tap()
        let days = app.descendants(matching: .any).matching(identifier: "todo.calendar.monthDay")
        XCTAssertTrue(days.firstMatch.waitForExistence(timeout: 10))
        var monday = cal
        monday.firstWeekday = 2 // the app's weeks start on Monday in Simplified Chinese (TodoCalendar.firstWeekday)
        let first = monday.dateInterval(of: .weekOfYear, for: monday.dateInterval(of: .month, for: .now)!.start)!.start
        let index = cal.dateComponents([.day], from: first, to: cal.startOfDay(for: .now)).day!
        let today = days.element(boundBy: index), target = days.element(boundBy: index + 1 < days.count ? index + 1 : index - 1)
        let monthStep = index + 1 < days.count ? 1 : -1
        let chip = today.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: today.frame.width / 2, dy: Self.monthFirstLine))
        let onTarget = chip.withOffset(CGVector(dx: target.frame.midX - today.frame.midX, dy: target.frame.minY - today.frame.minY))
        chip.press(forDuration: 0.6, thenDragTo: onTarget, withVelocity: .slow, thenHoldForDuration: 0.5)
        expectSaved("month", at: cal.date(byAdding: .day, value: monthStep, to: ten), visible: false)
        shot("drag-month")
        app.buttons["日"].tap()
    }

    /// An iPad's trackpad or mouse drags once it moves, with no hold (#2045): an hour later, then Undo.
    @MainActor func testPointerDragsOnIPad() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        guard UIDevice.current.userInterfaceIdiom == .pad else { throw XCTSkip("an iPad's pointer") }
        guard ProcessInfo.processInfo.environment["PERSONA_BEFORE"] == nil else { throw XCTSkip("not in the app before #2045") }
        continueAfterFailure = false
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openAssistantPage", "todos",
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        app.launchPinned()
        let calendarSwitch = app.buttons["日历"]
        XCTAssertTrue(calendarSwitch.waitForExistence(timeout: 60))
        calendarSwitch.tap()
        app.buttons["日"].tap()
        let ten = Calendar.current.date(bySettingHour: 10, minute: 0, second: 0, of: .now)!
        try resetReview(hub, to: ten)
        let review = entry("检查 PR #42")
        XCTAssertTrue(review.waitForExistence(timeout: 60))
        let start = review.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
        start.click(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: Self.hour)), withVelocity: .slow, thenHoldForDuration: 0.3)
        XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: ten.addingTimeInterval(3600), day: nil) },
                      "\((try? self.due(hub, "todo_review")).map { "\($0)" } ?? "?")")
        let undo = app.buttons["toast-action"]
        XCTAssertTrue(undo.waitForExistence(timeout: 10))
        undo.tap()
        XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: ten, day: nil) })
    }

    /// The way without dragging (VoiceOver's "Change Due Date…", opened here by `-openRescheduleTitled`): All Day on,
    /// Save, and "检查 PR #42" is due today, all day; Undo puts it back.
    @MainActor func testChangeDueDateWithoutDragging() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        guard ProcessInfo.processInfo.environment["PERSONA_BEFORE"] == nil else { throw XCTSkip("not in the app before #2045") }
        continueAfterFailure = false
        let ten = Calendar.current.date(bySettingHour: 10, minute: 0, second: 0, of: .now)!
        try resetReview(hub, to: ten)
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openAssistantPage", "todos",
                               "-openRescheduleTitled", "检查 PR #42",
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
        app.launchPinned()
        let calendarSwitch = app.buttons["日历"]
        XCTAssertTrue(calendarSwitch.waitForExistence(timeout: 60))
        // The sheet opens with the calendar: at once when the page opens on it (a tap above the sheet would close it).
        let allDay = app.switches["todo.calendar.rescheduleAllDay"]
        if !allDay.waitForExistence(timeout: 3) { calendarSwitch.tap() }
        XCTAssertTrue(allDay.waitForExistence(timeout: 30))
        XCTAssertTrue(app.navigationBars["改截止时间"].exists)
        shot("reschedule-sheet")
        allDay.switches.firstMatch.tap()
        app.buttons["todo.calendar.rescheduleSave"].tap()
        XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: nil, day: self.dayKey(ten)) })
        let undo = app.buttons["toast-action"]
        XCTAssertTrue(undo.waitForExistence(timeout: 10))
        undo.tap()
        XCTAssertTrue(wait { try self.due(hub, "todo_review") == Due(at: ten, day: nil) })
    }

    /// An hour on the axis (`Theme.CalendarGrid.hour`), and where a month day's first line is from its top (its date,
    /// `Theme.Size.iconS`, under `Theme.Space.xs`, then half a line).
    private static let hour: CGFloat = 52
    private static let monthFirstLine: CGFloat = 36

    /// Held still, then carried slowly to `target` and held there a moment before it is let go.
    @MainActor private func drag(_ element: XCUIElement, to target: XCUICoordinate) {
        element.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .press(forDuration: 0.6, thenDragTo: target, withVelocity: .slow, thenHoldForDuration: 0.5)
    }

    /// "检查 PR #42" due today at 10:00 again, whatever an earlier run left.
    private func resetReview(_ hub: String, to ten: Date) throws {
        try send(hub, "PATCH", "/api/v1/todos/todo_review", ["due_at": ISO8601DateFormatter().string(from: ten)])
    }

    /// A request to the mock hub, waited for; fails the test on an error status.
    private func send(_ hub: String, _ method: String, _ path: String, _ body: [String: Any]? = nil) throws {
        var req = URLRequest(url: URL(string: hub + path)!)
        req.httpMethod = method
        req.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
        if let body {
            req.httpBody = try JSONSerialization.data(withJSONObject: body)
            req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let done = expectation(description: "\(method) \(path)")
        let status = Status()
        URLSession.shared.dataTask(with: req) { _, response, _ in
            status.code = (response as? HTTPURLResponse)?.statusCode ?? 0
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        XCTAssertTrue((200..<300).contains(status.code), "\(method) \(path): \(status.code)")
    }

    /// A response's status, set on URLSession's queue and read once the request is done.
    private final class Status: @unchecked Sendable {
        var code = 0
    }

    private struct Due: Equatable {
        var at: Date?
        var day: String?
    }

    /// The mock's due date of a to-do: its moment, or its day.
    private func due(_ hub: String, _ id: String) throws -> Due {
        let t = try XCTUnwrap(todos(hub).first { $0["id"] as? String == id })
        let at = (t["due_at"] as? String).flatMap { ISO8601DateFormatter().date(from: $0) }
        return Due(at: at, day: t["due_date"] as? String)
    }

    private func dayKey(_ date: Date) -> String {
        let c = Calendar.current.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year!, c.month!, c.day!)
    }

    /// An entry on the calendar, by its title (its label starts with it).
    @MainActor private func entry(_ title: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", title + ",")).firstMatch
    }

    private func todos(_ hub: String) throws -> [[String: Any]] {
        let data = try Data(contentsOf: URL(string: hub + "/api/v1/todos")!)
        let body = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        return try XCTUnwrap(body["todos"] as? [[String: Any]])
    }

    /// Whether check holds within ten seconds.
    private func wait(_ check: () throws -> Bool) -> Bool {
        let deadline = Date.now.addingTimeInterval(10)
        while Date.now < deadline {
            if (try? check()) == true { return true }
            usleep(200_000)
        }
        return false
    }

    @MainActor private func shot(_ name: String) {
        // The screen's, not the app's: on an iPad on its side the app's comes back as the portrait frame buffer cut to
        // the screen's height. The screen's may also come back upright-portrait; it is turned to how it is held.
        let s = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "-ipad" : ""
            let png = upright(s.image).pngData() ?? s.pngRepresentation
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + device + look + ".png"))
        }
    }

    /// The image as the device is held: a portrait frame buffer of a device turned left (its top on the right) is
    /// turned a quarter counterclockwise.
    @MainActor private func upright(_ image: UIImage) -> UIImage {
        guard XCUIDevice.shared.orientation == .landscapeLeft, let cg = image.cgImage, cg.width < cg.height else { return image }
        let w = CGFloat(cg.width), h = CGFloat(cg.height)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        return UIGraphicsImageRenderer(size: CGSize(width: h, height: w), format: format).image { ctx in
            ctx.cgContext.translateBy(x: 0, y: w)
            ctx.cgContext.rotate(by: -.pi / 2)
            UIImage(cgImage: cg).draw(in: CGRect(x: 0, y: 0, width: w, height: h))
        }
    }
}
