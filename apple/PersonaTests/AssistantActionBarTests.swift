import SwiftUI
import Testing
@testable import Persona

/// The action bar above the assistant's composer (#3393): what its two trays, Needs You and In Progress, list and count
/// (each thing in one of them at most, finished things in neither), and its entries drawn exactly as low as the dock's
/// capsules they replace.
@MainActor
struct AssistantActionBarTests {
    private func key(_ id: String) -> SessionKey { SessionKey(group: "", machineID: "box", sessionID: id) }

    private func agent(_ id: String, _ state: AgentState, since: TimeInterval = 0) -> DockAgent {
        DockAgent(key: key(id), title: "agent \(id)", state: state, since: Date(timeIntervalSince1970: since))
    }

    private func task(_ id: String, _ state: TaskState, at: TimeInterval, bucket: TodoBucket? = nil, waits: Bool = false,
                      agents: [String] = []) -> ActionTrayContent.TaskRow {
        .init(id: id, state: state, createdAt: Date(timeIntervalSince1970: at), bucket: bucket, waitsOnUser: waits,
              agentKeys: Set(agents.map(key)))
    }

    private func plan(_ id: String, _ state: TaskPlan.State, tasks: Int, at: TimeInterval) -> TaskPlan {
        TaskPlan(id: id, conversation: "c", tasks: (0..<tasks).map { PlanTask(title: "task \($0)", prompt: "p", machine: "box", cwd: "/demo") },
                 state: state, createdAt: Date(timeIntervalSince1970: at))
    }

    private func ids(_ rows: [ActionTrayContent.Row]) -> [String] { rows.map(\.id) }

    /// Every row is in one entry at most, and each count is its entry's rows.
    private func expectOnce(_ content: ActionTrayContent, sourceLocation: SourceLocation = #_sourceLocation) {
        let all = ids(content.needsYou) + ids(content.running)
        #expect(Set(all).count == all.count, "a thing listed twice: \(all)", sourceLocation: sourceLocation)
        #expect(content.count(.needsYou) == content.needsYou.count, sourceLocation: sourceLocation)
        #expect(content.count(.running) == content.running.count, sourceLocation: sourceLocation)
    }

    @Test func twoEntriesOnly() {
        #expect(ActionTray.allCases == [.needsYou, .running])
    }

    /// A task waiting on a decision or a review is only in Needs You; once answered it is back in In Progress.
    @Test func taskWaitingOnTheUserIsOnlyInNeedsYou() {
        let waiting = ActionTrayContent(tasks: [
            task("review", .followup, at: 3, bucket: .open, waits: true),
            task("asks", .needsYou, at: 2, bucket: .open),
            task("works", .working, at: 1, bucket: .open),
        ], own: [])
        #expect(ids(waiting.needsYou) == ["task:asks", "task:review"])
        #expect(ids(waiting.running) == ["task:works"])
        expectOnce(waiting)
        let answered = ActionTrayContent(tasks: [task("review", .working, at: 3, bucket: .open)], own: [])
        #expect(answered.needsYou.isEmpty)
        #expect(ids(answered.running) == ["task:review"])
    }

    /// A running task is only in In Progress, and its agents are folded into its row, whoever started them.
    @Test func runningTaskFoldsItsAgents() {
        let content = ActionTrayContent(tasks: [task("t", .working, at: 1, bucket: .open, agents: ["a1", "a2"])],
                                        own: [agent("a1", .working), agent("a2", .needsYou), agent("solo", .working)])
        #expect(ids(content.running) == ["task:t", "agent:box/solo"])
        #expect(content.needsYou.isEmpty, "a task's agent shows on its own: \(ids(content.needsYou))")
        #expect(content.running.first == .task(id: "t", state: .working, agents: 2))
        expectOnce(content)
    }

    /// A manual agent (no task) that asks is only in Needs You; one at work is a row of In Progress.
    @Test func manualAgentAskingIsOnlyInNeedsYou() {
        let content = ActionTrayContent(tasks: [], own: [agent("asks", .needsYou, since: 1), agent("works", .working, since: 2)])
        #expect(ids(content.needsYou) == ["agent:box/asks"])
        #expect(ids(content.running) == ["agent:box/works"])
        expectOnce(content)
    }

    /// Finished things are in neither entry: done, failed to start long ago, accepted (closed), shelved.
    @Test func finishedThingsAreInNeither() {
        let content = ActionTrayContent(tasks: [
            task("done", .done, at: 1),
            task("failed-old", .failed, at: 2, bucket: .closed),
            task("accepted", .followup, at: 3, bucket: .closed),
            task("shelved", .followup, at: 4, bucket: .shelved),
            task("unseen", .unread, at: 5, bucket: .closed),
        ], own: [], plans: [plan("started", .started, tasks: 1, at: 1), plan("dropped", .dismissed, tasks: 1, at: 2)])
        #expect(content.needsYou.isEmpty)
        #expect(content.running.isEmpty)
        for tray in ActionTray.allCases { #expect(content.count(tray) == 0) }
    }

    /// A start that failed recently waits for a retry: Needs You, by the To-dos page's bucket.
    @Test func failedStartToRetryNeedsYou() {
        let content = ActionTrayContent(tasks: [task("failed", .failed, at: 1, bucket: .open, waits: true)], own: [])
        #expect(ids(content.needsYou) == ["task:failed"])
        #expect(content.running.isEmpty)
    }

    /// Plans waiting for Start, replies waiting for Send and the conversation's own cards are Needs You rows: one row
    /// per plan, however many tasks it holds.
    @Test func plansMessagesAndCardsNeedYou() {
        let at = { Date(timeIntervalSince1970: $0) }
        let content = ActionTrayContent(
            tasks: [task("asks", .needsYou, at: 1, bucket: .open)],
            own: [agent("asks", .needsYou)],
            plans: [plan("later", .open, tasks: 1, at: 20), plan("first", .open, tasks: 3, at: 10), plan("started", .started, tasks: 2, at: 5)],
            messages: [.init(itemID: "m2", feedID: "f2", at: at(9)), .init(itemID: "m1", feedID: "f1", at: at(8))],
            asks: [.init(id: "ix", title: "Send the mail?", createdAt: at(7))])
        #expect(ids(content.needsYou) == ["ask:ix", "task:asks", "agent:box/asks", "plan:first", "plan:later", "message:m1", "message:m2"])
        #expect(content.count(.needsYou) == 7)
        #expect(content.running.isEmpty)
        expectOnce(content)
    }

    @Test func runningListsWorkingFirstThenNewest() {
        let content = ActionTrayContent(tasks: [
            task("follow", .followup, at: 50, bucket: .open),
            task("working-old", .working, at: 10, bucket: .open),
            task("unread", .unread, at: 30, bucket: .open),
            task("working-new", .working, at: 40, bucket: .open),
            task("waiting", .waiting, at: 20, bucket: .open),
        ], own: [agent("solo-old", .working, since: 1), agent("solo-new", .working, since: 2)])
        #expect(ids(content.running) == ["task:working-new", "task:waiting", "task:working-old", "task:unread", "task:follow",
                                         "agent:box/solo-new", "agent:box/solo-old"])
        #expect(content.count(.running) == 7)
    }

    /// Without the To-dos page's bucket, done and failed are closed and the rest open.
    @Test func withoutABucketTheStateDecides() {
        let content = ActionTrayContent(tasks: [task("done", .done, at: 1), task("failed", .failed, at: 2), task("works", .working, at: 3)], own: [])
        #expect(ids(content.running) == ["task:works"])
        #expect(content.needsYou.isEmpty)
    }

    /// The To-dos page's Waiting section and a review the user has to make wait on the user; other sections do not.
    @Test func waitsOnUserReadsTheTriage() {
        let t = { (s: TodoSection, r: TodoTriageRule) in TodoTriage(section: s, rule: r, last: .distantPast) }
        #expect(ActionTrayContent.waitsOnUser(t(.waiting, .ownerDecides), review: nil))
        #expect(ActionTrayContent.waitsOnUser(t(.waiting, .failedStart), review: nil))
        #expect(ActionTrayContent.waitsOnUser(t(.justDone, .finishedUnseen), review: .pendingReview))
        #expect(!ActionTrayContent.waitsOnUser(t(.justDone, .finishedUnseen), review: nil))
        #expect(!ActionTrayContent.waitsOnUser(t(.working, .agentRunning), review: .ongoing))
        #expect(!ActionTrayContent.waitsOnUser(t(.pr, .prOpen), review: .rework))
    }

    @Test func emptyConversationCountsNothing() {
        let content = ActionTrayContent(tasks: [], own: [])
        for tray in ActionTray.allCases {
            #expect(content.rows(tray).isEmpty)
            #expect(content.count(tray) == 0)
        }
    }

    private func height(_ view: some View, _ size: DynamicTypeSize) -> CGFloat {
        let r = ImageRenderer(content: view.fixedSize().environment(\.dynamicTypeSize, size))
        r.scale = 1
        return r.cgImage.map { CGFloat($0.height) } ?? 0
    }

    /// Every look of the bar's entries is exactly as tall as the dock's All and its pills, at every text size.
    @Test(arguments: [DynamicTypeSize.large, .xxxLarge, .accessibility2])
    func entriesAreAsLowAsTheDock(size: DynamicTypeSize) {
        let all = height(Text("All 7").dockCapsule(), size)
        let pill = height(DockPillLabel(title: "Fix the widget", state: .working, waiting: false) {
            StatusDot(state: .working, size: Theme.Size.dot)
        }, size)
        #expect(all > 0)
        #expect(abs(all - pill) <= 1, "All \(all) pt, a pill \(pill) pt")
        for look in [DockCapsule.Look.plain, .selected, .waiting(.needsYou)] {
            let entry = height(HStack(spacing: Theme.Space.xs) { Text("In Progress"); Text("3").monospacedDigit() }.dockCapsule(look), size)
            #expect(abs(entry - all) <= 1, "\(look) is \(entry) pt, All \(all) pt at \(size)")
        }
    }

    /// At the default text size the capsule is the caption's line (footnote, 13 pt) plus 6 pt above and below: 28 pt
    /// for Latin text, nowhere near the 44 pt tap-target buttons the bar must not become.
    @Test func defaultCapsuleIsTheCaptionLineAndItsPadding() {
        let h = height(Text("Needs You").dockCapsule(), .large)
        let line = UIFont.preferredFont(forTextStyle: .footnote, compatibleWith: UITraitCollection(preferredContentSizeCategory: .large)).lineHeight
        #expect(abs(h - (line + 2 * Theme.Space.sm)) <= 1, "\(h) pt for a \(line) pt line")
        #expect(h < Theme.Size.tapTarget)
    }
}
