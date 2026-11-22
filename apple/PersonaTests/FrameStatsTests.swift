#if DEBUG
import Foundation
import Testing
@testable import Persona

/// FrameStats writes its numbers as JSON for the measuring scripts (docs/operations/testing.md「Jank measurement」).
@MainActor struct FrameStatsTests {
    @Test func stopWritesItsNumbersAsJSON() async throws {
        let dir = try #require(FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first)
        let file = dir.appending(path: "frames-unit-test.json")
        try? FileManager.default.removeItem(at: file)
        FrameStats.shared.start()
        try await Task.sleep(for: .milliseconds(300))
        FrameStats.shared.stop(label: "unit-test")
        let json = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
        #expect(json["label"] as? String == "unit-test")
        for key in ["busy_max_ms", "busy_p95_ms", "max_ms", "p95_ms"] {
            #expect(json[key] is Double, "\(key) is \(String(describing: json[key]))")
        }
    }
}
#endif
