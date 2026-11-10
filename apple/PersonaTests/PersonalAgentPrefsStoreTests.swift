import Foundation
import Testing
@testable import Persona

private actor PersonalAgentPrefsHub {
    private var stored: PersonalAgentPrefs
    private var refusePut = false
    private var refuseEveryPut = false
    private var putStatus = 500
    private var holdGet = false
    private var holdPut = false
    private var releaseHeldGet: AsyncStream<Void>.Continuation?
    private var releaseHeldPut: AsyncStream<Void>.Continuation?
    private let getStatus: Int?
    private let events: AsyncStream<String>.Continuation

    init(stored: PersonalAgentPrefs, getStatus: Int? = nil, events: AsyncStream<String>.Continuation) {
        self.stored = stored
        self.getStatus = getStatus
        self.events = events
    }

    func refuseNextPut() { refusePut = true }
    func refuseEveryPut(status: Int) {
        putStatus = status
        refusePut = true
        refuseEveryPut = true
    }

    func holdNextGet() { holdGet = true }
    func holdNextPut() { holdPut = true }
    func releaseGet() {
        releaseHeldGet?.yield(())
        releaseHeldGet?.finish()
        releaseHeldGet = nil
    }
    func releasePut() {
        releaseHeldPut?.yield(())
        releaseHeldPut?.finish()
        releaseHeldPut = nil
    }

    func answer(_ request: LocalRequest) async throws -> Data {
        if request.method == "PUT" {
            let changed = try JSONDecoder().decode(PersonalAgentPrefs.self, from: request.body ?? Data())
            if refusePut {
                refusePut = refuseEveryPut
                events.yield("put-failed")
                throw APIError(status: putStatus, message: "offline")
            }
            if holdPut {
                holdPut = false
                let gate = AsyncStream<Void>.makeStream()
                releaseHeldPut = gate.continuation
                events.yield("put-held")
                for await _ in gate.stream { break }
                releaseHeldPut = nil
            }
            stored = changed
            events.yield("PUT")
            return try JSONEncoder().encode(stored)
        }
        let snapshot = stored
        events.yield("GET")
        if holdGet {
            holdGet = false
            let gate = AsyncStream<Void>.makeStream()
            releaseHeldGet = gate.continuation
            events.yield("get-held")
            for await _ in gate.stream { break }
            releaseHeldGet = nil
        }
        if let getStatus { throw APIError(status: getStatus, message: "offline") }
        return try JSONEncoder().encode(snapshot)
    }
}

@MainActor struct PersonalAgentPrefsStoreTests {
    @Test func failedSaveRollsBackReportsAndReloads() async throws {
        var original = PersonalAgentPrefs()
        original.pins = ["before"]
        let events = AsyncStream<String>.makeStream()
        let hub = PersonalAgentPrefsHub(stored: original, events: events.continuation)
        let api = APIClient(config: ServerConfig(url: URL(string: "https://hub.invalid")!, token: "fixture"), transport: { try await hub.answer($0) })
        let name = "com.example.persona.prefs-tests." + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let store = PersonalAgentPrefsStore(defaults: defaults)
        await store.load(api)
        await hub.refuseNextPut()
        var reported = ""
        store.update(api, { $0.togglePin("after") }, onFailure: { reported = $0 })
        #expect(store.prefs.pins.contains("after"), "the edit is optimistic while the PUT is in flight")

        var putFailed = false
        for await event in events.stream {
            if event == "put-failed" { putFailed = true }
            if putFailed && event == "GET" { break }
        }
        #expect(reported.contains("offline"))
        #expect(store.prefs == original, "a failed save restores the hub-backed value instead of looking saved")
        events.continuation.finish()
    }

    @Test func aStaleReadDoesNotOverwriteANewerEdit() async throws {
        let events = AsyncStream<String>.makeStream()
        let hub = PersonalAgentPrefsHub(stored: PersonalAgentPrefs(), events: events.continuation)
        let api = APIClient(config: ServerConfig(url: URL(string: "https://hub.invalid")!, token: "fixture"), transport: { try await hub.answer($0) })
        let name = "com.example.persona.prefs-tests." + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let store = PersonalAgentPrefsStore(defaults: defaults)
        await store.load(api)
        await hub.holdNextGet()
        await hub.holdNextPut()

        let staleRead = Task { await store.load(api) }
        for await event in events.stream where event == "get-held" { break }
        store.update(api, { $0.togglePin("new") })
        for await event in events.stream where event == "put-held" { break }
        await hub.releaseGet()
        await staleRead.value
        #expect(store.prefs.pins == ["new"], "a GET that began before the edit must not erase its optimistic value")
        await hub.releasePut()
        for await event in events.stream where event == "PUT" { break }
        events.continuation.finish()
    }

    @Test func anOlderFailureDoesNotRollBackANewerEdit() async throws {
        let events = AsyncStream<String>.makeStream()
        let hub = PersonalAgentPrefsHub(stored: PersonalAgentPrefs(), getStatus: 500, events: events.continuation)
        let api = APIClient(config: ServerConfig(url: URL(string: "https://hub.invalid")!, token: "fixture"), transport: { try await hub.answer($0) })
        let name = "com.example.persona.prefs-tests." + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let store = PersonalAgentPrefsStore(defaults: defaults)
        await hub.refuseEveryPut(status: 500)
        var reports: [String] = []
        store.update(api, { $0.view.group = AgentGroupBy.none.rawValue }, onFailure: { _ in reports.append("first") })
        store.update(api, { $0.view.sort = AgentSortBy.name.rawValue }, onFailure: { _ in
            reports.append("second")
            events.continuation.yield("reported-second")
        })
        for await event in events.stream where event == "reported-second" { break }
        for _ in 0..<3 { await Task.yield() }
        #expect(reports == ["second"], "the superseded PUT neither rolls back nor reports over the latest failure")
        #expect(store.prefs.view.groupBy == .none, "the older default state must not replace the newer edit")
        #expect(store.prefs.view.sortBy == .updated, "the latest failure restores the state immediately before its own edit")
        events.continuation.finish()
    }

    @Test(arguments: [404, 405]) func anOlderHubKeepsLocalPreferences(status: Int) async throws {
        let events = AsyncStream<String>.makeStream()
        let hub = PersonalAgentPrefsHub(stored: PersonalAgentPrefs(), getStatus: status, events: events.continuation)
        let api = APIClient(config: ServerConfig(url: URL(string: "https://hub.invalid")!, token: "fixture"), transport: { try await hub.answer($0) })
        let name = "com.example.persona.prefs-tests." + UUID().uuidString
        let defaults = UserDefaults(suiteName: name)!
        defer { defaults.removePersistentDomain(forName: name) }
        let store = PersonalAgentPrefsStore(defaults: defaults)
        await hub.refuseEveryPut(status: status)
        var reported = ""
        store.update(api, { $0.togglePin("local") }, onFailure: { reported = $0 })
        for await event in events.stream where event == "put-failed" { break }
        await store.load(api)
        #expect(reported.isEmpty, "an older hub has no save failure: its preferences are intentionally local")
        #expect(store.prefs.pins == ["local"])
        events.continuation.finish()
    }
}
