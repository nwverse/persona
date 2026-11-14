import XCTest

/// A non-event inbox item's source (decisions line 13a): opening the detail builds neither `LarkChatView` nor the mail
/// reader, so nothing is reported read; View original builds it and reports what is on screen, and Collapse takes it
/// away again. A reused detail column starts collapsed for the next item. Run against scripts/mock/mock_hub.py without
/// MOCK_INBOX_EVENTS (PERSONA_MOCK_HUB).
///
/// The chat's read reports are read from `/mock/lark_reads`; the mail's seen writes from `GET /mock/mail`'s flags, where
/// the mock keeps them (`/mock/mail_flags` is filled by a handler the mail area's own answer always pre-empts, so it
/// stays empty however often the app writes).
final class InboxCollapsedSourceTests: XCTestCase {
    private static let chatItem = "lark:f1:100"
    private static let chatTitle = "韩梅梅"
    private static let chatFeed = "f1"
    private static let otherTitle = "周末徒步群"
    private static let otherPreview = "周六早上 8 点"
    private static let mailItem = "mail:acc1:t1:501"
    private static let mailAccount = "acc1"
    private static let mailUID = 501
    /// The mail's own words, past its preview: only the reader draws them.
    private static let mailBody = "议程见附件，有问题随时说"

    private var app: XCUIApplication!
    private var hub: String!
    private var regularWidth = false
    private var layout: String { regularWidth ? "regular" : "phone" }

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py without MOCK_INBOX_EVENTS (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "inbox-collapsed-source-failure-\(layout)"
            shot.lifetime = .keepAlways
            add(shot)
        }
        app?.terminate()
        super.tearDown()
    }

    @MainActor func testChatIsBuiltOnlyAfterViewOriginal() throws {
        try resetFixture(Self.chatItem)
        let baseline = try chatReadReports().count
        openDetail(Self.chatItem)
        let toggle = element("inbox.detail.source")
        requireHittable(toggle, "View original on the collapsed chat")
        assertLabel("看原文", on: toggle, "the collapsed chat source toggle")
        XCTAssertFalse(app.textFields["lark.box"].exists,
                       "the chat was built before View original:\n\(app.debugDescription)")

        sleep(2) // the chat reports a read 400 ms after its rows come on screen
        XCTAssertEqual(try chatReadReports().count, baseline, "opening the chat detail reported a read before View original")

        toggle.tap()
        XCTAssertTrue(app.textFields["lark.box"].waitForExistence(timeout: 15),
                      "View original did not build the chat:\n\(app.debugDescription)")
        assertLabel("收起", on: toggle, "the expanded chat source toggle")
        XCTAssertGreaterThan(try waitForReports(after: baseline, fetch: chatReadReports).count, baseline,
                             "View original did not report the \(Self.chatFeed) chat as read")

        toggle.tap()
        XCTAssertTrue(app.textFields["lark.box"].waitForNonExistence(timeout: 5), "Collapse left the chat on the page")
        assertLabel("看原文", on: toggle, "the collapsed chat source toggle again")
    }

    @MainActor func testMailIsBuiltOnlyAfterViewOriginal() throws {
        try resetFixture(Self.mailItem)
        let baseline = try mailSeenReports().count
        openDetail(Self.mailItem)
        let toggle = element("inbox.detail.source")
        requireHittable(toggle, "View original on the collapsed mail")
        assertLabel("看原文", on: toggle, "the collapsed mail source toggle")
        XCTAssertFalse(mailBodyElement().exists, "the mail reader was built before View original:\n\(app.debugDescription)")

        sleep(2)
        XCTAssertEqual(try mailSeenReports().count, baseline, "opening the mail detail reported it seen before View original")

        toggle.tap()
        XCTAssertTrue(mailBodyElement().waitForExistence(timeout: 15),
                      "View original did not build the mail reader:\n\(app.debugDescription)")
        XCTAssertGreaterThan(try waitForReports(after: baseline, fetch: mailSeenReports).count, baseline,
                             "View original did not report mail UID \(Self.mailUID) as seen")
        assertLabel("收起", on: toggle, "the expanded mail source toggle")

        toggle.tap()
        XCTAssertTrue(mailBodyElement().waitForNonExistence(timeout: 5), "Collapse left the mail reader on the page")
    }

    /// Collapsed, the page is still the chat's page: it names the chat in its bar, as the chat itself did before it was
    /// behind View original.
    @MainActor func testCollapsedChatDetailStillNamesTheChat() throws {
        try resetFixture(Self.chatItem)
        openDetail(Self.chatItem)
        XCTAssertTrue(app.navigationBars[Self.chatTitle].firstMatch.waitForExistence(timeout: 10),
                      "the collapsed chat detail does not name the chat:\n\(app.debugDescription)")
        XCTAssertFalse(app.textFields["lark.box"].exists, "the chat was built for its title")
    }

    /// Regular width keeps one detail column for every item: an expanded source must not stay open for the next item.
    @MainActor func testAnotherItemOpensCollapsed() throws {
        try resetFixture(Self.chatItem)
        openDetail(Self.chatItem)
        try requireWide()
        let toggle = element("inbox.detail.source")
        requireHittable(toggle, "View original on the collapsed chat")
        toggle.tap()
        XCTAssertTrue(app.textFields["lark.box"].waitForExistence(timeout: 15), "View original did not build the chat")

        guard let row = listRow(Self.otherTitle) else {
            return XCTFail("no \(Self.otherTitle) row in the list column:\n\(app.debugDescription)")
        }
        row.tapCenter()
        let next = element("inbox.detail.source")
        requireHittable(next, "\(Self.otherTitle)'s source toggle")
        assertLabel("看原文", on: next, "the next item's source toggle")
        XCTAssertFalse(app.textFields["lark.box"].exists, "the previous chat stayed open on the next item")
        // The collapsed line previews that item's newest line, so the page really is the other item's.
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", Self.otherPreview)).firstMatch
            .waitForExistence(timeout: 5), "\(Self.otherTitle)'s own row is not the one open:\n\(app.debugDescription)")
    }

    // MARK: - Driving the app

    @MainActor private func openDetail(_ item: String) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "mail", "-dev.openChat", "", "-openSplitFresh", "YES",
                               "-inbox.kind", "", "-inbox.source", "", "-openInboxItem", item]
        app.launchPinned()
        acceptIconAlert()
        XCTAssertTrue(app.windows.firstMatch.waitForExistence(timeout: 10), "no app window")
        regularWidth = app.windows.firstMatch.frame.width > 600
        requireHittable(element("inbox.detail.done"), "the \(item) detail")
    }

    /// A row of the list column at regular width (the open page's title is the same text, in the detail column).
    @MainActor private func listRow(_ title: String) -> XCUIElement? {
        let half = app.windows.firstMatch.frame.midX
        let deadline = Date.now.addingTimeInterval(20)
        repeat {
            let rows = app.staticTexts.matching(identifier: title).allElementsBoundByIndex
            if let row = rows.first(where: { $0.isHittable && $0.frame.maxX < half }) { return row }
            // Down the list column only: the detail column beside it scrolls the open page.
            let window = app.windows.firstMatch
            window.coordinate(withNormalizedOffset: CGVector(dx: 0.25, dy: 0.8))
                .press(forDuration: 0.05,
                       thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.25, dy: 0.3)),
                       withVelocity: .slow, thenHoldForDuration: 0.05)
            usleep(250_000)
        } while Date.now < deadline
        return nil
    }

    @MainActor private func requireWide() throws {
        guard regularWidth else { throw XCTSkip("needs an iPad or an open Duo: the reused detail column") }
    }

    @MainActor private func mailBodyElement() -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", Self.mailBody)).firstMatch
    }

    @MainActor private func element(_ id: String) -> XCUIElement {
        app.descendants(matching: .any)[id].firstMatch
    }

    @MainActor private func requireHittable(_ element: XCUIElement, _ what: String, timeout: TimeInterval = 10) {
        for _ in 0..<6 where !(element.exists && element.isHittable) {
            app.swipeUp(velocity: .slow)
            _ = element.waitForExistence(timeout: 1)
        }
        let predicate = NSPredicate(format: "exists == true AND hittable == true")
        let expectation = XCTNSPredicateExpectation(predicate: predicate, object: element)
        let met = XCTWaiter().wait(for: [expectation], timeout: timeout) == .completed
        XCTAssertTrue(met, "\(what) is not on screen:\n\(app.debugDescription)")
    }

    @MainActor private func assertLabel(_ label: String, on element: XCUIElement, _ what: String,
                                        timeout: TimeInterval = 5) {
        // The button reads as its preview and its action: the action is the part to look for, not the whole label.
        let expectation = XCTNSPredicateExpectation(predicate: NSPredicate(format: "label CONTAINS %@", label), object: element)
        XCTAssertEqual(XCTWaiter().wait(for: [expectation], timeout: timeout), .completed,
                       "\(what) does not say \(label):\n\(app.debugDescription)")
    }

    // MARK: - What the mock was told

    /// The item open again as unhandled, and the mail unseen: Done or a reader in the shared mock by an earlier run
    /// must not keep this one from reporting what is on screen (a done item's toolbar has no Done either).
    private func resetFixture(_ item: String) throws {
        try post("/api/v1/inbox/done", body: ["ids": [item], "done": false])
        if item == Self.mailItem {
            try post("/api/v1/mail/messages/\(Self.mailAccount)/\(Self.mailUID)/flags",
                     body: ["mailbox": "INBOX", "seen": false])
        }
    }

    private func post(_ path: String, body: [String: Any]) throws {
        let url = try XCTUnwrap(URL(string: hub + path))
        var request = URLRequest(url: url)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(withJSONObject: body)
        let answered = expectation(description: "POST " + path)
        URLSession.shared.dataTask(with: request) { _, response, error in
            XCTAssertNil(error)
            XCTAssertEqual((response as? HTTPURLResponse)?.statusCode, 200)
            answered.fulfill()
        }.resume()
        wait(for: [answered], timeout: 10)
    }

    /// Only this chat's reports: the mock is shared and other runs' chats report too.
    private func chatReadReports() throws -> [[String: Any]] {
        try records("/mock/lark_reads", key: "reads").filter { $0["feed"] as? String == Self.chatFeed }
    }

    /// Only this message's seen writes; the reset's unseen write, other accounts and other UIDs are left out.
    private func mailSeenReports() throws -> [[String: Any]] {
        try records("/mock/mail", key: "flags").filter {
            $0["account_id"] as? String == Self.mailAccount
                && ($0["uid"] as? NSNumber)?.intValue == Self.mailUID
                && $0["mailbox"] as? String == "INBOX"
                && $0["seen"] as? Bool == true
        }
    }

    private func records(_ path: String, key: String) throws -> [[String: Any]] {
        let url = try XCTUnwrap(URL(string: hub + path))
        let data = try Data(contentsOf: url)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        return try XCTUnwrap(object[key] as? [[String: Any]])
    }

    private func waitForReports(after baseline: Int, timeout: TimeInterval = 10,
                                fetch: () throws -> [[String: Any]]) throws -> [[String: Any]] {
        let deadline = Date.now.addingTimeInterval(timeout)
        var current = try fetch()
        while current.count <= baseline, Date.now < deadline {
            usleep(250_000)
            current = try fetch()
        }
        return current
    }
}
