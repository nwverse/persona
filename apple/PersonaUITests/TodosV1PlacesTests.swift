import XCTest

/// The To-dos places (#10465; docs/design/proposals/todos-redesign.md 3.3; DESIGN_SYSTEM 3.5 "To-dos places") against
/// the mock hub's To-dos view (`MOCK_TODO_VIEW=1`, `PERSONA_TODOS_HUB`), on an iPhone in its own layout: the drawer's
/// To-dos row folds open Today, Upcoming, the projects with their part of N and All Projects…; a place picked there or
/// from the list's title menu is the list; every place but All To-dos says what needs the owner elsewhere, with Show All.
/// `-showTodosV1 YES` stands for the release. With `PERSONA_TODOS_FRAMES=1` and `PERSONA_SHOTS=<dir>`, the V1 frames
/// this step draws are saved for review instead (`PERSONA_TODOS_SCHEME`, `PERSONA_TODOS_LANGUAGE`, `PERSONA_TODOS_COLOR`,
/// `PERSONA_TODOS_SIZE`, `PERSONA_TODOS_SUFFIX`, `PERSONA_TODOS_FRAMES_ONLY`, as `TodosV1ListTests`).
final class TodosV1PlacesTests: XCTestCase {
    private var app: XCUIApplication!
    private let env = ProcessInfo.processInfo.environment

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    @MainActor private func launch(_ hub: String, _ extra: [String] = [], layout: String = "separate", v1: Bool = true) throws {
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("iPhone only") }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos", "-app.navigation", layout]
            + (v1 ? ["-showTodosV1", "YES"] : []) + extra
        app.launchPinned()
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// ☰ at To-dos' root, until the drawer shows `row` (the places unfolded, if an earlier run left them folded).
    @MainActor private func drawer(showing row: String) {
        let menu = element("shell-place-sidebar"), target = element(row), fold = element("sidebar-todos-fold")
        XCTAssertTrue(menu.waitForExistence(timeout: 20), "no ☰:\n\(app.debugDescription)")
        for _ in 0..<3 where !target.exists {
            if menu.isHittable { menu.tap() }
            _ = target.waitForExistence(timeout: 5)
            if !target.exists, fold.exists, fold.value as? String != nil, !element("sidebar-todos-place-today").exists { fold.tap() }
        }
        XCTAssertTrue(target.exists, "no \(row) in the drawer:\n\(app.debugDescription)")
    }

    /// The list's title as a menu: the navigation bar's title button (the system's title menu has no identifier of its
    /// own; the bar's other buttons have theirs).
    @MainActor private func titleMenu() -> XCUIElement {
        let bar = app.navigationBars.firstMatch
        let known: Set<String> = ["shell-place-sidebar", "todos.more", "BackButton"]
        return bar.buttons.allElementsBoundByIndex.first { !known.contains($0.identifier) && $0.isHittable } ?? bar.staticTexts.firstMatch
    }

    /// The last number in a spoken label ("Kyoto trip, 1"): the count a badge adds to its row.
    private func number(_ label: String) -> Int? {
        label.split(whereSeparator: { !$0.isNumber }).last.flatMap { Int($0) }
    }

    private var hub: String {
        get throws {
            guard let hub = env["PERSONA_TODOS_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_VIEW=1 (PERSONA_TODOS_HUB)") }
            return hub
        }
    }

    /// The drawer's To-dos row folds open the places, each project with its part of N; Today picked there is the list,
    /// with what needs the owner elsewhere and Show All back to All To-dos.
    @MainActor func testTheDrawerOpensAPlace() throws {
        try launch(try hub)
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        let n = try XCTUnwrap(number(element("todos.bar.needs_you").label))
        drawer(showing: "sidebar-todos-place-today")
        for id in ["upcoming", "project:kyoto", "project:weather", "project:home", "more"] {
            XCTAssertTrue(element("sidebar-todos-place-" + id).exists, "no \(id) under To-dos:\n\(app.debugDescription)")
        }
        for project in ["kyoto", "weather", "home"] {
            XCTAssertEqual(number(element("sidebar-todos-place-project:" + project).label), 1, "\(project)'s part of N")
        }
        XCTAssertNil(number(element("sidebar-todos-place-today").label), "Today draws no count")
        // Folded by the owner and back: the places go and come, To-dos stays.
        element("sidebar-todos-fold").tap()
        XCTAssertTrue(element("sidebar-todos-place-today").waitForNonExistence(timeout: 5), "the places folded away")
        XCTAssertTrue(element("sidebar-todos").exists)
        element("sidebar-todos-fold").tap()
        XCTAssertTrue(element("sidebar-todos-place-today").waitForExistence(timeout: 5))

        element("sidebar-todos-place-today").tap()
        let notice = element("todos.places.hiddenNeeds")
        XCTAssertTrue(notice.waitForExistence(timeout: 20), "Today says what needs the owner elsewhere:\n\(app.debugDescription)")
        XCTAssertTrue(notice.label.contains(String(n)) || element("todos.places.hiddenNeeds").staticTexts.allElementsBoundByIndex
            .contains { $0.label.contains(String(n)) }, "all \(n) are elsewhere: \(notice.label)")
        XCTAssertFalse(element("todos.section.needs_you").exists, "nothing in Today needs the owner")
        element("todos.places.showAll").tap()
        XCTAssertTrue(element("todos.section.needs_you").waitForExistence(timeout: 10), "Show All: All To-dos")
        XCTAssertFalse(element("todos.places.hiddenNeeds").exists)
    }

    /// The title menu lists the same places; a project picked there is the list in place, its bar counting its own rows.
    @MainActor func testTheTitleMenuPicksAPlace() throws {
        try launch(try hub)
        XCTAssertTrue(element("todos.bar.needs_you").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        XCTAssertTrue(element("todos.row.t05").waitForExistence(timeout: 10), "the newsletter is listed in All To-dos")
        let title = titleMenu()
        XCTAssertTrue(title.exists, "no title menu:\n\(app.debugDescription)")
        title.tap()
        // A menu item keeps no identifier: the project by its name, which is the hub's (the knowledge base's title).
        let kyoto = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Kyoto trip'")).firstMatch
        XCTAssertTrue(kyoto.waitForExistence(timeout: 10), "no Kyoto trip in the title menu:\n\(app.debugDescription)")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Weather App'")).firstMatch.exists)
        kyoto.tap()
        XCTAssertTrue(element("todos.row.t01").waitForExistence(timeout: 10), "Kyoto trip's to-do")
        XCTAssertTrue(element("todos.row.t05").waitForNonExistence(timeout: 5), "another project's to-do left the list")
        XCTAssertEqual(number(element("todos.bar.needs_you").label), 1, "the place's part of N")
        XCTAssertTrue(element("todos.places.hiddenNeeds").exists, "the other two need the owner elsewhere")
    }

    /// Upcoming lists the dated to-dos by day, each day with its +; To-dos opened again is on the last place.
    @MainActor func testUpcomingIsByDayAndToDosReopensOnIt() throws {
        try launch(try hub, ["-openTodoPlace", "upcoming", "-todos.v1.upcomingCalendar", "NO"])
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        let days = app.descendants(matching: .any).matching(NSPredicate(format: "identifier BEGINSWITH 'todos.upcoming.add.day:'"))
        XCTAssertTrue(days.firstMatch.waitForExistence(timeout: 20), "no day headings:\n\(app.debugDescription)")
        XCTAssertFalse(element("todos.bar.all").exists, "Upcoming is by day, with no section bar")
        XCTAssertTrue(element("todos.row.t11").exists, "the passport is dated")
        XCTAssertFalse(element("todos.row.t13").exists, "the car insurance has no date")
        // Another page, then To-dos again: still Upcoming.
        drawer(showing: "sidebar-inbox")
        element("sidebar-inbox").tap()
        XCTAssertTrue(element("inbox.more").waitForExistence(timeout: 20), "no Inbox:\n\(app.debugDescription)")
        drawer(showing: "sidebar-todos")
        element("sidebar-todos").tap()
        XCTAssertTrue(days.firstMatch.waitForExistence(timeout: 20), "To-dos reopened on Upcoming:\n\(app.debugDescription)")
    }

    // MARK: Frames

    /// The app again with `args`, on its list or Upcoming's calendar.
    @MainActor private func relaunch(_ hub: String, _ args: [String]) throws {
        app?.terminate()
        try launch(hub, args)
        let up = element("todos.v1").waitForExistence(timeout: 60) || element("todo.calendar.title").exists
        XCTAssertTrue(up, "no V1 list")
    }

    /// The frames this step draws, for the review (no checks beyond reaching them): `~drawer`, `~menu`, `~today`,
    /// `~kyoto`, and Upcoming as a list and as a calendar.
    @MainActor func testFrames() throws {
        guard env["PERSONA_TODOS_FRAMES"] == "1", let hub = env["PERSONA_TODOS_HUB"], let dir = env["PERSONA_SHOTS"] else {
            throw XCTSkip("PERSONA_TODOS_FRAMES=1 with PERSONA_TODOS_HUB and PERSONA_SHOTS")
        }
        let scheme = env["PERSONA_TODOS_SCHEME"] ?? "light"
        let language = env["PERSONA_TODOS_LANGUAGE"] ?? "en"
        let locale = ["en": "en_US", "zh-Hans": "zh_CN", "ja": "ja_JP"][language] ?? "en_US"
        var extra = ["-ui.scheme", scheme, "-AppleLanguages", "(\(language))", "-AppleLocale", locale]
        if let color = env["PERSONA_TODOS_COLOR"] { extra += ["-ui.color", color] }
        if let size = env["PERSONA_TODOS_SIZE"] { extra += ["-UIPreferredContentSizeCategoryName", size] }
        let frames = Set((env["PERSONA_TODOS_FRAMES_ONLY"] ?? "").split(separator: ",").map(String.init))
        let wants = { (frame: String) in frames.isEmpty || frames.contains(frame) }
        let suffix = env["PERSONA_TODOS_SUFFIX"].map { "-" + $0 } ?? ""
        let shot = { (frame: String) in
            sleep(2) // the bar's reveal and the rows' marks settle
            let png = XCUIScreen.main.screenshot().pngRepresentation
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("iphone-list\(frame)\(suffix)-\(scheme).png"))
        }
        let relaunch = { (args: [String]) in try self.relaunch(hub, extra + args) }
        try relaunch(["-openTodoPlace", "all"])
        _ = element("todos.bar.all").waitForExistence(timeout: 10)
        if wants("~drawer") {
            drawer(showing: "sidebar-todos-place-today")
            shot("~drawer")
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)).tap() // the page beside the drawer closes it
            _ = element("todos.bar.all").waitForExistence(timeout: 5)
        }
        if wants("~menu") {
            titleMenu().tap()
            _ = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'Kyoto trip'")).firstMatch.waitForExistence(timeout: 5)
            shot("~menu")
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.04, dy: 0.6)).tap()
        }
        for (frame, place) in [("~today", "today"), ("~kyoto", "project:kyoto"), ("~upcoming", "upcoming")] where wants(frame) {
            try relaunch(["-openTodoPlace", place, "-todos.v1.upcomingCalendar", "NO"])
            shot(frame)
        }
        if wants("~upcoming-calendar") {
            try relaunch(["-openTodoPlace", "upcoming", "-todos.v1.upcomingCalendar", "YES", "-todos.calendar.mode", "week"])
            _ = element("todo.calendar.title").waitForExistence(timeout: 10)
            shot("~upcoming-calendar")
        }
        if wants("~all-projects") {
            try relaunch(["-openTodoPlace", "all"])
            drawer(showing: "sidebar-todos-place-more")
            element("sidebar-todos-place-more").tap()
            _ = element("todos.places.project.project:kyoto").waitForExistence(timeout: 10)
            shot("~all-projects")
        }
    }

    /// Today's To-dos with the gate off, as `main` draws them (plan 2.6.1: before = after): its calendar, which takes
    /// its colours from a parameter now (`TodoCalendarView.state`, nil as before), and the drawer's To-dos row. Run with
    /// the same test bundle against a build of `main` and of this branch (`PERSONA_TODOS_TODAY=1`).
    @MainActor func testTodayFrames() throws {
        guard env["PERSONA_TODOS_TODAY"] == "1", let hub = env["PERSONA_TODOS_HUB"], let dir = env["PERSONA_SHOTS"] else {
            throw XCTSkip("PERSONA_TODOS_TODAY=1 with PERSONA_TODOS_HUB and PERSONA_SHOTS")
        }
        let scheme = env["PERSONA_TODOS_SCHEME"] ?? "light"
        let suffix = env["PERSONA_TODOS_SUFFIX"].map { "-" + $0 } ?? ""
        let shot = { (frame: String) in
            sleep(2)
            let png = XCUIScreen.main.screenshot().pngRepresentation
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("iphone-today\(frame)\(suffix)-\(scheme).png"))
        }
        try launch(hub, ["-ui.scheme", scheme, "-AppleLanguages", "(en)", "-AppleLocale", "en_US", "-todos.view", "calendar",
                         "-todos.calendar.mode", "week"], v1: false)
        XCTAssertTrue(element("todo.calendar.title").waitForExistence(timeout: 60), "no calendar:\n\(app.debugDescription)")
        shot("~calendar")
        drawer(showing: "sidebar-todos")
        shot("~drawer")
    }
}
