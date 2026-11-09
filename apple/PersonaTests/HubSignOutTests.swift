import Foundation
import Testing
@testable import Persona

/// Disconnecting signs this device out of each hub first (#1824): only a revoked credential makes the hub drop the
/// device's push and Live Activity registrations, so a hub it may still be signed in to has to be named.
@MainActor
struct HubSignOutTests {
    private func hub(_ id: String, host: String = "hub.example.com", watch: String? = nil) -> PairedHub {
        var h = PairedHub(id: id, config: ServerConfig(url: URL(string: "https://\(host)")!, token: "t_\(id)"))
        h.watchDeviceID = watch
        return h
    }

    private func signOut(_ hubs: [PairedHub], _ fakes: [String: FakeHub]) async -> [String] {
        await AppModel.signOut(hubs) { fakes[$0.id]!.client }.map(\.id)
    }

    private func calls(_ fake: FakeHub) -> [String] { fake.requests.map { "\($0.method) \($0.path)" } }

    @Test func revokesTheWatchCredentialBeforeItsOwn() async {
        let fake = FakeHub()
        fake.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_phone"}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/dev_watch", json: #"{"ok":true}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/dev_phone", json: #"{"ok":true}"#)

        let missed = await signOut([hub("a", watch: "dev_watch")], ["a": fake])

        #expect(missed.isEmpty)
        // Its own goes last: once it is revoked, nothing else can be asked of the hub.
        #expect(calls(fake) == ["GET /api/v1/auth/tokens", "DELETE /api/v1/auth/tokens/dev_watch", "DELETE /api/v1/auth/tokens/dev_phone"])
    }

    @Test func aWatchCredentialThatWontGoDoesNotKeepItsOwn() async {
        let fake = FakeHub()
        fake.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_phone"}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/dev_phone", json: #"{"ok":true}"#)

        let missed = await signOut([hub("a", watch: "dev_watch")], ["a": fake])

        #expect(missed.isEmpty, "the watch registers no pushes")
        #expect(fake.sent("DELETE", "/api/v1/auth/tokens/dev_phone").count == 1)
    }

    @Test func namesTheHubsItMayStillBeSignedInTo() async {
        let ok = FakeHub(), away = FakeHub(), master = FakeHub(), refusing = FakeHub(), demo = FakeHub()
        ok.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_ok"}"#)
        ok.on("DELETE", "/api/v1/auth/tokens/dev_ok", json: #"{"ok":true}"#)
        away.on("GET", "/api/v1/auth/tokens") { _ in throw URLError(.cannotConnectToHost) }
        // The master token has no id: nothing to revoke, the hub keeps pushing.
        master.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":""}"#)
        refusing.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_refusing"}"#)
        refusing.on("DELETE", "/api/v1/auth/tokens/dev_refusing") { _ in throw APIError(status: 500, message: "no database") }

        let missed = await signOut([hub("ok"), hub("away"), hub("master"), hub("refusing"), hub("demo", host: "demo.invalid")],
                                   ["ok": ok, "away": away, "master": master, "refusing": refusing, "demo": demo])

        #expect(missed == ["away", "master", "refusing"], "in the order given; a demo hub is not a hub")
        #expect(ok.sent("DELETE", "/api/v1/auth/tokens/dev_ok").count == 1)
        #expect(calls(away) == ["GET /api/v1/auth/tokens"], "out of reach: not asked again")
        #expect(calls(master) == ["GET /api/v1/auth/tokens"])
        #expect(demo.requests.isEmpty)
    }

    @Test func aPairingTokenIsNamedNotRevoked() async {
        // The trade for the device's own token failed: the hub knows it as `pair_<hash>`, which has no row to revoke
        // (DELETE would answer 404) and keeps the pushes registered with it.
        let fake = FakeHub()
        fake.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"pair_0123abcd"}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/dev_watch", json: #"{"ok":true}"#)
        fake.on("DELETE", "/api/v1/auth/tokens/pair_0123abcd") { _ in throw APIError(status: 404, message: "no such device") }

        let missed = await signOut([hub("a", watch: "dev_watch")], ["a": fake])

        #expect(missed == ["a"])
        #expect(calls(fake) == ["GET /api/v1/auth/tokens", "DELETE /api/v1/auth/tokens/dev_watch"])
    }

    @Test func aCredentialRevokedAlreadyIsSignedOut() async {
        // Revoked from another device (Settings › Devices): the hub dropped the pushes then.
        let revoked = FakeHub(), raced = FakeHub()
        revoked.on("GET", "/api/v1/auth/tokens") { _ in throw APIError(status: 401, message: "invalid token") }
        // Revoked between the two requests.
        raced.on("GET", "/api/v1/auth/tokens", json: #"{"tokens":[],"current":"dev_raced"}"#)
        raced.on("DELETE", "/api/v1/auth/tokens/dev_raced") { _ in throw APIError(status: 404, message: "no such device") }

        let missed = await signOut([hub("revoked", watch: "dev_watch"), hub("raced")], ["revoked": revoked, "raced": raced])

        #expect(missed.isEmpty)
        #expect(calls(revoked) == ["GET /api/v1/auth/tokens"])
    }
}
