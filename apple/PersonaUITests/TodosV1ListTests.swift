import XCTest

/// The V1 To-dos list (#10465, docs/apple/ios.md "To-dos (V1, #10465)") against the mock hub's To-dos view, in the
/// Sidebar layout on an iPhone, whose drawer has both the To-dos row and the Pages heading that folds it.
/// `-showTodosV1 YES` stands for the release; the list still needs a hub that lists `todo_view`.
///
/// - `PERSONA_TODOS_HUB` (`MOCK_TODO_VIEW=1`): the drawer's To-dos row, the folded Pages heading, the bar's Needs you
///   and the Needs you header say one N.
/// - `PERSONA_TODOS_LATE_HUB` (`MOCK_TODO_VIEW=late`): a hello that lists the view only after a reconnect never swaps the
///   page that is up; the next time To-dos opens, it is the V1 list.
/// - `PERSONA_TODOS_FRAMES=1` with `PERSONA_SHOTS=<dir>` (and `PERSONA_TODOS_HUB`): the V1 frames this list draws, saved
///   for review (`PERSONA_TODOS_SCHEME` light or dark, `PERSONA_TODOS_LANGUAGE`, `PERSONA_TODOS_COLOR`).
final class TodosV1ListTests: XCTestCase {
    private var app: XCUIApplication!
    private let env = ProcessInfo.processInfo.environment

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard UIDevice.current.userInterfaceIdiom == .phone else { throw XCTSkip("iPhone only") }
    }

    @MainActor private func launch(_ hub: String, _ extra: [String] = [], layout: String = "sidebar") {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos", "-showTodosV1", "YES",
                               "-app.navigation", layout] + extra
        app.launchPinned()
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// ☰ at a place's root, then a place in the drawer; ☰ tried again while the page under it still settles.
    @MainActor private func open(_ place: String) {
        let menu = element("shell-place-sidebar"), row = element(place)
        XCTAssertTrue(menu.waitForExistence(timeout: 20), "no ☰:\n\(app.debugDescription)")
        for _ in 0..<3 where !row.exists {
            if menu.isHittable { menu.tap() }
            _ = row.waitForExistence(timeout: 5)
        }
        XCTAssertTrue(row.exists, "no \(place) in the drawer:\n\(app.debugDescription)")
        row.tap()
    }

    /// The last number in a spoken label ("Needs you, 3"): the count a badge adds to its row.
    private func number(_ label: String) -> Int? {
        label.split(whereSeparator: { !$0.isNumber }).last.flatMap { Int($0) }
    }

    @MainActor func testOneNumberEverywhere() throws {
        guard let hub = env["PERSONA_TODOS_HUB"] else { throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_VIEW=1 (PERSONA_TODOS_HUB)") }
        launch(hub)
        let bar = element("todos.bar.needs_you")
        XCTAssertTrue(bar.waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        let header = element("todos.section.needs_you")
        XCTAssertTrue(header.waitForExistence(timeout: 20))
        let n = try XCTUnwrap(number(bar.label), bar.label)
        XCTAssertGreaterThan(n, 0)
        XCTAssertEqual(header.value as? String, String(n), "the Needs you header")

        element("sidebar-drawer-button").tap()
        let row = element("sidebar-todos")
        XCTAssertTrue(row.waitForExistence(timeout: 10), "no drawer:\n\(app.debugDescription)")
        XCTAssertEqual(number(row.label), n, "the drawer's To-dos row: \(row.label)")
        let places = element("sidebar-fold-places")
        places.tap()
        let tally = element("sidebar-fold-places.todos")
        XCTAssertTrue(tally.waitForExistence(timeout: 10), "no tally on the folded Pages heading:\n\(app.debugDescription)")
        XCTAssertEqual(tally.value as? String, String(n), "the folded Pages heading")
        places.tap() // unfolded again, as the next run expects
        XCTAssertTrue(row.waitForExistence(timeout: 10))
    }

    @MainActor func testAHelloThatComesLateNeverSwapsThePageUp() throws {
        guard let hub = env["PERSONA_TODOS_LATE_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_VIEW=late (PERSONA_TODOS_LATE_HUB)")
        }
        // Nothing remembered from an earlier run: the first hello decides, and it lacks the view. The phone's own layout:
        // ☰ opens the drawer from any place.
        launch(hub, ["-hubTodoFeatures.", "0"], layout: "separate")
        let today = element("todos.filter")
        XCTAssertTrue(today.waitForExistence(timeout: 60), "today's list first:\n\(app.debugDescription)")
        // The mock's hellos: without, with, without, with, MOCK_TODO_VIEW_LATE_SECONDS (5 s) apart.
        let until = Date().addingTimeInterval(22)
        while Date() < until {
            XCTAssertFalse(element("todos.v1").exists, "the page swapped under the owner")
            XCTAssertTrue(today.exists, "today's list went")
            sleep(1)
        }
        // Opened again, by way of the Inbox: the V1 list.
        open("sidebar-inbox")
        XCTAssertTrue(element("inbox.more").waitForExistence(timeout: 20), "no Inbox:\n\(app.debugDescription)")
        open("sidebar-todos")
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 20), "the V1 list the next time:\n\(app.debugDescription)")
    }

    /// The calm rule: with only the owner's plain to-dos left, no bar and no section headers; Someday stays folded at the
    /// end. The mock's other to-dos are deleted for it and put back after (`/mock/reset`).
    @MainActor func testTheCalmListHasNoBar() throws {
        guard let hub = env["PERSONA_TODOS_HUB"] else { throw XCTSkip("needs MOCK_TODO_VIEW=1 (PERSONA_TODOS_HUB)") }
        let base = try XCTUnwrap(URL(string: hub))
        func call(_ method: String, _ path: String) throws -> Data {
            var request = URLRequest(url: base.appending(path: path))
            request.httpMethod = method
            request.setValue("Bearer mock", forHTTPHeaderField: "Authorization")
            var out: Data?
            let done = expectation(description: path)
            URLSession.shared.dataTask(with: request) { data, _, _ in out = data; done.fulfill() }.resume()
            wait(for: [done], timeout: 20)
            return try XCTUnwrap(out)
        }
        defer { _ = try? call("GET", "mock/reset") }
        struct List: Decodable { struct Row: Decodable { var id: String }; var todos: [Row] }
        let plain = ["t11", "t12", "t14"]
        for row in try JSONDecoder().decode(List.self, from: call("GET", "api/v1/todos")).todos
        where !plain.contains(row.id) && !row.id.hasPrefix("t12-") {
            _ = try call("DELETE", "api/v1/todos/" + row.id)
        }
        launch(hub)
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        XCTAssertTrue(element("todos.row.t11").waitForExistence(timeout: 20))
        XCTAssertTrue(element("todos.fold.someday").exists, "Someday folded at the end")
        XCTAssertFalse(element("todos.bar.all").exists, "a calm list draws no bar")
        XCTAssertFalse(element("todos.section.todo").exists, "nor section headers")
    }

    /// The frames the list draws, for the review (no checks beyond reaching them).
    @MainActor func testFrames() throws {
        guard env["PERSONA_TODOS_FRAMES"] == "1", let hub = env["PERSONA_TODOS_HUB"], let dir = env["PERSONA_SHOTS"] else {
            throw XCTSkip("PERSONA_TODOS_FRAMES=1 with PERSONA_TODOS_HUB and PERSONA_SHOTS")
        }
        let scheme = env["PERSONA_TODOS_SCHEME"] ?? "light"
        let language = env["PERSONA_TODOS_LANGUAGE"] ?? "en"
        var extra = ["-ui.scheme", scheme, "-AppleLanguages", "(\(language))", "-AppleLocale", language == "en" ? "en_US" : "zh_CN"]
        if let color = env["PERSONA_TODOS_COLOR"] { extra += ["-ui.color", color] }
        if let size = env["PERSONA_TODOS_SIZE"] { extra += ["-UIPreferredContentSizeCategoryName", size] }
        let frames = Set((env["PERSONA_TODOS_FRAMES_ONLY"] ?? "").split(separator: ",").map(String.init))
        // `list` names the list as it opens (its frame id has no state).
        let wants = { (frame: String) in frames.isEmpty || frames.contains(frame.isEmpty ? "list" : frame) }
        let suffix = env["PERSONA_TODOS_SUFFIX"].map { "-" + $0 } ?? ""
        let shot = { (frame: String) in
            sleep(2) // the bar's reveal and the rows' marks settle
            let png = XCUIScreen.main.screenshot().pngRepresentation
            try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("iphone-list\(frame)\(suffix)-\(scheme).png"))
        }
        // The iPhone's own layout, as the frames draw it: ☰ on the list, the shell's drawer.
        launch(hub, extra, layout: "separate")
        // The list; a calm one has no bar.
        XCTAssertTrue(element("todos.v1").waitForExistence(timeout: 60), "no V1 list:\n\(app.debugDescription)")
        _ = element("todos.bar.all").waitForExistence(timeout: 10)
        if wants("") { shot("") }
        // A menu goes with a tap beside it, at the page's leading edge (it opens from the trailing one), then the page is
        // there to tap again.
        let away = {
            self.app.coordinate(withNormalizedOffset: CGVector(dx: 0.04, dy: 0.6)).tap()
            let bar = self.element("todos.bar.needs_you")
            for _ in 0..<10 where !bar.isHittable { usleep(300_000) }
        }
        if wants("~more") {
            element("todos.more").tap()
            shot("~more")
            away()
        }
        if wants("~group") {
            XCTAssertTrue(element("todos.grouping").waitForExistence(timeout: 10), "no grouping menu:\n\(app.debugDescription)")
            element("todos.grouping").tap()
            shot("~group")
            away()
        }
        for (frame, id) in [("~filter-needs", "needs_you"), ("~filter-progress", "in_progress"), ("~filter-todo", "todo")] where wants(frame) {
            element("todos.bar." + id).tap()
            shot(frame)
        }
        if wants("~scrolled") {
            element("todos.bar.all").tap()
            app.collectionViews.firstMatch.swipeUp(velocity: .slow)
            shot("~scrolled")
            app.collectionViews.firstMatch.swipeDown(velocity: .fast)
        }
        if wants("~drawer") {
            element("todos.bar.all").tap()
            element("shell-place-sidebar").tap()
            XCTAssertTrue(element("sidebar-todos").waitForExistence(timeout: 10))
            shot("~drawer")
        }
    }
}
