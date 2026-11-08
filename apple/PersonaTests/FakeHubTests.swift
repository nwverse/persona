import Foundation
import Testing
@testable import Persona

@MainActor
struct FakeHubTests {
    @Test func actorOwnedRoutesAreSafeFromDetachedRequests() async throws {
        let hub = FakeHub()
        var calls = 0
        hub.on("GET", "/actor-owned") { _ in
            MainActor.assertIsolated()
            calls += 1
            // Route execution must be outside the table lock: handlers can
            // register the next response and inspect the recorded request.
            #expect(hub.sent("GET", "/actor-owned").count == 1)
            hub.on("GET", "/next", json: #"{"ok":true}"#)
            return Data(#"{"first":true}"#.utf8)
        }
        let client = hub.client
        let first = try await Task.detached {
            try await client.request("GET", "/actor-owned")
        }.value
        #expect(first == Data(#"{"first":true}"#.utf8))
        #expect(calls == 1)
        let next = try await Task.detached {
            try await client.request("GET", "/next")
        }.value
        #expect(next == Data(#"{"ok":true}"#.utf8))
        #expect(hub.sent("GET", "/next").count == 1)
    }
}
