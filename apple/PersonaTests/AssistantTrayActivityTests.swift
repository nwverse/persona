import Foundation
import Testing
@testable import Persona

@MainActor
struct AssistantTrayActivityTests {
    private func session(_ fields: [String: Any] = [:], id: String = "agent") throws -> Session {
        var json: [String: Any] = ["machine_id": "computer", "session_id": id, "cwd": "/demo", "state": "working"]
        json.merge(fields) { _, new in new }
        return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: json))
    }

    private let workflow: [String: Any] = [
        "task_id": "workflow-task", "run_id": "workflow-run", "name": "Review changes", "running": true,
        "phase": "Verify", "phases": ["Review", "Verify"], "phase_index": 2,
        "agents_done": 5, "agents_started": 6
    ]

    @Test func backgroundTasksKeepRunningWhileTheParentWaits() throws {
        let agent = try session(["background_working": true, "background": [
            ["id": "build", "kind": "shell"], ["id": "monitor", "kind": "monitor"]
        ]])
        let activity = AssistantTrayActivity(sessions: [agent])
        #expect(agent.waitsToBeWoken)
        #expect(activity.isActive)
        #expect(activity.backgroundTasks == 2)
        #expect(activity.workflowCount == 0)
        #expect(activity.subtitle == String(localized: "\(2) background tasks running"))
    }

    @Test func workflowUsesTheHeadersPhaseAndProgressWithoutDoubleCountingItsMarker() throws {
        let agent = try session(["background_working": true, "workflows": [workflow],
                                 "background": [["id": "workflow-task", "kind": "workflow", "run_id": "workflow-run"]]])
        let activity = AssistantTrayActivity(sessions: [agent])
        #expect(activity.isActive)
        #expect(activity.workflowCount == 1)
        #expect(activity.backgroundTasks == 0)
        #expect(activity.workflows.first?.phase == agent.leadWorkflow?.phaseLabel)
        #expect(activity.workflows.first?.counts == agent.leadWorkflow?.counts)
        #expect(activity.workflows.first?.counts == "5/6+")
        #expect(activity.subtitle.contains("Verify 2/2"))
    }

    @Test func subagentAndItsBackgroundMarkerAreOneRunningAgent() throws {
        let agent = try session(["subagents": [["id": "child", "session_id": "agent@child", "running": true]],
                                 "background": [["id": "child", "kind": "agent"]]])
        let activity = AssistantTrayActivity(sessions: [agent])
        #expect(activity.isActive)
        #expect(activity.subagents == 1)
        #expect(activity.backgroundTasks == 0)
    }

    @Test func distinctForegroundAndBackgroundSubagentsBothCount() throws {
        let agent = try session(["subagents": [["id": "foreground", "session_id": "agent@foreground", "running": true]],
                                 "background": [["id": "background", "kind": "agent"]]])
        #expect(AssistantTrayActivity(sessions: [agent]).subagents == 2)
    }

    @Test func endedSubagentMarkerDoesNotKeepTheSpinnerRunning() throws {
        let agent = try session(["subagents": [["id": "child", "session_id": "agent@child", "running": false]],
                                 "background": [["id": "child", "kind": "agent"]]])
        #expect(!AssistantTrayActivity(sessions: [agent]).isActive)
    }

    @Test func endedWorkDoesNotMakeARealWaitLookActive() throws {
        var ended = workflow
        ended["running"] = false
        ended["status"] = "completed"
        let agent = try session(["background_waiting": true, "workflows": [ended],
                                 "subagents": [["id": "child", "session_id": "agent@child", "running": false]],
                                 "background": [["id": "workflow-task", "kind": "workflow", "run_id": "workflow-run"]]])
        #expect(agent.waitsToBeWoken)
        #expect(!AssistantTrayActivity(sessions: [agent]).isActive)
    }

    @Test func aWakeUpWithoutBackgroundWorkKeepsTheClock() throws {
        let agent = try session(["background_waiting": true])
        #expect(agent.waitsToBeWoken)
        #expect(!AssistantTrayActivity(sessions: [agent]).isActive)
        #expect(AssistantTrayActivity(sessions: [agent]).subtitle.isEmpty)
    }

    @Test func boundedSessionSummariesStillShowRunningWork() throws {
        let agent = try session(["background_count": 3, "running_workflows": 1, "running_subagents": 2])
        let activity = AssistantTrayActivity(sessions: [agent])
        #expect(activity.isActive)
        #expect(activity.backgroundTasks == 2)
        #expect(activity.subagents == 2)
        #expect(activity.workflowCount == 1)
    }

    @Test func everyActiveTaskAgentContributesButDuplicateSessionsDoNot() throws {
        let shell = try session(["background": [["id": "build", "kind": "shell"]]], id: "shell")
        let workflowAgent = try session(["workflows": [workflow]], id: "workflow")
        let activity = AssistantTrayActivity(sessions: [shell, workflowAgent, shell])
        #expect(activity.backgroundTasks == 1)
        #expect(activity.workflowCount == 1)
        #expect(activity.workflows.count == 1)
        #expect(activity.subtitle.contains("Verify 2/2"))
    }

    @Test func manualAgentSnapshotChangesWhenWorkflowProgressChanges() throws {
        let agent = try session(["workflows": [workflow], "background_working": true])
        let before = DockAgent.going([agent])
        var update = agent
        update.workflows?[0].agentsDone = 6
        let after = DockAgent.going([update])
        #expect(before != after)
        #expect(before.first?.activity.workflows.first?.counts == "5/6+")
        #expect(after.first?.activity.workflows.first?.counts == "6/6+")
        #expect(after.first?.backgroundWaiting == true)
    }
}
