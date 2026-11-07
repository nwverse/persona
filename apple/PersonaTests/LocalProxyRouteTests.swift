import Foundation
import Testing
@testable import Persona

/// The local proxy route (docs/operations/network.md "Connecting to hub through a local SOCKS5 proxy"): what is kept with
/// the pairing and what it defaults to, and the forward itself, through a SOCKS5 proxy to a hub started here on the
/// loopback. Picking the way is `HubWay`'s, shared with the SSH and in-app tunnels (HubWayTests).
@Suite(.serialized)
struct LocalProxyRouteTests {
    // MARK: What is kept

    @Test func aSavedProxyKeepsItsLoginAndDefaults() throws {
        let sparse = try JSONDecoder().decode(LocalProxyRoute.self, from: Data(#"{"port":1080}"#.utf8))
        #expect(sparse == LocalProxyRoute(port: 1080))
        #expect(sparse.host == "127.0.0.1" && sparse.login == nil && sparse.targetHost == nil && sparse.targetPort == nil && !sparse.always)
        let full = LocalProxyRoute(host: "::1", port: 7891, username: " me ", password: "s3cret", targetHost: "hub.internal.test",
                                   targetPort: 8080, always: true)
        #expect(try JSONDecoder().decode(LocalProxyRoute.self, from: JSONEncoder().encode(full)) == full)
        #expect(full.label == "[::1]:7891")
        #expect(full.login?.username == "me" && full.login?.password == "s3cret")
        // Pairings saved before the proxy decode as they were; one with a proxy keeps it, password and all.
        let old = #"{"hubs":[{"id":"h1","config":{"url":"http://hub-b.example.com:18660","token":"t"},"group":"","addedAt":0}],"groupNames":{}}"#
        var hub = try JSONDecoder().decode(HubRegistry.self, from: Data(old.utf8)).hubs[0]
        #expect(hub.localProxy == nil)
        hub.localProxy = full
        #expect(try JSONDecoder().decode(PairedHub.self, from: JSONEncoder().encode(hub)).localProxy == full)
        #expect(!LocalProxyRoute(port: 0).isUsable)
        #expect(!LocalProxyRoute(port: 1080, targetPort: 70000).isUsable)
        #expect(!LocalProxyRoute(host: " ", port: 1080).isUsable)
    }

    @Test func theHubsAddressDefaultsToThePairedOne() {
        let route = LocalProxyRoute(port: 1080)
        let lan = URL(string: "http://192.0.2.20:18660")!
        #expect(route.targetHost(for: lan) == "192.0.2.20" && route.targetPort(for: lan) == 18660)
        #expect(route.targetPort(for: URL(string: "http://hub.lan")!) == 80)
        // An https address: the forward carries plain HTTP, so the hub's HTTP port.
        let tls = URL(string: "https://hub.example.com/persona")!
        #expect(route.targetHost(for: tls) == "hub.example.com" && route.targetPort(for: tls) == LocalProxyRoute.hubHTTPPort)
        #expect(route.targetHost(for: URL(string: "http://[2001:db8::5]:18660")!) == "2001:db8::5")
        let set = LocalProxyRoute(port: 1080, targetHost: " 198.51.100.5 ", targetPort: 8080)
        #expect(set.targetHost(for: lan) == "198.51.100.5" && set.targetPort(for: lan) == 8080)
        #expect(set.dial(for: lan) == LocalProxyForward.Dial(proxyHost: "127.0.0.1", proxyPort: 1080, targetHost: "198.51.100.5", targetPort: 8080))
        #expect(LocalProxyRoute(port: 1080, targetHost: "2001:db8::5").dial(for: lan).target == "[2001:db8::5]:18660")
    }

    @Test func theHealthCheckKeepsTheHubsPath() {
        let local = URL(string: "http://127.0.0.1:52001")!
        #expect(LocalProxies.healthURL(hub: URL(string: "https://hub.example.com/persona/")!, via: local)?.absoluteString
                == "http://127.0.0.1:52001/persona/api/v1/health")
        #expect(LocalProxies.healthURL(hub: URL(string: "http://192.0.2.20:18660")!, via: local)?.absoluteString
                == "http://127.0.0.1:52001/api/v1/health")
    }

    // MARK: The forward

    private func hubAndProxy(login: (username: String, password: String)? = nil, notAHub: Bool = false) async throws -> (FakeHealthHub, FakeSOCKS5) {
        let hub = try FakeHealthHub(notAHub: notAHub), proxy = try FakeSOCKS5(login: login)
        try await hub.start()
        try await proxy.start()
        return (hub, proxy)
    }

    /// The paired address is one nothing answers at: only the proxy can get there.
    private let paired = URL(string: "http://hub-behind-proxy.invalid:18660")!

    @MainActor @Test func testConnectionReachesTheHubThroughTheProxy() async throws {
        let (hub, proxy) = try await hubAndProxy(login: ("me", "s3cret"))
        defer { hub.stop(); proxy.stop() }
        let route = LocalProxyRoute(port: proxy.port, username: "me", password: "s3cret", targetHost: "127.0.0.1", targetPort: hub.port)
        try await LocalProxies().verify(route, hub: paired)
        #expect(proxy.requests.first == FakeSOCKS5.Asked(host: "127.0.0.1", port: hub.port, username: "me"))
        var wrong = route
        wrong.password = "wrong"
        await #expect(throws: SOCKS5Error.loginRejected) { try await LocalProxies().verify(wrong, hub: self.paired) }
        let (page, open) = try await hubAndProxy(notAHub: true)
        defer { page.stop(); open.stop() }
        await #expect(throws: LocalProxyCheck.notAHub("127.0.0.1:\(page.port)")) {
            try await LocalProxies().verify(LocalProxyRoute(port: open.port, targetHost: "127.0.0.1", targetPort: page.port), hub: self.paired)
        }
    }

    @MainActor @Test func theHubsRequestsGoThroughTheForward() async throws {
        let (hub, proxy) = try await hubAndProxy()
        defer { hub.stop(); proxy.stop() }
        let proxies = LocalProxies()
        let route = LocalProxyRoute(port: proxy.port, targetHost: "127.0.0.1", targetPort: hub.port)
        let local = try await proxies.open("h1", route: route, hub: paired)
        defer { proxies.close("h1") }
        #expect(HubTransport.via(paired) == local && proxies.routed.contains("h1") && proxies.states["h1"] == .up)
        // The app's own requests to the hub's address, as APIClient makes them: they land on the hub behind the proxy.
        let api = APIClient(config: ServerConfig(url: paired, token: "t"))
        let url = api.url("/api/v1/health")
        #expect(url.host() == "127.0.0.1" && url.port == local.port)
        let (data, resp) = try await APIClient.uncachedSession.data(from: url)
        #expect((resp as? HTTPURLResponse)?.statusCode == 200 && String(decoding: data, as: UTF8.self) == #"{"ok":true}"#)
        // A WebSocket to the hub goes to the same place.
        let ws = HubTransport.resolve(URL(string: "ws://hub-behind-proxy.invalid:18660/api/v1/ws")!, hub: paired)
        #expect(ws.absoluteString == "ws://127.0.0.1:\(local.port!)/api/v1/ws")
        proxies.close("h1")
        #expect(HubTransport.via(paired) == nil && !proxies.routed.contains("h1"))
        #expect((try? await APIClient.uncachedSession.data(from: url)) == nil) // the listener is gone
    }

    @MainActor @Test func aProxyThatStoppedWorkingIsNotTaken() async throws {
        let (hub, proxy) = try await hubAndProxy()
        defer { hub.stop() }
        let proxies = LocalProxies()
        let route = LocalProxyRoute(port: proxy.port, targetHost: "127.0.0.1", targetPort: hub.port)
        let local = try await proxies.open("h1", route: route, hub: paired)
        defer { proxies.close("h1") }
        // Picked again while it is still there: the same forward, the same address.
        #expect(try await proxies.open("h1", route: route, hub: paired) == local)
        proxy.stop() // the app serving it quit
        try await Task.sleep(for: .milliseconds(100))
        do {
            _ = try await proxies.open("h1", route: route, hub: paired)
            Issue.record("took a proxy that is not there")
        } catch SOCKS5Error.unreachable {}
        #expect(HubTransport.via(paired) == nil && !proxies.routed.contains("h1"))
        guard case .down? = proxies.states["h1"] else {
            Issue.record("no reason kept: \(String(describing: proxies.states["h1"]))")
            return
        }
    }

    @MainActor @Test func theDirectWayIsAskedEveryRecheckUntilItAnswers() async throws {
        let (hub, proxy) = try await hubAndProxy()
        defer { hub.stop(); proxy.stop() }
        let proxies = LocalProxies()
        _ = try await proxies.open("h1", route: LocalProxyRoute(port: proxy.port, targetHost: "127.0.0.1", targetPort: hub.port), hub: paired)
        defer { proxies.close("h1") }
        // 120 s scaled down: the hub's own address answers from the third ask on.
        var asked = 0
        var moved = false
        let start = ContinuousClock.now
        proxies.watchDirect("h1", every: .milliseconds(100), answers: { asked += 1; return asked >= 3 }, moveBack: { moved = true })
        for _ in 0..<100 where !moved { try await Task.sleep(for: .milliseconds(20)) }
        #expect(moved && asked == 3)
        #expect(ContinuousClock.now - start >= .milliseconds(300))
        // Closed meanwhile (the group moved on): the watch ends without moving anything.
        var late = false
        proxies.watchDirect("h1", every: .milliseconds(50), answers: { true }, moveBack: { late = true })
        proxies.close("h1")
        try await Task.sleep(for: .milliseconds(200))
        #expect(!late)
    }
}
