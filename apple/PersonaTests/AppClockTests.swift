import XCTest
@testable import Persona

#if DEBUG
final class AppClockTests: XCTestCase {
    private let key = "showClockAt"
    private let defaults = UserDefaults.standard
    private var previous: Any?

    override func setUp() {
        super.setUp()
        previous = defaults.object(forKey: key)
        AppClock.resetForTests()
    }

    override func tearDown() {
        defaults.set(previous, forKey: key)
        AppClock.resetForTests()
        previous = nil
        super.tearDown()
    }

    func testLaunchArgumentPinsNowAndIsReadOnlyOnce() {
        defaults.set("2026-01-01T12:00:00Z", forKey: key)
        let pinned = Date(timeIntervalSince1970: 1_767_268_800)
        XCTAssertEqual(AppClock.now, pinned)

        defaults.set("2030-01-01T12:00:00Z", forKey: key)
        XCTAssertEqual(AppClock.now, pinned, "a later defaults write is not a new launch")
    }

    func testLaunchArgumentAcceptsFractionalSeconds() {
        defaults.set("2026-01-01T12:00:00.123Z", forKey: key)
        XCTAssertEqual(AppClock.now.timeIntervalSince1970, 1_767_268_800.123, accuracy: 0.000_001)
    }

    func testNoLaunchArgumentUsesTheSystemClock() {
        defaults.removeObject(forKey: key)
        let before = Date()
        let actual = AppClock.now
        let after = Date()
        XCTAssertGreaterThanOrEqual(actual, before)
        XCTAssertLessThanOrEqual(actual, after)
    }

    func testShowcaseTimesUseThePinnedClock() throws {
        struct Showcase: Decodable {
            var items: [InboxItem]
            var detail: String
        }

        defaults.set("2026-01-01T12:00:00Z", forKey: key)
        let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        let data = try Data(contentsOf: repo.appending(path: "spec/fixtures/inbox/showcase.json"))
        let showcase = try JSON.decoder.decode(Showcase.self, from: data)
        let item = try XCTUnwrap(showcase.items.first { $0.id == showcase.detail })
        let updatedAt = try XCTUnwrap(item.updatedAt)
        let checkAt = try XCTUnwrap(item.follow?.checkAt)
        var utc = Calendar(identifier: .gregorian)
        utc.timeZone = TimeZone(identifier: "UTC")!

        XCTAssertEqual(LarkTime.tier(of: updatedAt, calendar: utc), .today)
        XCTAssertEqual(LarkTime.short(updatedAt, calendar: utc),
                       updatedAt.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits)))
        XCTAssertEqual(InboxNow.time(checkAt, lang: "en", now: AppClock.now, calendar: utc), "Jan 2, 09:00")
        // The Apple test scheme runs in zh-Hans; the comma comes from the existing localized date-and-time pattern.
        XCTAssertEqual(InboxNow.text(for: item, calendar: utc), "等张三确认新报价 · 1月2日，09:00 再看：问更正后的报价行不行")
    }
}
#endif
