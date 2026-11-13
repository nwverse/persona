import XCTest

/// The hub's link in the shell (docs/apple/shell.md「Connection status and toasts」): Waiting for Network / Connecting… / Updating… float under the top bar
/// like a toast (#413). Here: in every state, on both pages, it shows, sits below the top bar's buttons, and nothing on
/// the page moves (the controls a finger uses, the Agents rows), while it shows and once it goes. It used to take a row
/// of room, pushing everything down and back.
final class ShellStatusRowTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = true
    }

    @MainActor private func launch(_ page: String, _ extra: [String]) {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-tab", page, "-dev.openChat", "", "-dev.folded", ""]
            + (page == "assistant" ? ["-newAssistant", "YES"] : []) + extra
        app.launchPinned()
        XCTAssertTrue(app.descendants(matching: .any)["probe.shell"].waitForExistence(timeout: 15), "no shell")
    }

    @MainActor private func attachScreen(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// Waits until the row shows `status` (none / link).
    @MainActor @discardableResult private func expectRow(_ status: String, within: TimeInterval, _ what: String) -> Bool {
        let start = Date()
        while app.shellValue("status") != status, Date().timeIntervalSince(start) < within { usleep(100_000) }
        let ok = app.shellValue("status") == status
        if !ok { attachScreen(what); XCTFail("the row is not \(status) within \(within) s: \(what) \(app.shellTrace)") }
        return ok
    }

    /// The controls a finger uses at the top of `page`, by name.
    @MainActor private func controls(_ page: String) -> [(String, XCUIElement)] {
        let any = app.descendants(matching: .any)
        var c: [(String, XCUIElement)] = [("face", app.buttons[page == "dev" ? "work-sidebar-button" : "assistant-sidebar-button"].firstMatch),
                                          ("助手", app.buttons["shell-assistant"].firstMatch), ("Agents", app.buttons["shell-agents"].firstMatch)]
        if page == "dev" {
            c += [("新建会话", app.buttons["agents-new"].firstMatch), ("全部", app.buttons["agents.filter.all"].firstMatch),
                  ("search", app.searchFields.firstMatch)]
        } else {
            c += [("新对话", app.buttons["assistant-new"].firstMatch), ("composer", any["shell-composer-field"].firstMatch)]
        }
        return c
    }

    @MainActor private func frames(_ page: String) -> [String: CGRect] {
        Dictionary(uniqueKeysWithValues: controls(page).compactMap { name, e in e.exists && !e.frame.isEmpty ? (name, e.frame) : nil })
    }

    /// The Agents rows on screen, top to bottom: their titles and where they are.
    /// The first four (reading every row's place takes seconds, and the link's state comes and goes meanwhile).
    @MainActor private func rows() -> [(String, CGFloat)] {
        let screen = app.windows.firstMatch.frame
        let all = app.descendants(matching: .any).matching(identifier: "agents.row")
        return (0..<4).map { all.element(boundBy: $0) }
            .filter { $0.exists && screen.intersects($0.frame) }
            .map { (Self.stable($0.label), $0.frame.minY) }
            .sorted { $0.1 < $1.1 }
    }

    /// A row's label without its relative time: 「19 分钟前」 turns 「20 分钟前」 between two reads a minute apart.
    private static func stable(_ label: String) -> String {
        label.components(separatedBy: "、").filter { $0.range(of: #"^(\d+ ?(秒|分钟|小时|天)前|刚刚)$"#, options: .regularExpression) == nil }
            .joined(separator: "、")
    }

    /// The link's states come and go (`-showConnectionCycle`): while one shows and after it goes, everything is where
    /// it was, and the pill is below the bar's buttons.
    @MainActor func testTheLinkFloatsAndMovesNothingInEveryState() {
        for page in ["dev", "assistant"] {
            for phase in ["waiting", "connecting", "updating"] {
                let what = "\(phase) on \(page)"
                // Fine for 12 s, then the state for 12 s (reading every control's place takes seconds), again and again.
                launch(page, ["-showConnectionCycle", "\(phase):12:12"])
                if page == "dev" { app.revealAgentsSearch() }
                guard expectRow("none", within: 10, "\(what), fine first") else { app.terminate(); continue }
                sleep(1)
                let before = frames(page)
                let rowsBefore = page == "dev" ? rows() : []
                guard expectRow("link", within: 30, what) else { app.terminate(); continue }
                sleep(1) // its fade
                let pill = app.descendants(matching: .any)["shell-connection"].firstMatch
                XCTAssertTrue(pill.exists, "no link pill (\(what))")
                let pillFrame = pill.frame // read now: the frames below take seconds, and the state does not last
                for (name, f) in frames(page) {
                    if let b = before[name] { XCTAssertEqual(f.minY, b.minY, accuracy: 1, "\(name) moved \(f.minY - b.minY) as the link showed (\(what))") }
                }
                for name in ["face", "助手", "Agents", "新建会话", "新对话"] {
                    if let f = before[name] { XCTAssertGreaterThanOrEqual(pillFrame.minY, f.maxY - 1, "the pill \(pillFrame) is over \(name) \(f) (\(what))") }
                }
                if page == "dev" {
                    let rowsDuring = rows()
                    XCTAssertEqual(rowsDuring.map(\.0), rowsBefore.map(\.0).prefix(rowsDuring.count).map { $0 }, "the rows changed (\(what))")
                    for (b, d) in zip(rowsBefore, rowsDuring) where b.0 == d.0 {
                        XCTAssertEqual(d.1, b.1, accuracy: 1, "row \(b.0) moved \(d.1 - b.1) (\(what))")
                    }
                }
                attachScreen(what)
                guard expectRow("none", within: 16, "\(what), back") else { app.terminate(); continue }
                sleep(1)
                for (name, f) in frames(page) {
                    if let b = before[name] { XCTAssertEqual(f.minY, b.minY, accuracy: 1, "\(name) moved after \(what)") }
                }
                app.terminate()
            }
        }
    }
}
