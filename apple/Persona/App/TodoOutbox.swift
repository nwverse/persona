import Foundation
import Observation
import PersonaWire
#if os(iOS)
import UIKit
#endif

/// The To-dos list's writes that wait on this device (#10465; docs/apple/ios.md "To-dos (V1, #10465)"): a tick, Someday
/// and a delete wait out their Undo window (`Theme.Motion.todoUndo`), so Undo sends nothing and a to-do deleted by
/// mistake is never gone from the hub; a reopen, a "Remove from Someday" and a new to-do go at once. Each write keeps its
/// op id (`X-Persona-Op-Id`) from the tap on, so one sent again after an answer that never came is done once where the
/// hub keeps receipts (a new to-do), and is the same write again elsewhere (closing a closed to-do, deleting a deleted
/// one).
///
/// - Kept on disk (StateCache) from the tap: a write the app was quit or ended with goes when the hub next answers.
/// - Sent when its window ends, every one at once when the scene leaves the foreground, and again after the hub answers
///   a read (`hubAnswered`) for one that could not reach it.
/// - A write the hub turns down is dropped and said; one that did not reach it waits for the hub's next answer.
/// - A write stays until the list read after it has landed: until the hub's view says the same, the list draws it (the
///   tick struck through, the row gone or in Someday, the new row) and the numbers count it (`pending`).
@MainActor @Observable
final class TodoOutbox {
    static let shared = TodoOutbox(deps: .live)

    nonisolated enum Write: Codable, Equatable, Sendable {
        /// `PATCH …/todos/{id}` with `done`: the tick, and a reopen.
        case done(Bool)
        /// `PUT` (true) or `DELETE` (false) `…/someday`, on no set day.
        case someday(Bool)
        /// `DELETE …/todos/{id}`.
        case delete
        /// `POST /api/v1/todos`, under the to-do's own id here (`newID`) until the hub answers with its own.
        case create(title: String, notes: String?)

        /// The write that takes this one back once the hub has it; none for a delete.
        func reverse() -> Write? {
            switch self {
            case .done(let done): .done(!done)
            case .someday(let on): .someday(!on)
            case .delete: nil
            case .create: .delete
            }
        }
    }

    nonisolated struct Entry: Codable, Equatable, Identifiable, Sendable {
        /// The op id: `X-Persona-Op-Id` on every send of it.
        var id: UUID
        /// The hub-level group it was made on: it goes to that hub, and is drawn only while that hub's pages are shown.
        var hub: String
        /// The to-do's id; a new one's own (`TodoOutbox.newID`) until the hub answers.
        var todo: String
        /// The to-do's assistant (its `manager` stamp; a new one's, the assistant it is made for). nil with one.
        var manager: String?
        var write: Write
        var made: Date
        /// When its Undo window ends: sent then.
        var due: Date
        /// It could not reach the hub: it goes again when the hub next answers, not on the timer.
        var waits = false
        /// A new to-do's id on the hub, once it answered: the list has its own row for it then.
        var created: String?

        /// A new to-do as the list draws it until the hub has it.
        var newItem: TodoItem? {
            guard case .create(let title, let notes) = write, created == nil else { return nil }
            return TodoItem(id: todo, title: title, notes: notes, createdAt: made, updatedAt: made, manager: manager)
        }
    }

    /// How a send went.
    nonisolated enum Sent: Equatable, Sendable {
        /// The hub has it (a new to-do: under this id).
        case done(created: String?)
        /// It did not reach the hub, or nobody knows whether it did: kept, to go again with the same op id.
        case kept
        /// The hub turned it down: dropped, and said.
        case refused(String)
    }

    struct Deps {
        var load: @MainActor () -> [Entry]
        var save: @MainActor ([Entry]) -> Void
        /// The hub-level group shown now.
        var hub: @MainActor () -> String?
        /// Sends it; a create tells its new id (`landed`) as the store's own row for it lands.
        var send: @MainActor (_ entry: Entry, _ landed: @escaping @MainActor (String) -> Void) async -> Sent
        /// A write the hub turned down: said where the owner is.
        var refused: @MainActor (Entry, String) -> Void
        var now: @MainActor () -> Date = { Date() }
        /// The app's own: it sends every write as the scene leaves the foreground.
        var followsScene = false

        static var live: Deps {
            Deps(load: { StateCache.load(TodoOutbox.stateName, as: [Entry].self) ?? [] },
                 save: { StateCache.save(TodoOutbox.stateName, $0) },
                 hub: { AppModel.shared.hub?.group },
                 send: { entry, landed in await TodoStore.shared.send(entry, landed: landed) },
                 refused: { _, message in
                     Haptics.error()
                     AppModel.shared.toast = Toast(message, style: .error)
                 },
                 followsScene: true)
        }
    }

    /// Every write still here, each hub's, in the order they were made.
    private(set) var entries: [Entry] = []
    @ObservationIgnored private let deps: Deps
    /// Being sent now: never sent twice at once, and not taken back by a removal.
    @ObservationIgnored private var sending: Set<UUID> = []
    /// Taken back while being sent: once the hub has it, its reverse goes.
    @ObservationIgnored private var takenBack: Set<UUID> = []
    /// The writes the hub took lately, with the new to-do's id for a create: an Undo that comes after the send takes
    /// them back with their reverse.
    @ObservationIgnored private var landed: [UUID: (entry: Entry, created: String?)] = [:]
    @ObservationIgnored private var timer: Task<Void, Never>?
    @ObservationIgnored private var timedFor: Date?
    @ObservationIgnored private var observers: [NSObjectProtocol] = []
    static let stateName = "todo-outbox"
    /// A write older than this (its hub never came back) is let go.
    static let keepFor: TimeInterval = 7 * 24 * 3600

    init(deps: Deps) {
        self.deps = deps
        let now = deps.now()
        entries = deps.load().filter { now.timeIntervalSince($0.made) < Self.keepFor }
        #if os(iOS)
        if deps.followsScene {
            let center = NotificationCenter.default
            for name in [UIApplication.didEnterBackgroundNotification, UIApplication.willTerminateNotification] {
                observers.append(center.addObserver(forName: name, object: nil, queue: .main) { @Sendable [weak self] _ in
                    MainActor.assumeIsolated { self?.sendAllNow() }
                })
            }
        }
        #endif
        armTimer()
    }

    /// The id a new to-do has on this device until the hub names it.
    static func newID() -> String { "new-" + UUID().uuidString.lowercased() }

    // MARK: What the list draws

    /// The shown hub's writes, the latest for each to-do; a new one's only until the hub's row for it is in.
    var writes: [String: Entry] {
        guard let hub = deps.hub() else { return [:] }
        var out: [String: Entry] = [:]
        for entry in entries where entry.hub == hub && entry.created == nil { out[entry.todo] = entry }
        return out
    }

    /// What the list's rows and every number show of the shown hub's writes (`TodoCountsSource`): a deleted to-do is
    /// gone, one put in Someday is there, a new one is listed.
    var pending: [String: TodoCountsSource.Pending] {
        guard let hub = deps.hub() else { return [:] }
        var out: [String: TodoCountsSource.Pending] = [:]
        for entry in entries where entry.hub == hub {
            switch entry.write {
            case .delete: out[entry.todo] = .gone
            case .someday(true) where out[entry.todo] != .gone: out[entry.todo] = .someday
            case .someday(false) where out[entry.todo] == .someday: out[entry.todo] = nil
            case .create: if let item = entry.newItem { out[entry.todo] = .added(item) }
            default: break
            }
        }
        return out
    }

    // MARK: Writing

    /// Holds `write` on to-do `todo` for `hold` (none: it goes now). nil without a hub.
    @discardableResult
    func add(_ write: Write, todo: String, manager: String?, hold: Duration = .zero) -> Entry? {
        guard let hub = deps.hub() else { return nil }
        let now = deps.now()
        let entry = Entry(id: UUID(), hub: hub, todo: todo, manager: manager, write: write, made: now,
                          due: now.addingTimeInterval(hold.seconds))
        entries.append(entry)
        deps.save(entries)
        if hold <= .zero { sendOne(entry) } else { armTimer() }
        return entry
    }

    /// Undo: a write still here is let go and nothing is sent; one being sent, or one the hub has, is taken back with
    /// its reverse (a reopen, Someday's removal, a new to-do's delete). false: it cannot be (a delete the hub has).
    @discardableResult
    func takeBack(_ id: UUID) -> Bool {
        if let i = entries.firstIndex(where: { $0.id == id }) {
            guard sending.contains(id) else {
                entries.remove(at: i)
                deps.save(entries)
                armTimer()
                return true
            }
            guard entries[i].write.reverse() != nil else { return false }
            takenBack.insert(id)
            return true
        }
        guard let (entry, created) = landed.removeValue(forKey: id), let back = entry.write.reverse() else { return false }
        add(back, todo: created ?? entry.todo, manager: entry.manager)
        return true
    }

    /// The hub answered a read: the writes whose window ended go, those that could not reach it again.
    func hubAnswered() {
        let now = deps.now()
        sendPicked { $0.due <= now }
    }

    /// The scene leaves the foreground: every write goes now, its window or not (iOS may end the app before it ends).
    func sendAllNow() {
        #if os(iOS)
        let app = UIApplication.shared
        var task = UIBackgroundTaskIdentifier.invalid
        task = app.beginBackgroundTask(withName: "todo-outbox") {
            app.endBackgroundTask(task)
            task = .invalid
        }
        sendPicked({ _ in true }) {
            if task != .invalid { app.endBackgroundTask(task) }
        }
        #else
        sendPicked { _ in true }
        #endif
    }

    /// Sends the shown hub's writes `which` picks, not those being sent; `finished` once they are answered.
    private func sendPicked(_ which: (Entry) -> Bool, finished: (@MainActor () -> Void)? = nil) {
        let hub = deps.hub()
        let picked = entries.filter { $0.hub == hub && !sending.contains($0.id) && which($0) }
        guard !picked.isEmpty else { finished?(); return }
        var left = picked.count
        for entry in picked {
            sendOne(entry) {
                left -= 1
                if left == 0 { finished?() }
            }
        }
    }

    private func sendOne(_ entry: Entry, answered: (@MainActor () -> Void)? = nil) {
        guard !sending.contains(entry.id) else { answered?(); return }
        sending.insert(entry.id)
        armTimer()
        Task { [weak self] in
            guard let self else { return }
            let sent = await deps.send(entry) { [weak self] created in self?.created(entry.id, as: created) }
            sending.remove(entry.id)
            finish(entry, sent)
            answered?()
        }
    }

    /// A new to-do the hub named: its own row takes the place of the one drawn for it.
    private func created(_ id: UUID, as todo: String) {
        guard let i = entries.firstIndex(where: { $0.id == id }) else { return }
        entries[i].created = todo
    }

    private func finish(_ entry: Entry, _ sent: Sent) {
        let back = takenBack.remove(entry.id) != nil
        switch sent {
        case .done(let answered):
            // A create's new id, as the answer says it or as its row landed (`landed`).
            let created = answered ?? entries.first { $0.id == entry.id }?.created
            entries.removeAll { $0.id == entry.id }
            deps.save(entries)
            if back, let reverse = entry.write.reverse() {
                add(reverse, todo: created ?? entry.todo, manager: entry.manager)
            } else {
                landed[entry.id] = (entry, created)
                if landed.count > 32, let oldest = landed.min(by: { $0.value.entry.made < $1.value.entry.made })?.key {
                    landed[oldest] = nil
                }
            }
        case .kept:
            guard let i = entries.firstIndex(where: { $0.id == entry.id }) else { return }
            if back {
                // Taken back before it reached the hub: nothing went, so nothing goes.
                entries.remove(at: i)
            } else {
                entries[i].waits = true
            }
            deps.save(entries)
        case .refused(let message):
            entries.removeAll { $0.id == entry.id }
            deps.save(entries)
            if !back { deps.refused(entry, message) }
        }
        armTimer()
    }

    /// One timer, to the end of the soonest window among the shown hub's writes that wait for it.
    private func armTimer() {
        let hub = deps.hub()
        let next = entries.filter { $0.hub == hub && !$0.waits && !sending.contains($0.id) }.map(\.due).min()
        guard next != timedFor else { return }
        timer?.cancel()
        timedFor = next
        guard let next else {
            timer = nil
            return
        }
        let wait = max(0, next.timeIntervalSince(deps.now()))
        timer = Task { [weak self] in
            try? await Task.sleep(for: .seconds(wait))
            guard !Task.isCancelled, let self else { return }
            timedFor = nil
            timer = nil
            let now = deps.now()
            sendPicked { !$0.waits && $0.due <= now }
            armTimer()
        }
    }

    // MARK: How a send went

    /// What an error from the hub means for a write: a refusal is dropped (said with its message), anything else is kept
    /// to go again. A to-do already gone is no refusal of a delete, nor of a write on it the delete made moot.
    nonisolated static func outcome(_ error: Error, of write: Write) -> Sent {
        if TodoNotThere.gone(error) { return .done(created: nil) }
        guard let api = error as? APIError, (400..<500).contains(api.status), ![408, 425, 429].contains(api.status) else {
            return .kept // offline, a timeout, the hub busy or restarting, or a repeat it cannot answer yet
        }
        return .refused(api.message)
    }
}

extension TodoStore {
    /// Sends one of the list's held writes (`TodoOutbox`) through the to-do's own assistant, with its op id, and reads the
    /// list again after it, so the views that show it have landed before the outbox lets it go.
    func send(_ entry: TodoOutbox.Entry, landed: @escaping @MainActor (String) -> Void = { _ in },
              api injected: APIClient? = nil) async -> TodoOutbox.Sent {
        do {
            let created = try await write(entry.write, todo: entry.todo, manager: entry.manager, opID: entry.id, landed: landed,
                                          api: injected)
            return .done(created: created)
        } catch {
            return TodoOutbox.outcome(error, of: entry.write)
        }
    }
}

private extension Duration {
    var seconds: TimeInterval {
        let (whole, atto) = components
        return TimeInterval(whole) + TimeInterval(atto) / 1e18
    }
}
