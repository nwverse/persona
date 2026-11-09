import XCTest

/// Connecting… is said once on an agent's conversation in the shell (docs/apple/shell.md「Said once」): the chat's title says
/// it, so the shell's floating pill keeps away. It showed both, the title's line and the pill just under the bar.
/// Back on the Agents list, whose bar does not say it, the pill shows again.
final class ShellChatLinkOnceTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = true
    }

    @MainActor private func attachScreen(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }

    @MainActor func testAnAgentsChatSaysConnectingOnce() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-probes", "YES", "-tab", "dev", "-dev.folded", "",
                               "-dev.openChat", "macbook/a1f3c2d4-0000-4000-8000-000000000006", "-showConnection", "connecting"]
        app.launchPinned()
        let any = app.descendants(matching: .any)
        XCTAssertTrue(any["probe.shell"].waitForExistence(timeout: 15), "no shell")
        // The slim bar (the chrome folded, as it opens on a phone) and then the full header: one Connecting… each time.
        let slim = any["chat.compactTitle"].firstMatch
        XCTAssertTrue(slim.waitForExistence(timeout: 15), "the chat did not open")
        sleep(1) // the pill's transition, had it come
        attachScreen("chat-slim-connecting")
        XCTAssertTrue(slim.label.contains("Connecting") || slim.label.contains("连接中"), "the slim bar does not say it: \(slim.label)")
        XCTAssertFalse(any["shell-connection"].exists, "the pill says Connecting… again under the chat's slim bar")
        slim.tap()
        let title = any["chat.title"].firstMatch
        XCTAssertTrue(title.waitForExistence(timeout: 5), "the header did not open")
        sleep(1)
        attachScreen("chat-header-connecting")
        XCTAssertTrue(title.label.contains("Connecting") || title.label.contains("连接中"), "the title does not say it: \(title.label)")
        XCTAssertFalse(any["shell-connection"].exists, "the pill says Connecting… again under the chat's title")

        // Back on the list: its bar does not say it, so the pill does.
        app.buttons.matching(NSPredicate(format: "label IN %@", ["返回", "Back"])).firstMatch.tap()
        XCTAssertTrue(any["shell-connection"].waitForExistence(timeout: 5), "the pill did not come back on the Agents list")
        attachScreen("list-connecting")
    }
}
