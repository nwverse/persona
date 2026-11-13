import Foundation
import Testing
@testable import Persona

/// Finding a Persona chat by what was said in it (#6284): the manager's answer decoded, merged into the local matches,
/// the matched sentence emphasized, and the search asked once the keys settle, never again of a manager without it.
@MainActor
struct AssistantChatSearchTests {
    private func session(_ id: String, _ title: String, archived: Bool = false) throws -> Session {
        let o: [String: Any] = ["session_id": id, "machine_id": "server", "status": "idle", "title": title, "assistant": true, "archived": archived]
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
    }

    private static let answerJSON = """
    {"results": [
      {"session_id": "trip", "machine_id": "server", "title": "Trip", "hits": [
        {"item_id": "i1#0", "seq": 1, "role": "user", "ts": "2026-11-13T09:01:00Z", "snippet": "😀 我想去 BlueLane 看看", "ranges": [[7, 15]]}]},
      {"session_id": "old", "machine_id": "server", "title": "Old", "archived": true, "hits": [
        {"item_id": "i3#0", "seq": 3, "role": "assistant", "snippet": "旧的 BlueLane", "ranges": [[3, 11]]}]},
      {"session_id": "named", "machine_id": "server", "title": "BlueLane plan", "title_match": true, "hits": [
        {"item_id": "i9#0", "seq": 9, "role": "user", "snippet": "BlueLane", "ranges": [[0, 8]]}]},
      {"session_id": "gone", "machine_id": "server", "title": "Gone", "hits": []}
    ], "partial": true, "indexed": {"done": 3, "total": 5}}
    """

    @Test func decodesTheManagersAnswer() throws {
        let a = try JSON.decoder.decode(AssistantSearchAnswer.self, from: Data(Self.answerJSON.utf8))
        #expect(a.partial)
        #expect(a.results.map(\.sessionID) == ["trip", "old", "named", "gone"])
        #expect(a.results[0].hits[0].itemID == "i1#0" && a.results[0].hits[0].ranges == [[7, 15]])
        #expect(a.results[2].titleMatch == true && a.results[1].archived == true)
    }

    @Test func mergesTheManagersResultsFirstThenTheLocalMatches() throws {
        let a = try JSON.decoder.decode(AssistantSearchAnswer.self, from: Data(Self.answerJSON.utf8))
        let trip = try session("trip", "Trip"), old = try session("old", "Old", archived: true)
        let named = try session("named", "BlueLane plan"), local = try session("local", "BlueLane notes")
        let rows = AssistantSearchMerge.rows(local: [local, named], answer: a, known: [trip, named, local, old])
        // a result the app does not know (gone) is left out; a title match opens as usual; a local match follows
        #expect(rows.map(\.session.sessionID) == ["trip", "old", "named", "local"])
        #expect(rows.map(\.hit?.itemID) == ["i1#0", "i3#0", nil, nil])
        // before the manager answers: the local matches alone
        #expect(AssistantSearchMerge.rows(local: [local], answer: nil, known: [local]).map(\.hit) == [nil])
    }

    @Test func emphasizesTheMatchCountingUTF16() throws {
        let a = try JSON.decoder.decode(AssistantSearchAnswer.self, from: Data(Self.answerJSON.utf8))
        let text = AssistantSearchMerge.snippet(a.results[0].hits[0])
        let bold = text.runs.filter { $0.inlinePresentationIntent == .stronglyEmphasized }.map { String(text[$0.range].characters) }
        #expect(bold == ["BlueLane"])
        // ranges past the end or reversed are clipped, never crash
        let odd = AssistantSearchAnswer.Hit(itemID: "x", seq: 1, role: "user", snippet: "ab", ranges: [[1, 99], [5, 2], [0]])
        let oddBold = AssistantSearchMerge.snippet(odd).runs.filter { $0.inlinePresentationIntent == .stronglyEmphasized }
        #expect(oddBold.count == 1)
    }

    @Test func asksOnceTheKeysSettleAndNeverAgainOfAManagerWithoutIt() async throws {
        let calls = Calls()
        let client = APIClient(config: ServerConfig(url: URL(string: "https://fake.invalid")!, token: "t"), transport: { req in
            await calls.add(req.path + "?" + (req.query["q"] ?? ""))
            if await calls.missing { throw APIError(status: 404, message: "not found") }
            return Data(Self.answerJSON.utf8)
        })
        let search = AssistantChatSearch()
        search.search("Blue", api: client, on: true)
        search.search("BlueLane", api: client, on: true) // the one before is cancelled
        try await Task.sleep(for: .milliseconds(600))
        #expect(await calls.list == ["/api/v1/assistant/search?BlueLane"])
        #expect(search.answer(for: " BlueLane ")?.results.count == 4)
        #expect(search.answer(for: "Blue") == nil)

        // off (the hub does not list the feature) or blank: nothing asked, the answer forgotten
        search.search("BlueLane", api: client, on: false)
        #expect(search.answer(for: "BlueLane") == nil)

        await calls.setMissing()
        search.search("trip", api: client, on: true)
        try await Task.sleep(for: .milliseconds(600))
        search.search("again", api: client, on: true)
        try await Task.sleep(for: .milliseconds(600))
        #expect(await calls.list == ["/api/v1/assistant/search?BlueLane", "/api/v1/assistant/search?trip"])
        #expect(search.answer(for: "again") == nil)
    }
}

private actor Calls {
    var list: [String] = []
    var missing = false
    func add(_ s: String) { list.append(s) }
    func setMissing() { missing = true }
}
