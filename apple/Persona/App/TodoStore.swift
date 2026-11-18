import Foundation
import Observation
import PersonaWire
#if canImport(UIKit)
import UIKit
#endif

/// Hub-wide todo list management (docs/assistant/todos.md, issue #401):
/// The hub's todos, cached locally in StateCache, refreshed on {"type":"todos"} WS event and (re)connect.
/// Supports 404 gracefully (older hubs without todos support: hides entrance, no error).
@MainActor @Observable
final class TodoStore {
    static let shared = TodoStore(managers: .live, followsScene: true)

    private(set) var todos: [TodoItem] = [] {
        didSet {
            byID = Self.index(todos)
            let q = QuietTasks(todos)
            if q != quiet { quiet = q } // read by the badges and the Mac's menu bar: told only when it changes
        }
    }
    private(set) var byID: [String: TodoItem] = [:]
    /// The quiet tasks and their agents (#1731), which stay off the island, notifications and badges.
    private(set) var quiet = QuietTasks()
    private(set) var loading = false
    /// false when GET /api/v1/todos returned 404 (old hub without todos)
    private(set) var isSupported = true
    private(set) var spacesSupported = false
    private(set) var changingSpaces = false
    var error: String?

    // MARK: The V1 list's data (#10465, docs/apple/ios.md "To-dos (V1, #10465)")

    /// Each row's view by to-do id, as the last read of the list had them; none from an older manager. Kept apart from
    /// `todos`: a change to a view alone (a run's latest line) redraws nothing that reads the facts, and tells the watch
    /// and the widgets nothing (`hubListChanged`).
    private(set) var views: [String: TodoSummary] = [:]
    /// The list's counts as the hub sent them; nil from a hub without them. Pages read `countsSource`.
    private(set) var counts: TodoCounts?
    /// The places (`GET /api/v1/todos/places`), read with the list while V1 is on and the hub lists `todo_places`.
    private(set) var places: TodoPlaces?
    /// Whether the views are known: kept from the last run, or a read of the list ended (answered or failed). Until
    /// then the V1 list draws nothing rather than filing its rows by the app's rules first and by the views a moment
    /// later.
    private(set) var viewsRead = false
    /// Every To-dos number the pages show (decision S2-23), counted while V1 is on.
    let countsSource: TodoCountsSource

    @ObservationIgnored private let reads = CoalescedRefresh()
    @ObservationIgnored private var mutationRevision = 0
    @ObservationIgnored private var activeReadRevision: Int?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var lastPushed: [String: TodoItem]
    @ObservationIgnored private let cacheWriter: (@MainActor ([TodoItem]) -> Void)?
    @ObservationIgnored private let hubListChanged: @MainActor () -> Void
    @ObservationIgnored private let managers: Managers
    @ObservationIgnored private let saver = Debouncer(.seconds(1))
    @ObservationIgnored private let v1: V1
    @ObservationIgnored private let viewSaver = Debouncer(.seconds(1))
    /// The moments the rows' views change by time alone (their `next_change_at`), soonest first, parsed once per
    /// change of the views (P4).
    @ObservationIgnored private var changes: [Date] = []
    @ObservationIgnored private var changeTimer: Task<Void, Never>?
    @ObservationIgnored private var timedFor: Date?
    /// The last moment the timer fired for: a read that still says it (the hub's clock behind this one) waits for a push.
    @ObservationIgnored private var firedFor = Date.distantPast
    /// The scene is in front: the timer runs only then (P9).
    @ObservationIgnored private var active = true
    /// The client the last read went through when a caller gave one (tests); the timer's read uses it too.
    @ObservationIgnored private var lastInjected: APIClient?
    @ObservationIgnored private var sceneObservers: [NSObjectProtocol] = []
    private static let stateName = "todo-items"
    static let viewStateName = "todo-view"

    /// What the V1 list needs besides the rows (#10465): whether V1 is on, the hub's to-do features, the sessions to
    /// file a row without a view, and the views' own cache entry, written only while V1 is on.
    struct V1 {
        var isOn: @MainActor () -> Bool
        var features: @MainActor () -> TodoFeatures
        var lookup: @MainActor () -> TodosLogic.SessionLookup?
        var loadCache: @MainActor () -> TodoViewCache?
        /// nil: take the entry away.
        var saveCache: (@MainActor (TodoViewCache?) -> Void)?
        /// How long after a row's `next_change_at` it is read again: the hub's clock may be a little behind this one.
        var changeSlack: Duration = .seconds(1)

        static var live: V1 {
            .init(isOn: { TodosV1.shared.isOn }, features: { AppModel.shared.todoFeatures },
                  lookup: { TodosLogic.makeSessionLookup(AppModel.shared.sessionsNow) },
                  loadCache: { StateCache.load(TodoStore.viewStateName, as: TodoViewCache.self) },
                  saveCache: { cache in
                      if let cache { StateCache.save(TodoStore.viewStateName, cache) } else { StateCache.remove(TodoStore.viewStateName) }
                  })
        }
    }

    /// Whose a to-do is on a hub with several managers (docs/assistant/managers.md "Pages"): the list is the hub's merged
    /// one, each to-do stamped with its `manager`; the pages list the current assistant's, a new one is made for it, and
    /// a change to one goes to its own manager.
    struct Managers {
        /// The current assistant's scope; nil with one manager (nothing is filtered or named).
        var scope: @MainActor () -> AssistantScope?
        /// The manager a request about a thing stamped with one names (`AppModel.itemManager`): none for the default's.
        var naming: @MainActor (String?) -> String?
        /// Whether the hub answered its roster in this run (`AppModel.rosterAsked`): before it has, a new to-do names
        /// no manager (`AppModel.namedAssistant`).
        var known: @MainActor () -> Bool = { true }
        /// The app's, asked each time: the store is first made while `AppModel.shared` restores what it saved.
        static var live: Managers {
            .init(scope: { AppModel.shared.assistantScope }, naming: { AppModel.shared.itemManager($0) },
                  known: { AppModel.shared.rosterAsked })
        }
        /// As `model` has them (the app's; a test's own model).
        static func of(_ model: AppModel) -> Managers {
            .init(scope: { [weak model] in model?.assistantScope }, naming: { [weak model] in model?.itemManager($0) },
                  known: { [weak model] in model?.rosterAsked == true })
        }
        /// One manager: a store of a test's own, which goes by no app's roster.
        static var one: Managers { .init(scope: { nil }, naming: { _ in nil }) }
    }

    /// Whose a new to-do is: the current assistant's, or the assistant's whose thing (an agent, a project's computer)
    /// it is made for, by that thing's `manager` stamp.
    enum Owner {
        case current
        case of(String?)
    }

    /// The manager a new to-do of `owner` is made for and stamped with; nil with one manager, and before the roster is
    /// read.
    private func manager(of owner: Owner) -> String? {
        guard managers.known(), let scope = managers.scope() else { return nil }
        switch owner {
        case .current: return scope.current
        case .of(let stamp): return scope.owner(stamp)
        }
    }

    /// The open to-dos a thing stamped `manager` (an agent, a project's computer) can be linked to: its own
    /// assistant's, whichever is current (docs/assistant/managers.md "Pages"). All of them with one manager.
    func openTodos(of manager: String?) -> [TodoItem] {
        guard let scope = managers.scope() else { return todos.filter { !$0.isDone } }
        let owner = scope.owner(manager)
        return todos.filter { !$0.isDone && scope.owner($0.manager) == owner }
    }

    /// Reads the list unless it is still the one tagged (`APIClient.todos(etag:)`); tests answer it themselves.
    typealias Read = @MainActor (APIClient, String?) async throws -> TodosPage
    private let read: Read
    /// The last list read, its views and counts, and its tag: when the hub says it did not change (304), they are
    /// applied again, as a full read of them would have been, without reading them (#9590).
    private var lastRead: (etag: String, page: TodosPage)?

    /// `followsScene`: the app's own store, whose `next_change_at` timer stops while the scene is not in front.
    init(cachedTodos: [TodoItem]? = nil,
         cacheWriter: (@MainActor ([TodoItem]) -> Void)? = { StateCache.save(TodoStore.stateName, $0) },
         hubListChanged: @escaping @MainActor () -> Void = {
        AppModel.shared.summariesChanged() // its questions name their tasks
    }, managers: Managers = .one, read: @escaping Read = { try await $0.todos(etag: $1) }, v1: V1 = .live,
         followsScene: Bool = false) {
        self.read = read
        self.managers = managers
        self.v1 = v1
        countsSource = TodoCountsSource(scope: managers.scope, lookup: v1.lookup)
        let initial = cachedTodos ?? StateCache.load(Self.stateName, as: [TodoItem].self) ?? []
        let initialIndex = Self.index(initial)
        todos = initial
        byID = initialIndex
        quiet = QuietTasks(initial)
        lastPushed = initialIndex
        self.cacheWriter = cacheWriter
        self.hubListChanged = hubListChanged
        // The views kept with the rows they were read with: none for rows that are not there (another hub's).
        if v1.isOn(), !initial.isEmpty, let kept = v1.loadCache() {
            views = kept.views.filter { initialIndex[$0.key] != nil }
            counts = kept.counts
            places = kept.places
            changes = Self.changeMoments(views)
            viewsRead = true
        }
        #if canImport(UIKit)
        if followsScene {
            active = UIApplication.shared.applicationState == .active
            let center = NotificationCenter.default
            sceneObservers.append(center.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) {
                @Sendable [weak self] _ in MainActor.assumeIsolated { self?.sceneActive(true) }
            })
            sceneObservers.append(center.addObserver(forName: UIApplication.willResignActiveNotification, object: nil, queue: .main) {
                @Sendable [weak self] _ in MainActor.assumeIsolated { self?.sceneActive(false) }
            })
        }
        #endif
        // Counted once the app is made: the current assistant is the app model's, which may be making this store now.
        Task { @MainActor [weak self] in self?.recount() }
    }

    private static func index(_ list: [TodoItem]) -> [String: TodoItem] {
        var result: [String: TodoItem] = [:]
        for todo in list { result[todo.id] = todo }
        return result
    }

    /// The to-dos the pages list: the current assistant's, on a hub with several. `todos` stays whole: an agent's to-do
    /// and a quiet task are found whichever assistant's they are, and another assistant's opens from a link as itself.
    var scopedTodos: [TodoItem] {
        guard let scope = managers.scope() else { return todos }
        return todos.filter { scope.has($0.manager) }
    }
    var openTodos: [TodoItem] { scopedTodos.filter { !$0.isDone } }
    /// The To-dos badge (the drawer, the Mac's sidebar): the current assistant's open todos, quiet tasks left out (#1731,
    /// the main conversation tells about them). The list itself still has them.
    var badgeCount: Int { scopedTodos.count(where: { !$0.isDone && !$0.quiet }) }

    /// The manager a request about to-do `id` names: its own, as the list stamped it, whichever assistant is current
    /// (docs/protocol/protocol.md "Per-manager routing"); none for the default's, or one the list doesn't have.
    func named(_ id: String) -> String? { managers.naming(byID[id]?.manager) }

    /// The client a change to to-do `id` goes through: the one given (tests) or the hub's, naming the to-do's manager.
    private func client(for id: String, _ injected: APIClient?) -> APIClient? {
        (injected ?? AppModel.shared.api)?.manager(named(id))
    }

    /// An answer about one to-do comes from its manager without a stamp: it keeps the one the list gave it.
    private func stamped(_ answer: TodoItem, as manager: String?) -> TodoItem {
        guard answer.manager == nil, let manager else { return answer }
        var answer = answer
        answer.manager = manager
        return answer
    }

    func forget() {
        generation += 1
        lastRead = nil
        reads.reset()
        activeReadRevision = nil
        loading = false
        changingSpaces = false
        todos = []
        error = nil
        isSupported = true
        spacesSupported = false
        persist([])
        views = [:]
        counts = nil
        places = nil
        viewsRead = false
        changes = []
        firedFor = .distantPast
        armTimer()
        if v1.isOn() {
            countsSource.reset()
            // Through the debounce, so a write of the last hub's views still waiting cannot land after it.
            if let save = v1.saveCache { viewSaver.run { save(nil) } }
        }
    }

    /// Automatic invalidations share in-flight reads, without adding delay to unclassified todo/feed hints.
    /// The returned task covers this invalidation and at most one dirty follow-up, not an endless event pump.
    @discardableResult
    func reload(api: APIClient? = nil) -> Task<Void, Never> {
        reads.invalidate { [self] in await fetch(api: api) }
    }

    /// Manual refresh shares an eligible read, without starting a parallel GET or adding delay.
    func refresh(api: APIClient? = nil) async {
        let staleRead = activeReadRevision.map { $0 != mutationRevision } ?? false
        await reads.refresh(afterCurrent: staleRead) { [self] in await fetch(api: api) }
    }

    private func confirmWrite(api: APIClient) async {
        mutationRevision += 1
        await reads.refresh(afterCurrent: true) { [self] in await fetch(api: api) }
    }

    /// The hub's merged list, every manager's: read with none named, also after a change that named one.
    private func fetch(api injected: APIClient? = nil) async {
        if let injected { lastInjected = injected }
        guard let api = (injected ?? AppModel.shared.api)?.manager(nil) else { return }
        let requestedGeneration = generation
        let requestedRevision = mutationRevision
        activeReadRevision = requestedRevision
        loading = true
        defer {
            if generation == requestedGeneration {
                loading = false
                activeReadRevision = nil
            }
        }
        do {
            let answer = try await read(api, lastRead?.etag)
            guard generation == requestedGeneration, mutationRevision == requestedRevision, !Task.isCancelled else { return }
            let page: TodosPage
            if answer.list != nil {
                page = answer
                lastRead = answer.etag.map { ($0, answer) }
            } else if let kept = lastRead?.page {
                page = kept
            } else {
                return // a 304 to a tag this store did not send
            }
            let list = page.list ?? []
            isSupported = true
            spacesSupported = TodosLogic.supportsSpaces(list)
            apply(list, recounting: false)
            applyView(page.views, page.counts)
            recount()
            error = nil
            viewsRead = true
            // Facts only: a view's change alone is no news for the watch and the widgets.
            if byID != lastPushed {
                lastPushed = byID
                hubListChanged()
            }
            await readPlaces(api, listChanged: answer.list != nil, generation: requestedGeneration)
        } catch let apiErr where TodosLogic.isOldHub(apiErr) {
            guard generation == requestedGeneration, mutationRevision == requestedRevision, !Task.isCancelled else { return }
            // Older hub without todos: hide entrance without error
            isSupported = false
            spacesSupported = false
            error = nil
            viewsRead = true
        } catch {
            guard generation == requestedGeneration, mutationRevision == requestedRevision, !Task.isCancelled else { return }
            if let shown = error.shown { self.error = shown }
            viewsRead = true
        }
    }

    private func apply(_ list: [TodoItem], recounting: Bool = true) {
        guard list != todos else { return }
        todos = list
        persist(list)
        if recounting { recount() }
    }

    /// The views and counts of a read, each set only when it changed.
    private func applyView(_ newViews: [String: TodoSummary], _ newCounts: TodoCounts?) {
        var changed = false
        if newViews != views {
            views = newViews
            changes = Self.changeMoments(newViews)
            changed = true
        }
        if newCounts != counts {
            counts = newCounts
            changed = true
        }
        if changed { persistView() }
        armTimer()
    }

    /// The places, read with the list while V1 is on and the hub lists them: again only when the list changed (a 304
    /// changes none of its numbers). A hub or manager without the route ("not there") has none; any other failure keeps
    /// the last until the next read.
    private func readPlaces(_ api: APIClient, listChanged: Bool, generation requested: Int) async {
        guard v1.isOn(), v1.features().places, listChanged || places == nil else { return }
        do {
            let read = try await api.todoPlaces()
            guard generation == requested, !Task.isCancelled, read != places else { return }
            places = read
        } catch let error where TodoNotThere.matches(error) {
            guard generation == requested, places != nil else { return }
            places = nil
        } catch {
            return
        }
        persistView()
        recount()
    }

    private func persistView() {
        guard v1.isOn(), let save = v1.saveCache else { return }
        viewSaver.run { [weak self] in
            guard let self else { return }
            save(TodoViewCache(views: views, counts: counts, places: places))
        }
    }

    /// Counts again while V1 is on (nothing reads the numbers before).
    private func recount() {
        guard v1.isOn() else { return }
        countsSource.update(.init(rows: todos, views: views, counts: counts, places: places, now: Date()))
    }

    // MARK: Rows that change by time alone (decision S2-20)

    /// The `next_change_at` moments of `views`, soonest first.
    private static func changeMoments(_ views: [String: TodoSummary]) -> [Date] {
        views.values.compactMap { $0.nextChangeAt.flatMap(JSON.parseDate) }.sorted()
    }

    /// One timer, to the earliest moment a row's view changes by time alone, then the list is read again (coalesced
    /// with any other read): only while V1 is on and the scene is in front. A moment already passed (the scene was
    /// away) is read at once, once.
    private func armTimer() {
        let due = v1.isOn() && active ? changes.first { $0 > firedFor } : nil
        guard due != timedFor else { return }
        changeTimer?.cancel()
        timedFor = due
        guard let due else {
            changeTimer = nil
            return
        }
        let wait = Duration.seconds(max(0, due.timeIntervalSinceNow)) + v1.changeSlack
        changeTimer = Task { [weak self] in
            try? await Task.sleep(for: wait)
            guard !Task.isCancelled, let self else { return }
            firedFor = due
            timedFor = nil
            changeTimer = nil
            reload(api: lastInjected)
        }
    }

    /// The scene came to the front or left it.
    func sceneActive(_ active: Bool) {
        guard active != self.active else { return }
        self.active = active
        armTimer()
    }

    private func persist(_ list: [TodoItem]) {
        guard let cacheWriter else { return }
        saver.run { cacheWriter(list) }
    }

    func todo(id: String) -> TodoItem? {
        byID[id]
    }

    /// A new to-do is the current assistant's (or `owner`'s): its manager is named, and the answer stamped with it.
    func create(title: String, notes: String? = nil, due: APIClient.TodoPlanChange.Due? = nil, owner: Owner = .current,
                api injected: APIClient? = nil) async throws -> TodoItem {
        let made = manager(of: owner)
        guard let api = (injected ?? AppModel.shared.api)?.manager(managers.naming(made)) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        let created = try await stamped(api.createTodo(title: title, notes: notes, due: due), as: made)
        guard generation == targetGeneration else { return created }
        mutationRevision += 1
        var updated = todos
        updated.insert(created, at: 0)
        apply(updated)
        return created
    }

    func update(id: String, title: String? = nil, notes: String? = nil, done: Bool? = nil, api injected: APIClient? = nil) async throws -> TodoItem {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        let owner = byID[id]?.manager
        let res = try await stamped(api.updateTodo(id: id, title: title, notes: notes, done: done), as: owner)
        guard generation == targetGeneration else { return res }
        mutationRevision += 1
        var updated = todos
        if let idx = updated.firstIndex(where: { $0.id == id }) {
            updated[idx] = res
        } else {
            updated.insert(res, at: 0)
        }
        apply(updated)
        return res
    }

    /// Changes a todo's due date, reminder or priority (#1683).
    @discardableResult
    func updatePlan(id: String, _ change: APIClient.TodoPlanChange, api injected: APIClient? = nil) async throws -> TodoItem {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        let owner = byID[id]?.manager
        let res = try await stamped(api.updateTodoPlan(id: id, change), as: owner)
        guard generation == targetGeneration else { return res }
        mutationRevision += 1
        var updated = todos
        if let idx = updated.firstIndex(where: { $0.id == id }) {
            updated[idx] = res
        } else {
            updated.insert(res, at: 0)
        }
        apply(updated)
        return res
    }

    /// Moves a to-do to another due date (#2045: dragged on the calendar, or its "Change Due Date…"): drawn there at
    /// once, before the hub answers; a failed save puts it back and says why. The task ends with whether it was saved.
    @discardableResult
    func reschedule(_ id: String, to due: APIClient.TodoPlanChange.Due, api: APIClient? = nil) -> Task<Bool, Never> {
        guard let before = todo(id: id) else { return Task { false } }
        let targetGeneration = generation
        // Optimistic, as toggleDone: a read already in flight cannot replace it.
        mutationRevision += 1
        var updated = todos
        if let idx = updated.firstIndex(where: { $0.id == id }) {
            updated[idx] = TodoCalendar.moved(before, to: due)
            apply(updated)
        }
        return Task {
            do {
                try await updatePlan(id: id, APIClient.TodoPlanChange(due: due), api: api)
                return true
            } catch {
                // Back as it was: only this to-do, so what else changed meanwhile stays.
                guard generation == targetGeneration else { return false }
                mutationRevision += 1
                var back = todos
                if let idx = back.firstIndex(where: { $0.id == id }) {
                    back[idx] = before
                    apply(back)
                }
                // Felt as well as shown (offline has its own haptic, in `notDone`).
                if !error.isOffline { Haptics.error() }
                AppModel.shared.show(error)
                return false
            }
        }
    }

    /// `api` is the client to write through (tests pass a fake hub's); the app uses the current hub connection.
    /// Closes an open to-do or reopens a closed one. `done` says which when the row knows better than `doneAt`: a failed
    /// start waiting on the owner reads open and is closed for good (#3713, `TodoTriageLogic.checksAsOpen`).
    func toggleDone(_ todo: TodoItem, done: Bool? = nil, api: APIClient? = nil) {
        let targetGeneration = generation
        let newDone = done ?? !todo.isDone
        let original = todos
        // Optimistic update; a read already in flight cannot replace it.
        mutationRevision += 1
        var updated = todos
        if let idx = updated.firstIndex(where: { $0.id == todo.id }) {
            updated[idx].doneAt = newDone ? Date() : nil
            updated[idx].startError = nil // closed by the owner, or reopened: no longer failed (the hub does the same)
            apply(updated)
        }
        Task {
            do {
                _ = try await update(id: todo.id, done: newDone, api: api)
            } catch {
                // Revert on failure: `todos` already holds the optimistic list, so put back the one saved before it.
                guard generation == targetGeneration else { return }
                mutationRevision += 1
                apply(original)
                AppModel.shared.show(error)
            }
        }
    }

    /// Marks a to-do seen (Got It, or opening it: #5707, docs/assistant/todo-status-sop.md "Seen") or, with `seen` false,
    /// takes the mark off (Undo). The list moves it at once (principle 7); the hub's mark takes the place of the local
    /// one, and a failure puts back the mark it had and throws. `after`: the moment that put it in Just finished, so the
    /// mark shown before the hub answers is not earlier than it on a clock behind the hub's. Only the mark is taken from
    /// the answer: the list's own fields (its ask) are not in it.
    func setSeen(id: String, _ seen: Bool, after: Date = .distantPast, api injected: APIClient? = nil) async throws {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        let before = byID[id]?.seenAt
        markSeen(id, seen ? max(Date(), after) : nil)
        do {
            let res = try await api.setTodoSeen(id: id, seen: seen)
            guard generation == targetGeneration else { return }
            markSeen(id, res.seenAt)
        } catch {
            if generation == targetGeneration { markSeen(id, before) }
            throw error
        }
    }

    /// Sets to-do `id`'s seen mark in the list; a read already in flight cannot put the old one back.
    private func markSeen(_ id: String, _ at: Date?) {
        guard let idx = todos.firstIndex(where: { $0.id == id }) else { return }
        mutationRevision += 1
        var updated = todos
        updated[idx].seenAt = at
        apply(updated)
    }

    func delete(id: String, api injected: APIClient? = nil) async throws {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        try await api.deleteTodo(id: id)
        guard generation == targetGeneration else { return }
        mutationRevision += 1
        var updated = todos
        updated.removeAll { $0.id == id }
        apply(updated)
    }

    /// Someday on to-do `id`, back on `until` (nil: on no set day); `clearSomeday` brings it back now (#10226,
    /// docs/assistant/todos.md "Restores and Someday"). Waits for the hub; the row's view follows with the read that
    /// confirms it. Only for a hub that lists `todo_view` (an older one answers 404).
    func setSomeday(id: String, until: Date?, api injected: APIClient? = nil) async throws {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        try await confirmed(id, api: api) { try await $0.setSomeday(id: id, until: until) }
    }

    func clearSomeday(id: String, api injected: APIClient? = nil) async throws {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        try await confirmed(id, api: api) { try await $0.clearSomeday(id: id) }
    }

    /// Restore on an inactive to-do: the hub's, so every device sees it (the app's own rules keep theirs on the device,
    /// `TodoRestores`, for hubs without `todo_view`).
    func restore(id: String, api injected: APIClient? = nil) async throws {
        guard let api = client(for: id, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        try await confirmed(id, api: api) { try await $0.restore(id: id) }
    }

    /// A write whose answer is the to-do: it takes the row's place (keeping the list's stamp), and the list is read again
    /// for the view, which the answer does not carry.
    private func confirmed(_ id: String, api: APIClient, _ write: (APIClient) async throws -> TodoItem) async throws {
        let targetGeneration = generation
        let res = try await stamped(write(api), as: byID[id]?.manager)
        guard generation == targetGeneration else { return }
        mutationRevision += 1
        var updated = todos
        if let idx = updated.firstIndex(where: { $0.id == id }) { updated[idx] = res } else { updated.insert(res, at: 0) }
        apply(updated)
        await confirmWrite(api: api)
    }

    func linkAgent(todoID: String, machineID: String, sessionID: String, api injected: APIClient? = nil) async throws {
        guard let api = client(for: todoID, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        do {
            try await api.linkTodoAgent(todoID: todoID, machineID: machineID, sessionID: sessionID)
        } catch {
            if generation == targetGeneration {
                await confirmWrite(api: api)
            }
            throw error
        }
        if generation == targetGeneration {
            await confirmWrite(api: api)
        }
    }

    func unlinkAgent(todoID: String, machineID: String, sessionID: String, api injected: APIClient? = nil) async throws {
        guard let api = client(for: todoID, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        do {
            try await api.unlinkTodoAgent(todoID: todoID, machineID: machineID, sessionID: sessionID)
        } catch {
            if generation == targetGeneration {
                await confirmWrite(api: api)
            }
            throw error
        }
        if generation == targetGeneration {
            await confirmWrite(api: api)
        }
    }

    /// Membership changes immediately; the hub then supplies the authoritative Agent attachments.
    @discardableResult
    func setSpace(todoID: String, space: TodoItem.Space, linked: Bool, api injected: APIClient? = nil) async throws -> Bool {
        guard !changingSpaces else { throw APIError(status: 409, message: String(localized: "Updating projects. Try again in a moment.")) }
        guard let api = client(for: todoID, injected) else { throw APIError(status: 0, message: String(localized: "Not connected to a hub [Todo Store 04f822]")) }
        let targetGeneration = generation
        changingSpaces = true
        defer { if generation == targetGeneration { changingSpaces = false } }
        mutationRevision += 1
        let original = todos
        let via = "\(space.machineID)/\(space.space)"
        var optimistic = todos
        for index in optimistic.indices {
            guard linked || optimistic[index].id == todoID else { continue }
            optimistic[index].spaces?.removeAll { $0.machineID == space.machineID && $0.space == space.space }
            if !linked || optimistic[index].id != todoID {
                optimistic[index].agents.removeAll { $0.source == "space" && $0.via == via }
            }
            if linked && optimistic[index].id == todoID {
                optimistic[index].spaces = (optimistic[index].spaces ?? []) + [space]
            }
        }
        apply(optimistic)
        do {
            if linked {
                try await api.linkTodoSpace(todoID: todoID, machineID: space.machineID, space: space.space)
            } else {
                try await api.unlinkTodoSpace(todoID: todoID, machineID: space.machineID, space: space.space)
            }
            guard generation == targetGeneration else { return true }
            await confirmWrite(api: api)
            return true
        } catch {
            guard generation == targetGeneration else { throw error }
            mutationRevision += 1
            apply(original)
            if TodosLogic.isLegacySpacesRoute(error) {
                spacesSupported = false
                let upgrade = APIError(status: 404, message: String(localized: "This hub can't attach a whole group yet. Update the hub and try again."))
                self.error = upgrade.message
                throw upgrade
            }
            let message = error.shown
            self.error = message
            await confirmWrite(api: api)
            guard generation == targetGeneration else { throw error }
            self.error = message
            throw error
        }
    }

    func link(_ target: TodoLinkTarget, to todoID: String, api: APIClient? = nil) async throws {
        switch target {
        case .space(let project):
            try await setSpace(todoID: todoID, space: project, linked: true, api: api)
        case .agent(let machine, let session, _):
            try await linkAgent(todoID: todoID, machineID: machine, sessionID: session, api: api)
        }
    }

    func unlink(_ target: TodoLinkTarget, from todoID: String, api: APIClient? = nil) async throws {
        switch target {
        case .space(let project):
            try await setSpace(todoID: todoID, space: project, linked: false, api: api)
        case .agent(let machine, let session, _):
            try await unlinkAgent(todoID: todoID, machineID: machine, sessionID: session, api: api)
        }
    }

    /// Creation failure throws; a later link failure returns a message that preserves the created todo.
    func createAndLink(title: String, target: TodoLinkTarget, owner: Owner = .current, api: APIClient? = nil) async throws -> String? {
        if target.isSpace && changingSpaces {
            throw APIError(status: 409, message: String(localized: "Updating projects. Try again in a moment."))
        }
        let created = try await create(title: title, owner: owner, api: api)
        do {
            try await link(target, to: created.id, api: api)
            return nil
        } catch {
            return TodosLogic.createdLinkFailure(error, isSpace: target.isSpace)
        }
    }
}

/// What the V1 list draws besides the rows, kept on disk beside them (`TodoStore.viewStateName`, written only while V1
/// is on): a launch draws the list filed as the hub filed it, with its numbers, before the first read.
nonisolated struct TodoViewCache: Codable, Equatable, Sendable {
    var views: [String: TodoSummary] = [:]
    var counts: TodoCounts?
    var places: TodoPlaces?
}
