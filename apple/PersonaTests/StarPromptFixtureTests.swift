import SwiftUI
import XCTest
@testable import Persona

/// Replays the shared fixture spec/fixtures/star-prompt/cases.json (#5761): when the empty Agents list's pill, the Agents
/// list's card and the onboarding toast show, by the rule the web (`web/src/lib/star-prompt.test.ts`) and Android replay
/// too. The model's own tests follow: what it keeps in this device's defaults, and Demo Mode.
@MainActor
final class StarPromptFixtureTests: XCTestCase {
    private static let repo = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()

    private struct Fixture: Decodable {
        var version: Int
        var cases: [Case]
    }

    private struct Case: Decodable {
        var name: String
        var now: Int64
        var state: StarPromptState
        var event: StarPromptEvent?
        var expected: Expected
    }

    private struct Expected: Decodable {
        var state: StarPromptState
        var pill: Bool
        var card: Bool
        var toast: Bool
    }

    func testEveryCaseShowsWhatTheFixtureSays() throws {
        let data = try Data(contentsOf: Self.repo.appending(path: "spec/fixtures/star-prompt/cases.json"))
        let fixture = try JSONDecoder().decode(Fixture.self, from: data)
        XCTAssertEqual(fixture.version, 1)
        XCTAssertGreaterThanOrEqual(fixture.cases.count, 13)
        for c in fixture.cases {
            let (state, toast) = c.event.map { c.state.after($0, now: c.now) } ?? (c.state, false)
            XCTAssertEqual(state, c.expected.state, c.name)
            XCTAssertEqual(state.showsPill, c.expected.pill, "\(c.name): pill")
            XCTAssertEqual(state.showsCard(now: c.now), c.expected.card, "\(c.name): card")
            XCTAssertEqual(toast, c.expected.toast, "\(c.name): toast")
        }
    }

    /// A damaged or older value falls back field by field, and a huge count cannot overflow Later.
    func testDamagedValuesStartOver() throws {
        let damaged = Data(#"{"launches":-4,"threshold":0,"snoozedUntil":"soon","done":"yes"}"#.utf8)
        XCTAssertEqual(try JSONDecoder().decode(StarPromptState.self, from: damaged), .start)
        XCTAssertEqual(try JSONDecoder().decode(StarPromptState.self, from: Data("{}".utf8)), .start)
        let big = StarPromptState(launches: .max, threshold: .max / 2 + 1)
        XCTAssertEqual(big.after(.later, now: 0).state.threshold, .max)
        XCTAssertEqual(big.after(.launch, now: 0).state.launches, .max)
    }

    /// Each event is kept in this device's defaults and read back by the next launch; Demo Mode neither shows nor counts.
    func testTheModelKeepsItsStateAndSkipsDemoMode() throws {
        let suite = "StarPromptFixtureTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = StarPromptModel(defaults: defaults)
        XCTAssertTrue(model.showsPill(demo: false))
        XCTAssertFalse(model.showsPill(demo: true))
        model.launched(demo: true)
        XCTAssertEqual(model.state.launches, 0, "an Agent started in Demo Mode doesn't count")
        for _ in 0..<StarPromptState.start.threshold { model.launched(demo: false) }
        XCTAssertTrue(model.showsCard(demo: false))
        XCTAssertFalse(model.showsCard(demo: true))
        model.later()
        XCTAssertFalse(model.showsCard(demo: false), "Later holds the card")
        XCTAssertFalse(model.onboarded(demo: true), "no toast in Demo Mode")
        XCTAssertTrue(model.onboarded(demo: false))
        XCTAssertFalse(model.onboarded(demo: false), "once per device")
        model.hidePill()
        XCTAssertFalse(model.showsPill(demo: false))

        let next = StarPromptModel(defaults: defaults)
        XCTAssertEqual(next.state, model.state, "read back as written")
        XCTAssertEqual(next.state.threshold, StarPromptState.start.threshold * 2)
    }

    /// Star on GitHub opens the project and turns every prompt off.
    func testStarOpensTheProjectAndStopsEveryPrompt() throws {
        let suite = "StarPromptFixtureTests.\(UUID().uuidString)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let model = StarPromptModel(defaults: defaults)
        let opened = OpenedURLs()
        model.star(open: OpenURLAction { url in
            opened.add(url)
            return .handled
        })
        XCTAssertEqual(opened.urls, [StarPromptModel.repoURL])
        XCTAssertEqual(StarPromptModel.repoURL.absoluteString, "https://github.com/nwverse/persona")
        XCTAssertTrue(model.state.done)
        XCTAssertFalse(model.showsPill(demo: false))
        XCTAssertFalse(model.onboarded(demo: false), "no toast after a star")
        XCTAssertTrue(StarPromptModel(defaults: defaults).state.done)
    }
}

/// What `OpenURLAction` was asked to open.
private final class OpenedURLs: @unchecked Sendable {
    private let lock = NSLock()
    private var list: [URL] = []

    var urls: [URL] { lock.withLock { list } }
    func add(_ url: URL) { lock.withLock { list.append(url) } }
}
