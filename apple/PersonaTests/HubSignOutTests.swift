import Foundation
import Testing
@testable import Persona

/// Disconnecting signs this device out of each hub first (#1824): only a revoked credential makes the hub drop the
/// device's push and Live Activity registrations, so a hub it could not sign out of has to be named.
@MainActor
struct HubSignOutTests {
    private func hub(_ id: String, host: String, watch: String? = nil) -> PairedHub {
        var h = PairedHub(id: id, config: ServerConfig(url: URL(string: "https://\(host)")!, token: "t_\(id)"))
        h.watchDeviceID = watch
        return h
    }

    private func signOut(_ hubs: [PairedHub], _ fakes: [String: FakeHub]) async -> [String] {
        await AppModel.signOut(hubs) { fakes[$0.id]!.client }.map(\.id)
    }

    @Test func revokesTheWatchCredentialThenItsOwn() async {
        let fake = FakeHub()
        fake.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_phone"}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/dev_watch", json: #"{"ok":true}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/dev_phone", json: #"{"ok":true}"#)

        let missed = await signOut([hub("a", host: "a.example.com", watch: "dev_watch")], ["a": fake])

        #expect(missed.isEmpty)
        #expect(fake.requests.map { "\($0.method) \($0.path)" } == [
            "DELETE /api/v1/auth/tokens/dev_watch", "GET /api/v1/auth/tokens", "DELETE /api/v1/auth/tokens/dev_phone",
        ])
    }

    @Test func namesTheHubsItCouldNotSignOutOf() async {
        let ok = FakeHub(), away = FakeHub(), master = FakeHub(), demo = FakeHub()
        ok.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_ok"}"#)
        ok.on("DELETE", "/api/v1/auth/tokens/dev_ok", json: #"{"ok":true}"#)
        // The master token has no id: nothing to revoke, the hub keeps sending.
        master.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":""}"#)

        let missed = await signOut([hub("ok", host: "ok.example.com"), hub("away", host: "away.example.com"),
                                    hub("master", host: "master.example.com"), hub("demo", host: "demo.invalid")],
                                   ["ok": ok, "away": away, "master": master, "demo": demo])

        #expect(missed == ["away", "master"], "in the order they were paired; a demo hub is not a hub")
        #expect(ok.sent("DELETE", "/api/v1/auth/tokens/dev_ok").count == 1)
        #expect(master.requests.allSatisfy { $0.method == "GET" })
        #expect(demo.requests.isEmpty)
    }
}
