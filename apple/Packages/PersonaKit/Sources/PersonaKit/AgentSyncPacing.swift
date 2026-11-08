import Foundation

/// How often a working agent's chat is pulled in the background (`AgentSync`, docs/apple/ios.md「后台同步」): at most once
/// every `workingGap`. Its transcript grows every second or two, and each pull is a request, a decode of the chat's
/// whole disk cache and a write of it again. The moment its turn ends or it needs the user (when it is likely to be
/// opened) it is pulled at once. Opening it brings the rest over its own subscription.
public enum AgentSyncPacing {
    public static let workingGap: TimeInterval = 60

    /// Unseen recent chats can warm their disk cache. A retained store waits for its next visible subscription,
    /// rather than publishing hidden transcript updates or advancing the sequence that subscription will replay.
    public static func prefetchAllowed(hasRetainedStore: Bool, isSubscribed: Bool) -> Bool {
        !hasRetainedStore && !isSubscribed
    }

    /// When a chat with news is pulled: now (nil), or once `workingGap` has passed since its last pull while its
    /// agent is still working.
    public static func heldUntil(working: Bool, lastPull: Date?, now: Date) -> Date? {
        guard working, let lastPull, now.timeIntervalSince(lastPull) < workingGap else { return nil }
        return lastPull.addingTimeInterval(workingGap)
    }

    /// A chat on screen follows its session over its own subscription. When that stream stops (a node whose transcript
    /// watch ended, a lost push), nothing else brings the new lines until the chat is opened again. The session list
    /// keeps coming and says the transcript grew: its `rev` moved after the chat's last items came, or its
    /// `updated_at` is newer than the chat's newest line. Then, once no items came for `staleQuiet`, the chat asks for
    /// what came after it, at most once every `stalePullEvery`, and not twice for the same `updated_at` (a write that
    /// added no line). The web's rule and numbers (`web/src/lib/stale-page.ts`).
    ///
    /// The rev is what catches the last line of a burst whose push was lost (#1179): its `updated_at` is within
    /// `staleSlack` of the line before, which the chat holds. The node reports revs on its next tick, mostly after the
    /// items: a rev that moved for lines that did come costs one empty pull once the chat is quiet (its arrival ends
    /// the question).
    public static let staleQuiet: TimeInterval = 4
    public static let stalePullEvery: TimeInterval = 10
    /// `updated_at` is the transcript's last write and a line's `ts` its own clock: closer than this is the same write.
    public static let staleSlack: TimeInterval = 1

    /// When a chat on screen asks for what its subscription may have lost: nil when it is not behind (or already asked
    /// about this `updatedAt`, or nothing came for it yet: its backlog is still on its way), else the moment its quiet
    /// and pacing allow, now or later.
    /// - Parameters:
    ///   - updatedAt: the session's `updated_at` as the list has it now.
    ///   - newestLine: the time of the chat's newest line that has one (nil: none has).
    ///   - revMoved: when this device last heard the session's `rev` change (nil: never, or the list has none).
    ///   - lastArrival: when items last came for the chat (a push, a page, a pull, streamed words); nil: none yet.
    ///   - lastPull: when the chat last asked this way.
    ///   - pulledFor: the `updatedAt` it last asked about.
    public static func catchUpAt(updatedAt: Date?, newestLine: Date?, revMoved: Date? = nil, lastArrival: Date?,
                                 lastPull: Date?, pulledFor: Date?) -> Date? {
        guard let lastArrival else { return nil }
        let revBehind = revMoved.map { $0 > lastArrival } ?? false
        var timeBehind = false
        if let updatedAt, updatedAt != pulledFor {
            timeBehind = newestLine.map { updatedAt > $0.addingTimeInterval(staleSlack) } ?? true
        }
        guard revBehind || timeBehind else { return nil }
        // Quiet since the rev moved too: its lines may still be on their way.
        let quiet = max(lastArrival, revMoved ?? lastArrival).addingTimeInterval(staleQuiet)
        let paced = lastPull?.addingTimeInterval(stalePullEvery) ?? .distantPast
        return max(quiet, paced)
    }
}
