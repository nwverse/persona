import Foundation
import SwiftUI
import Testing
@testable import Persona
import PersonaWire

/// The To-dos list's search (#10465; DESIGN_SYSTEM 3.4 "To-dos list", "List controls"): the titles and notes of every
/// to-do the device holds, open and closed; a title match keeps the row's line 2, a notes match says the sentence that
/// matched with its words marked as search in chat marks them; results carry no inline action; the model looks off the
/// main actor, a newer key taking over, a cleared field is the list again at once, and a push while it searches looks
/// through the to-dos it changed.
struct TodoSearchTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)
    private let words = TodoWords(locale: Locale(identifier: "en_US"))

    private func filed(_ id: String, _ section: String) -> TodoFiled {
        TodoFiled(id: id, manager: nil, section: section, closed: section == TodoListSection.done, rank: 0, inactive: false,
                  byView: true, quiet: false, partOf: nil, project: nil, asks: [])
    }

    /// The title first, so a row found by its name keeps its own line 2; else the sentence of the notes that holds the
    /// words, on one line, every match in it marked (UTF-16, as the chat search's hits); case, accents and width aside.
    @Test func aTitleThenTheSentenceOfTheNotes() throws {
        #expect(TodoSearch.hit(TodoItem(id: "a", title: "Plan the Kyoto trip", notes: "Kyoto in April"), words: "kyoto")
                == TodoSearchHit(), "found by its name")
        let item = TodoItem(id: "b", title: "Book a room", notes: "Two of us.\nRyokan near Gion, under ¥60k a night. Call first.")
        let found = try #require(TodoSearch.hit(item, words: "gion")?.found)
        #expect(found.text == "Ryokan near Gion, under ¥60k a night.")
        #expect(found.marks == [[12, 16]])
        #expect(TodoSearch.hit(item, words: "passport") == nil)
        // Accents and case aside; every match in the sentence, and only that sentence.
        let accents = try #require(TodoSearch.sentence(in: "Kyōto first, then kyoto again. Not here.", matching: "KYOTO"))
        #expect(accents.text == "Kyōto first, then kyoto again.")
        #expect(accents.marks == [[0, 5], [18, 23]])
        // Offsets in UTF-16, past a character outside the basic plane.
        let emoji = try #require(TodoSearch.sentence(in: "🎉 Party near Gion.", matching: "gion"))
        #expect(emoji.marks == [[14, 18]] && (emoji.text as NSString).substring(with: NSRange(location: 14, length: 4)) == "Gion")
        // Words that run over a sentence's end keep both sentences.
        let across = try #require(TodoSearch.sentence(in: "One. Two three. Four.", matching: "Two three. Four"))
        #expect(across.text == "Two three. Four.")
        #expect(TodoSearch.hits(["a": item, "b": TodoItem(id: "b", title: "Taxes")], words: "gion").keys.sorted() == ["a"])
        #expect(TodoSearch.hits(["a": item], words: "").isEmpty)
    }

    /// A result found by its notes says the sentence in place of its line 2, keeps its mark and says it to VoiceOver; one
    /// found by its title keeps its line; no result offers Mark Done, which the same row offers outside the search.
    @MainActor @Test func aResultSaysWhatMatchedAndOffersNoInlineAction() throws {
        let item = TodoItem(id: "a", title: "Book the dentist", notes: "Ask for the 9:00 slot. Bring the card.")
        let view = TodoSummary(phase: "ready", section: TodoListSection.justFinished, reason: "finished_seen",
                               line: TodoLine(key: TodoLineKey.finishedSeen), rank: 0)
        let row = filed("a", TodoListSection.justFinished)
        let plain = TodoRowContentLogic.content(row, item: item, view: view, now: now, words: words)
        #expect(plain.markDone && plain.found == nil, "Mark Done outside the search")
        let byTitle = TodoRowContentLogic.content(row, item: item, view: view, now: now, words: words,
                                                  search: TodoSearch.hit(item, words: "dentist"))
        #expect(!byTitle.markDone && byTitle.found == nil && byTitle.said == plain.said, "found by its name: its line kept")
        let hit = try #require(TodoSearch.hit(item, words: "card"))
        let byNotes = TodoRowContentLogic.content(row, item: item, view: view, now: now, words: words, search: hit)
        #expect(!byNotes.markDone && byNotes.found?.text == "Bring the card." && byNotes.mark == plain.mark)
        #expect(byNotes.spoken.contains("Bring the card.") && byNotes.spoken.hasPrefix("Book the dentist"), "\(byNotes.spoken)")
        // The words marked as search in chat marks them: the accent's soft ground, in the primary color; the rest
        // secondary. Cut a few characters before the first match once the sentence is long.
        let marked = TodoFoundText.marked(TodoFoundLine(text: "Bring the card.", marks: [[10, 14]]), lead: 8)
        let lit = marked.runs.filter { $0.backgroundColor != nil }.map { String(marked[$0.range].characters) }
        #expect(lit == ["card"])
        let long = TodoFoundText.marked(TodoFoundLine(text: "A long sentence before the words, then Gion.", marks: [[39, 43]]), lead: 8)
        #expect(String(long.characters).hasPrefix("…") && String(long.characters).hasSuffix("then Gion."))
    }

    /// 2,000 to-dos with notes looked through, as the list does off the main actor at each key, and the list laid out
    /// from what was found: timed here for the PR's numbers (a Debug build on the simulator), and bounded far
    /// above them so a busy machine never fails it.
    @Test func twoThousandToDosWithNotes() async {
        let topics = ["ryokan", "passport", "dentist", "newsletter", "garden gate", "insurance", "photos", "widget"]
        var items: [String: TodoItem] = [:]
        var rows: [TodoFiled] = []
        for n in 0..<2_000 {
            let id = "t\(n)", topic = topics[n % topics.count]
            let notes = "Notes for \(topic) number \(n). Call them before Friday and keep the receipt. "
                + "Ask about the price, the dates and what is included; write it down here."
            items[id] = TodoItem(id: id, title: "To-do \(n) about \(topic)", notes: notes,
                                 doneAt: n % 5 == 0 ? now.addingTimeInterval(Double(-n * 600)) : nil)
            let section = n % 5 == 0 ? TodoListSection.done : [TodoListSection.todo, TodoListSection.working, TodoListSection.waiting][n % 3]
            rows.append(TodoFiled(id: id, manager: nil, section: section, closed: section == TodoListSection.done, rank: n,
                                  inactive: false, byView: true, quiet: false, partOf: nil, project: nil, asks: []))
        }
        let clock = ContinuousClock()
        var found: [String: TodoSearchHit] = [:]
        var times: [Duration] = []
        for query in ["receipt", "ryokan", "nothing like it"] { // every row's notes, an eighth's titles, none
            times.append(clock.measure { found = TodoSearch.hits(items, words: query) })
        }
        let start = clock.now
        let off = await TodoSearch.find(items, words: "receipt") // as the list runs it, off the main actor
        let offMain = clock.now - start
        #expect(off.count == 2_000)
        #expect(found.isEmpty)
        let hits = TodoSearch.hits(items, words: "receipt")
        #expect(hits.count == 2_000 && hits.values.allSatisfy { $0.found?.text.contains("receipt") == true })
        var input = TodosListLayout.Input(rows: rows, items: items, now: now, calendar: Calendar(identifier: .gregorian))
        input.query = "receipt"
        input.hits = hits
        var laid = TodosListLayout.Output.empty
        let layout = clock.measure { laid = TodosListLayout.layout(input) }
        #expect(laid.groups.flatMap(\.keys).count == 2_000)
        let ms = { (d: Duration) in String(format: "%.1f", Double(d.components.attoseconds) / 1e15 + Double(d.components.seconds) * 1000) }
        print("p13-timing: 2000 to-dos with notes: look through (all match notes) \(ms(times[0])) ms, (titles) \(ms(times[1])) ms, "
              + "(none) \(ms(times[2])) ms; off the main actor (all match notes) \(ms(offMain)) ms; lay out 2000 results \(ms(layout)) ms")
        #expect(times.allSatisfy { $0 < .seconds(5) } && offMain < .seconds(5) && layout < .seconds(5))
    }
}

/// The list's model while it searches, against a `FakeHub` through a store of its own.
@MainActor
struct TodoSearchModelTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func model(_ store: TodoStore, hub: FakeHub) -> TodosListModel {
        let now = now
        return TodosListModel(deps: TodosListDeps(store: store, outbox: TodosListModelTests.outbox(store, hub: hub), api: { hub.client },
                                                  now: { now }, calendar: { Calendar(identifier: .gregorian) }, projects: { [] },
                                                  toast: { _ in }, show: { _ in }, undoWindow: .seconds(60)))
    }

    private static let rows = [
        TodoViewJSON.row("n", title: "Pick the ryokan", view: TodoViewJSON.view(TodoListSection.needsYou, asks: ["a1"], line: "decision")),
        TodoViewJSON.row("n2", title: "Approve the plan", view: TodoViewJSON.view(TodoListSection.needsYou, rank: 1, asks: ["a2"], line: "decision")),
        TodoViewJSON.row("w", title: "Draft the newsletter", extra: #""notes":"Mention the ryokan trip. Keep it short.""#,
                         view: TodoViewJSON.view(TodoListSection.working)),
        TodoViewJSON.row("d", title: "Book the ryokan", extra: #""done_at":"2027-01-13T09:00:00Z""#,
                         view: TodoViewJSON.view(TodoListSection.done, phase: "done", line: "done")),
        TodoViewJSON.row("t", title: "Renew passport", view: TodoViewJSON.view(TodoListSection.todo, line: "rest")),
    ]

    private static func list(_ rows: [String] = rows) -> String {
        TodoViewJSON.list(rows, counts: TodoViewJSON.counts(needsYou: 2, inProgress: 1, open: 4,
            sections: [TodoListSection.needsYou: 2, TodoListSection.working: 1, TodoListSection.todo: 1, TodoListSection.done: 1]))
    }

    private static let places = #"{"places":[{"id":"all","kind":"all","title":"","needs_you":2}],"counts":{}}"#

    private func started(_ hub: FakeHub) async throws -> (TodoStore, TodosListModel) {
        hub.on("GET", "/api/v1/todos/places", json: Self.places)
        let store = TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}, v1: .on())
        let list = model(store, hub: hub)
        list.start()
        await store.refresh(api: hub.client)
        try await waitForSearchTest { list.ready && list.hasRows }
        return (store, list)
    }

    /// Open and closed matches in the list's sections, Needs you first and Done last; each heading counts what matches
    /// (Needs you's too, in a plain number), while N, the drawer's number, does not change; a notes match says its
    /// sentence; Done today is not folded away.
    @Test func closedToDosAreFoundAndTheHeadingsCountMatches() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        let n = list.needsYou
        #expect(n == 2 && !list.groups.flatMap(\.drawnKeys).contains("/d"), "the closed one is not in the list")
        list.query = "ryokan"
        try await waitForSearchTest { list.searchedWords == "ryokan" }
        #expect(list.groups.map(\.id) == [TodoListSection.needsYou, TodoListSection.working, "done:week"], "closed two days ago")
        #expect(list.groups.flatMap(\.drawnKeys) == ["/n", "/w", "/d"])
        #expect(list.groups.map(\.count) == [1, 1, 1], "what matches")
        #expect(list.needsYou == n && store.countsSource.needsYou == n, "N stays")
        #expect(list.row("/w")?.content.found?.text == "Mention the ryokan trip.", "the notes' sentence")
        #expect(list.row("/n")?.content.found == nil, "found by its name")
        list.query = ""
        #expect(!list.searching && list.groups.map(\.id).first == TodoListSection.needsYou && list.groups[0].keys == ["/n", "/n2"],
                "cleared: the list again at once")
        #expect(list.row("/w")?.content.found == nil, "its own line again")
    }

    /// Keys typed in a burst: each takes over from the look before it, so the list lands once, for the last words; what
    /// it showed stays until then, nothing being looked through on the main actor.
    @Test func keysInABurstLandOnceForTheLastWords() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        for typed in ["p", "pa", "pas", "pass"] { list.query = typed }
        #expect(!list.searching && list.searchedWords.isEmpty, "nothing yet: the look runs off the main actor")
        try await waitForSearchTest { list.searching }
        #expect(list.searchedWords == "pass" && list.groups.flatMap(\.drawnKeys) == ["/t"])
        list.query = "pass " // a space after the words: the same search
        #expect(list.searchedWords == "pass")
    }

    /// A push while the list searches looks through the to-dos it changed: one whose notes now hold the words joins the
    /// results, one that no longer holds them leaves.
    @Test func aPushWhileSearchingLooksThroughWhatChanged() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        list.query = "passport"
        try await waitForSearchTest { list.searchedWords == "passport" }
        #expect(list.groups.flatMap(\.drawnKeys) == ["/t"])
        var rows = Self.rows
        rows[1] = TodoViewJSON.row("n2", title: "Approve the plan", extra: #""notes":"Check the passport dates first.""#,
                                   view: TodoViewJSON.view(TodoListSection.needsYou, rank: 1, asks: ["a2"], line: "decision"))
        rows[4] = TodoViewJSON.row("t", title: "Renew the ID card", view: TodoViewJSON.view(TodoListSection.todo, line: "rest"))
        hub.on("GET", "/api/v1/todos", json: Self.list(rows))
        await store.refresh(api: hub.client)
        try await waitForSearchTest { list.groups.flatMap(\.drawnKeys) == ["/n2"] }
        #expect(list.row("/n2")?.content.found?.text == "Check the passport dates first.")
    }

    /// A push of more to-dos than the main actor looks through is looked through off it again; a space typed after the
    /// words meanwhile is the same search, so that look goes on and lands (cancelled there, the results stayed behind
    /// the store until the next push). The space is typed on every turn of the main actor, so it meets the look.
    @Test func aSpaceTypedWhileAPushIsLookedThroughLetsItLand() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list())
        let (store, list) = try await started(hub)
        defer { store.forget() }
        list.query = "passport"
        try await waitForSearchTest { list.searchedWords == "passport" }
        let more = (0..<80).map { n in
            TodoViewJSON.row("x\(n)", title: n == 79 ? "Passport photos" : "Errand \(n)", view: TodoViewJSON.view(TodoListSection.todo, line: "rest"))
        }
        hub.on("GET", "/api/v1/todos", json: Self.list(Self.rows + more))
        await store.refresh(api: hub.client)
        let found = { list.groups.flatMap(\.drawnKeys).contains("/x79") }
        let end = ContinuousClock.now + .seconds(5)
        var space = true
        while !found(), ContinuousClock.now < end {
            list.query = space ? "passport " : "passport"
            space.toggle()
            await Task.yield()
        }
        #expect(found() && list.searchedWords == "passport", "the push's match, in spite of the spaces")
        #expect(Set(list.groups.flatMap(\.drawnKeys)) == ["/t", "/x79"])
    }
}

/// Waits (up to 5 s) for something the search or the store does.
@MainActor
private func waitForSearchTest(_ condition: @MainActor () -> Bool) async throws {
    for _ in 0..<250 where !condition() { try await Task.sleep(for: .milliseconds(20)) }
    try #require(condition(), "it did not happen in time")
}
