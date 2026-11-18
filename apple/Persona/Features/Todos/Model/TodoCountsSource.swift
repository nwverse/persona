import Foundation
import Observation
import PersonaWire

/// Every To-dos number in one place (#10465, decision S2-23; DESIGN_SYSTEM 4.8, "every place that collects requests
/// shows the same number"): the To-dos row in the drawer and sidebar, the folded Pages tally, the section bar, the Needs
/// you header, the places and the to-do's page all read these, so an answer anywhere moves every number in one update.
///
/// - One manager: the hub's `counts` and places as sent.
/// - Several managers, or a hub that sends no counts: counted from the current assistant's rows as filed
///   (`TodoFiling`, `TodoCountsFallback`), since the hub adds every manager's up while the pages list the current
///   assistant's; only its own project places are shown, each with its part of N counted the same way.
/// - Less what the owner answered on this device that the hub's view does not show yet (`answered(_:of:)`): a to-do in
///   Needs you whose every ask is answered leaves N at once, and comes back if the answer is undone.
///
/// Each number is set only when it changes, so a push that moves none redraws nothing that reads them.
@MainActor @Observable
final class TodoCountsSource {
    private(set) var counts = TodoCounts(sections: Dictionary(uniqueKeysWithValues: TodoListSection.all.map { ($0, 0) }))
    /// All To-dos, Today, Upcoming, then the projects; All only from a hub without places.
    private(set) var places: [TodoPlace] = [TodoPlace(id: TodoPlaceKind.allValue, kind: TodoPlaceKind.allValue)]
    /// N: the to-dos that need the owner.
    var needsYou: Int { counts.needsYou }

    /// What the store holds: every row (each manager's), their views, and the hub's counts and places.
    struct Input {
        var rows: [TodoItem] = []
        var views: [String: TodoSummary] = [:]
        var counts: TodoCounts?
        var places: TodoPlaces?
        var now = Date.distantPast
    }

    @ObservationIgnored private var input = Input()
    /// The asks answered on this device that the hub's view still lists, by to-do id.
    @ObservationIgnored private var overlay: [String: Set<String>] = [:]
    @ObservationIgnored private let scope: @MainActor () -> AssistantScope?
    @ObservationIgnored private let lookup: @MainActor () -> TodosLogic.SessionLookup?
    @ObservationIgnored private var watchesScope = false

    /// `scope`: the current assistant's, nil with one manager (`TodoStore.Managers.scope`). `lookup`: the sessions of the
    /// rows' Agents, read only to file a row without a view.
    init(scope: @escaping @MainActor () -> AssistantScope?, lookup: @escaping @MainActor () -> TodosLogic.SessionLookup? = { nil }) {
        self.scope = scope
        self.lookup = lookup
    }

    /// The store's rows, views, counts or places changed: answers the hub now shows are let go, and the numbers counted
    /// again.
    func update(_ input: Input) {
        self.input = input
        for (todo, asks) in overlay {
            let listed = Set(input.views[todo]?.asks?.map(\.id) ?? [])
            let kept = asks.intersection(listed)
            overlay[todo] = kept.isEmpty ? nil : kept
        }
        recount()
    }

    /// The owner answered ask `ask` of to-do `todo` here; it counts as answered until the hub's view stops listing it.
    func answered(_ ask: String, of todo: String) {
        guard !(overlay[todo]?.contains(ask) ?? false) else { return }
        overlay[todo, default: []].insert(ask)
        recount()
    }

    /// The answer was undone, or the hub refused it: the ask counts again.
    func unanswered(_ ask: String, of todo: String) {
        guard overlay[todo]?.remove(ask) != nil else { return }
        if overlay[todo]?.isEmpty == true { overlay[todo] = nil }
        recount()
    }

    /// Another hub, or none: nothing kept.
    func reset() {
        overlay = [:]
        update(Input())
    }

    private func recount() {
        let scope = watchedScope()
        let rows = scope.map { s in input.rows.filter { s.has($0.manager) } } ?? input.rows
        let hubCounts = scope == nil ? input.counts : nil
        // Filing is needed to count, to place several managers' projects, or to take answers off.
        let filed = hubCounts == nil || scope != nil || !overlay.isEmpty
            ? TodoFiling.file(rows, views: input.views, now: input.now, lookup: { lookup() }) : []
        var next = hubCounts ?? TodoCountsFallback.counts(filed)
        let carried = overlay.isEmpty ? [] : TodoCountsFallback.carried(filed)
        var awayByProject: [String: Int] = [:]
        for row in filed where row.section == TodoListSection.needsYou && !carried.contains(row.id) && !row.asks.isEmpty
            && Set(row.asks).isSubset(of: overlay[row.id] ?? []) {
            next.sections[TodoListSection.needsYou] = max(0, (next.sections[TodoListSection.needsYou] ?? 0) - 1)
            guard !row.quiet else { continue }
            next.needsYou = max(0, next.needsYou - 1)
            if let project = row.project { awayByProject[project, default: 0] += 1 }
        }
        if next != counts { counts = next }
        let nextPlaces = places(scope: scope, filed: filed, awayByProject: awayByProject)
        if nextPlaces != places { places = nextPlaces }
    }

    private func places(scope: AssistantScope?, filed: [TodoFiled], awayByProject: [String: Int]) -> [TodoPlace] {
        guard let sent = input.places?.places, !sent.isEmpty else {
            return [TodoPlace(id: TodoPlaceKind.allValue, kind: TodoPlaceKind.allValue, needsYou: counts.needsYou)]
        }
        let byProject = scope == nil ? [:] : TodoCountsFallback.needsYouByProject(filed)
        var out: [TodoPlace] = []
        for var place in sent {
            switch place.kind {
            case TodoPlaceKind.allValue:
                place.needsYou = counts.needsYou
            case TodoPlaceKind.project:
                let slug = place.project ?? ""
                if let scope {
                    guard scope.has(place.manager) else { continue }
                    place.needsYou = byProject[slug] ?? 0
                }
                place.needsYou = max(0, place.needsYou - (awayByProject[slug] ?? 0))
            default:
                break // Today and Upcoming draw no count; a kind this build does not know is kept as sent
            }
            out.append(place)
        }
        return out
    }

    /// The current assistant, followed: when the owner switches assistants the numbers are counted for the new one.
    private func watchedScope() -> AssistantScope? {
        guard !watchesScope else { return scope() }
        watchesScope = true
        return withObservationTracking { scope() } onChange: { [weak self] in
            // Called once, before the change lands: counted again once it has.
            Task { @MainActor [weak self] in
                self?.watchesScope = false
                self?.recount()
            }
        }
    }
}
