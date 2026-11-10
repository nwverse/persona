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

    @Test func dispatchPushesOnlyItsResolvedAgent() {
        let tasks = store()
        let key = SessionKey(group: "hub", machineID: "computer", sessionID: "dispatched")
        tasks.detailTask = "unrelated"
        tasks.detailAgent = SessionKey(machineID: "other", sessionID: "other")
        tasks.openDispatchedTask("new", agent: key, pushAgent: true)
        #expect(tasks.fullAgent == key)
        #expect(tasks.detailTask == nil)
        #expect(tasks.detailAgent == nil)
        #expect(tasks.showBoard == false)
    }

    @Test func dispatchWithoutAnAgentOpensOnlyItsTaskDetail() {
        let tasks = store()
        tasks.expand(SessionKey(machineID: "old", sessionID: "old"))
        tasks.openDispatchedTask("starting", agent: nil, pushAgent: true)
        #expect(tasks.fullAgent == nil)
        #expect(tasks.detailTask == "starting")
        #expect(tasks.detailAgent == nil)
    }

    @Test func macDispatchDetailRetainsTheTodoIdentity() {
        let tasks = store()
        tasks.openDispatchedTask("new", agent: SessionKey(machineID: "computer", sessionID: "agent"), pushAgent: false)
        #expect(tasks.detailTask == "new")
        #expect(tasks.fullAgent == nil)
        #expect(tasks.detailAgent == nil)
    }

    @Test func forgettingDropsAPendingPush() {
        let tasks = store()
        tasks.expand(SessionKey(machineID: "box", sessionID: "agent-3"))
        tasks.forget()
        #expect(tasks.fullAgent == nil)
    }
}
