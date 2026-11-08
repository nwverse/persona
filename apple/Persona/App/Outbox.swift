import Foundation

// Messages written while the hub is out of reach (docs/apple/ios.md「连接状态」): they stay in their chat as pending bubbles
// with a clock (Sends when connected) and go out by themselves once the app is connected and caught up, in the order they
// were written. Kept on disk too, so quitting the app (or iOS ending it in the background) does not lose them.
// Sent bubbles live here too: they stay until the transcript's copy arrives (`settleSend`, `showSent`).

enum SendOutcome: Equatable {
    case sent, failed, waiting

    var state: PendingMessage.State {
        switch self {
        case .sent: .sent
        case .failed: .failed
        case .waiting: .waiting
        }
    }
}

extension AppModel {
    /// Messages still going out (written offline, or being sent).
    var outboxCount: Int {
        chats.values.reduce(0) { n, s in n + s.pending.count { $0.state == .waiting || $0.state == .sending } }
    }

    /// A message the hub already has, on screen as sent until its transcript copy arrives: a new Persona conversation's
    /// first one went with the start request, and the node shows no transcript until claude has written it (#511).
    /// `id`: the message id the hub sent it under (an inbox hand-off's client id), which its transcript copy carries.
    func showSent(_ key: SessionKey, text: String, images: [ImagePayload], id: UUID = UUID()) {
        let msg = PendingMessage(id: id, text: text, images: images)
        chat(key).pending.append(msg)
        settleSend(key, msg.id, .sent)
    }

    /// Records how a send went; a sent bubble stays until its transcript copy arrives (a minute at most).
    func settleSend(_ key: SessionKey, _ id: UUID, _ outcome: SendOutcome) {
        let store = chat(key)
        guard let i = store.pending.firstIndex(where: { $0.id == id }) else { return }
        store.pending[i].state = outcome.state
        saveOutbox()
        switch outcome {
        case .sent:
            Task {
                // Normally the transcript shows it within a second or two. Behind a long turn it may only be written
                // when that turn ends (Codex keeps its queue to itself): stay while the agent works, then go.
                try? await Task.sleep(for: .seconds(60))
                for _ in 0..<90 where store.pending.contains(where: { $0.id == id }) && session(key)?.status.isActive == true {
                    try? await Task.sleep(for: .seconds(10))
                }
                store.pending.removeAll { $0.id == id }
            }
        case .waiting where socket(key.group).map({ $0.state == .connected && !$0.stalled }) == true:
            // The socket looks fine but the request got no answer (a weak network): try again shortly.
            Task {
                try? await Task.sleep(for: .seconds(10))
                flushOutbox()
            }
        default: break
        }
    }

    /// Sends what waited for the network, group by group (docs/protocol/multi-hub.md): each group whose link is up sends its own,
    /// chat by chat and in order, while the others wait for theirs. A group out of reach neither keeps the others'
    /// messages back nor is asked (#4704: only the hub-level group's link was looked at, so another group's messages
    /// stayed put while that one was down, and one group's dead requests held up the rest). A chat that is not open
    /// first fetches its latest messages: one that did arrive before the link broke is in the transcript and drops
    /// out (ChatStore.apply).
    func flushOutbox() {
        let waiting = chats.filter { $0.value.pending.contains { $0.state == .waiting } }.map(\.key)
        let batches = OutboxFlush.batches(waiting, ready: { self.isLive($0) }, busy: flushingOutbox)
        for (group, keys) in batches {
            flushingOutbox.insert(group)
            Task {
                defer { flushingOutbox.remove(group) }
                for key in keys {
                    let store = chat(key)
                    if !isSubscribed(key) {
                        if store.loadedOnce { await fill(key) } else { await loadInitial(key) }
                    }
                    for msg in store.pending where msg.state == .waiting {
                        guard isLive(group), let i = store.pending.firstIndex(where: { $0.id == msg.id }) else { break }
                        store.pending[i].state = .sending
                        let outcome = await sendPending(key, msg)
                        settleSend(key, msg.id, outcome)
                        if outcome == .waiting { break } // keep the order: the rest go after it
                    }
                }
            }
        }
    }

    private static let outboxName = "outbox"

    func saveOutbox() {
        outboxWriter.run { [weak self] in // one write at a time, the latest last
            guard let self else { return }
            let list = self.chats.flatMap { key, store in
                store.pending.filter { $0.state != .sent }.map { OutboxEntry(key: key, message: $0) }
            }
            StateCache.save(Self.outboxName, list)
        }
    }

    /// At launch: what was still going out comes back as waiting (a resend carries the same id, so one that did
    /// arrive is not typed twice).
    func restoreOutbox() {
        for e in StateCache.load(Self.outboxName, as: [OutboxEntry].self) ?? [] {
            var msg = e.message
            if msg.state == .failed { // a failed one stays failed, the user decides; not forever
                if let at = msg.createdAt, at < Date().addingTimeInterval(-86400) { continue }
            } else {
                msg.state = .waiting
            }
            let store = chat(e.key)
            if !store.pending.contains(where: { $0.id == msg.id }) { store.pending.append(msg) }
        }
    }
}

private nonisolated struct OutboxEntry: Codable, Sendable {
    var key: SessionKey
    var message: PendingMessage
}

/// Which chats' waiting messages go out now (pure, for the tests).
nonisolated enum OutboxFlush {
    /// `keys` (chats with waiting messages) by group: only groups whose link is `ready` and that are not `busy` sending
    /// already. Within a group the chats keep a stable order.
    static func batches(_ keys: [SessionKey], ready: (String) -> Bool, busy: Set<String>) -> [String: [SessionKey]] {
        var out: [String: [SessionKey]] = [:]
        for key in keys where !busy.contains(key.group) && ready(key.group) { out[key.group, default: []].append(key) }
        return out.mapValues { $0.sorted { ($0.machineID, $0.sessionID) < ($1.machineID, $1.sessionID) } }
    }
}
