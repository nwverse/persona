import XCTest

/// /loop in an agent's composer (#2601, as on the web): the quick commands offer it to a Claude agent and picking it
/// only puts `/loop ` in the box, with a line over it saying what is still missing; a Codex agent's have no /loop.
/// The quick commands are in the composer's + menu in every style.
/// Demo Mode (Demo.json): a Claude agent at rest and the Codex one at work.
final class LoopCommandUITests: XCTestCase {
    private let claude = "macbook/a1f3c2d4-0000-4000-8000-000000000006"
    private let codex = "server/a1f3c2d4-0000-4000-8000-000000000004"
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor private func open(_ session: String) {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-openSession", session]
        app.launchPinned()
    }

    private var field: XCUIElement { app.textViews.firstMatch.exists ? app.textViews.firstMatch : app.textFields.firstMatch }

    /// Opens the quick commands; returns the /loop entry and one that is always there.
    /// `draft`: typed in the box first (before the "/" of the prompt bar's menu).
    @MainActor private func openCommands(after draft: String = "") -> (loop: XCUIElement, report: XCUIElement) {
        let plus = app.buttons.matching(NSPredicate(format: "label == '添加' OR label == '添加照片或文件' OR label == '添加附件'")).firstMatch
        XCTAssertTrue(plus.waitForExistence(timeout: 20), "no + in the composer")
        sleep(2) // the session list loads
        clearDraft() // a failed run's draft stays for the next launch
        if !draft.isEmpty { field.typeText(draft) }
        plus.tap()
        XCTAssertTrue(app.buttons["照片"].waitForExistence(timeout: 5), "the + menu did not open")
        return (app.buttons["定时重复 /loop"], app.buttons["汇报进展"])
    }

    /// Empties the box, focused, at the end of what is there.
    @MainActor private func clearDraft() {
        field.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap()
        let value = field.value as? String ?? ""
        let typed = value == field.placeholderValue ? 0 : value.count
        if typed > 0 { field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: typed + 2)) }
    }

    @MainActor private func shot(_ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "\(name) \(UITestStyle.name)"
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testAClaudeAgentsMenuStartsALoopDraft() {
        open(claude)
        let (loop, _) = openCommands()
        XCTAssertTrue(loop.waitForExistence(timeout: 5), "no /loop in the quick commands")
        shot("quick commands")
        loop.tap()
        let hint = app.descendants(matching: .any)["chat.loopHint"]
        XCTAssertTrue(hint.waitForExistence(timeout: 5), "no line over the box for the /loop draft")
        XCTAssertTrue(hint.label.contains("格式：/loop"), "the line does not say what is missing: \(hint.label)")
        XCTAssertEqual((field.value as? String)?.trimmingCharacters(in: .whitespaces), "/loop", "the box does not hold the command")
        field.typeText("10m 看下 CI")
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "每 10 分钟重复：看下 CI"))
                          .firstMatch.waitForExistence(timeout: 5), "the line does not follow the draft: \(field.value ?? "")")
        shot("/loop draft")
        clearDraft()
        sleep(2) // drafts are saved a moment after the last change
    }

    /// What was typed first becomes the task: picking /loop puts the word before it, not in its place.
    @MainActor func testWhatIsTypedBecomesTheLoopsTask() {
        open(claude)
        let (loop, _) = openCommands(after: "看下 CI")
        XCTAssertTrue(loop.waitForExistence(timeout: 5), "no /loop in the quick commands")
        loop.tap()
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "由 Claude 自己定节奏重复：看下 CI"))
                          .firstMatch.waitForExistence(timeout: 5), "the typed words are not the task: \(field.value ?? "")")
        field.typeText("10m ") // the caret is after the word
        XCTAssertTrue(app.descendants(matching: .any).matching(NSPredicate(format: "label == %@", "每 10 分钟重复：看下 CI"))
                          .firstMatch.waitForExistence(timeout: 5), "the interval did not go after the word: \(field.value ?? "")")
        shot("/loop over a draft")
        clearDraft()
        sleep(2) // drafts are saved a moment after the last change
    }

    @MainActor func testACodexAgentsMenuHasNoLoop() {
        open(codex)
        let (loop, report) = openCommands()
        XCTAssertTrue(report.waitForExistence(timeout: 5), "the quick commands did not open")
        XCTAssertFalse(loop.exists, "/loop offered to a Codex agent")
        clearDraft()
        sleep(2)
    }
}
