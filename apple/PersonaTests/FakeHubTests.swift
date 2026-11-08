import Foundation
import Testing
@testable import Persona

@MainActor
struct FakeHubTests {
    private final class State {
        var calls = 0
    }

    @Test func backgroundTransportRunsRoutesOnTheirActor() async throws {
        let hub = FakeHub()
        let state = State()
        hub.on("GET", "/fixture") { _ in
            MainActor.assertIsolated()
            state.calls += 1
            return Data("answer".utf8)
        }
        let transport = try #require(hub.client.transport)
        let reply = try await Task.detached {
            try await transport(LocalRequest(method: "GET", path: "/fixture", query: [:], body: nil))
        }.value
        #expect(reply == Data("answer".utf8))
        #expect(state.calls == 1)
        #expect(hub.sent("GET", "/fixture").count == 1)
    }

    @Test func backgroundTransportPreservesRouteErrors() async throws {
        let hub = FakeHub()
        hub.on("DELETE", "/fixture") { _ in
            MainActor.assertIsolated()
            throw APIError(status: 409, message: "fixture refusal")
        }
        let transport = try #require(hub.client.transport)
        let status = await Task.detached {
            do {
                _ = try await transport(LocalRequest(method: "DELETE", path: "/fixture", query: [:], body: nil))
                return 0
            } catch let error as APIError {
                return error.status
            } catch {
                return -1
            }
        }.value
        #expect(status == 409)
        #expect(hub.sent("DELETE", "/fixture").count == 1)
    }
}
