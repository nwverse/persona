import Foundation
import Observation
import Testing
@testable import Persona
import PersonaWire

/// The To-dos places (#10465; docs/design/proposals/todos-redesign.md 3.3; DESIGN_SYSTEM 3.5 "To-dos places"): which
/// places the sidebar and the title menu list, what each holds, its numbers and its notice, and where a pick leaves
/// To-dos.
@MainActor
struct TodosPlacesModelTests {
    private static let calendar: Calendar = {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = TimeZone(identifier: "UTC")!
        return c
    }()
    /// Wednesday 18 November 2026, 10:14 UTC: the prototype's now.
    private static let now = Date(timeIntervalSince1970: 1_795_004_040)

    private static func place(_ id: String, needsYou: Int = 0, manager: String? = nil) -> TodoPlace {
        if let slug = TodoPlaceRef(id: id).project {
            return TodoPlace(id: id, kind: TodoPlaceKind.project, title: slug.capitalized, project: slug, needsYou: needsYou, manager: manager)
        }
        return TodoPlace(id: id, kind: id, needsYou: needsYou)
    }

    private static let fixed = [place("all", needsYou: 3), place("today"), place("upcoming")]

    private static func filed(_ id: String, _ section: String = TodoListSection.todo, closed: Bool = false, project: String? = nil,
                              partOf: String? = nil, quiet: Bool = false) -> TodoFiled {
        TodoFiled(id: id, manager: nil, section: section, closed: closed, rank: 0, inactive: false, byView: true, quiet: quiet,
                  partOf: partOf, project: project, asks: section == TodoListSection.needsYou ? ["ask:" + id] : [])
    }

    private static func day(_ offset: Int) -> String {
        TodosLogic.dayKey(calendar.date(byAdding: .day, value: offset, to: now)!, calendar: calendar)
    }

    // MARK: The sidebar's and the title menu's places

    @Test func theSidebarListsTodayUpcomingAndTheProjectsInTheHubsOrder() {
        let entries = TodosPlaces.entries(Self.fixed + [Self.place("project:home"), Self.place("project:kyoto", needsYou: 1),
                                                       Self.place("project:weather", needsYou: 1)])
        #expect(entries.fixed.map(\.id) == ["today", "upcoming"])
        #expect(entries.projects.map(\.id) == ["project:home", "project:kyoto", "project:weather"])
        #expect(entries.more, "All Projects… follows the projects")
    }

    /// Five projects at most; one that needs the owner is never left behind All Projects…, and the ones listed keep the
    /// hub's order.
    @Test func atMostFiveProjectsThoseThatNeedYouFirst() {
        let projects = (1...7).map { Self.place("project:p\($0)", needsYou: $0 >= 6 ? 1 : 0) }
        let entries = TodosPlaces.entries(Self.fixed + projects)
        #expect(entries.projects.map(\.id) == ["project:p1", "project:p2", "project:p3", "project:p6", "project:p7"])
        #expect(entries.more)
    }

    /// A hub without `todo_places` (or one that answered 405): the store has All alone, so there is nothing to fold open
    /// or pick, and a place picked before is All To-dos again.
    @Test func withoutTheHubsPlacesAllToDosAlone() {
        let entries = TodosPlaces.entries([Self.place("all", needsYou: 2)])
        #expect(entries.isEmpty)
        #expect(TodosPlaces.effective(.today, places: [Self.place("all")]) == .all)
        #expect(TodosPlaces.effective(.project("kyoto"), places: [Self.place("all")]) == .all)
    }

    /// No projects but the knowledge base has some: All Projects… is still offered (a project with nothing open is a place).
    @Test func allProjectsWithOnlyTheKnowledgeBasesProjects() {
        #expect(!TodosPlaces.entries(Self.fixed).more)
        #expect(TodosPlaces.entries(Self.fixed, knowsProjects: true).more)
    }

    /// A project place is drawn while the hub or the knowledge base knows it; another assistant's is All To-dos.
    @Test func aProjectNobodyKnowsIsAllToDos() {
        let places = Self.fixed + [Self.place("project:kyoto", needsYou: 1)]
        #expect(TodosPlaces.effective(.project("kyoto"), places: places) == .project("kyoto"))
        #expect(TodosPlaces.effective(.project("garden"), places: places) == .all)
        let garden = TodoProject(slug: "garden", name: "Garden", long: false)
        #expect(TodosPlaces.effective(.project("garden"), places: places, projects: [garden]) == .project("garden"))
        #expect(TodosPlaces.effective(.upcoming, places: places) == .upcoming)
    }

    @Test func titles() {
        let places = Self.fixed + [TodoPlace(id: "project:kyoto", kind: TodoPlaceKind.project, title: "Kyoto trip", project: "kyoto")]
        let kb = [TodoProject(slug: "garden", name: "The garden", long: false)]
        #expect(TodosPlaces.title(.project("kyoto"), places: places, projects: kb) == "Kyoto trip")
        #expect(TodosPlaces.title(.project("garden"), places: places, projects: kb) == "The garden")
        #expect(TodosPlaces.title(.project("shed"), places: places, projects: kb) == "shed")
        #expect(!TodosPlaces.title(.today, places: places, projects: kb).isEmpty)
        #expect(TodosPlaces.title(.today, places: places, projects: kb) != TodosPlaces.title(.upcoming, places: places, projects: kb))
    }

    // MARK: What a place holds

    @Test func todayHoldsWhatIsDueTodayOverdueOrRemindingToday() {
        let items: [String: TodoItem] = [
            "due": TodoItem(id: "due", title: "due", dueDate: Self.day(0)),
            "late": TodoItem(id: "late", title: "late", dueDate: Self.day(-2)),
            "earlier": TodoItem(id: "earlier", title: "earlier", dueAt: Self.now.addingTimeInterval(-3600)),
            "remind": TodoItem(id: "remind", title: "remind", remindAt: Self.now.addingTimeInterval(3600)),
            "later": TodoItem(id: "later", title: "later", dueDate: Self.day(1)),
            "plain": TodoItem(id: "plain", title: "plain"),
            "closed": TodoItem(id: "closed", title: "closed", dueDate: Self.day(0)),
            "someday": TodoItem(id: "someday", title: "someday", dueDate: Self.day(0)),
        ]
        let rows = ["due", "late", "earlier", "remind", "later", "plain"].map { Self.filed($0) }
            + [Self.filed("closed", TodoListSection.done, closed: true), Self.filed("someday", TodoListSection.someday)]
        let today = TodosPlaces.rows(rows, in: .today, items: items, now: Self.now, calendar: Self.calendar).map(\.id)
        #expect(today == ["due", "late", "earlier", "remind"])
        let upcoming = TodosPlaces.rows(rows, in: .upcoming, items: items, now: Self.now, calendar: Self.calendar).map(\.id)
        #expect(upcoming == ["due", "late", "earlier", "later"])
    }

    /// A project holds its rows, closed ones too (the bar's Done); a step its parent carries is never a row of its own.
    @Test func aProjectHoldsItsRowsLessTheStepsTheirParentsCarry() {
        let rows = [Self.filed("t01", TodoListSection.needsYou, project: "kyoto"),
                    Self.filed("t01-s1", project: "kyoto", partOf: "t01"),
                    Self.filed("t08", TodoListSection.done, closed: true, project: "kyoto"),
                    Self.filed("t02", project: "weather")]
        let kyoto = TodosPlaces.rows(rows, in: .project("kyoto"), items: [:], now: Self.now, calendar: Self.calendar).map(\.id)
        #expect(kyoto == ["t01", "t08"])
        #expect(TodosPlaces.rows(rows, in: .all, items: [:], now: Self.now, calendar: Self.calendar).count == 4)
    }

    /// "3 coming up this week": open to-dos due in the seven days after today, whatever day of the week today is.
    @Test func comingUpIsTheNextSevenDays() {
        let items = Dictionary(uniqueKeysWithValues: [(-1), 0, 1, 2, 7, 8].map { n in
            ("d\(n)", TodoItem(id: "d\(n)", title: "d", dueDate: Self.day(n)))
        })
        let rows = items.keys.sorted().map { Self.filed($0) }
        #expect(TodosPlaces.comingUp(rows, items: items, now: Self.now, calendar: Self.calendar) == 3)
    }

    @Test func eachPlaceCapturesWithItsDefault() {
        let today = TodosPlaces.capture(.today, now: Self.now, calendar: Self.calendar)
        #expect(today.due == Self.calendar.startOfDay(for: Self.now) && today.project == nil)
        #expect(TodosPlaces.capture(.upcoming, now: Self.now, calendar: Self.calendar).asksDate)
        #expect(TodosPlaces.capture(.project("kyoto"), now: Self.now, calendar: Self.calendar).project == "kyoto")
        #expect(TodosPlaces.capture(.all, now: Self.now, calendar: Self.calendar) == TodoPlaceCapture())
    }

    // MARK: Upcoming

    /// Overdue first, then each day that has a to-do, soonest first; inside a day by the due moment, then the hub's order.
    @Test func upcomingIsByDay() {
        let items: [String: TodoItem] = [
            "late": TodoItem(id: "late", title: "late", dueDate: Self.day(-1)),
            "fri2": TodoItem(id: "fri2", title: "fri2", dueDate: Self.day(2)),
            "fri1": TodoItem(id: "fri1", title: "fri1", dueAt: Self.calendar.date(byAdding: .hour, value: 49, to: Self.now)),
            "thu": TodoItem(id: "thu", title: "thu", dueDate: Self.day(1)),
        ]
        let rows = ["late", "fri2", "fri1", "thu"].map { Self.filed($0) }
        let out = TodoUpcomingLayout.layout(rows, items: items, now: Self.now, calendar: Self.calendar)
        #expect(out.groups.map(\.id) == [TodoUpcomingLayout.overdue, "day:" + Self.day(1), "day:" + Self.day(2)])
        #expect(out.groups[2].keys == ["/fri1", "/fri2"])
        #expect(TodoUpcomingLayout.day(out.groups[1].id, calendar: Self.calendar) == Self.calendar.date(byAdding: .day, value: 1,
                                                                                                         to: Self.calendar.startOfDay(for: Self.now)))
        #expect(TodoUpcomingLayout.day(TodoUpcomingLayout.overdue, calendar: Self.calendar) == nil)
        #expect(!out.calm && out.sections.count == 4)
    }

    /// Upcoming's calendar colours each to-do by its view's phase, not by its sessions.
    @Test func theCalendarReadsTheView() {
        let view = { (phase: String, reason: String) in TodoSummary(phase: phase, section: phase, reason: reason, rank: 0) }
        #expect(TodoUpcomingLayout.calendarState(view(TodoPhase.needsYou, TodoReason.agentAsks), fallback: .notStarted) == .needsYou)
        #expect(TodoUpcomingLayout.calendarState(view(TodoPhase.needsYou, TodoReason.failedStart), fallback: .notStarted) == .failed)
        #expect(TodoUpcomingLayout.calendarState(view(TodoPhase.ready, TodoReason.agentRunning), fallback: .notStarted) == .unread)
        #expect(TodoUpcomingLayout.calendarState(nil, fallback: .done) == .done)
    }

    // MARK: Picks

    /// The sidebar's pick opens To-dos on the place at its root, once; the title menu's changes the list in place; a
    /// page built for the app switcher's snapshot moves nothing.
    @Test func aSidebarPickOpensThePlaceAtItsRoot() {
        let scene = PlacesTestScene()
        let workspace = TodoWorkspace(active: { scene.active })
        workspace.open(TodoPlaceKind.today)
        #expect(workspace.place == TodoPlaceKind.today)
        #expect(workspace.takePick())
        #expect(!workspace.takePick(), "taken once")
        workspace.open(TodoPlaceKind.today)
        #expect(!workspace.takePick(), "the place in front picked again: its stack's own rule")
        workspace.pick(TodoPlaceKind.upcoming)
        #expect(workspace.place == TodoPlaceKind.upcoming && !workspace.takePick(), "the title menu changes the list where it is")
        scene.active = false
        workspace.open(TodoPlaceKind.allValue)
        #expect(workspace.place == TodoPlaceKind.upcoming)
    }

    /// The sidebar's To-dos row: from elsewhere To-dos comes back on its last place; while it is in front (beside the
    /// sidebar, or its page under the drawer) it is All To-dos, at its root.
    @Test func theToDosRowReopensOnTheLastPlace() {
        let workspace = TodoWorkspace(active: { true })
        let hub = FakeHub()
        let store = TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}, v1: .on(api: hub.client))
        defer { store.forget() }
        let places = TodosPlacesModel(deps: .init(store: store, workspace: workspace, projects: { [] }))
        places.open(.today)
        _ = workspace.takePick()
        #expect(!places.reopen(inFront: false) && workspace.place == TodoPlaceKind.today && !workspace.takePick())
        workspace.page(up: true)
        #expect(places.reopen(inFront: false) && workspace.place == TodoPlaceKind.allValue && workspace.takePick())
        workspace.page(up: false)
        places.open(.upcoming)
        _ = workspace.takePick()
        #expect(places.reopen(inFront: true) && workspace.place == TodoPlaceKind.allValue)
        #expect(!places.reopen(inFront: true), "All To-dos in front: nothing to pick, the tab-bar rule")
    }

    // MARK: Against a hub

    private static let list = TodoViewJSON.list([
        TodoViewJSON.row("t01", extra: #""project":"kyoto""#, view: TodoViewJSON.view(TodoListSection.needsYou, asks: ["a1"])),
        TodoViewJSON.row("t02", extra: #""project":"weather""#, view: TodoViewJSON.view(TodoListSection.needsYou, rank: 1, asks: ["a2"])),
        TodoViewJSON.row("t03", extra: #""project":"weather""#, view: TodoViewJSON.view(TodoListSection.working)),
        TodoViewJSON.row("t11", extra: #""due_date":"2026-11-20""#, view: TodoViewJSON.view(TodoListSection.todo, line: "rest")),
    ], counts: TodoViewJSON.counts(needsYou: 2, inProgress: 1, open: 4,
                                   sections: [TodoListSection.needsYou: 2, TodoListSection.working: 1, TodoListSection.todo: 1]))

    private static let placesJSON = #"""
    {"places":[{"id":"all","kind":"all","title":"","needs_you":2},{"id":"today","kind":"today","title":"","needs_you":0},
    {"id":"upcoming","kind":"upcoming","title":"","needs_you":0},
    {"id":"project:kyoto","kind":"project","title":"Kyoto trip","project":"kyoto","needs_you":1},
    {"id":"project:weather","kind":"project","title":"Weather App","project":"weather","needs_you":1}],"counts":{}}
    """#

    private func store(_ hub: FakeHub, places: Bool = true) -> TodoStore {
        TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {},
                  v1: .on(features: TodoFeatures(view: true, places: places), api: hub.client))
    }

    private func placesModel(_ store: TodoStore, _ workspace: TodoWorkspace) -> TodosPlacesModel {
        TodosPlacesModel(deps: .init(store: store, workspace: workspace, projects: { [] }))
    }

    private func listModel(_ store: TodoStore, hub: FakeHub) -> TodosListModel {
        TodosListModel(deps: TodosListDeps(store: store, api: { hub.client }, now: { Self.now }, calendar: { Self.calendar },
                                           projects: { [] }))
    }

    @Test func aHubWithPlacesListsThem() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list)
        hub.on("GET", "/api/v1/todos/places", json: Self.placesJSON)
        let store = store(hub)
        defer { store.forget() }
        let places = placesModel(store, TodoWorkspace(active: { true }))
        await store.refresh(api: hub.client)
        try await waitForPlacesTest { !places.entries.isEmpty }
        #expect(places.entries.projects.map(\.title) == ["Kyoto trip", "Weather App"])
        #expect(places.entries.projects.map(\.needsYou) == [1, 1])
        #expect(places.title(.project("weather")) == "Weather App")
    }

    /// A hub whose hello does not list `todo_places`, and one that answers the route 405: All To-dos only.
    @Test func withoutTodoPlacesOrA405AllToDosOnly() async throws {
        for listed in [false, true] {
            let hub = FakeHub()
            hub.on("GET", "/api/v1/todos", json: Self.list)
            hub.on("GET", "/api/v1/todos/places") { _ in throw APIError(status: 405, message: "method not allowed") }
            let store = store(hub, places: listed)
            defer { store.forget() }
            let workspace = TodoWorkspace(active: { true })
            workspace.pick(TodoPlaceKind.today)
            let places = placesModel(store, workspace)
            await store.refresh(api: hub.client)
            try await waitForPlacesTest { !store.todos.isEmpty }
            #expect(places.entries.isEmpty)
            #expect(places.current == .all, "a place picked before is All To-dos")
        }
    }

    /// In a place the bar and the Needs you header count its rows; the notice counts what needs the owner elsewhere,
    /// and Show All goes back to All To-dos, where there is no notice.
    @Test func aPlacesNumbersAndWhatNeedsYouElsewhere() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: Self.list)
        hub.on("GET", "/api/v1/todos/places", json: Self.placesJSON)
        let store = store(hub)
        defer { store.forget() }
        let workspace = TodoWorkspace(active: { true })
        let places = placesModel(store, workspace)
        let list = listModel(store, hub: hub)
        list.start()
        await store.refresh(api: hub.client)
        try await waitForPlacesTest { list.ready && !places.entries.isEmpty }
        #expect(list.needsYou == 2 && list.hiddenNeeds == 0)

        places.show(.project("weather"))
        list.place = places.current
        #expect(list.groups.map(\.id) == [TodoListSection.needsYou, TodoListSection.working])
        #expect(list.needsYou == 1, "the place's part of N")
        #expect(list.entries.first(where: { $0.choice == .inProgress })?.count == 1)
        #expect(list.hiddenNeeds == 1, "Kyoto trip's to-do needs the owner too")

        list.choice = .needsYou
        places.show(.today)
        list.place = places.current
        #expect(list.choice == .all, "another place lands on All")
        #expect(list.isEmpty && list.hiddenNeeds == 2)

        places.show(.all) // Show All
        list.place = places.current
        #expect(list.hiddenNeeds == 0 && list.groups.count == 3)
    }

    /// Two assistants: the places' projects are the current one's, each part counted from its own rows.
    @Test func twoManagersListTheCurrentAssistantsProjects() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/todos", json: TodoViewJSON.list([
            TodoViewJSON.row("h1", extra: #""manager":"home","project":"garden""#,
                             view: TodoViewJSON.view(TodoListSection.needsYou, asks: ["a"])),
            TodoViewJSON.row("w1", extra: #""manager":"work","project":"weather""#,
                             view: TodoViewJSON.view(TodoListSection.needsYou, asks: ["b"])),
            TodoViewJSON.row("w2", extra: #""manager":"work","project":"weather""#,
                             view: TodoViewJSON.view(TodoListSection.needsYou, rank: 1, asks: ["c"])),
        ]))
        hub.on("GET", "/api/v1/todos/places", json: #"""
        {"places":[{"id":"all","kind":"all","title":"","needs_you":3},{"id":"today","kind":"today","title":"","needs_you":0},
        {"id":"upcoming","kind":"upcoming","title":"","needs_you":0},
        {"id":"project:garden","kind":"project","title":"Garden","project":"garden","needs_you":1,"manager":"home"},
        {"id":"project:weather","kind":"project","title":"Weather App","project":"weather","needs_you":2,"manager":"work"}],
        "counts":{}}
        """#)
        let scope = AssistantScope(group: "", current: "work", defaultID: "home", installed: ["home", "work"])
        let store = TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}, managers: .init(scope: { scope }, naming: { $0 }),
                              v1: .on(api: hub.client))
        defer { store.forget() }
        let places = placesModel(store, TodoWorkspace(active: { true }))
        await store.refresh(api: hub.client)
        try await waitForPlacesTest { !places.entries.isEmpty }
        #expect(places.entries.projects.map(\.id) == ["project:weather"])
        #expect(places.entries.projects.first?.needsYou == 2)
    }
}

/// Whether the test's scene is active.
@MainActor private final class PlacesTestScene {
    var active = true
}

/// Waits (up to 5 s) for something the store does.
@MainActor
private func waitForPlacesTest(_ condition: @MainActor () -> Bool) async throws {
    for _ in 0..<250 where !condition() { try await Task.sleep(for: .milliseconds(20)) }
    try #require(condition(), "it did not happen in time")
}
