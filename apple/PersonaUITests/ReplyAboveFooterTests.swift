import XCTest

/// The newest reply in Persona's main conversation never ends under its footer (#7382): with the capsule row over the
/// composer, a long reply streamed in at the newest end keeps its last line above the capsules' top edge while it streams
/// and once it is written, and a drag toward the newest end does not spring back onto a place that hides it.
///   PORT=18873 MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_LONG_HISTORY=1 python3 scripts/mock/mock_hub.py &
///   TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:18873 xcodebuild test … -only-testing:PersonaUITests/ReplyAboveFooterTests
/// `TEST_RUNNER_PERSONA_SHOTS=<directory>` keeps the screenshots (`TEST_RUNNER_PERSONA_SHOTS_SUFFIX` after each name).
final class ReplyAboveFooterTests: XCTestCase {
    private var hub = ""
    /// Persona's main conversation: the dock's capsules over its composer (MOCK_TASKS=1).
    private let persona = "server/b2e4d6f8-0000-4000-8000-000000000001"
    /// The last words of the long reply (`/mock/say?preset=markdown`).
    private let replyEnd = "有问题随时说"

    override func setUpWithError() throws {
        continueAfterFailure = true
        guard let h = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_LONG_HISTORY=1 (PERSONA_MOCK_HUB)")
        }
        hub = h
        _ = try? Data(contentsOf: URL(string: hub + "/mock/reset")!)
    }

    @MainActor func testStreamedReplyEndsAboveTheCapsules() throws {
        let scheme = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-dev.openChat", "", "-openAssistant", persona,
                               "-openChatsFresh", "YES", "-openImagesFresh", "YES", "-ui.scheme", scheme]
        app.launchPinned()
        dismissAlerts()
        let bar = app.otherElements["action-bar"].firstMatch
        XCTAssertTrue(app.buttons["action-bar.running"].waitForExistence(timeout: 20), "no capsules over the composer:\n\(app.debugDescription)")
        settle(2)

        // A long reply, streamed at the hub's pace (a delta every 80 ms) after a message from the user.
        var c = URLComponents(string: hub + "/mock/say")!
        c.queryItems = [URLQueryItem(name: "preset", value: "markdown"), URLQueryItem(name: "user", value: "看一下三台机器的凭据"),
                        URLQueryItem(name: "stream", value: "1"), URLQueryItem(name: "steps", value: "60"),
                        URLQueryItem(name: "ms", value: "80")]
        _ = try Data(contentsOf: c.url!)

        // While it streams: sampled every half second, the lowest line the list draws stays above the capsules.
        var worst: (over: CGFloat, at: Int) = (-.infinity, -1)
        for i in 0..<9 {
            settle(0.5)
            let m = try measure(app, bar: bar)
            if m.newest - m.footer > worst.over { worst = (m.newest - m.footer, i) }
            if i == 4 { shot(app, "streaming-\(scheme)") }
        }
        XCTAssertLessThanOrEqual(worst.over, 1, "while streaming, the newest line ran \(worst.over) pt under the capsules' top (sample \(worst.at))")

        // Written: the reply's last words show, above the capsules.
        let end = app.staticTexts.containing(NSPredicate(format: "label CONTAINS %@", replyEnd)).firstMatch
        XCTAssertTrue(end.waitForExistence(timeout: 15), "the reply's end never showed")
        settle(2)
        shot(app, "written-\(scheme)")
        let written = try measure(app, bar: bar)
        XCTAssertLessThanOrEqual(written.newest - written.footer, 1,
                                 "written, the newest line is \(written.newest - written.footer) pt under the capsules' top")
        XCTAssertLessThanOrEqual(end.frame.maxY, written.footer + 1,
                                 "the reply's last line (\(end.frame.maxY)) is under the capsules' top (\(written.footer))")

        // A drag toward the newest end and back: the list rests where its end is in view, not under the footer.
        let list = try XCTUnwrap(app.scrollViews.allElementsBoundByIndex.max { $0.frame.height < $1.frame.height })
        let start = list.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6))
        start.press(forDuration: 0.05, thenDragTo: start.withOffset(CGVector(dx: 0, dy: -160)), withVelocity: .slow, thenHoldForDuration: 0.2)
        settle(2)
        let released = try measure(app, bar: bar)
        XCTAssertLessThanOrEqual(end.frame.maxY, released.footer + 1,
                                 "after a drag the reply's last line (\(end.frame.maxY)) went back under the capsules' top (\(released.footer))")
        app.terminate()
    }

    /// From one snapshot: the lowest bottom edge of what the chat list lays out (its rows' texts, buttons, pictures), and
    /// the top edge of the footer's first row (the capsules).
    @MainActor private func measure(_ app: XCUIApplication, bar: XCUIElement) throws -> (newest: CGFloat, footer: CGFloat) {
        let root = try app.snapshot()
        var scrolls: [XCUIElementSnapshot] = []
        walk(root) { if $0.elementType == .scrollView { scrolls.append($0) } }
        let list = try XCTUnwrap(scrolls.max { $0.frame.height < $1.frame.height })
        let drawn: Set<XCUIElement.ElementType> = [.staticText, .button, .image, .link, .textView]
        var newest = -CGFloat.infinity
        walk(list) { s in
            let f = s.frame
            // Nothing cut off at the list's bottom edge: a line laid out past it is what the bug hides.
            guard drawn.contains(s.elementType), f.height > 0, f.height < list.frame.height, f.maxY > list.frame.minY else { return }
            newest = max(newest, f.maxY)
        }
        let footer = bar.frame.minY
        XCTAssertGreaterThan(newest, list.frame.minY, "nothing drawn in the chat list")
        return (newest, footer)
    }

    private func walk(_ s: XCUIElementSnapshot, _ visit: (XCUIElementSnapshot) -> Void) {
        visit(s)
        for c in s.children { walk(c, visit) }
    }

    private func settle(_ seconds: TimeInterval) { RunLoop.current.run(until: Date().addingTimeInterval(seconds)) }

    /// SpringBoard's notification question, and the icon-changed notice (the icon follows the appearance).
    private func dismissAlerts() {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for _ in 0..<2 {
            let ok = springboard.alerts.buttons.matching(NSPredicate(format: "label IN %@", ["Allow", "允许", "OK", "好"])).firstMatch
            if ok.waitForExistence(timeout: 2) { ok.tap() }
        }
    }

    /// An attachment, and a file in PERSONA_SHOTS when set.
    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let env = ProcessInfo.processInfo.environment
        let full = "reply-footer-\(name)\(env["PERSONA_SHOTS_SUFFIX"] ?? "")"
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = env["PERSONA_SHOTS"] {
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(dir)/\(full).png"))
        }
    }
}
