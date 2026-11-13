import XCTest

/// Got It on the To-dos list (#5707, docs/assistant/todo-status-sop.md "Seen") against the mock hub (`MOCK_TODO_LIST=1`):
/// unfinished to-dos (Other Open) are listed open above Just Finished; Got It on a Just Finished row sits at its right, on
/// the status line under the time (#6578), takes a finger beside it, files the row under Done at once without opening
/// the to-do, and the toast's Undo puts it back. `TEST_RUNNER_PERSONA_TODOS_HUB=<mock URL>`; `TEST_RUNNER_PERSONA_SHOTS=<dir>`
/// also saves the screens (`TEST_RUNNER_PERSONA_SHOTS_APPEARANCE` names them).
final class TodoSeenTests: XCTestCase {
    private var app: XCUIApplication!

    @MainActor func testGotItFilesAJustFinishedRowUnderDoneAndUndoPutsItBack() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_TODOS_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TODO_LIST=1 (PERSONA_TODOS_HUB)")
        }
        continueAfterFailure = false
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-openShellPage", "todos", "-todos.view", "list",
                               "-todos.filter", "all", "-todos.sort", "recent"]
        app.launchPinned()

        XCTAssertTrue(app.buttons["todos.filter"].waitForExistence(timeout: 60))
        // Other Open starts open: its header is a plain section header, not a fold button (Shelved's is one)
        XCTAssertTrue(app.descendants(matching: .any)["todos.section.other"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["todos.section.other"].exists, "Other Open starts folded")
        shot("todos-seen-top")

        let gotIt = app.buttons["todos.seen.tl_speed"]
        for _ in 0..<8 where !gotIt.isHittable { app.swipeUp() }
        XCTAssertTrue(gotIt.waitForExistence(timeout: 10), "no Got It on the Just Finished row")
        // at the row's right, on the status line under the title, not a line of its own at the bottom (#6578)
        let row = app.descendants(matching: .any)["todos.row.tl_speed"]
        XCTAssertTrue(row.exists, "no Just Finished row")
        XCTAssertGreaterThan(gotIt.frame.minX, row.frame.midX, "Got It is not at the row's right")
        XCTAssertLessThan(gotIt.frame.midY, row.frame.midY, "Got It is not on the status line")
        shot("todos-seen-row")
        // a finger 18 pt under its middle, past the capsule, still takes Got It (a 44 pt target), never the row
        gotIt.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).withOffset(CGVector(dx: 0, dy: 18)).tap()
        let undo = app.buttons.matching(NSPredicate(format: "label IN %@", ["Undo", "撤销", "取り消す"])).firstMatch // i18n-ignore: the toast's Undo in each language
        XCTAssertTrue(undo.waitForExistence(timeout: 5), "no toast with Undo")
        XCTAssertTrue(gotIt.waitForNonExistence(timeout: 5), "the row stayed in Just Finished")
        XCTAssertTrue(app.buttons["todos.filter"].isHittable, "Got It also opened the to-do")
        shot("todos-seen-toast")
        undo.tap()
        XCTAssertTrue(app.buttons["todos.seen.tl_speed"].waitForExistence(timeout: 10), "Undo did not put it back")
    }

    @MainActor private func shot(_ name: String) {
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
