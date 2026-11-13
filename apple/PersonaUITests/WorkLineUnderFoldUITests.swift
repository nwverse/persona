import XCTest

/// A work line right under a long message from another session opens when tapped (#6645), against
/// scripts/mock/mock_hub.py with MOCK_PEER_FOLD=1 (PERSONA_MOCK_HUB). The message folds to a few lines with Expand; the
/// rest of its words are still laid out under the fold, unseen, and the upside-down chat list draws an older row over the
/// newer one below it: those words took the tap, and the work line never opened. Four work lines, each right under such
/// a message: a tool and a reply (the one reported), tools only, a reply only, and the running one. A long press there is
/// the pressed row's too: the message above does not lift.
/// `testRecord` (PERSONA_RECORD=1) taps the reported one slowly for a screen recording, light then dark, and prints each
/// tap ("FOLDTAP <epoch> <x> <y>") and each part ("FOLDSEG <scheme> <epoch>") so the recording can be cut and marked.
final class WorkLineUnderFoldUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private let chat = "macbook/a1f3c2d4-0000-4000-8000-000000006645"

    /// The work lines by what they read (their `work-head` value): the reported one, tools only, a reply only, running.
    private let reported = NSPredicate(format: "value BEGINSWITH %@", "工具 1 · 回复 1")
    private let toolsOnly = NSPredicate(format: "value BEGINSWITH %@", "工具 2")
    private let replyOnly = NSPredicate(format: "value BEGINSWITH %@", "回复 1")
    private let running = NSPredicate(format: "value CONTAINS %@", "跑剩下两条")

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py with MOCK_PEER_FOLD=1 (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    @MainActor private func launch(scheme: String = "light") {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-openChatsFresh", "YES", "-ui.scheme", scheme,
                               "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN", "-tab", "dev", "-openSession", chat]
        app.launchPinned()
        // The app's icon follows the appearance: switching it, the system says so in an alert over the chat.
        let notice = NSPredicate(format: "label CONTAINS[c] 'icon' OR label CONTAINS '图标'")
        for source in [app!, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let alert = source.alerts.containing(notice).firstMatch
            if alert.waitForExistence(timeout: 2), alert.buttons.firstMatch.exists { alert.buttons.firstMatch.tap(); break }
        }
        XCTAssertTrue(head(running).waitForExistence(timeout: 20), "the running work line shows")
        XCTAssertTrue(app.buttons.matching(identifier: "message.expand").firstMatch.waitForExistence(timeout: 5),
                      "the message above it is folded, with Expand")
        sleep(2) // the chat settles at its newest end
    }

    private func head(_ which: NSPredicate) -> XCUIElement {
        app.buttons.matching(identifier: "work-head").matching(which).firstMatch
    }

    /// Where the element is on screen: rows of the flipped chat list may report flipped frames (`MessageLiftUITests`).
    private func onScreen(_ e: XCUIElement) -> CGRect {
        let window = app.windows.firstMatch.frame
        var f = e.frame
        if !window.contains(CGPoint(x: f.midX, y: f.midY)) { f.origin.y = window.height - f.maxY }
        return f
    }

    /// Drags the chat toward older lines until the work line is in the middle of the screen, clear of the bars.
    @MainActor private func reveal(_ which: NSPredicate) {
        let window = app.windows.firstMatch.frame
        for _ in 0..<12 {
            let e = head(which)
            if e.exists {
                let y = onScreen(e).midY
                if y > window.height * 0.2, y < window.height * 0.75 { return }
            }
            // Slowly, held at the end: a plain drag flings the upside-down list well past the line (docs/lessons/ios.md).
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
                .press(forDuration: 0.1, thenDragTo: app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6)),
                       withVelocity: .slow, thenHoldForDuration: 0.3)
            sleep(1)
        }
        XCTFail("work line \(which.predicateFormat) never came on screen")
    }

    /// Taps the work line's words, where a person does, and prints where and when.
    @MainActor private func tap(_ which: NSPredicate) {
        let f = onScreen(head(which))
        let at = CGPoint(x: f.minX + min(40, f.width / 2), y: f.midY)
        print(String(format: "FOLDTAP %.3f %.1f %.1f", Date().timeIntervalSince1970, at.x, at.y),
              "frame \(head(which).frame) shown \(f) window \(app.windows.firstMatch.frame)")
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: at.x, dy: at.y)).tap()
    }

    private func isOpen(_ which: NSPredicate, timeout: TimeInterval = 4) -> Bool {
        let open = NSPredicate(format: "label == %@", "收起过程")
        let end = Date().addingTimeInterval(timeout)
        while Date() < end {
            if head(NSCompoundPredicate(andPredicateWithSubpredicates: [which, open])).exists { return true }
            usleep(200_000)
        }
        return false
    }

    /// Each of the four work lines opens on a tap, and folds again on another.
    @MainActor func testAWorkLineUnderAFoldedMessageOpens() throws {
        launch()
        for (name, which) in [("running", running), ("reply only", replyOnly), ("tools only", toolsOnly), ("tool and reply", reported)] {
            reveal(which)
            tap(which)
            XCTAssertTrue(isOpen(which), "the \(name) work line under the folded message did not open")
            XCTAssertTrue(app.buttons["work-fold-bottom"].firstMatch.waitForExistence(timeout: 3), "no steps under the \(name) work line")
            reveal(which)
            tap(which)
            XCTAssertFalse(isOpen(which, timeout: 2), "the \(name) work line did not fold again")
        }
    }

    /// A long press under the folded message is the row's under it, not the message's: on the work line nothing lifts
    /// (it is no message), on the answer under that the answer lifts, not the message above.
    @MainActor func testBLongPressUnderAFoldedMessageLiftsWhatIsPressed() throws {
        let lifted = { self.app.descendants(matching: .any)["message-lift"].firstMatch }
        launch()
        reveal(reported)
        let f = onScreen(head(reported))
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: f.minX + min(40, f.width / 2), dy: f.midY)).press(forDuration: 0.8)
        XCTAssertFalse(lifted().waitForExistence(timeout: 2), "a long press on the work line lifted the message above it")

        launch()
        reveal(reported)
        let answer = app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "等它的界面测试结果")).firstMatch
        XCTAssertTrue(answer.waitForExistence(timeout: 5), "the answer under the work line shows")
        let a = onScreen(answer)
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: a.minX + 40, dy: a.minY + 10)).press(forDuration: 0.8)
        XCTAssertTrue(lifted().waitForExistence(timeout: 3), "a long press on the answer lifted nothing")
        XCTAssertFalse(lifted().staticTexts["来自 release-worker"].exists, "a long press on the answer lifted the message above it")
        XCTAssertTrue(lifted().staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "等它的界面测试结果")).firstMatch.exists,
                      "the answer is not what lifted")
    }

    /// For the recording, in light and then dark: the reported work line tapped every three seconds, four times.
    @MainActor func testRecord() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_RECORD"] == "1" else { throw XCTSkip("PERSONA_RECORD=1 records") }
        for scheme in ["light", "dark"] {
            launch(scheme: scheme)
            reveal(reported)
            sleep(1)
            print(String(format: "FOLDSEG %@ %.3f", scheme, Date().timeIntervalSince1970))
            sleep(1)
            for _ in 0..<4 {
                reveal(reported)
                tap(reported)
                sleep(3)
            }
            print(String(format: "FOLDSEGEND %@ %.3f", scheme, Date().timeIntervalSince1970))
        }
    }
}
