import Foundation
import Testing
@testable import Persona

/// Full Screen in the assistant's agent sheet: the sheet goes down (both of its sources cleared, so it does not rise
/// again) and the agent is handed to the assistant page to push.
@MainActor
struct TaskSheetExpandTests {
    private func store() -> TasksStore {
        TasksStore(todoStore: TodoStore(cachedTodos: [], cacheWriter: nil, hubListChanged: {}))
    }

    @Test func expandingAnAgentClosesItsSheetAndAsksForThePush() {
        let tasks = store()
        let key = SessionKey(machineID: "box", sessionID: "agent-1")
        tasks.detailAgent = key
        tasks.expand(key)
        #expect(tasks.detailAgent == nil)
        #expect(tasks.detailTask == nil)
        #expect(tasks.fullAgent == key)
    }

    @Test func expandingFromATaskClosesTheTaskSheet() {
        let tasks = store()
        let key = SessionKey(machineID: "box", sessionID: "agent-2")
        tasks.detailTask = "todo-1"
        tasks.expand(key)
        #expect(tasks.detailTask == nil)
        #expect(tasks.fullAgent == key)
    }

    @Test func dispatchCarriesOnlyItsResolvedAgentToThePresenter() {
        let tasks = store()
        let key = SessionKey(group: "hub", machineID: "computer", sessionID: "dispatched")
        tasks.detailTask = "unrelated"
        tasks.detailAgent = SessionKey(machineID: "other", sessionID: "other")
        tasks.openDispatchedTask("new", agent: key)
        #expect(tasks.fullAgent == nil)
        #expect(tasks.detailTask == "new")
        #expect(tasks.detailTaskAgent == key)
        #expect(tasks.detailAgent == nil)
        #expect(tasks.showBoard == false)
    }

    @Test func dispatchWithoutAnAgentOpensOnlyItsTaskDetail() {
        let tasks = store()
        tasks.expand(SessionKey(machineID: "old", sessionID: "old"))
        tasks.openDispatchedTask("starting", agent: nil)
        #expect(tasks.fullAgent == nil)
        #expect(tasks.detailTask == "starting")
        #expect(tasks.detailAgent == nil)
    }

    @Test func macDispatchDetailTargetsTheCorrespondingSession() {
        let tasks = store()
        let key = SessionKey(group: "hub", machineID: "computer", sessionID: "agent")
        tasks.openDispatchedTask("new", agent: key)
        #expect(tasks.detailTask == "new")
        #expect(tasks.detailTaskAgent == key)
        #expect(tasks.fullAgent == nil)
        #expect(tasks.detailAgent == nil)
    }

    @Test func eachMacPlanLinkTargetsItsOwnSession() {
        let tasks = store()
        let first = SessionKey(group: "hub", machineID: "computer", sessionID: "first")
        let second = SessionKey(group: "hub", machineID: "another", sessionID: "second")
        for (todo, key) in [("first-task", first), ("second-task", second)] {
            tasks.openDispatchedTask(todo, agent: key)
            #expect(tasks.detailTask == todo)
            #expect(tasks.detailTaskAgent == key)
        }
        tasks.detailTask = "ordinary-task"
        #expect(tasks.detailTaskAgent == nil, "an ordinary task must not inherit a dispatch's session")
    }

    @Test func macStartingTaskHasNoInventedSessionTarget() {
        let tasks = store()
        tasks.openDispatchedTask("old", agent: SessionKey(machineID: "computer", sessionID: "old"))
        tasks.openDispatchedTask("starting", agent: nil)
        #expect(tasks.detailTask == "starting")
        #expect(tasks.detailTaskAgent == nil)
    }

    @Test func expandingADispatchClearsItsDetailSessionTarget() {
        let tasks = store()
        let key = SessionKey(machineID: "computer", sessionID: "agent")
        tasks.openDispatchedTask("task", agent: key)
        tasks.expand(key)
        #expect(tasks.fullAgent == key)
        #expect(tasks.detailTask == nil)
        #expect(tasks.detailTaskAgent == nil)
    }

    @Test func forgettingDropsADispatchedDetailSession() {
        let tasks = store()
        tasks.openDispatchedTask("task", agent: SessionKey(group: "hub", machineID: "computer", sessionID: "agent"))
        tasks.forget()
        #expect(tasks.detailTask == nil)
        #expect(tasks.detailTaskAgent == nil)
    }

    #if os(iOS)
    @Test func readingPlaceIsScopedToPairManagerMachineAndSession() {
        var hub = PairedHub(config: ServerConfig(url: URL(string: "http://example.invalid")!, token: "first"))
        let first = TaskHandoffPairing(hub)
        let key = SessionKey(group: "g", machineID: "a/b", sessionID: "c")
        let saved = first.readingKey(key, manager: "manager")
        #expect(first.readingKey(key, manager: "other") != saved)
        #expect(first.readingKey(.init(group: "g", machineID: "a", sessionID: "b/c"), manager: "manager") != saved)
        hub.tradedPairing = DeviceCredentialTrade.fingerprint("first")
        hub.config.token = "device"
        #expect(TaskHandoffPairing(hub).readingKey(key, manager: "manager") == saved)
        hub.tradedPairing = nil
        hub.config.token = "second"
        #expect(TaskHandoffPairing(hub).readingKey(key, manager: "manager") != saved)
        #expect(!saved.contains("first"))
    }
    @Test func returnRejectsAnIdenticalConversationFromAnotherPairing() {
        let folder = FileManager.default.temporaryDirectory.appending(path: "task-origin-" + UUID().uuidString)
        let model = AppModel(stateFolder: folder)
        let pair = PairedHub(id: "same-hub", config: ServerConfig(url: URL(string: "http://example.invalid")!,
                                                                  token: "first"), group: "same-group")
        model.registry = HubRegistry(hubs: [pair])
        model.buildLinks()
        let key = SessionKey(group: pair.group, machineID: "same-machine", sessionID: "same-conversation")
        let previous = UserDefaults.standard.object(forKey: "assistant.current")
        defer {
            UserDefaults.standard.set(previous, forKey: "assistant.current")
            try? FileManager.default.removeItem(at: folder)
        }
        model.assistantCurrent = key
        var route = TaskHandoffRoute(destination: .todo("same-task"), origin: key, title: "Source",
                                     pairing: TaskHandoffPairing(pair), manager: model.assistantManagerID,
                                     generation: TasksStore.shared.generation)
        #expect(route.belongs(to: model))
        model.registry.hubs[0].config.token = "second"
        #expect(!route.belongs(to: model))
        model.registry.hubs[0] = pair
        #expect(route.belongs(to: model))
        route.generation &+= 1
        #expect(!route.belongs(to: model))
    }
    #endif

    @Test func forgettingDropsAPendingPush() {
        let tasks = store()
        tasks.expand(SessionKey(machineID: "box", sessionID: "agent-3"))
        tasks.forget()
        #expect(tasks.fullAgent == nil)
    }
}

/// The task sheet's edge swipe (#5603): only a mostly horizontal, long enough swipe from the leading edge closes it.
struct TaskSheetEdgeSwipeTests {
    @Test func rightSwipeFromTheEdgeCloses() {
        #expect(TaskDetailSheet.edgeSwipeCloses(start: CGPoint(x: 5, y: 300), translation: CGSize(width: 120, height: 10)))
    }
    @Test func otherDragsDoNot() {
        #expect(!TaskDetailSheet.edgeSwipeCloses(start: CGPoint(x: 100, y: 300), translation: CGSize(width: 200, height: 0)))
        #expect(!TaskDetailSheet.edgeSwipeCloses(start: CGPoint(x: 5, y: 300), translation: CGSize(width: 30, height: 0)))
        #expect(!TaskDetailSheet.edgeSwipeCloses(start: CGPoint(x: 5, y: 300), translation: CGSize(width: 120, height: 200)))
        #expect(!TaskDetailSheet.edgeSwipeCloses(start: CGPoint(x: 5, y: 300), translation: CGSize(width: -120, height: 0)))
    }
}
