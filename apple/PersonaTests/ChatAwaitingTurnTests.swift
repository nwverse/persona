import Foundation
import Testing
@testable import Persona

/// The wait between a send and the hub saying the session works (#3853, the web's #3654): the turn counts as started
/// while your message is the newest thing and nothing answered it, the session still idle / done, for a minute.
@MainActor
struct ChatAwaitingTurnTests {
    private let now = Date(timeIntervalSince1970: 1_800_000_000)

    private func item(_ seq: Int, _ kind: ItemKind, role: String = "assistant", text: String? = nil, ts: Date? = nil) -> Item {
        Item(id: "\(seq)#0", seq: seq, ts: ts, role: role, kind: kind, text: text, toolUseID: nil, toolName: nil, toolInput: nil,
             isError: nil, image: nil)
    }

    private func blocks(_ items: [Item]) -> [ChatBlock] { ChatStore.rebuild(items, settled: nil).blocks }

    /// A finished exchange, then your new message, sent `ago` seconds before now.
    private func echoed(_ ago: TimeInterval) -> [ChatBlock] {
        blocks([item(1, .text, role: "user", text: "first", ts: now.addingTimeInterval(-600)),
                item(2, .text, text: "done", ts: now.addingTimeInterval(-590)),
                item(3, .text, role: "user", text: "next", ts: now.addingTimeInterval(-ago))])
    }

    private func sending(_ ago: TimeInterval, _ state: PendingMessage.State = .sent) -> [PendingMessage] {
        var p = PendingMessage(text: "next", images: [])
        p.state = state
        p.createdAt = now.addingTimeInterval(-ago)
        return [p]
    }

    @Test func aMessageNotEchoedYetIsStartingWhileTheSessionIsIdle() {
        let before = blocks([item(1, .text, role: "user", text: "first", ts: now.addingTimeInterval(-600)),
                             item(2, .text, text: "done", ts: now.addingTimeInterval(-590))])
        for status in [SessionStatus.idle, .done, .unknown] {
            #expect(ChatAwaitingTurn.of(pending: sending(2), last: before.last, status: status, failedSince: nil, now: now) == .sending)
        }
        // still sending counts too; waiting for the network or failed does not
        #expect(ChatAwaitingTurn.of(pending: sending(2, .sending), last: before.last, status: .idle, failedSince: nil, now: now) == .sending)
        #expect(ChatAwaitingTurn.of(pending: sending(2, .waiting), last: before.last, status: .idle, failedSince: nil, now: now) == .none)
        #expect(ChatAwaitingTurn.of(pending: sending(2, .failed), last: before.last, status: .idle, failedSince: nil, now: now) == .none)
    }

    @Test func anEchoedMessageWithNothingAfterItIsTheRunningTurn() throws {
        let b = echoed(3)
        guard case .turn(let t) = try #require(b.last) else { Issue.record("no turn"); return }
        let w = ChatAwaitingTurn.of(pending: [], last: b.last, status: .done, failedSince: nil, now: now)
        #expect(w == .echoed(id: t.id))
        #expect(w.turnID == t.id)
        // ChatLines then draws that turn's work line as live, before the hub says working.
        let lines = ChatLines(openWork: []).build(b, live: w.turnID)
        var live = false
        for line in lines { if case .work(_, _, true, _, _) = line.content { live = true } }
        #expect(live)
    }

    @Test func itEndsWithAnAnswerTheHubsStatusAFailureOrAMinute() {
        let b = echoed(3)
        // the hub's working / needs-you take over; offline is nothing at work
        for status in [SessionStatus.working, .blocked, .offline] {
            #expect(ChatAwaitingTurn.of(pending: sending(2), last: b.last, status: status, failedSince: nil, now: now) == .none)
            #expect(ChatAwaitingTurn.of(pending: [], last: b.last, status: status, failedSince: nil, now: now) == .none)
        }
        // something answered it
        let answered = blocks([item(1, .text, role: "user", text: "next", ts: now.addingTimeInterval(-3)),
                               item(2, .text, text: "on it", ts: now.addingTimeInterval(-1))])
        #expect(ChatAwaitingTurn.of(pending: [], last: answered.last, status: .idle, failedSince: nil, now: now) == .none)
        // a minute without the hub saying working
        #expect(ChatAwaitingTurn.of(pending: [], last: echoed(61).last, status: .idle, failedSince: nil, now: now) == .none)
        #expect(ChatAwaitingTurn.of(pending: sending(61), last: nil, status: .idle, failedSince: nil, now: now) == .none)
        // the hub reported this turn failed (after the message, or without a time); an older failure is the last turn's
        #expect(ChatAwaitingTurn.of(pending: [], last: b.last, status: .idle, failedSince: now.addingTimeInterval(-1), now: now) == .none)
        #expect(ChatAwaitingTurn.of(pending: [], last: b.last, status: .idle, failedSince: .distantFuture, now: now) == .none)
        #expect(ChatAwaitingTurn.of(pending: [], last: b.last, status: .idle, failedSince: now.addingTimeInterval(-300), now: now) != .none)
    }

    @Test func theDeadlineIsAMinuteAfterTheNewestMessage() {
        #expect(ChatAwaitingTurn.deadline(pending: sending(2), last: echoed(30).last) == now.addingTimeInterval(58))
        #expect(ChatAwaitingTurn.deadline(pending: [], last: echoed(30).last) == now.addingTimeInterval(30))
        let answered = blocks([item(1, .text, role: "user", text: "next", ts: now), item(2, .text, text: "on it", ts: now)])
        #expect(ChatAwaitingTurn.deadline(pending: [], last: answered.last) == nil)
    }

    @Test func aPendingMessageWithNoTimeStartsNoWait() {
        let before = blocks([item(1, .text, role: "user", text: "first", ts: now.addingTimeInterval(-600)),
                             item(2, .text, text: "done", ts: now.addingTimeInterval(-590))])
        var old = sending(0)
        old[0].createdAt = nil
        #expect(ChatAwaitingTurn.of(pending: old, last: before.last, status: .idle, failedSince: nil, now: now) == .none)
        #expect(ChatAwaitingTurn.deadline(pending: old, last: before.last) == nil)
        // next to a timed one, the timed one decides
        let both = old + sending(5)
        #expect(ChatAwaitingTurn.of(pending: both, last: before.last, status: .idle, failedSince: nil, now: now) == .sending)
        #expect(ChatAwaitingTurn.deadline(pending: both, last: before.last) == now.addingTimeInterval(-5 + ChatAwaitingTurn.window))
    }

    /// The hub's working is for a message still on its way when the session went to work after it was written (within
    /// a minute); a message sent while the agent was already at work joins the running turn, whose work line stays.
    private func forPending(_ pending: [PendingMessage], _ status: SessionStatus = .working, since: Date?,
                            newest: Date? = nil) -> Bool {
        ChatAwaitingTurn.workIsForPending(pending, status: status, stateSince: since, newestLine: newest)
    }

    @Test func workThatStartedAfterAPendingMessageIsThatMessages() {
        // sent 3 s ago, at work for 1 s: the agent took it in
        let working = now.addingTimeInterval(-1)
        #expect(forPending(sending(3), since: working))
        #expect(forPending(sending(3, .sending), .blocked, since: working))
        // at work since before it was sent: the turn running then goes on, the message joins it
        #expect(!forPending(sending(3), since: now.addingTimeInterval(-30)))
        // not at work, or no time for either
        for status in [SessionStatus.idle, .done, .unknown, .offline] {
            #expect(!forPending(sending(3), status, since: working))
        }
        #expect(!forPending(sending(3), since: nil))
        var untimed = sending(3)
        untimed[0].createdAt = nil
        #expect(!forPending(untimed, since: working))
        // waiting for the network or failed: not on its way; none at all: nothing to yield to
        #expect(!forPending(sending(3, .waiting), since: working))
        #expect(!forPending(sending(3, .failed), since: working))
        #expect(!forPending([], since: working))
        // work that began over a minute after it (a stuck bubble, the next turn started on the computer)
        #expect(!forPending(sending(70), since: working))
        // a stuck bubble next to a fresh one: the fresh one decides
        #expect(forPending(sending(600) + sending(3), since: working))
    }

    /// The hub's `stateSince` moves on every change of state (blocked and back, its own restart): work the transcript
    /// already shows after the message is the newest turn's, and its line stays lit.
    @Test func workTheTranscriptShowsAfterAPendingMessageKeepsItsLine() {
        // at work again for 1 s (approved after a block, or the hub restarted), the message sent 3 s ago
        let since = now.addingTimeInterval(-1)
        // the running turn wrote a step after the send: it is that turn's work
        #expect(!forPending(sending(3), since: since, newest: now.addingTimeInterval(-2)))
        // a bubble whose copy was never matched, its turn running in the transcript: not dark to its end
        #expect(!forPending(sending(30), since: now.addingTimeInterval(-28), newest: now))
        // the transcript stops before the send: the work is still the message's
        #expect(forPending(sending(3), since: since, newest: now.addingTimeInterval(-4)))
    }
}
