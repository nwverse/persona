import XCTest

/// The current hub's write count in Settings and in each original Allow/Deny prompt.
final class PhoneWriteQueueUITests: XCTestCase {
    override func setUp() { continueAfterFailure = false }

    private var vars: [String: String] { ProcessInfo.processInfo.environment }

    private func hub() throws -> URL {
        guard let value = vars["PERSONA_PHONE_QUEUE_HUB"], let hub = URL(string: value), hub.scheme == "http", hub.host() == "127.0.0.1" else {
            throw XCTSkip("Needs a loopback phone write queue mock in PERSONA_PHONE_QUEUE_HUB")
        }
        return hub
    }

    private func pair(_ hub: URL) -> String {
        "persona://pair?url=" + hub.absoluteString.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=" + UITestToken.device
    }

    @MainActor private func launch(_ hub: URL, language: String, second: URL? = nil, registerTools: Bool = false) -> XCUIApplication {
        let app = XCUIApplication()
        var args = ["-tab", "settings", "-dev.openChat", "", "-AppleLanguages", "(\(language))", "-AppleLocale", language]
        if let size = vars["PERSONA_PHONE_QUEUE_TYPE"] { args += ["-UIPreferredContentSizeCategoryName", size] }
        if second != nil { args += ["-openHub", "1"] }
        app.launchArguments = args + ["-pairURL", pair(hub)]
        if let second { app.launchArguments += ["-pairURL2", pair(second)] }
        app.launchPinned()
        if registerTools {
            // The screenshot pairing argument deliberately suppresses real tool offers. Resume the saved loopback pairing
            // as a normal app so the Hub delivers to the actual registered calendar tool on this simulator.
            XCTAssertTrue(app.descendants(matching: .any)["settings-list"].firstMatch.waitForExistence(timeout: 25))
            app.terminate()
            app.launchArguments = args
            app.launchPinned()
        }
        return app
    }

    @MainActor private func openSettingsRow(_ id: String, in app: XCUIApplication) {
        let list = app.descendants(matching: .any)["settings-list"].firstMatch
        XCTAssertTrue(list.waitForExistence(timeout: 25))
        let row = app.buttons[id].firstMatch
        for _ in 0..<10 where !row.isHittable { list.swipeUp() }
        for _ in 0..<6 where !row.isHittable { list.swipeDown(velocity: .slow) }
        XCTAssertTrue(row.isHittable, "Settings entry \(id): \(app.debugDescription)")
        row.tap()
    }

    @MainActor private func openPhoneData(_ app: XCUIApplication) {
        openSettingsRow("settings-phone-data", in: app)
        XCTAssertTrue(app.descendants(matching: .any)["iphone-setup-entry"].firstMatch.waitForExistence(timeout: 12))
    }

    /// The system confirmation dismisses the compact Settings cover. Reopen it the same way as a user.
    @MainActor private func returnToPhoneData(_ app: XCUIApplication) {
        let setup = app.descendants(matching: .any)["iphone-setup-entry"].firstMatch
        if setup.isHittable { return }
        let list = app.descendants(matching: .any)["settings-list"].firstMatch
        if !list.isHittable {
            let settings = app.buttons.matching(NSPredicate(format: "label == %@", "Settings")).firstMatch
            if !settings.isHittable {
                let sidebar = app.buttons["assistant-sidebar-button"].firstMatch
                XCTAssertTrue(sidebar.waitForExistence(timeout: 8), app.debugDescription)
                sidebar.tapCenter()
            }
            XCTAssertTrue(settings.waitForExistence(timeout: 8), app.debugDescription)
            settings.tapCenter()
        }
        if !setup.isHittable { openPhoneData(app) }
    }

    @MainActor private func shot(_ name: String) {
        let attachment = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    @MainActor private func downloads<T: Decodable>(_ type: T.Type, _ url: URL) async throws -> T {
        let (data, _) = try await URLSession.shared.data(from: url)
        return try JSONDecoder().decode(type, from: data)
    }

    @MainActor private func reads(_ hub: URL) async throws -> Int {
        struct Hits: Decodable { var hits: [String: Int] }
        return try await downloads(Hits.self, hub.appending(path: "mock/hits")).hits["GET /api/v1/phone/write-queue", default: 0]
    }

    @MainActor func testPhoneDataShowsTheHubsPendingCount() async throws {
        let hub = try hub()
        guard let count = vars["PERSONA_PHONE_QUEUE_COUNT"] else { throw XCTSkip("Needs an expected pending count") }
        let language = vars["PERSONA_PHONE_QUEUE_LANGUAGE"] ?? "en"
        let previousReads = try await reads(hub)
        let app = launch(hub, language: language)
        openPhoneData(app)
        let notice = app.descendants(matching: .any)["phone-write-queue-count"].firstMatch
        if count == "0" {
            let deadline = ContinuousClock.now + .seconds(8)
            while try await reads(hub) == previousReads, ContinuousClock.now < deadline { try await Task.sleep(for: .milliseconds(100)) }
            let completed = try await reads(hub)
            XCTAssertGreaterThan(completed, previousReads)
            XCTAssertFalse(notice.exists)
        } else {
            let available = notice.waitForExistence(timeout: 8)
            shot("phone-write-settings")
            XCTAssertTrue(available, "The hub offers \(count) pending mobile writes")
            let expected = switch language {
            case "zh-Hans": "此 Hub 有 \(count) 项手机写入待确认"
            case "ja": "この Hub で承認待ちのスマートフォンへの書き込みは \(count) 件です"
            default: count == "1" ? "1 phone write awaiting confirmation on this hub" : "\(count) phone writes awaiting confirmation on this hub"
            }
            XCTAssertEqual(notice.label, expected)
        }
    }

    /// MOCK_PHONE_WRITE_PENDING=2 MOCK_PHONE_WRITE_DELIVERY=1. The mock consumes each real WS result once.
    @MainActor func testEachConfirmationRefreshesTheHubsSharedCount() async throws {
        guard vars["PERSONA_PHONE_QUEUE_DELIVERY"] == "1" else { throw XCTSkip("Needs two isolated queued calendar writes") }
        let hub = try hub(), app = launch(hub, language: "en", registerTools: true)
        XCTAssertTrue(app.descendants(matching: .any)["settings-list"].firstMatch.waitForExistence(timeout: 25))
        let notice = app.descendants(matching: .any)["phone-write-queue-count"].firstMatch
        for (title, count) in [("Example calendar item A", "2 phone writes"), ("Example calendar item B", "1 phone write")] {
            try await deliverCalendarWrite(title, hub: hub)
            let alert = app.alerts.containing(.button, identifier: "Deny").firstMatch
            XCTAssertTrue(alert.waitForExistence(timeout: 15), app.debugDescription)
            let words = alert.staticTexts.matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@", title, count + " awaiting confirmation on this hub")).firstMatch
            let visible = words.waitForExistence(timeout: 8)
            shot("phone-write-confirmation-\(count.prefix(1))")
            XCTAssertTrue(visible, alert.debugDescription)
            XCTAssertTrue(alert.buttons["Allow"].isEnabled)
            let beforeDecision = try await reads(hub)
            alert.buttons["Deny"].tap()
            XCTAssertTrue(alert.waitForNonExistence(timeout: 8))
            returnToPhoneData(app)
            let until = ContinuousClock.now + .seconds(8)
            while try await reads(hub) == beforeDecision, ContinuousClock.now < until { try await Task.sleep(for: .milliseconds(100)) }
            let refreshed = try await reads(hub)
            XCTAssertGreaterThan(refreshed, beforeDecision)
            if title.hasSuffix("A") {
                let one = NSPredicate(format: "identifier == %@ AND label == %@", "phone-write-queue-count",
                                      "1 phone write awaiting confirmation on this hub")
                XCTAssertTrue(app.descendants(matching: .any).matching(one).firstMatch.waitForExistence(timeout: 8))
            }
        }
        XCTAssertTrue(notice.waitForNonExistence(timeout: 8), "The hub has a result for each write")
    }

    /// Two independent mock hubs with 2 and 4 entries; the second uses MOCK_PHONE_WRITE_DELIVERY=1.
    @MainActor func testConfirmationFromAnotherHubShowsOnlyItsOwnCount() async throws {
        guard let url = vars["PERSONA_PHONE_QUEUE_OTHER_HUB"], let other = URL(string: url), other.scheme == "http", other.host() == "127.0.0.1" else {
            throw XCTSkip("Needs a second independent hub offering four phone writes")
        }
        let selected = try hub()
        let before = try await reads(selected), otherBefore = try await reads(other)
        let app = launch(selected, language: "en", second: other, registerTools: true)
        XCTAssertTrue(app.descendants(matching: .any)["settings-list"].firstMatch.waitForExistence(timeout: 25))
        let rootReads = try await reads(selected), otherRootReads = try await reads(other)
        XCTAssertEqual(rootReads, before)
        XCTAssertEqual(otherRootReads, otherBefore)
        openPhoneData(app)
        let notice = app.descendants(matching: .any)["phone-write-queue-count"].firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 8))
        XCTAssertEqual(notice.label, "2 phone writes awaiting confirmation on this hub")
        try await deliverCalendarWrite("Other hub's calendar item", hub: other)
        let alert = app.alerts.containing(.button, identifier: "Deny").firstMatch
        let label = alert.staticTexts.matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@",
                                                          "Other hub's calendar item", "4 phone writes awaiting confirmation on this hub")).firstMatch
        XCTAssertTrue(label.waitForExistence(timeout: 10), alert.debugDescription)
        alert.buttons["Deny"].tap()
        XCTAssertTrue(alert.waitForNonExistence(timeout: 8))
        returnToPhoneData(app)
        XCTAssertTrue(notice.waitForExistence(timeout: 8))
        XCTAssertEqual(notice.label, "2 phone writes awaiting confirmation on this hub")

        // A dropped socket invalidates the selected hub's hint even while the other pairing is still live.
        let drop = selected.appending(path: "mock/down").appending(queryItems: [.init(name: "s", value: "8")])
        _ = try await URLSession.shared.data(from: drop)
        XCTAssertTrue(notice.waitForNonExistence(timeout: 6))
        XCTAssertTrue(notice.waitForExistence(timeout: 30))
        XCTAssertEqual(notice.label, "2 phone writes awaiting confirmation on this hub")

        // Select the second pairing in Settings. It has now received its own result and offers three.
        app.navigationBars.buttons.firstMatch.tap()
        openSettingsRow("settings.hubs", in: app)
        let pick = app.buttons["Make Current Hub"].firstMatch
        for _ in 0..<8 where !pick.isHittable { app.swipeUp(velocity: .slow) }
        XCTAssertTrue(pick.isHittable, app.debugDescription)
        pick.tap()
        let home = app.descendants(matching: .any)["settings-list"].firstMatch
        if !home.isHittable { app.navigationBars.buttons.firstMatch.tap() }
        openPhoneData(app)
        XCTAssertTrue(notice.waitForExistence(timeout: 10))
        XCTAssertEqual(notice.label, "3 phone writes awaiting confirmation on this hub")
    }

    @MainActor private func deliverCalendarWrite(_ title: String, hub: URL) async throws {
        struct Delivery: Decodable { var sent: Int }
        let args = ["title": title, "start": "2030-04-08T10:00:00Z", "end": "2030-04-08T10:30:00Z"]
        let json = String(decoding: try JSONEncoder().encode(args), as: UTF8.self)
        let url = hub.appending(path: "mock/tool_call").appending(queryItems: [
            .init(name: "name", value: "phone_calendar_create_event"), .init(name: "args", value: json),
        ])
        let deadline = ContinuousClock.now + .seconds(20)
        var sent = 0
        while sent == 0, ContinuousClock.now < deadline {
            sent = try await downloads(Delivery.self, url).sent
            if sent == 0 { try await Task.sleep(for: .milliseconds(150)) }
        }
        XCTAssertEqual(sent, 1, "The isolated simulated phone must register its real calendar tool")
    }
}
