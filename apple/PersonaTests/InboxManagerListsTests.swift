import Foundation
import Testing
@testable import Persona

/// The Inbox's Rules and Muted lists with several managers (docs/assistant/managers.md "Grouping"): each manager's own
/// list is read with it named and shown under its name, the default first; one that can't be read says so in its place;
/// Remove and Unmute go to the manager whose it is (still after another became the default), a rule or mute with the same
/// id elsewhere stays, and one that fails to go comes back alone.
@MainActor
struct InboxManagerListsTests {
    private static let roster = ManagerRoster(managers: [
        HubManager(id: "assistant", name: "助手", online: true, isDefault: true),
        HubManager(id: "m-2", name: "工作", online: true),
        HubManager(id: "m-3", name: "家里", online: false),
    ], defaultID: "assistant")

    /// A hub whose managers each answer `path` with their own `key` list: none named is the default's; 家里 is offline.
    private static func hub(_ path: String, _ key: String, mine: String, work: String) -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", path) { req in
            switch req.headers["X-Persona-Manager"] {
            case nil: return Data(#"{"\#(key)":[\#(mine)]}"#.utf8)
            case "m-2": return Data(#"{"\#(key)":[\#(work)]}"#.utf8)
            default: throw APIError(status: 503, message: "assistant offline")
            }
        }
        return hub
    }

    @Test func eachManagersRulesAreReadUnderItsNameAndRemovedOnIt() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies",
                           mine: #"{"id":"p1","source":"mail","tag":"sender","value":"a@example.com","name":"甲","tier":"action","created_at":"2026-11-04T01:00:00Z"}"#,
                           work: #"{"id":"p1","source":"mail","tag":"sender","value":"b@example.com","name":"乙","tier":"noise","created_at":"2026-11-05T01:00:00Z"}"#)
        var named: [String?] = []
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { req in
            named.append(req.headers["X-Persona-Manager"])
            return Data(#"{"ok":true}"#.utf8)
        }
        var toasts: [Toast] = []
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { toasts.append($0) }, roster: { Self.roster }))
        await m.load()
        #expect(m.shares?.map(\.manager.id) == ["assistant", "m-2", "m-3"])
        #expect(m.shares?.map { $0.items.map(\.name) } == [["甲"], ["乙"], []])
        #expect(m.shares?.map { $0.items.first?.manager } == ["assistant", "m-2", nil])
        #expect(m.shares?.map(\.failed) == [nil, nil, "离线中：它回来后这里会显示它的规则。"])
        #expect(m.state.value?.map(\.name) == ["乙", "甲"])

        await m.remove(try #require(m.shares?[1].items.first))
        #expect(named == ["m-2"])
        #expect(m.shownShares?.map(\.manager.id) == ["assistant", "m-3"]) // 工作 has none left
        #expect(m.state.value?.map(\.name) == ["甲"]) // the default's rule with the same id stays
        #expect(toasts.map(\.message) == ["已移除“乙”的规则"])
    }

    @Test func aRuleThatFailsToGoComesBackUnderItsManager() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies", mine: "",
                           work: #"{"id":"p9","source":"mail","tag":"sender","value":"b@example.com","name":"乙","tier":"noise"}"#)
        hub.on("DELETE", "/api/v1/inbox/policies/p9") { _ in throw APIError(status: 400, message: "坏了") }
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, roster: { Self.roster }))
        await m.load()
        await m.remove(try #require(m.shares?[1].items.first))
        #expect(m.shownShares?.map(\.manager.id) == ["m-2", "m-3"])
        #expect(m.shares?[1].items.map(\.id) == ["p9"])
    }

    @Test func aRuleThatFailsToGoLeavesWhatChangedMeanwhile() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies",
                           mine: #"{"id":"p1","source":"mail","tag":"sender","value":"a@example.com","name":"甲","tier":"action"}"#,
                           work: #"{"id":"p9","source":"mail","tag":"sender","value":"b@example.com","name":"乙","tier":"noise"}"#)
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { _ in Data(#"{"ok":true}"#.utf8) }
        hub.on("DELETE", "/api/v1/inbox/policies/p9") { _ in throw APIError(status: 400, message: "坏了") }
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, roster: { Self.roster }))
        await m.load()
        let mine = try #require(m.shares?[0].items.first), work = try #require(m.shares?[1].items.first)
        async let failing: Void = m.remove(work)
        async let going: Void = m.remove(mine)
        _ = await (failing, going)
        #expect(m.state.value?.map(\.id) == ["p9"], "the default's rule, gone meanwhile, doesn't come back")
        #expect(m.shownShares?.map(\.manager.id) == ["m-2", "m-3"])
    }

    @Test func theDefaultsRulesGoToItAfterAnotherBecameTheDefault() async throws {
        let hub = Self.hub("/api/v1/inbox/policies", "policies",
                           mine: #"{"id":"p1","source":"mail","tag":"sender","value":"a@example.com","name":"甲","tier":"action"}"#, work: "")
        var named: [String?] = []
        hub.on("DELETE", "/api/v1/inbox/policies/p1") { req in
            named.append(req.headers["X-Persona-Manager"])
            return Data(#"{"ok":true}"#.utf8)
        }
        var roster = Self.roster
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, roster: { roster }))
        await m.load()
        let before = m.rosterKey
        roster = ManagerRoster(managers: Self.roster.managers, defaultID: "m-2") // made the default on another device
        #expect(m.rosterKey != before, "the list is read again")
        await m.remove(try #require(m.shares?[0].items.first))
        #expect(named == ["assistant"], "the manager it came from, named now that it isn't the default")
    }

    @Test func oneManagerReadsOneListAsBefore() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/inbox/policies", json: #"{"policies":[]}"#)
        let one = ManagerRoster(managers: [HubManager(id: "assistant", name: "助手", online: true, isDefault: true)], defaultID: "assistant")
        let m = InboxRulesModel(deps: .init(api: { hub.client }, toast: { _ in }, roster: { one }))
        await m.load()
        #expect(m.shares == nil && m.isEmpty)
        #expect(hub.sent("GET", "/api/v1/inbox/policies").map { $0.headers["X-Persona-Manager"] } == [nil])
    }

    @Test func eachManagersMutesAreReadUnderItsNameAndTakenBackOnIt() async throws {
        let hub = Self.hub("/api/v1/inbox/mutes", "mutes",
                           mine: #"{"id":"m1","source":"github","tag":"repo","value":"example-org/one","name":"example-org/one","created_at":"2026-11-04T01:00:00Z"}"#,
                           work: #"{"id":"m1","source":"github","tag":"repo","value":"example-org/two","name":"example-org/two","created_at":"2026-11-05T01:00:00Z"}"#)
        var named: [String?] = []
        hub.on("DELETE", "/api/v1/inbox/mutes/m1") { req in
            named.append(req.headers["X-Persona-Manager"])
            if req.headers["X-Persona-Manager"] == nil { throw APIError(status: 400, message: "坏了") }
            return Data(#"{"ok":true}"#.utf8)
        }
        var shown: [String] = []
        let m = InboxMutesModel(deps: .init(api: { hub.client }, roster: { Self.roster }, show: { shown.append("\($0)") }))
        await m.load()
        #expect(m.shares?.map { $0.items.map(\.name) } == [["example-org/one"], ["example-org/two"], []])
        #expect(m.shares?.map(\.failed) == [nil, nil, "离线中：它回来后这里会显示它的静音。"])
        #expect(m.state.value?.map(\.name) == ["example-org/two", "example-org/one"])

        await m.unmute(try #require(m.shares?[1].items.first))
        #expect(m.shownShares?.map(\.manager.id) == ["assistant", "m-3"])
        await m.unmute(try #require(m.shares?[0].items.first))
        #expect(named == ["m-2", nil])
        #expect(m.shares?[0].items.map(\.name) == ["example-org/one"]) // the default's failed: back
        #expect(m.shares?[1].items.isEmpty == true && shown.count == 1)
    }
}
