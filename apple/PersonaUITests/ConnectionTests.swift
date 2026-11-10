import XCTest

/// The title turns into Connecting… while the hub is out of reach and back afterwards, a short drop shows nothing, and a
/// message written meanwhile waits and goes by itself (docs/apple/ios.md「Connection status」). Against scripts/mock/mock_hub.py, whose
/// /mock/down?s=<seconds> takes the hub away (TEST_RUNNER_PERSONA_MOCK_HUB=http://127.0.0.1:<port>).
final class ConnectionTests: XCTestCase {
    private let widget = "a1f3c2d4-0000-4000-8000-000000000001"

    private func hub() throws -> String {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        return hub
    }

    private func launch(_ hub: String, _ args: [String]) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        // A fresh install asks for notifications first, over the page (these tests only wait, so no interruption
        // monitor would ever fire). A short look: the tests time the hub's outage against the app's launch.
        let allow = XCUIApplication(bundleIdentifier: "com.apple.springboard").buttons
            .matching(NSPredicate(format: "label IN %@", ["允许", "Allow"])).firstMatch
        if allow.waitForExistence(timeout: 1) { allow.tap() }
        return app
    }

    private func mock(_ hub: String, _ path: String) -> [String: Any] {
        guard let data = try? Data(contentsOf: URL(string: hub + path)!),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return obj
    }

    private func status(_ app: XCUIApplication) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "查看连接详情")).firstMatch
    }

    /// Whether the status showed at any point within `seconds` (checked every 0.1 s).
    private func shows(_ el: XCUIElement, within seconds: TimeInterval) -> Bool {
        let deadline = Date.now.addingTimeInterval(seconds)
        while Date.now < deadline {
            if el.exists { return true }
            usleep(100_000)
        }
        return false
    }

    private func shot(_ name: String) {
        let s = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        s.name = name
        s.lifetime = .keepAlways
        add(s)
    }

    /// The hub goes away for a while: nothing for the drop's grace (8 s, docs/operations/hub-restart.md), then Connecting… in the
    /// title, then the title comes back on its own.
    @MainActor func testOutageShowsConnectingThenRecovers() throws {
        let hub = try hub()
        let app = launch(hub, ["-tab", "dev", "-dev.openChat", ""]) // not the chat an earlier test left open
        XCTAssertTrue(app.buttons["新建会话"].waitForExistence(timeout: 15))
        sleep(3)
        XCTAssertFalse(status(app).exists, "a status while connected")
        _ = mock(hub, "/mock/down?s=16")
        XCTAssertFalse(shows(status(app), within: 6), "a status within the drop's grace")
        XCTAssertTrue(status(app).waitForExistence(timeout: 6), "no 连接中… while the hub is down:\n\(app.debugDescription)")
        XCTAssertTrue(status(app).label.hasPrefix("连接中…"), status(app).label)
        shot("connecting")
        let gone = NSPredicate(format: "exists == false")
        expectation(for: gone, evaluatedWith: status(app))
        waitForExpectations(timeout: 30) // back up after 16 s, plus the backoff
    }

    /// A drop shorter than the delay (coming back to the app, switching networks) shows nothing at all.
    @MainActor func testShortDropDoesNotFlash() throws {
        let hub = try hub()
        let app = launch(hub, ["-tab", "dev", "-dev.openChat", ""]) // not the chat an earlier test left open
        XCTAssertTrue(app.buttons["新建会话"].waitForExistence(timeout: 15))
        sleep(3)
        _ = mock(hub, "/mock/down?s=0.2")
        XCTAssertFalse(shows(status(app), within: 4), "the title flashed for a reconnect that took about a second")
    }

    /// Written while the hub is down: once the drop's grace is over the bubble says "Sends when connected" (within it the send
    /// keeps trying), and the message reaches the hub once it is back.
    @MainActor func testMessageWaitsForTheHub() throws {
        let hub = try hub()
        let text = "断网时写的消息 \(Int.random(in: 1000...9999))"
        let app = launch(hub, ["-openSession", "macbook/" + widget, "-openSendMessage", text, "-openSendDelay", "9"])
        sleep(4)
        _ = mock(hub, "/mock/down?s=14")
        let waiting = app.staticTexts["连上后自动发送"]
        XCTAssertTrue(waiting.waitForExistence(timeout: 12), "no waiting bubble:\n\(app.debugDescription)")
        shot("waiting")
        assertArrivesOnce(hub, text)
    }

    // Exactly once, whatever happens while it waits (the app resends with the message's id, the hub and node drop
    // what they have seen; hub restarts are the server's tests, TestSendRetryAfterRestartAndRefusal).

    /// The link drops and comes back again and again, before and after the message goes: still one.
    @MainActor func testMessageArrivesOnceThroughOutages() throws {
        let hub = try hub()
        let text = "几次断网的消息 \(Int.random(in: 1000...9999))"
        let app = launch(hub, ["-openSession", "macbook/" + widget, "-openSendMessage", text, "-openSendDelay", "9"])
        sleep(4)
        _ = mock(hub, "/mock/down?s=14") // past the drop's grace (8 s)
        XCTAssertTrue(app.staticTexts["连上后自动发送"].waitForExistence(timeout: 12), "no waiting bubble")
        _ = mock(hub, "/mock/down?s=6") // longer down while it waits
        assertArrivesOnce(hub, text, settle: 0)
        for _ in 0..<2 { // and drops after it went
            _ = mock(hub, "/mock/down?s=3")
            sleep(8)
        }
        XCTAssertEqual(sent(hub, text).count, 1, "a reconnect sent it again")
    }

    /// Written offline, then the app goes to the background and the hub comes back meanwhile: in the background the
    /// app does not reconnect (#248), so nothing goes until it is in front again, and then it goes once.
    @MainActor func testWaitingMessageThroughTheBackground() throws {
        let hub = try hub()
        let text = "进后台前写的消息 \(Int.random(in: 1000...9999))"
        let app = launch(hub, ["-openSession", "macbook/" + widget, "-openSendMessage", text, "-openSendDelay", "9"])
        sleep(4)
        _ = mock(hub, "/mock/down?s=16") // past the drop's grace (8 s)
        XCTAssertTrue(app.staticTexts["连上后自动发送"].waitForExistence(timeout: 12), "no waiting bubble")
        XCUIDevice.shared.press(.home)
        sleep(12) // the hub is back while the app is away
        XCTAssertTrue(sent(hub, text).isEmpty, "sent from the background (it should not reconnect there)")
        app.activate()
        assertArrivesOnce(hub, text)
    }

    /// Written offline, the app is quit while it waits, and opened again (the outbox is on disk): one.
    @MainActor func testWaitingMessageAfterRelaunch() throws {
        let hub = try hub()
        let text = "退出前没发出的消息 \(Int.random(in: 1000...9999))"
        var app = launch(hub, ["-openSession", "macbook/" + widget, "-openSendMessage", text, "-openSendDelay", "9"])
        sleep(4)
        _ = mock(hub, "/mock/down?s=25")
        XCTAssertTrue(app.staticTexts["连上后自动发送"].waitForExistence(timeout: 12), "no waiting bubble")
        sleep(1) // the outbox is written
        app.terminate()
        app = launch(hub, ["-openSession", "macbook/" + widget]) // not written again: what the outbox kept
        XCTAssertTrue(app.staticTexts["连上后自动发送"].waitForExistence(timeout: 15), "the waiting message did not come back")
        assertArrivesOnce(hub, text)
    }

    private func sent(_ hub: String, _ text: String) -> [[String: Any]] {
        (mock(hub, "/mock/sent")["sent"] as? [[String: Any]] ?? []).filter { $0["text"] as? String == text }
    }

    /// It reaches the hub within 40 s, then nothing more comes for `settle` seconds.
    private func assertArrivesOnce(_ hub: String, _ text: String, settle: UInt32 = 8,
                                   file: StaticString = #filePath, line: UInt = #line) {
        let deadline = Date.now.addingTimeInterval(40)
        var got: [[String: Any]] = []
        while Date.now < deadline {
            got = sent(hub, text)
            if !got.isEmpty { break }
            usleep(500_000)
        }
        sleep(settle)
        got = sent(hub, text)
        let ids = got.map { $0["client_msg_id"] as? String ?? "-" }
        XCTAssertEqual(got.count, 1, "the waiting message should reach the hub once, after it is back: \(ids)", file: file, line: line)
    }
}
