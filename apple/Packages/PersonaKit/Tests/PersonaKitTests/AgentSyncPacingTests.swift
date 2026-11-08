import Foundation
import Testing
@testable import PersonaKit

/// Chats not on screen are pulled at most once a minute while their agent works, and at once when its turn ends
/// (`AgentSyncPacing.heldUntil`): each pull decodes and rewrites the chat's whole disk cache.
struct AgentSyncPacingTests {
    let t0 = Date(timeIntervalSinceReferenceDate: 1000)

    @Test func retainedHiddenChatsWaitForVisibility() {
        #expect(!AgentSyncPacing.prefetchAllowed(hasRetainedStore: true, isSubscribed: false))
    }

    @Test func visibleSubscriptionOwnersDoNotGetDuplicatePrefetch() {
        #expect(!AgentSyncPacing.prefetchAllowed(hasRetainedStore: true, isSubscribed: true))
        #expect(!AgentSyncPacing.prefetchAllowed(hasRetainedStore: false, isSubscribed: true))
    }

    @Test func unseenRecentChatsStillWarmTheirDiskCache() {
        #expect(AgentSyncPacing.prefetchAllowed(hasRetainedStore: false, isSubscribed: false))
    }

    @Test func aWorkingAgentWaitsForItsTurn() {
        #expect(AgentSyncPacing.heldUntil(working: true, lastPull: nil, now: t0) == nil) // never pulled: now
        let held = AgentSyncPacing.heldUntil(working: true, lastPull: t0, now: t0.addingTimeInterval(10))
        #expect(held == t0.addingTimeInterval(AgentSyncPacing.workingGap))
        #expect(AgentSyncPacing.heldUntil(working: true, lastPull: t0, now: t0.addingTimeInterval(AgentSyncPacing.workingGap)) == nil)
    }

    @Test func aTurnEndingIsPulledAtOnce() {
        #expect(AgentSyncPacing.heldUntil(working: false, lastPull: t0, now: t0.addingTimeInterval(1)) == nil)
    }
}

/// A chat on screen whose subscription stopped bringing items asks for them (`AgentSyncPacing.catchUpAt`): once its
/// session is newer than its newest line and nothing came for `staleQuiet`; not while it is current, not twice for the
/// same `updated_at`, not more often than `stalePullEvery`.
struct ChatCatchUpPacingTests {
    /// The chat's newest line, and when items last came for it.
    let line = Date(timeIntervalSinceReferenceDate: 1000)
    /// The session's `updated_at`: the transcript grew since.
    var written: Date { line.addingTimeInterval(30) }

    func at(_ updated: Date?, newest: Date?, revMoved: Date? = nil, arrival: Date?, pull: Date? = nil, pulledFor: Date? = nil) -> Date? {
        AgentSyncPacing.catchUpAt(updatedAt: updated, newestLine: newest, revMoved: revMoved, lastArrival: arrival,
                                  lastPull: pull, pulledFor: pulledFor)
    }

    @Test func aChatBehindItsSessionAsksOnceTheStreamIsQuiet() {
        let quietEnds = line.addingTimeInterval(AgentSyncPacing.staleQuiet)
        #expect(at(written, newest: line, arrival: line) == quietEnds)
        // items came a moment ago (an empty batch, a push): it waits for the quiet from then
        let pushed = written.addingTimeInterval(1)
        #expect(at(written, newest: line, arrival: pushed) == pushed.addingTimeInterval(AgentSyncPacing.staleQuiet))
        // nothing in the chat has a time yet: behind all the same
        #expect(at(written, newest: nil, arrival: line) == quietEnds)
    }

    @Test func aCurrentChatDoesNotAsk() {
        // the newest line is that write (its own clock may be a moment behind the file's)
        #expect(at(line.addingTimeInterval(AgentSyncPacing.staleSlack), newest: line, arrival: line) == nil)
        #expect(at(line.addingTimeInterval(-60), newest: line, arrival: line) == nil)
        // no word from the list
        #expect(at(nil, newest: line, arrival: line) == nil)
        // nothing came for the chat yet (its backlog is on its way): not quiet, just early
        #expect(at(written, newest: line, arrival: nil) == nil)
    }

    @Test func itAsksOncePerWriteAndNotTooOften() {
        // asked about this write already: it added no line
        #expect(at(written, newest: line, arrival: line, pulledFor: written) == nil)
        // a newer write is asked about, but not within `stalePullEvery` of the last ask
        let asked = line.addingTimeInterval(20)
        #expect(at(written, newest: line, arrival: line, pull: asked, pulledFor: line.addingTimeInterval(10))
                == asked.addingTimeInterval(AgentSyncPacing.stalePullEvery))
    }

    /// The last line of a burst, written 0.3 s after the one before, whose push was lost (#1179): its `updated_at` is
    /// within the slack of the newest line here, but the rev moved after the last items came.
    @Test func aLostLastLineOfABurstIsAskedForByItsRev() {
        let next = line.addingTimeInterval(0.3)
        #expect(at(next, newest: line, arrival: line) == nil) // the time alone cannot tell
        let heard = line.addingTimeInterval(1.5) // the node's next tick
        #expect(at(next, newest: line, revMoved: heard, arrival: line) == heard.addingTimeInterval(AgentSyncPacing.staleQuiet))
        // without any `updated_at` too
        #expect(at(nil, newest: line, revMoved: heard, arrival: line) == heard.addingTimeInterval(AgentSyncPacing.staleQuiet))
        // paced like the rest
        let asked = line.addingTimeInterval(1)
        #expect(at(next, newest: line, revMoved: heard, arrival: line, pull: asked) == asked.addingTimeInterval(AgentSyncPacing.stalePullEvery))
    }

    @Test func aRevThatMovedBeforeTheLastItemsAsksNothing() {
        // its lines came after it (a pull that found nothing counts: items came for the chat)
        #expect(at(line, newest: line, revMoved: line.addingTimeInterval(-1), arrival: line) == nil)
        #expect(at(line, newest: line, revMoved: line, arrival: line) == nil)
        // the time rule still asks on its own
        #expect(at(written, newest: line, revMoved: line.addingTimeInterval(-1), arrival: line)
                == line.addingTimeInterval(AgentSyncPacing.staleQuiet))
    }
}
