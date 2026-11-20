import XCTest

/// A standing (「常驻」) lead spins while something under it runs. Its to-do text counts what waits on you (「等你 N」),
/// and the sidebar shows separate Agent totals when the hub supplies them (#6626, #11712).
/// Against scripts/mock/mock_hub.py with MOCK_LEADERS=1
/// (PERSONA_LEADERS_HUB), whose tree has a lead with an Agent at work (「基础设施」) and one counting a working to-do whose
/// Agent has stopped (「增长」).
/// SHOT_DIR keeps the screenshots.
final class DirectionRowsUITests: XCTestCase {
    private var hub: String!
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_LEADERS_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_LEADERS=1 (PERSONA_LEADERS_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch(_ extra: [String] = []) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-newAssistant", "YES", "-dev.openChat", ""] + extra
        app.launchPinned()
    }

    /// The drawer's button on the phone (the hittable one of the pager's pages; tapped again while a busy simulator has
    /// not opened the drawer yet); the iPad's column is there already. Open once its To-dos entry shows: with four
    /// leads the All Chats row is below the fold, and the lazy list does not make it.
    @MainActor private func openSidebar(timeout: TimeInterval = 30) {
        let all = app.buttons["sidebar-todos"].firstMatch
        let end = Date().addingTimeInterval(timeout)
        while Date() < end && !(all.exists && all.isHittable) {
            if let menu = app.buttons.matching(identifier: "assistant-sidebar-button").allElementsBoundByIndex.first(where: \.isHittable) {
                menu.tapCenter()
                if all.waitForExistence(timeout: 5) { break }
            } else {
                usleep(500_000)
            }
        }
        XCTAssertTrue(all.exists, "the sidebar did not open:\n\(app.debugDescription)")
    }

    @MainActor private func shot(_ name: String) {
        let screen = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: screen)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["SHOT_DIR"] {
            let suffix = ProcessInfo.processInfo.environment["SHOT_SUFFIX"].map { "-" + $0 } ?? ""
            try? screen.pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/directions-\(name)\(suffix).png"))
        }
    }

    /// A lead's row, by its direction's name (the row's label starts with it).
    @MainActor private func lead(_ name: String) -> XCUIElement {
        app.buttons.matching(identifier: "sidebar-conversation").matching(NSPredicate(format: "label BEGINSWITH %@", name)).firstMatch
    }

    /// Scroll the drawer itself on a phone and the sidebar column on a tablet.
    @MainActor private func showInSidebar(_ element: XCUIElement) {
        let rect = app.frame
        let x = app.buttons["sidebar-todos"].firstMatch.frame.midX - rect.minX
        let origin = app.coordinate(withNormalizedOffset: .zero)
        for _ in 0..<6 {
            if element.exists && element.isHittable && element.frame.maxY < rect.maxY - 44 { return }
            origin.withOffset(CGVector(dx: x, dy: rect.height * 0.76))
                .press(forDuration: 0.1, thenDragTo: origin.withOffset(CGVector(dx: x, dy: rect.height * 0.5)))
        }
    }

    @MainActor func testDirectionRowsShowBothAgentCounts() throws {
        let env = ProcessInfo.processInfo.environment
        guard let fixture = env["PERSONA_DIRECTION_COUNTS_FIXTURE"] else {
            throw XCTSkip("Point PERSONA_LEADERS_HUB at MOCK_LEADERS=1 and set PERSONA_DIRECTION_COUNTS_FIXTURE to current or legacy.")
        }
        let language = env["PERSONA_DIRECTION_LANGUAGE"] ?? "zh-Hans"
        var args = ["-AppleLanguages", "(\(language))"]
        if let size = env["PERSONA_DIRECTION_TYPE"] { args += ["-UIPreferredContentSizeCategoryName", size] }
        launch(args)
        openSidebar()
        let product = lead("Persona 产品")
        let infra = lead("基础设施")
        showInSidebar(infra)
        XCTAssertTrue(product.waitForExistence(timeout: 15))
        XCTAssertTrue(infra.waitForExistence(timeout: 15))
        let labels: (product: String, infra: String, stopped: String, attention: String, spawned: String)
        switch language {
        case "en": labels = ("Running 2 · Stopped 3", "Running 0 · Stopped 1", "Stopped", "1 need you", "Spawned from Chats")
        case "ja": labels = ("実行中 2 · 停止中 3", "実行中 0 · 停止中 1", "停止中", "対応待ち 1", "チャットから派生")
        default: labels = ("运行中 2 · 已停 3", "运行中 0 · 已停 1", "已停", "等你 1", "对话派生")
        }
        // The separate fold control appears when the tree arrives, even on older hubs that omit agent_counts.
        let toggle = app.buttons.matching(identifier: "sidebar-direction-toggle").firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 15))
        let rows = XCTAttachment(string: [product.label, infra.label].joined(separator: "\n"))
        rows.name = "direction-accessibility-labels"
        rows.lifetime = .keepAlways
        add(rows)
        shot("agent-counts")
        XCTAssertTrue(product.label.contains(labels.attention), product.label)
        XCTAssertTrue(infra.label.contains(labels.attention), infra.label)
        if fixture == "legacy" {
            XCTAssertFalse(product.label.contains(labels.stopped), product.label)
            XCTAssertFalse(infra.label.contains(labels.stopped), infra.label)
        } else {
            XCTAssertTrue(product.label.contains(labels.product), product.label)
            XCTAssertTrue(infra.label.contains(labels.infra), infra.label)
        }
        XCTAssertFalse(product.label.contains(labels.spawned), product.label)
    }

    @MainActor func testTheFoldOpensTasksAndTheRowOpensTheConversation() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_DIRECTION_COUNTS_FIXTURE"] == "current" else {
            throw XCTSkip("Requires MOCK_LEADERS=1 and MOCK_DIRECTION_AGENT_COUNTS=1.")
        }
        launch(["-AppleLanguages", "(en)"])
        openSidebar()
        let toggle = app.buttons.matching(identifier: "sidebar-direction-toggle")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "Persona 产品")).firstMatch
        let task = app.buttons.matching(identifier: "sidebar-direction-task")
            .matching(NSPredicate(format: "label BEGINSWITH %@", "天气 App 发 1.3 版")).firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 15))
        if task.exists { toggle.tap() }
        toggle.tap()
        XCTAssertTrue(task.waitForExistence(timeout: 8))
        toggle.tap()
        XCTAssertTrue(task.waitForNonExistence(timeout: 8))
        lead("Persona 产品").tap()
        openSidebar()
        XCTAssertTrue(lead("Persona 产品").isSelected)
    }

    @MainActor func testAnOrdinarySpawnedConversationKeepsItsSource() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_DIRECTION_ORIGINS"] == "1" else {
            throw XCTSkip("Requires the fixture with MOCK_ORIGINS=1.")
        }
        launch(["-AppleLanguages", "(en)"])
        openSidebar()
        let all = app.buttons["sidebar-all-chats"].firstMatch
        showInSidebar(all)
        XCTAssertTrue(all.exists && all.isHittable)
        all.tap()
        let spawned = app.buttons["auto-filter-spawned"].firstMatch
        XCTAssertTrue(spawned.waitForExistence(timeout: 10))
        spawned.tap()
        let ordinary = app.buttons.matching(identifier: "auto-conversation")
            .matching(NSPredicate(format: "label CONTAINS %@", "竞品定价")).firstMatch
        XCTAssertTrue(ordinary.waitForExistence(timeout: 8))
        XCTAssertTrue(ordinary.label.contains("Spawned from Chats"), ordinary.label)
        XCTAssertFalse(ordinary.label.contains("Stopped"), ordinary.label)
    }

    @MainActor func testALeadSpinsOnWhatRunsUnderItAndSaysOnlyWhatWaitsOnYou() {
        launch()
        openSidebar()
        let infra = lead("基础设施")
        XCTAssertTrue(infra.waitForExistence(timeout: 10), "no 基础设施 lead:\n\(app.debugDescription)")
        sleep(2) // the tree's read lands after the conversations
        shot("sidebar") // before the checks, so a failing run still shows what it drew
        // An Agent of one of its to-dos is at work: the row spins, and VoiceOver hears the spinner's word.
        XCTAssertTrue(infra.label.contains("工作中"), "基础设施 has an Agent at work but does not spin: \(infra.label)")
        XCTAssertTrue(infra.label.contains("等你 1"), "基础设施's to-do waiting on you is not said: \(infra.label)")
        // A working to-do whose Agent has stopped: nothing runs, so no spinner (the working count used to spin it).
        let growth = lead("增长")
        XCTAssertTrue(growth.exists, "no 增长 lead")
        XCTAssertFalse(growth.label.contains("工作中"), "增长 spins with nothing running under it: \(growth.label)")
        // A lead never labels its Agent totals as the old working-to-do count (「在做 N」).
        let leads = app.buttons.matching(identifier: "sidebar-conversation").allElementsBoundByIndex
        for row in leads where ["基础设施", "增长", "支付对账", "Persona 产品"].contains(where: { row.label.hasPrefix($0) }) {
            XCTAssertFalse(row.label.contains("在做"), "a lead still says 在做: \(row.label)")
        }
    }

    @MainActor func testTheToDosPinnedSectionsEndAsTheirRowsDo() {
        // The list view whatever an earlier test left (the calendar keeps itself per device).
        launch(["-todos.grouping", "direction", "-todos.view", "list"])
        openSidebar()
        app.buttons["sidebar-todos"].firstMatch.tap()
        let header = app.descendants(matching: .any).matching(identifier: "todos.direction").firstMatch
        XCTAssertTrue(header.waitForExistence(timeout: 15), "the To-dos page is not grouped by 常驻:\n\(app.debugDescription)")
        sleep(2)
        shot("todos")
        // A list builds a row, and its section's header, only once it is on screen, and 「增长」 sits below the fold on a
        // phone: read the headers while scrolling down to it.
        var labels: [String] = []
        let list = app.collectionViews.firstMatch
        for round in 0..<8 {
            for h in app.descendants(matching: .any).matching(identifier: "todos.direction").allElementsBoundByIndex where !labels.contains(h.label) {
                labels.append(h.label)
            }
            if labels.contains(where: { $0.hasPrefix("增长") }) || round == 7 { break }
            list.swipeUp(velocity: .slow)
        }
        XCTAssertFalse(labels.isEmpty)
        for label in labels {
            XCTAssertFalse(label.contains("在做"), "a 常驻 section still says 在做: \(label)")
        }
        // the spinner exactly where something runs: 「基础设施」 has an Agent at work, 「增长」's working to-do has none
        let infra = labels.first { $0.hasPrefix("基础设施") }
        XCTAssertTrue(infra?.contains("工作中") == true, "基础设施's section does not spin: \(infra ?? "missing")")
        let growth = labels.first { $0.hasPrefix("增长") }
        XCTAssertTrue(growth.map { !$0.contains("工作中") } ?? false, "增长's section spins or is missing: \(growth ?? "missing")")
    }
}
