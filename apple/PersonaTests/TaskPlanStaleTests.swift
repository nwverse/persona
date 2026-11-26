import Foundation
import Testing
@testable import Persona
import PersonaWire

/// A plan card held as open after the hub started, dismissed or withdrew it while no event reached the app (a hub
/// restart, a long background): Needs You kept listing it and its Start / Don't Start answered 409 every time.
@MainActor
struct TaskPlanStaleTests {
    private static let feed = "/api/v1/assistant/conversations/main/feed"

    private func store() -> TasksStore {
        TasksStore(todoStore: TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}), viewStore: AssistantViewsStore(),
                   toast: { _ in })
    }

    private func plan(_ id: String, conversation: String = "main", state: TaskPlan.State = .open) -> TaskPlan {
        TaskPlan(id: id, conversation: conversation, tasks: [PlanTask(title: "task", prompt: "p", machine: "mac", cwd: "/demo")],
                 state: state)
    }

    private func json(_ id: String, conversation: String = "main", state: String) -> String {
        #"{"plan":{"id":"\#(id)","conversation":"\#(conversation)","tasks":[],"state":"\#(state)"}}"#
    }

    @Test func anOpenPlanTheHubNoLongerListsIsReadAgain() async {
        let hub = FakeHub()
        hub.on("GET", Self.feed, json: #"{"items":[],"pending_plan_ids":["waiting"]}"#)
        hub.on("GET", "/api/v1/task-plans/withdrawn", json: json("withdrawn", state: "dismissed"))
        let tasks = store()
        tasks.received(plan("withdrawn"))
        tasks.received(plan("waiting"))
        tasks.received(plan("other", conversation: "elsewhere"))

        await tasks.load("main", refreshTodos: false, api: hub.client)?.value

        #expect(tasks.plans["withdrawn"]?.state == .dismissed)
        #expect(tasks.plans["waiting"]?.state == .open)
        #expect(tasks.plans["other"]?.state == .open)
        // only the one the hub no longer lists is read again
        #expect(hub.sent("GET", "/api/v1/task-plans/waiting").isEmpty)
        #expect(hub.sent("GET", "/api/v1/task-plans/other").isEmpty)
    }

    @Test func anOpenPlanTheHubNoLongerHasGoes() async {
        let hub = FakeHub()
        hub.on("GET", Self.feed, json: #"{"items":[],"pending_plan_ids":[]}"#)
        hub.on("GET", "/api/v1/task-plans/gone") { _ in throw APIError(status: 404, message: "no such plan") }
        let tasks = store()
        tasks.received(plan("gone"))

        await tasks.load("main", refreshTodos: false, api: hub.client)?.value

        #expect(tasks.plans["gone"] == nil)
    }

    /// An older hub, or any conversation but the main one, lists no pending plans: nothing is read again.
    @Test func noPendingListChecksNothing() async {
        let hub = FakeHub()
        hub.on("GET", Self.feed, json: #"{"items":[]}"#)
        let tasks = store()
        tasks.received(plan("held"))

        await tasks.load("main", refreshTodos: false, api: hub.client)?.value

        #expect(tasks.plans["held"]?.state == .open)
        #expect(hub.sent("GET", "/api/v1/task-plans/held").isEmpty)
    }

    @Test func aRefusedDismissOfAWithdrawnPlanIsNoError() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/task-plans/withdrawn", json: json("withdrawn", state: "dismissed"))
        let tasks = store()
        let held = plan("withdrawn")
        tasks.received(held)

        let settled = await tasks.planRefused(held, error: APIError(status: 409, message: "this plan was already started or dismissed"),
                                              api: hub.client)

        #expect(settled)
        #expect(tasks.plans["withdrawn"]?.state == .dismissed)
    }

    @Test func aRefusalOfAPlanStillOpenStaysAnError() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/task-plans/open", json: json("open", state: "open"))
        let tasks = store()
        let held = plan("open")
        tasks.received(held)

        #expect(!(await tasks.planRefused(held, error: APIError(status: 409, message: "busy"), api: hub.client)))
        // another failure is not read again
        #expect(!(await tasks.planRefused(held, error: APIError(status: 503, message: "down"), api: hub.client)))
        #expect(hub.sent("GET", "/api/v1/task-plans/open").count == 1)
        #expect(tasks.plans["open"]?.state == .open)
    }
}
