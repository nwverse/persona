import Foundation
import Testing
@testable import Persona
import PersonaWire

/// What a test's outbox did: what it sent, what it kept on disk, what it said was turned down, and how the next send
/// goes.
@MainActor private final class OutboxProbe {
    var hub: String? = "g"
    var sent: [TodoOutbox.Entry] = []
    var saved: [TodoOutbox.Entry] = []
    var refusals: [String] = []
    var answer: TodoOutbox.Sent = .done(created: nil)
    /// A create's new id, told as its row lands.
    var created: String?

    func outbox(loading: [TodoOutbox.Entry] = []) -> TodoOutbox {
        TodoOutbox(deps: .init(load: { loading }, save: { [weak self] in self?.saved = $0 }, hub: { [weak self] in self?.hub },
                               send: { [weak self] entry, landed in
                                   guard let self else { return .kept }
                                   sent.append(entry)
                                   if let created { landed(created) }
                                   return answer
                               },
                               refused: { [weak self] _, message in self?.refusals.append(message) }))
    }
}

/// The To-dos list's held writes (#10465, `TodoOutbox`): a write waits out its Undo window and then goes, Undo within
/// it sends nothing, leaving the foreground sends everything at once, a write kept on disk goes again at launch with its
/// op id, and what the hub turns down is dropped and said.
@MainActor
struct TodoOutboxTests {
    private let window: Duration = .milliseconds(200)

    @Test func aHeldWriteGoesWhenItsWindowEnds() async throws {
        let probe = OutboxProbe()
        let outbox = probe.outbox()
        let entry = try #require(outbox.add(.done(true), todo: "t1", manager: nil, hold: window))
        #expect(probe.sent.isEmpty, "nothing goes before the window ends")
        #expect(probe.saved.map(\.id) == [entry.id], "kept on disk from the tap")
        try await waitForOutboxTest { !probe.sent.isEmpty }
        #expect(probe.sent.map(\.id) == [entry.id] && probe.sent.first?.write == .done(true))
        try await waitForOutboxTest { outbox.entries.isEmpty }
        #expect(probe.saved.isEmpty)
    }

    @Test func undoWithinTheWindowSendsNothing() async throws {
        let probe = OutboxProbe()
        let outbox = probe.outbox()
        let entry = try #require(outbox.add(.delete, todo: "t1", manager: nil, hold: window))
        #expect(outbox.pending["t1"] == .gone, "the row goes at once")
        #expect(outbox.takeBack(entry.id))
        #expect(outbox.entries.isEmpty && outbox.pending.isEmpty)
        try await Task.sleep(for: .milliseconds(400)) // past the window: a proof that nothing went needs it to have passed
        #expect(probe.sent.isEmpty)
    }

    /// The scene leaves the foreground: every write goes now, its window or not.
    @Test func leavingTheForegroundSendsEveryWriteNow() async throws {
        let probe = OutboxProbe()
        let outbox = probe.outbox()
        outbox.add(.someday(true), todo: "t1", manager: nil, hold: .seconds(30))
        outbox.add(.done(true), todo: "t2", manager: nil, hold: .seconds(30))
        outbox.sendAllNow()
        try await waitForOutboxTest { probe.sent.count == 2 }
        #expect(Set(probe.sent.map(\.todo)) == ["t1", "t2"])
    }

    /// One that did not reach the hub waits, kept on disk; the next launch sends it again, with the same op id, once the
    /// hub answers a read.
    @Test func aWriteKeptOnDiskGoesAgainAtLaunchWithItsOpID() async throws {
        let first = OutboxProbe()
        first.answer = .kept
        let before = first.outbox()
        let entry = try #require(before.add(.done(true), todo: "t1", manager: "work", hold: .zero))
        try await waitForOutboxTest { before.entries.first?.waits == true }
        #expect(first.saved.first?.waits == true)
        // The app ends; the next launch reads what was kept.
        let next = OutboxProbe()
        let after = next.outbox(loading: first.saved)
        #expect(after.writes["t1"]?.id == entry.id, "the row is still drawn ticked")
        try await Task.sleep(for: .milliseconds(100))
        #expect(next.sent.isEmpty, "a write that waits for the hub is not sent on the timer")
        after.hubAnswered()
        try await waitForOutboxTest { after.entries.isEmpty }
        #expect(next.sent.map(\.id) == [entry.id] && next.sent.first?.manager == "work")
    }

    @Test func whatTheHubTurnsDownIsDroppedAndSaid() async throws {
        let probe = OutboxProbe()
        probe.answer = .refused("This to-do is closed")
        let outbox = probe.outbox()
        outbox.add(.someday(true), todo: "t1", manager: nil)
        try await waitForOutboxTest { !probe.refusals.isEmpty }
        #expect(outbox.entries.isEmpty && probe.refusals == ["This to-do is closed"])
    }

    /// Undo after the hub has it takes it back with its reverse; a delete the hub has cannot be.
    @Test func undoAfterTheHubHasItSendsTheReverse() async throws {
        let probe = OutboxProbe()
        let outbox = probe.outbox()
        let tick = try #require(outbox.add(.done(true), todo: "t1", manager: nil))
        try await waitForOutboxTest { outbox.entries.isEmpty }
        #expect(outbox.takeBack(tick.id))
        try await waitForOutboxTest { probe.sent.count == 2 }
        #expect(probe.sent.last?.write == .done(false) && probe.sent.last?.todo == "t1")
        let delete = try #require(outbox.add(.delete, todo: "t2", manager: nil))
        try await waitForOutboxTest { outbox.entries.isEmpty }
        #expect(!outbox.takeBack(delete.id))
    }

    /// A new to-do is listed until the hub names it; the hub's row takes its place in the same step, and Undo deletes
    /// the to-do the hub made.
    @Test func aNewToDoIsListedUntilTheHubNamesIt() async throws {
        let probe = OutboxProbe()
        probe.created = "t9"
        let outbox = probe.outbox()
        let id = TodoOutbox.newID()
        // Not sent yet: no hub for a moment.
        probe.hub = nil
        #expect(outbox.add(.create(title: "Renew passport", notes: nil), todo: id, manager: nil) == nil, "no hub, no write")
        probe.hub = "g"
        let entry = try #require(outbox.add(.create(title: "Renew passport", notes: nil), todo: id, manager: nil))
        guard case .added(let item)? = outbox.pending[id] else { Issue.record("not listed"); return }
        #expect(item.title == "Renew passport")
        try await waitForOutboxTest { outbox.entries.isEmpty }
        #expect(outbox.pending.isEmpty && outbox.writes.isEmpty)
        #expect(outbox.takeBack(entry.id))
        try await waitForOutboxTest { probe.sent.count == 2 }
        #expect(probe.sent.last?.write == .delete && probe.sent.last?.todo == "t9")
    }

    /// Only the shown hub's writes go, and are drawn.
    @Test func onlyTheShownHubsWritesGo() async throws {
        let probe = OutboxProbe()
        probe.hub = "other"
        let outbox = probe.outbox()
        outbox.add(.done(true), todo: "t1", manager: nil, hold: .seconds(30))
        probe.hub = "g"
        #expect(outbox.writes.isEmpty)
        outbox.sendAllNow()
        try await Task.sleep(for: .milliseconds(100))
        #expect(probe.sent.isEmpty && outbox.entries.count == 1)
    }

    /// Which answers drop a write and which keep it: a refusal is dropped, anything that may not have reached the hub is
    /// kept, and a to-do already gone is what a delete asked for.
    @Test func howASendWent() {
        let gone = APIError(status: 404, message: "todo not found", fields: ["code": TodoNotThere.goneCode])
        #expect(TodoOutbox.outcome(gone, of: .delete) == .done(created: nil))
        #expect(TodoOutbox.outcome(gone, of: .done(true)) == .done(created: nil))
        #expect(TodoOutbox.outcome(APIError(status: 409, message: "closed"), of: .someday(true)) == .refused("closed"))
        #expect(TodoOutbox.outcome(APIError(status: 0, message: "offline"), of: .done(true)) == .kept)
        #expect(TodoOutbox.outcome(APIError(status: 502, message: "relay"), of: .done(true)) == .kept)
        #expect(TodoOutbox.outcome(APIError(status: 429, message: "busy"), of: .delete) == .kept)
        let unknown = APIError(status: 202, message: "?", fields: ["code": OpReceipt.unknownCode])
        #expect(TodoOutbox.outcome(unknown, of: .create(title: "A", notes: nil)) == .kept)
        #expect(TodoOutbox.outcome(URLError(.notConnectedToInternet), of: .done(true)) == .kept)
    }
}

/// Waits (up to 5 s) for something the outbox does.
@MainActor
func waitForOutboxTest(_ condition: @MainActor () -> Bool) async throws {
    for _ in 0..<250 where !condition() { try await Task.sleep(for: .milliseconds(20)) }
    try #require(condition(), "it did not happen in time")
}
