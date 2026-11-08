import XCTest

/// Copy Diagnostics (#3783): the main conversation's menu in the drawer copies the chat's structure-only trace (ChatTrace), so a
/// reply that went missing can be told apart afterwards. On demo data, in the new shell.
final class ChatTraceMenuTests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor func testTheDrawersMainRowCopiesTheDiagnostics() {
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-app.tab", "assistant", "-dev.openChat", ""]
        app.launchPinned()
        let drawer = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(drawer.waitForExistence(timeout: 15), "no sidebar button")
        sleep(1)
        drawer.tapCenter()
        let main = app.buttons["sidebar-main"].firstMatch
        XCTAssertTrue(main.waitForExistence(timeout: 5), "no Persona row at the top of the drawer")
        sleep(1)
        main.press(forDuration: 1.2)
        let copy = app.buttons["拷贝诊断"].firstMatch
        XCTAssertTrue(copy.waitForExistence(timeout: 5), "no 拷贝诊断 in the main conversation's menu")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "drawer-main-menu"
        shot.lifetime = .keepAlways
        add(shot)
        copy.tap()
        XCTAssertTrue(app.staticTexts["已拷贝诊断"].waitForExistence(timeout: 5), "no toast for the copy")
    }
}
