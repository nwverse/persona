import Foundation
import Testing
@testable import Persona

@MainActor struct SessionListTests {
    private func row(_ n: Int, text: String = "") throws -> Session {
        try JSON.decoder.decode(Session.self, from: Data(#"{"machine_id":"demo","session_id":"s\#(n)","title":"Agent \#(n)","last_text":"\#(text)","state":"working","waiting":false}"#.utf8))
    }
    private func counts(_ total: Int = 8000) -> SessionListCounts {
        SessionListCounts(total: total, machine: ["demo": total], project: [:], group: [:], state: ["working": total], revision: "r1")
    }
    private func model(_ fetch: @escaping SessionListModel<Session>.Fetch) -> SessionListModel<Session> {
        SessionListModel(id: { $0.id }, removalID: { SessionListIdentity.runtime($0, group: "")?.path },
                         fetch: fetch, counts: { _ in counts() })
    }

    @Test func fetchedPagesDoNotBecomeUrgentOnlyInsertions() throws {
        let first = try [row(0), row(1)]
        let next = try first + [row(2), row(3)]
        #expect(AgentsFeed.appendsPage(next, after: first))
        #expect(!AgentsFeed.appendsPage(first, after: first))
        #expect(!AgentsFeed.appendsPage(try [row(2)] + first, after: first))
        #expect(!AgentsFeed.appendsPage(try [row(1), row(0), row(2)], after: first))
    }

    @Test func firstPageIsFiftyAndTraversalIsFrozen() async throws {
        var requests: [(String?, String?)] = []
        let list = model { q, cursor, since in
            #expect(q.parameters["limit"] == "50")
            #expect(q.parameters["summary"] == "1")
            requests.append((cursor, since))
            let offset = cursor.flatMap(Int.init) ?? 0
            let end = min(offset + 50, 8000)
            return SessionListPage(sessions: try (offset..<end).map { try row($0) }, remove: [], hasMore: end < 8000,
                                   nextCursor: end < 8000 ? String(end) : nil, revision: "r0", total: 8000)
        }
        await list.load()
        #expect(list.rows.count == 50)
        #expect(requests.count == 1)
        #expect(list.total == 8000)
        #expect(list.counts?.state["working"] == 8000)
        for _ in 1..<160 { await list.next() }
        #expect(list.rows.count == 8000)
        #expect(Set(list.rows.map(\.key)).count == 8000)
        #expect(requests.count == 160)
        #expect(!list.hasMore)
    }

    @Test func deltaUpdatesOnlyLoadedRowsAndProtectsFutureFrozenPages() async throws {
        let changed = try row(0, text: "changed")
        let later = try row(50, text: "newer than frozen")
        let list = model { _, cursor, since in
            if since != nil {
                return SessionListPage(sessions: [changed, later], remove: [#"["demo","s1"]"#], hasMore: false, nextCursor: nil, revision: "r1", total: 7999)
            }
            let start = cursor == nil ? 0 : 50
            return SessionListPage(sessions: try (start..<start + 50).map { try row($0) }, remove: [], hasMore: true, nextCursor: String(start + 50), revision: "r0", total: 8000)
        }
        await list.load()
        await list.synchronize()
        #expect(list.rows.count == 49)
        #expect(list.rows.first?.lastText == "changed")
        #expect(!list.rows.contains { $0.sessionID == "s50" })
        #expect(list.revision == "r1")
        await list.next()
        #expect(list.rows.count == 99)
        #expect(list.rows.last { $0.sessionID == "s50" }?.lastText == "newer than frozen")
        #expect(list.total == 7999)
    }

    @Test func incompleteDeltaDoesNotAdvanceOrPartiallyApply() async throws {
        var failing = true
        let list = model { _, cursor, since in
            if since != nil {
                #expect(since == "r0")
                if cursor != nil && failing { throw APIError(status: 503, message: "retry") }
                return SessionListPage(sessions: [try row(cursor == nil ? 0 : 1, text: "new")], remove: [],
                                       hasMore: cursor == nil, nextCursor: cursor == nil ? "delta" : nil, revision: "r1", total: 8000)
            }
            return SessionListPage(sessions: [try row(0), try row(1)], remove: [], hasMore: false, nextCursor: nil, revision: "r0", total: 8000)
        }
        await list.load()
        await list.synchronize()
        #expect(list.revision == "r0")
        #expect(list.rows.allSatisfy { $0.lastText == "" })
        failing = false
        await list.synchronize()
        #expect(list.revision == "r1")
        #expect(list.rows.allSatisfy { $0.lastText == "new" })
    }

    @Test func expiredRevisionGetsANewBaseline() async throws {
        var calls = 0
        let list = model { _, _, since in
            calls += 1
            if since == "r0" { throw APIError(status: 409, message: "expired", fields: ["code": "revision_expired"]) }
            return SessionListPage(sessions: [try row(calls)], remove: [], hasMore: false, nextCursor: nil,
                                   revision: calls == 1 ? "r0" : "r2", total: 8000)
        }
        await list.load()
        await list.synchronize()
        #expect(list.revision == "r2")
        #expect(list.rows.count == 1)
        #expect(calls <= 4)
    }

    @Test func queryChangeFencesLateAnswers() async throws {
        let list = model { q, _, _ in
            if q.text == "old" { try? await Task.sleep(for: .milliseconds(100)) }
            return SessionListPage(sessions: [try row(q.text == "old" ? 0 : 1)], remove: [], hasMore: false, nextCursor: nil, revision: "r0", total: 1)
        }
        let old = Task { await list.select(SessionListQuery(text: "old")) }
        await Task.yield()
        await list.select(SessionListQuery(text: "new"))
        await old.value
        #expect(list.rows.map(\.sessionID) == ["s1"])
        #expect(list.query.text == "new")
    }

    @Test func legacyEnvelopeKeepsFullListAndNoCountsRequest() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/sessions", json: #"{"sessions":[{"session_id":"a"},{"session_id":"b"}]}"#)
        var countCalls = 0
        let list = SessionListModel<Session>(id: { $0.id }, removalID: { $0 },
            fetch: { try await hub.client.sessionListPage($0, cursor: $1, since: $2) }, counts: { _ in countCalls += 1; return counts() })
        await list.load()
        await list.next()
        #expect(list.rows.count == 2)
        #expect(!list.paged && !list.hasMore)
        #expect(countCalls == 0)
        #expect(hub.sent("GET", "/api/v1/sessions").count == 1)
    }

    @Test func runtimeHTTPUsesOpaqueTokensAndAllFilters() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/sessions", json: #"{"sessions":[],"remove":[],"revision":"opaque","total":8000,"has_more":false}"#)
        let query = SessionListQuery(state: "working", machine: "demo", project: "/demo/project", group: "space", source: "runtime", provider: "codex", text: "search")
        _ = try await hub.client.sessionListPage(query, cursor: "a+/=", since: "revision+/=")
        let sent = try #require(hub.sent("GET", "/api/v1/sessions").first)
        #expect(sent.query["cursor"] == "a+/=")
        #expect(sent.query["since"] == "revision+/=")
        #expect(sent.query["machine_id"] == "demo")
        #expect(sent.query["project"] == "/demo/project")
        #expect(sent.query["group"] == "space")
        #expect(sent.query["state"] == "working")
        #expect(sent.query["q"] == "search")
    }

    @Test func personalListRebindsCredentialsAndHubButReusesAnUnchangedBaseline() throws {
        let hub = FakeHub()
        let model = PersonalAgentListModel()
        model.connect(hub.client)
        let first = try #require(model.page)
        model.connect(hub.client)
        #expect(model.page === first)
        let credential = APIClient(config: ServerConfig(url: hub.client.config.url, token: "changed-fixture-token"))
        model.connect(credential)
        let second = try #require(model.page)
        #expect(second !== first)
        model.connect(APIClient(config: credential.config, group: "other"))
        #expect(model.page !== second)
    }

    @Test func aRenameCannotTurnAnExistingDetailIntoNotFound() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/sessions") { request in
            #expect(request.query["limit"] == "50")
            #expect(request.query["summary"] == "0")
            let sessions = request.query["q"] == nil ? #"[{"machine_id":"demo","session_id":"s1","title":"renamed","queued":["full detail"]}]"# : "[]"
            return Data(#"{"sessions":\#(sessions),"has_more":false,"revision":"r1","total":1}"#.utf8)
        }
        let detail = try await hub.client.sessionListDetail(SessionKey(machineID: "demo", sessionID: "s1"), title: "old title")
        #expect(detail.title == "renamed")
        #expect(detail.queued == ["full detail"])
        #expect(hub.sent("GET", "/api/v1/sessions").count == 2)
    }

    @Test func aManualScrollCancelsAPendingRestore() async throws {
        let reader = ReaderAnchor()
        var restored = false
        reader.shown = { (0...100, false) }
        reader.reveal = { _, _ in restored = true }
        reader.row("demo/s0", top: 50)
        reader.willChange()
        reader.gone("demo/s0")
        reader.cancel()
        try await Task.sleep(for: .milliseconds(20))
        #expect(!restored)
    }

    @Test func summaryWaitingAndDetailsDoNotConflict() throws {
        let summary = try JSON.decoder.decode(Session.self, from: Data(#"{"machine_id":"demo","session_id":"s0","state":"idle","waiting":true,"background_count":2,"running_subagents":1,"running_workflows":1}"#.utf8))
        #expect(summary.isWaiting)
        #expect(summary.listBackgroundCount == 2)
        var full = try row(0)
        full.permissionMode = "plan"
        full.effort = "high"
        full.ultracode = true
        full.queued = ["next"]
        full.listWaiting = nil
        let merged = summary.preservingDetails(from: full)
        #expect(merged.permissionMode == "plan")
        // A list row has no ultracode: the mark on the agent's to-do stays (#12).
        #expect(merged.ultracode == true)
        #expect(merged.queued == ["next"])
        #expect(merged.state == .idle)
        #expect(merged.isWaiting)
        let shaped = AgentsFeed.shape([summary], searching: false).sessions[0]
        #expect(shaped.isWaiting)
    }

    @Test func negotiatedHintsAreNotRowRevisionsAndWatchIsUnchanged() throws {
        let query = LiveConnection.queryItems(EventCursor(), sessionList: true)
        #expect(query.first?.value?.contains("sessions_list") == true)
        #expect(LiveConnection.queryItems(EventCursor()).first?.value?.contains("sessions_list") == false)
        let ev = ServerEvent.decode(Data(#"{"type":"sessions_list","revision":"opaque"}"#.utf8))
        guard case .sessionsList(let revision)? = ev?.stamped("other") else { Issue.record("hint lost"); return }
        #expect(revision == "opaque")
        #expect(SessionListIdentity.runtime(#"["machine/with/slashes","session/with/slashes"]"#, group: "other") ==
                SessionKey(group: "other", machineID: "machine/with/slashes", sessionID: "session/with/slashes"))
    }
}
