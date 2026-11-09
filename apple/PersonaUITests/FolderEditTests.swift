import XCTest

/// Rename, move, select, move to the trash and Undo in a machine's home (#315, docs/protocol/protocol.md "Changing files"),
/// against scripts/mock/mock_hub.py started with MOCK_FILES=1 MOCK_FILE_OPS=1:
///   TEST_RUNNER_PERSONA_FILES_HUB=http://127.0.0.1:<port>
/// TEST_RUNNER_PERSONA_SHOTS=<dir> also saves a screenshot of each step there. The mock's home has code/ and notes.txt.
final class FolderEditTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_FILES_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_FILES=1 MOCK_FILE_OPS=1 (PERSONA_FILES_HUB)")
        }
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook"]
        app.launchPinned()
    }

    private func shot(_ name: String) {
        let s = app.screenshot()
        let a = XCTAttachment(screenshot: s)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"].map { "-" + $0 } ?? ""
            let device = UIDevice.current.userInterfaceIdiom == .pad ? "-ipad" : ""
            try? s.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(name + device + look + ".png"))
        }
    }

    private func openHome() {
        let files = app.buttons["machine.files"]
        XCTAssertTrue(files.waitForExistence(timeout: 40), "no 文件 row on the machine page")
        for _ in 0..<6 where !files.isHittable { app.swipeUp(velocity: .slow) }
        files.tap()
        XCTAssertTrue(app.staticTexts["notes.txt"].waitForExistence(timeout: 15))
    }

    func testRenameMoveTrashAndUndo() {
        openHome()

        // Long press: the changes under Save / Copy Path.
        app.staticTexts["notes.txt"].press(forDuration: 1.2)
        let rename = app.buttons["重命名"]
        XCTAssertTrue(rename.waitForExistence(timeout: 5), "no 重命名 in the menu")
        XCTAssertTrue(app.buttons["移动"].exists && app.buttons["移到废纸篓"].exists, "no 移动 / 移到废纸篓 in the menu")
        shot("edit-menu")
        rename.tap()

        let alert = app.alerts.firstMatch
        XCTAssertTrue(alert.waitForExistence(timeout: 5), "no rename alert")
        let field = alert.textFields.firstMatch
        XCTAssertEqual(field.value as? String, "notes.txt", "the field does not start with the old name")
        shot("edit-rename")
        field.tap()
        // Return commits the letters (the simulator's keyboard may be pinyin, which holds them as a composition).
        field.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: 12) + "todo.txt\n")
        if alert.buttons["重命名"].exists { alert.buttons["重命名"].tap() }
        XCTAssertTrue(app.staticTexts["todo.txt"].waitForExistence(timeout: 15), "the renamed file never showed up")
        XCTAssertFalse(app.staticTexts["notes.txt"].exists, "the old name is still listed")

        // Move into code/ with the folder picker.
        app.staticTexts["todo.txt"].press(forDuration: 1.2)
        XCTAssertTrue(app.buttons["移动"].waitForExistence(timeout: 5))
        app.buttons["移动"].tap()
        let here = app.buttons["files.move.here"]
        XCTAssertTrue(here.waitForExistence(timeout: 10), "no folder picker")
        XCTAssertFalse(here.isEnabled, "moving to the folder it is in is offered")
        let picker = app.collectionViews["files.move.list"]
        XCTAssertTrue(picker.staticTexts["code"].waitForExistence(timeout: 10))
        shot("edit-move")
        picker.staticTexts["code"].tap()
        XCTAssertTrue(app.staticTexts["weather"].waitForExistence(timeout: 10), "code/ did not open in the picker")
        XCTAssertTrue(app.buttons["files.move.here"].isEnabled)
        shot("edit-move-code")
        app.buttons["files.move.here"].tap()
        XCTAssertTrue(app.staticTexts["已把「todo.txt」移到 code"].waitForExistence(timeout: 10), "no moved toast")
        shot("edit-moved")
        XCTAssertTrue(app.staticTexts["todo.txt"].waitForNonExistence(timeout: 15), "the moved file is still in the home")

        // Select, trash with the confirmation, Undo from the toast.
        app.buttons["files.more"].tap()
        XCTAssertTrue(app.buttons["选择"].waitForExistence(timeout: 5), "no 选择 in the ⋯ menu")
        app.buttons["选择"].tap()
        XCTAssertTrue(app.staticTexts["files.select.count"].waitForExistence(timeout: 5), "no selection bar")
        XCTAssertFalse(app.buttons["files.select.trash"].isEnabled, "trash offered with nothing selected")
        app.staticTexts["code"].tap()
        XCTAssertEqual(app.staticTexts["files.select.count"].label, "已选 1 项")
        XCTAssertTrue(app.buttons.matching(NSPredicate(format: "selected == true")).firstMatch.waitForExistence(timeout: 5), "the row is not marked selected")
        sleep(1) // the checkmark fades in
        shot("edit-select")
        app.buttons["files.select.trash"].tap()
        let confirm = app.alerts["把「code」移到废纸篓？"]
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "no confirmation before the trash")
        shot("edit-trash-confirm")
        confirm.buttons["移到废纸篓"].tap()
        XCTAssertTrue(app.buttons["toast-action"].waitForExistence(timeout: 10), "no Undo on the toast")
        XCTAssertTrue(app.staticTexts["code"].waitForNonExistence(timeout: 10), "code/ is still listed")
        shot("edit-trashed")
        app.buttons["toast-action"].tap()
        XCTAssertTrue(app.staticTexts["code"].waitForExistence(timeout: 15), "Undo did not put code/ back")
        shot("edit-restored")
    }

    /// iPad: files dragged in from another app outline the folder (the DEBUG `-showFilesDrop`; a drag from another
    /// app can't be driven from here).
    func testDropOutlinesTheFolder() throws {
        guard UIDevice.current.userInterfaceIdiom == .pad else { throw XCTSkip("iPad only") }
        app.terminate()
        app.launchArguments += ["-showFilesDrop", "YES"]
        app.launchPinned()
        openHome()
        XCTAssertTrue(app.otherElements["files.drop"].waitForExistence(timeout: 5) || app.images["files.drop"].exists, "no drop outline")
        shot("edit-drop")
    }
}
