import Foundation
import Testing
@testable import Persona

/// Settings › Claude Code Settings › Live Replies (`LiveRepliesModel`, #1309) against a FakeHub: read when shown, the new
/// value at once on a change, the old one back with why on a failure, the other online computers too, no switch on an
/// older hub.
@MainActor struct LiveRepliesTests {
    private let box = MachineRef(group: "", id: "box")
    private let studio = MachineRef(group: "", id: "studio")
    private static let path = "/api/v1/machines/box/claude-live-replies"

    private func make(_ hub: FakeHub, api: ((MachineRef) -> APIClient?)? = nil, toasts: @escaping (Toast) -> Void = { _ in }) -> LiveRepliesModel {
        LiveRepliesModel(ref: box, deps: .init(api: api ?? { _ in hub.client }, name: { $0.id == "studio" ? "Studio" : $0.id }, toast: toasts))
    }

    private func body(_ req: LocalRequest) throws -> [String: Bool] {
        let data = try #require(req.body)
        return try #require(try JSONSerialization.jsonObject(with: data) as? [String: Bool])
    }

    @Test func readsTheSwitchWhenShown() async {
        let hub = FakeHub()
        hub.on("GET", Self.path, json: #"{"on":false}"#)
        let m = make(hub)
        #expect(m.on == nil)
        await m.load()
        #expect(m.on == false && m.loadError == nil && !m.unsupported)
    }

    @Test func aChangeShowsAtOnceAndKeepsWhatTheHubSays() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.path, json: #"{"on":true}"#)
        var seen: Bool?
        var m: LiveRepliesModel!
        hub.on("PUT", Self.path) { _ in
            seen = m.on
            return Data(#"{"on":false}"#.utf8)
        }
        var toasts: [Toast] = []
        m = make(hub, toasts: { toasts.append($0) })
        await m.load()
        await m.set(false)
        #expect(seen == false, "the switch moved before the hub answered")
        #expect(m.on == false && m.error == nil && !m.saving)
        let put = try #require(hub.sent("PUT", Self.path).first)
        #expect(try body(put) == ["on": false])
        #expect(toasts.map(\.message) == ["已保存"])
    }

    @Test func aFailedChangePutsTheOldValueBackAndSaysWhy() async {
        let hub = FakeHub()
        hub.on("GET", Self.path, json: #"{"on":true}"#)
        hub.on("PUT", Self.path) { _ in throw APIError(status: 500, message: "写不进设置") }
        var toasts = 0
        let m = make(hub, toasts: { _ in toasts += 1 })
        await m.load()
        await m.set(false)
        #expect(m.on == true)
        #expect(m.error == "没改成：写不进设置")
        #expect(toasts == 0)
    }

    /// Apply to All Online Machines: the other computers change too; one of them failing is said by its name, and the
    /// shown one keeps its new value.
    @Test func theOtherOnlineComputersChangeToo() async throws {
        let hub = FakeHub()
        hub.on("GET", Self.path, json: #"{"on":true}"#)
        hub.on("PUT", Self.path, json: #"{"on":false}"#)
        hub.on("PUT", "/api/v1/machines/studio/claude-live-replies") { _ in throw APIError(status: 503, message: "离线") }
        var toasts = 0
        let m = make(hub, toasts: { _ in toasts += 1 })
        await m.load()
        await m.set(false, others: [studio, box])
        #expect(m.on == false)
        #expect(hub.sent("PUT", Self.path).count == 1, "the shown computer once, though it was listed again")
        let other = try #require(hub.sent("PUT", "/api/v1/machines/studio/claude-live-replies").first)
        #expect(try body(other) == ["on": false])
        #expect(m.error == "Studio：离线" && toasts == 0)
    }

    @Test func allChangedSaysHowMany() async {
        let hub = FakeHub()
        hub.on("GET", Self.path, json: #"{"on":false}"#)
        hub.on("PUT", Self.path, json: #"{"on":true}"#)
        hub.on("PUT", "/api/v1/machines/studio/claude-live-replies", json: #"{"on":true}"#)
        var toasts: [Toast] = []
        let m = make(hub, toasts: { toasts.append($0) })
        await m.load()
        await m.set(true, others: [studio])
        #expect(m.on == true && m.error == nil)
        #expect(toasts.map(\.message) == ["已应用到 2 台机器"])
    }

    @Test func anOlderHubHasNoSwitch() async {
        let hub = FakeHub() // no route: 404, as a hub before #1309
        let m = make(hub)
        await m.load()
        #expect(m.unsupported && m.on == nil && m.loadError == nil)
        await m.set(false)
        #expect(hub.sent("PUT", Self.path).isEmpty)
    }

    @Test func aReadThatFailsSaysSoAndRetryClearsIt() async {
        let hub = FakeHub()
        var fail = true
        hub.on("GET", Self.path) { _ in
            if fail { throw URLError(.timedOut) }
            return Data(#"{"on":true}"#.utf8)
        }
        let m = make(hub)
        await m.load()
        #expect(m.on == nil && m.loadError == "读不到这台电脑的设置：连不上 hub")
        fail = false
        await m.load()
        #expect(m.on == true && m.loadError == nil)
    }

    @Test func notConnectedToItsHubSaysSo() async {
        let m = make(FakeHub(), api: { _ in nil })
        await m.load()
        #expect(m.loadError == "还没连上这台电脑所在的 hub" && m.on == nil)
    }

    @Test func aCancelledReadSaysNothing() async {
        let hub = FakeHub()
        hub.on("GET", Self.path) { _ in throw CancellationError() }
        let m = make(hub)
        await m.load()
        #expect(m.on == nil && m.loadError == nil && !m.unsupported)
    }
}
