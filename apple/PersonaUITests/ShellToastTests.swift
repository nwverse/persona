import XCTest

/// The app's toasts in the shell (docs/apple/shell.md「Connection status and toasts」, #1053): the result of something just done (Archived, Copied…)
/// floats over everything in a window of its own and moves nothing: the Agents list's first row, the drawer's rows and
/// the status row stay where they are while it shows, what is under it takes the taps, and it is not under the
/// keyboard. It used to open the status row, which pushed the pages and the drawer down and back.
final class ShellToastTests: XCTestCase {
    private var app: XCUIApplication!

    struct Place: Decodable { var row: Double }
    struct Scroll: Decodable { var now: Place? }

    override func setUp() {
        continueAfterFailure = true
    }

    /// Demo Mode in the shell; toasts `at` seconds after launch (`-showToasts`), time enough to read the page first.
    @MainActor private func launch(_ page: String, toasts: String, _ extra: [String] = []) {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-dev.openChat", "", "-showToasts", toasts]
            + (page == "dev" ? ["-app.tab", "chats", "-tab", "dev", "-openAgentsFresh", "YES"] : ["-tab", "assistant", "-newAssistant", "YES"])
            + extra
        app.launchPinned()
    }

    @MainActor private func attachScreen(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    private var toast: XCUIElement { app.descendants(matching: .any)["toast"].firstMatch }

    /// The page as read before any toast: none must be up yet (the launch took longer than the toasts' delay otherwise).
    @MainActor private func beforeTheToast(_ what: String) {
        XCTAssertFalse(toast.exists, "a toast was up before the page was read (\(what)): launch slower than the toasts' delay")
    }

    /// Waits for the toast with these words.
    @MainActor @discardableResult private func expectToast(_ words: String, _ what: String) -> Bool {
        let ok = toast.waitForExistence(timeout: 15) && toast.label.contains(words)
        XCTAssertTrue(ok, "no toast 「\(words)」 (\(what)): \(toast.exists ? toast.label : "none")")
        return ok
    }

    /// The toast brought no link state with it.
    @MainActor private func rowStayedFolded(_ what: String) {
        XCTAssertEqual(app.shellValue("opened"), "0", "the link's state showed for a toast (\(what)) \(app.shellTrace)")
    }

    /// Agents: the list's first row (`probe.agents-scroll`) and the bar's buttons stay put while toasts show, and the
    /// switch under them takes its tap.
    @MainActor func testAToastOverTheAgentsListMovesNothingAndLetsTheTapsThrough() {
        launch("dev", toasts: "10:success:已归档;12.3:success:已置顶")
        XCTAssertTrue(app.buttons.matching(identifier: "agents.row").firstMatch.waitForExistence(timeout: 15), "no agent in the list")
        let finger = ShellFinger(app)
        sleep(2) // the list settles, the search folds itself away once
        let row = { self.app.probe("agents-scroll", as: Scroll.self, page: "agents")?.now?.row }
        let new = app.buttons["agents-new"].firstMatch
        beforeTheToast("Agents")
        let rowBefore = row(), newBefore = new.frame
        XCTAssertNotNil(rowBefore, "no probe.agents-scroll")

        guard expectToast("已归档", "Agents") else { return }
        attachScreen("toast over the Agents list")
        XCTAssertEqual(row() ?? -1, rowBefore ?? -2, accuracy: 0.5, "the first row moved as the toast showed")
        XCTAssertEqual(new.frame.minY, newBefore.minY, accuracy: 0.5, "新建会话 moved as the toast showed")
        rowStayedFolded("Agents")
        // Below the top bar, not on the switch (it hid Assistant / Agents), and the top bar takes its taps meanwhile.
        XCTAssertGreaterThanOrEqual(toast.frame.minY, newBefore.maxY - 1, "the toast \(toast.frame) is on the top bar (新建会话 \(newBefore))")
        finger.switchTo(agents: false, "助手 tapped while the toast shows")
        XCTAssertTrue(toast.exists, "the toast went with the tap: it took it")
        finger.switchTo(agents: true, "Agents tapped while the toast shows")
        XCTAssertTrue(toast.waitForNonExistence(timeout: 10), "the toast did not go by itself")
        XCTAssertEqual(row() ?? -1, rowBefore ?? -2, accuracy: 0.5, "the first row moved as the toast went")
        rowStayedFolded("Agents, after")
        app.terminate()
    }

    /// The drawer: its rows stay put under a toast, and the row right under it (Persona, the main conversation) takes a
    /// tap there.
    @MainActor func testAToastOverTheDrawerMovesNothingAndLetsTheTapsThrough() {
        launch("assistant", toasts: "10:success:已归档;12.3:success:已删除")
        let face = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(face.waitForExistence(timeout: 15), "no face on 助手")
        face.tapCenter()
        let inbox = app.buttons["sidebar-inbox"].firstMatch
        XCTAssertTrue(inbox.waitForExistence(timeout: 5), "the drawer did not open")
        sleep(1) // the drawer's slide
        beforeTheToast("the drawer")
        let before = inbox.frame

        guard expectToast("已归档", "the drawer") else { return }
        attachScreen("toast over the drawer")
        XCTAssertEqual(inbox.frame.minY, before.minY, accuracy: 0.5, "收件箱 in the drawer moved as the toast showed")
        rowStayedFolded("the drawer")
        // A tap on the toast itself is the row's under it.
        let main = app.buttons["sidebar-main"].firstMatch, over = toast.frame
        XCTAssertTrue(main.frame.intersects(over), "Persona \(main.frame) is not under the toast \(over): this tap tells nothing")
        let both = over.intersection(main.frame) // on the toast and on the row
        app.windows.firstMatch.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: both.midX, dy: both.midY)).tap()
        // The row opens the main conversation: the drawer goes back in.
        XCTAssertTrue(app.descendants(matching: .any)["assistant-drawer-close"].firstMatch.waitForNonExistence(timeout: 5),
                      "Persona under the toast took no tap \(app.shellTrace)")
        app.terminate()
    }

    /// With the keyboard up, the toast is on screen above it.
    @MainActor func testAToastWithTheKeyboardUpIsAboveIt() {
        launch("assistant", toasts: "10:success:已复制")
        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 15), "no composer on 助手")
        field.tap()
        let keyboard = app.keyboards.firstMatch
        XCTAssertTrue(keyboard.waitForExistence(timeout: 5), "no keyboard")
        beforeTheToast("the keyboard")
        guard expectToast("已复制", "the keyboard") else { return }
        attachScreen("toast with the keyboard up")
        let screen = app.windows.firstMatch.frame
        XCTAssertTrue(screen.contains(toast.frame), "the toast \(toast.frame) is off the screen \(screen)")
        XCTAssertFalse(toast.frame.intersects(keyboard.frame), "the toast \(toast.frame) is under the keyboard \(keyboard.frame)")
        XCTAssertTrue(keyboard.exists, "the toast took the keyboard away")
        app.terminate()
    }
}
