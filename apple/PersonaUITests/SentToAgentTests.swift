import XCTest

/// What Persona sent to its tasks' agents in the main conversation (#1542): never the user's bubble. The hand-off sent with
/// a task's card is only on that card (「已发 1 条指令」, a tap lists it, a tap on it opens the text with Copy); a later
/// message is one folded line that opens to its text; the user's own word to a task stays the user's bubble. Against
/// scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_SENT=1 MOCK_TASKS_NO_ASKS=1 (PERSONA_MOCK_HUB). With PERSONA_SHOTS
/// set it writes the folded and open states there (PERSONA_SHOTS_APPEARANCE names the simulator's appearance in the file).
final class SentToAgentTests: XCTestCase {
    private var hub = ""
    private let main = "server/b2e4d6f8-0000-4000-8000-000000000001"

    override func setUpWithError() throws {
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_SENT=1 MOCK_TASKS_NO_ASKS=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        continueAfterFailure = false
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let env = ProcessInfo.processInfo.environment
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "unknown"
        try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/sent-\(appearance)-\(name).png"))
    }

    @MainActor func testPersonasMessagesFoldAndTheHandOffIsOnTheCard() throws {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        // The folded line is the previous look's (`AgentSentLine`); the new conversation layout draws Persona's message as
        // "Persona → @task" instead (#1666, NewChatLayoutTests).
        app.launchArguments = ["-pairURL", pair, "-app.tab", "assistant", "-tab", "assistant", "-openAssistant", main, "-ui.newChatLayout", "NO"]
        app.launchPinned()

        let any = app.descendants(matching: .any)
        func has(_ text: String) -> XCUIElement { any.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch }
        // The card's own button takes up the row's identifier too (SwiftUI): the row is the one not named by the card's title.
        let list = app.buttons.matching(NSPredicate(format: "identifier == %@ AND NOT (label CONTAINS %@)", "task-sent", "降低后台同步的内存占用")).firstMatch
        XCTAssertTrue(list.waitForExistence(timeout: 20), "no list of what was sent on the task's card:\n\(app.debugDescription)")
        sleep(1) // the list settles at the newest
        // The hand-off is not drawn on its own: its words are nowhere until the card's list is opened.
        XCTAssertFalse(has("常驻内存降下来").exists, "the hand-off is drawn outside its card")
        // The rework to the widget fix, whose card is far above: a folded line that says where it went.
        let line = any.matching(NSPredicate(format: "identifier == %@ AND label CONTAINS %@", "sent-line", "修小组件横屏崩溃")).firstMatch
        XCTAssertTrue(line.exists, "the rework is not a folded line saying where it went:\n\(app.debugDescription)")
        XCTAssertTrue(line.label.contains("督办"), "the folded line does not show the first line: \(line.label)")
        XCTAssertFalse(any["sent-detail"].exists, "a folded line starts open")
        XCTAssertTrue(has("先补重试和超时").exists, "the user's own word to a task is gone")
        try shot(app, "folded")

        line.coordinate(withNormalizedOffset: CGVector(dx: 0.8, dy: 0.5)).tap()
        let detail = any["sent-detail"].firstMatch
        XCTAssertTrue(detail.waitForExistence(timeout: 5), "the folded line did not open")
        XCTAssertTrue(has("补一张横屏的截图贴进 PR").exists, "the open line is not whole")
        sleep(1)
        try shot(app, "line-open")
        line.coordinate(withNormalizedOffset: CGVector(dx: 0.6, dy: 0.5)).tap()
        let folded = XCTNSPredicateExpectation(predicate: NSPredicate(format: "exists == false"), object: detail)
        XCTAssertEqual(XCTWaiter.wait(for: [folded], timeout: 5), .completed, "the line did not fold:\n\(app.debugDescription)")

        list.tap()
        let entry = any["task-sent-entry"].firstMatch
        XCTAssertTrue(entry.waitForExistence(timeout: 5), "the card's list did not open")
        XCTAssertTrue(entry.label.contains("常驻内存降下来"), "the list does not show the hand-off's first line: \(entry.label)")
        entry.tap()
        XCTAssertTrue(any["sent-copy"].firstMatch.waitForExistence(timeout: 5), "the hand-off did not open with Copy")
        XCTAssertTrue(has("PR 里有改前改后的 Allocations 截图").exists, "the open hand-off is not whole")
        sleep(1)
        try shot(app, "card-open")
        app.terminate()
    }
}
