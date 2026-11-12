#if os(iOS)
import Foundation
import WidgetKit

/// Writes the Home Screen widgets' snapshot (docs/apple/home-widgets.md「Data: the snapshot」): what the app shows, in
/// the few numbers and rows the widgets draw, into the App Group. The widget extension reads only that file: it never
/// reaches the hub and never reads the keychain.
///
/// - Coalesced: what the watch's summary follows (`AppModel.summariesChanged`) asks for a write; the write comes at most
///   every `pause`, built from the app's state as it is then (P7).
/// - Only on change: the bytes are compared without the time; the same content is not written again, except once a
///   `heartbeat` so the widgets do not call it stale while the app runs. Then only the kinds whose part changed reload
///   (`WidgetCenter` budgets reloads; the heartbeat runs only in the foreground, where reloads are not budgeted).
/// - Off the main thread: the file is written on a background task.
@MainActor
final class HomeWidgetWriter {
    static let shared = HomeWidgetWriter()
    static let pause: Duration = .seconds(5)
    static let heartbeat: TimeInterval = 20 * 60

    /// What to write: the app's state (`AppModel.homeSnapshot()`); DEBUG `-showHomeWidgetSample` writes the sample.
    var source: () -> HomeSnapshot? = { nil }
    /// The meeting being recorded, as the recording's Live Activity follows it (`RecordingActivity`); a change writes at
    /// once, so the Record widget turns into Stop as soon as the recording starts.
    var recording: HomeSnapshot.Recording? {
        didSet { if recording != oldValue { flush() } }
    }

    #if DEBUG
    /// `-showHomeWidgetSample YES`: write the gallery's made-up snapshot instead of the app's.
    private var sample = false

    func showSample() {
        sample = true
        flush()
    }
    #endif

    private var pending: Task<Void, Never>?
    private var written: (body: Data, parts: [HomeKind: Data], at: Date)?
    private let store: HomeSnapshotStore

    init(store: HomeSnapshotStore = .shared) {
        self.store = store
    }

    /// Asks for a write within `pause`.
    func schedule() {
        guard pending == nil else { return }
        pending = Task { [weak self] in
            try? await Task.sleep(for: Self.pause)
            guard !Task.isCancelled else { return }
            self?.pending = nil
            self?.write()
        }
    }

    /// Writes now (the app is going to the background, a recording started or stopped).
    func flush() {
        pending?.cancel()
        pending = nil
        write()
    }

    private func write(now: Date = .now) {
        #if DEBUG
        let made: HomeSnapshot? = sample ? HomeSamples.busy(zh: LanguageStore.shared.launched.hasPrefix("zh"), now: now) : source()
        #else
        let made = source()
        #endif
        guard var snapshot = made else { return }
        snapshot.recording = recording ?? snapshot.recording
        snapshot.at = 0
        guard let body = Self.encode(snapshot) else { return }
        if let written, written.body == body, now.timeIntervalSince(written.at) < Self.heartbeat { return }
        let parts = Self.parts(snapshot)
        let changed = HomeKind.allCases.filter { written?.parts[$0] != parts[$0] }
        let kinds = written?.body == body ? HomeKind.allCases : changed
        snapshot.at = now.timeIntervalSince1970
        guard let data = Self.encode(snapshot) else { return }
        self.written = (body, parts, now)
        let store = store
        Task.detached(priority: .utility) {
            do {
                try store.write(data)
            } catch {
                HomeSnapshotStore.log.error("widget snapshot not written: \(error.localizedDescription, privacy: .public)")
                return
            }
            await MainActor.run {
                for kind in kinds { WidgetCenter.shared.reloadTimelines(ofKind: kind.widgetKind) }
            }
        }
    }

    nonisolated static func encode(_ s: HomeSnapshot) -> Data? {
        let e = JSONEncoder()
        e.outputFormatting = .sortedKeys
        return try? e.encode(s)
    }

    /// Each kind's part of the snapshot, to tell which widgets changed. Pairing and the persona count for all.
    nonisolated static func parts(_ s: HomeSnapshot) -> [HomeKind: Data] {
        func data<T: Encodable>(_ value: T) -> Data { (try? JSONEncoder.sorted.encode(value)) ?? Data() }
        struct Common: Encodable { var paired: Bool; var look: HomeSnapshot.Look }
        let common = data(Common(paired: s.paired, look: s.look))
        let parts: [HomeKind: Data] = [
            .agents: data(s.agents) + data(s.machines.map { [$0.id, $0.name] }),
            .today: data(s.today),
            .todos: data(s.todos),
            .usage: data(s.usage),
            .computers: data(s.machines),
            .record: data(s.recording) + data(s.today),
            .health: data(s.health),
            .inbox: data(s.inbox),
        ]
        return parts.mapValues { common + $0 }
    }
}

private extension JSONEncoder {
    static var sorted: JSONEncoder {
        let e = JSONEncoder()
        e.outputFormatting = .sortedKeys
        return e
    }
}

/// `persona://widget/<kind>`: a tap on a widget opens the page it comes from.
enum HomeWidgetLink {
    static func kind(_ url: URL) -> HomeKind? {
        guard url.scheme == "persona", url.host() == "widget" else { return nil }
        return HomeKind(rawValue: url.lastPathComponent)
    }
}

extension AppModel {
    /// Where a widget's tap lands: Agents and the Inbox on their tabs, the Record widget on the recordings; the others
    /// on the app as it was (their pages come with their widgets).
    func openWidget(_ kind: HomeKind) {
        switch kind {
        case .agents: selectedTab = .chats
        case .inbox: selectedTab = .mail
        case .record: MeetingRecorder.openRecordings()
        default: break
        }
    }

    /// The widgets' snapshot of what the app shows now: the same rows and counts as the Agents tab and the Inbox
    /// (docs/apple/home-widgets.md「What each number counts」). Nil in Demo Mode: the widgets say to connect.
    func homeSnapshot() -> HomeSnapshot? {
        var s = HomeSnapshot()
        s.paired = isPaired && !isDemo
        guard s.paired else { return s }
        let look = fleetLook
        s.look = .init(name: persona.name, package: look.package)
        let sessions = scoped(allSessions)
        let byKey = Dictionary(sessions.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
        let names = Dictionary(machines.map { ($0.id, $0.name) }, uniquingKeysWith: { a, _ in a })
        let tasks = TasksStore.shared
        func place(_ key: SessionKey) -> TabBadges.Place {
            TabBadges.place(key, session: byKey[key], quiet: isQuiet(key), mutes: pinsStore(key.group)?.mutes)
        }
        s.agents = HomeAgents.build(
            needsYou: AgentsNeedsYou.rows(cards: scopedInteractions, waiting: AgentsNeedsYou.waiting(sessions)),
            sessions: sessions, place: place, session: { byKey[$0] }, machineName: { names[$0] },
            isTask: { tasks.task(of: $0) != nil })
        // Persona answering in one of its own conversations: the face thinks (`FleetPersona`'s `assistant`).
        s.agents.assistantAnswering = sessions.contains { $0.assistant && $0.state == .working }
        let loads = Dictionary(mergedFleet().stats?.loads.map { ($0.id, $0.percent) } ?? [], uniquingKeysWith: { a, _ in a })
        s.machines = machines.prefix(8).map { m in
            let rows = s.agents.rows.filter { $0.machine == m.id }
            return .init(id: m.id, name: m.name, online: m.online, working: rows.count { $0.state == .working },
                         needsYou: rows.count { $0.state == .needsYou }, load: loads[m.id], icon: m.icon)
        }
        s.inbox = HomeInbox.build(InboxStore.shared)
        return s
    }
}

/// The Agents widget's rows, as the Agents tab lists them: who waits for you first (oldest first, one row per session,
/// `AgentsNeedsYou`), then who works (longest first), then who finished and is unread (newest first); only what the
/// tab's badge counts (`TabBadges.place`: no muted agents, no quiet task's agents). Past `HomeSnapshot.Agents.cap` rows
/// are counted, not listed.
nonisolated enum HomeAgents {
    static func build(needsYou: [AgentsNeedsYou.Row], sessions: [Session], place: (SessionKey) -> TabBadges.Place,
                      session: (SessionKey) -> Session?, machineName: (String) -> String?,
                      isTask: (SessionKey) -> Bool) -> HomeSnapshot.Agents {
        var rows: [HomeSnapshot.AgentRow] = []
        for r in needsYou where place(r.key) == .agents {
            let s = session(r.key)
            rows.append(.init(id: r.key.machineID + "/" + r.key.sessionID, title: s?.displayTitle ?? r.cards.first?.title ?? "",
                              state: .needsYou, kind: isTask(r.key) ? .assistant : .coding, machine: r.key.machineID,
                              place: machineName(r.key.machineID), since: r.since?.timeIntervalSince1970,
                              ask: r.cards.first?.title ?? InteractionKind.screen.title))
        }
        let listed = Set(rows.map(\.id))
        func others(_ state: AgentState) -> [Session] {
            sessions.filter { !$0.assistant && $0.countedState == state && place($0.key) == .agents
                && !listed.contains($0.machineID + "/" + $0.sessionID) }
        }
        let working = others(.working).sorted { ($0.stateSince ?? .distantFuture) < ($1.stateSince ?? .distantFuture) }
        let done = others(.unread).sorted { ($0.stateSince ?? .distantPast) > ($1.stateSince ?? .distantPast) }
        for (list, state) in [(working, HomeSnapshot.AgentState.working), (done, .done)] {
            for s in list {
                rows.append(.init(id: s.machineID + "/" + s.sessionID, title: s.displayTitle, state: state,
                                  kind: isTask(s.key) ? .assistant : .coding, machine: s.machineID,
                                  place: machineName(s.machineID), since: (s.stateSince ?? s.updatedAt)?.timeIntervalSince1970,
                                  detail: state == .working ? s.activity : nil))
            }
        }
        var out = HomeSnapshot.Agents()
        out.rows = Array(rows.prefix(HomeSnapshot.Agents.cap))
        for row in rows.dropFirst(HomeSnapshot.Agents.cap) {
            switch row.state {
            case .needsYou: out.more.needsYou += 1
            case .working: out.more.working += 1
            case .done: out.more.done += 1
            }
        }
        return out
    }
}

/// The Inbox widget's numbers and its newest messages, as the Inbox counts them (`InboxStore.actionCount`, its badge).
enum HomeInbox {
    static let shown = 5

    static func build(_ store: InboxStore) -> HomeSnapshot.Inbox {
        let open = store.scopedItems.filter { !$0.isDone }.sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
        return .init(needsYou: store.actionCount, unread: store.unreadCount, latest: open.prefix(shown).map { item in
            .init(id: item.id, sender: item.sender ?? item.title, summary: item.summary ?? item.preview ?? item.title,
                  at: (item.updatedAt ?? item.startedAt ?? .now).timeIntervalSince1970, urgent: item.urgent == true,
                  needsYou: item.triage == .action)
        })
    }
}
#endif
