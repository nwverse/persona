import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Shared wire fixtures and the plan card's Start These Directly visibility (#17853).
@MainActor struct PlanStartWireTests {
    private static let fixtureURL = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
        .appending(path: "spec/fixtures/plan-start/plan_start.json")

    @Test func generatedTypeRoundTripsAllSharedFixtures() throws {
        let data = try Data(contentsOf: Self.fixtureURL)
        let root = try JSONSerialization.jsonObject(with: data) as? [String: Any] ?? [:]
        let cases = root["cases"] as? [[String: Any]] ?? []
        #expect(!cases.isEmpty)
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        var seen: Set<PlanStartReason> = []
        for c in cases {
            let name = c["name"] as? String ?? ""
            let expected = c["expected"] as? [String: Any] ?? [:]
            let sample = expected["start"] as? [String: Any] ?? [:]
            let raw = try JSONSerialization.data(withJSONObject: sample, options: [.sortedKeys, .withoutEscapingSlashes])
            let decoded = try JSONDecoder().decode(PlanStartView.self, from: raw)
            if case .unknown = decoded.reason { Issue.record("\(name): unexpected unknown reason") }
            seen.insert(decoded.reason)
            #expect(try encoder.encode(decoded) == raw, "\(name)")
        }
        #expect(seen == Set(PlanStartReason.all))
    }

    @Test func unknownReasonPreservesRawValue() throws {
        let raw = Data(#"{"reason":"future_gate","remember":[0]}"#.utf8)
        let decoded = try JSONDecoder().decode(PlanStartView.self, from: raw)
        #expect(decoded.reason == .unknown("future_gate"))
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        #expect(try encoder.encode(decoded) == raw)
    }

    private func plan(start: String?) throws -> TaskPlan {
        let extra = start.map { ",\"start\":\($0)" } ?? ""
        let raw = #"{"id":"p1","conversation":"c1","state":"open","tasks":[{"title":"Edit","prompt":"Edit","machine":"m1","cwd":"/code/app"}]}"#
            + extra + "}"
        return try JSONDecoder().decode(TaskPlan.self, from: Data(raw.utf8))
    }

    @Test func currentHubOffersMemoryOnlyForOrdinaryUncoveredWork() throws {
        let offered = try plan(start: #"{"reason":"start_authorization","remember":[0]}"#)
        #expect(offered.startRememberOffered)
        let covered = try plan(start: #"{"reason":"start_authorization"}"#)
        #expect(!covered.startRememberOffered)
        let owner = try plan(start: #"{"reason":"owner_step","remember":[0]}"#)
        #expect(!owner.startRememberOffered)
        let unknown = try plan(start: #"{"reason":"future_gate","remember":[0]}"#)
        #expect(!unknown.startRememberOffered)
    }

    @Test func olderHubWithoutStartKeepsTheLegacyOffer() throws {
        let legacy = try plan(start: nil)
        #expect(legacy.start == nil)
        #expect(legacy.startRememberOffered)
    }
}
