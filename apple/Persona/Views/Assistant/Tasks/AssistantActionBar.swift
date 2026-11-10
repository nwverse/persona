import SwiftUI

// The row above the assistant's composer on iPhone, iPad and Mac (#3393, docs/assistant/unified-entry.md): two entries,
// Needs You and In Progress, each thing in exactly one of them (#3387). Needs You is everything the user has to act on;
// In Progress is everything going on that does not need them, one row per thing (a task's agents are folded into its
// row and listed in its details). Finished things are in neither: the board (All) has them. Each entry opens a tray
// above the bar, one at a time; the entries are drawn as the old dock's capsules (`DockCapsule`), so the bar is as low.

/// One of the bar's entries, and the tray it opens.
nonisolated enum ActionTray: String, CaseIterable, Identifiable, Sendable {
    case needsYou = "needs-you", running

    var id: String { rawValue }
}

/// What the two trays list. Pure, so the grouping is tested without a hub; it mirrors #3387's rule on the web and
/// Android: a thing that waits for the user is only in Needs You, a thing going on only in In Progress, a finished
/// one in neither, and every count is the rows after that.
nonisolated struct ActionTrayContent: Equatable, Sendable {
    /// A task of the conversation, as it is now.
    struct TaskRow: Equatable, Sendable {
        var id: String
        var state: TaskState
        var createdAt: Date?
        /// Where it stands by the To-dos page's rule (`TodoTriageLogic.bucket`, #3331); nil reads it from `state`.
        var bucket: TodoBucket? = nil
        /// It waits on the user beyond what `state` says: a decision or a review to make (`needs_user`,
        /// `pending_review`, a deciding ask) or a start that failed to retry (`ActionTrayContent.waitsOnUser`).
        var waitsOnUser = false
        /// Its agents: folded into its row, never rows of their own.
        var agentKeys: Set<SessionKey> = []

        var needsYou: Bool { state == .needsYou || waitsOnUser }
    }

    /// A message card whose drafted reply waits for Send (`TasksStore.messageState` open, not put off with Later).
    struct Message: Equatable, Sendable {
        /// The inbox item the reply goes to.
        var itemID: String
        /// The feed item that carries the card.
        var feedID: String
        var at: Date
    }

    /// A card the conversation itself shows (Persona asking, an approval), answered below the bar.
    struct Ask: Equatable, Sendable {
        var id: String
        var title: String
        var createdAt: Date?
    }

    enum Row: Equatable, Sendable, Identifiable {
        case ask(Ask)
        case task(id: String, state: TaskState, agents: Int)
        case agent(DockAgent)
        case plan(id: String)
        case message(Message)

        var id: String {
            switch self {
            case .ask(let a): "ask:" + a.id
            case .task(let id, _, _): "task:" + id
            case .agent(let a): "agent:" + a.key.path
            case .plan(let id): "plan:" + id
            case .message(let m): "message:" + m.itemID
            }
        }
    }

    /// What waits for the user: the conversation's own cards, the tasks that wait on the user (most urgent, then the
    /// newest), the agents of no task that ask, the plans waiting for Start and the replies waiting for Send (oldest first).
    var needsYou: [Row] = []
    /// What goes on without the user: the open tasks (working first, then the newest), then the agents of no task at work.
    var running: [Row] = []

    init() {}

    /// `own`: the agents going on that no task stands for (`TasksStore.agents(of:)`, the main conversation's only);
    /// `plans`: the conversation's plans, the open ones wait for Start.
    init(tasks: [TaskRow], own: [DockAgent], plans: [TaskPlan] = [], messages: [Message] = [], asks: [Ask] = []) {
        // Open, shelved and closed by the To-dos page's rule (#3331): shelved and closed tasks are in neither entry.
        // Without a bucket, closed is done or failed to start.
        let finished: Set<TaskState> = [.done, .failed]
        let open = tasks.filter { ($0.bucket ?? (finished.contains($0.state) ? .closed : .open)) == .open }
        let urgent = { (a: TaskRow, b: TaskRow) -> Bool in
            if a.state.priority != b.state.priority { return a.state.priority < b.state.priority }
            return (a.createdAt ?? .distantPast) > (b.createdAt ?? .distantPast)
        }
        let row = { (t: TaskRow) in Row.task(id: t.id, state: t.state, agents: t.agentKeys.count) }
        // an agent of a task listed here is that task's row already, whoever started it
        let folded = open.reduce(into: Set<SessionKey>()) { $0.formUnion($1.agentKeys) }
        let own = own.filter { !folded.contains($0.key) }
        let oldest = { (a: Date?, b: Date?) in (a ?? .distantPast) < (b ?? .distantPast) }
        let newest = { (a: DockAgent, b: DockAgent) -> Bool in
            if a.since != b.since { return (a.since ?? .distantPast) > (b.since ?? .distantPast) }
            return a.key.path < b.key.path
        }
        needsYou = asks.sorted { oldest($0.createdAt, $1.createdAt) }.map(Row.ask)
            + open.filter(\.needsYou).sorted(by: urgent).map(row)
            + own.filter(\.isWaiting).sorted(by: newest).map(Row.agent)
            + plans.filter { $0.state == .open }.sorted { oldest($0.createdAt, $1.createdAt) }.map { Row.plan(id: $0.id) }
            + messages.sorted { $0.at < $1.at }.map(Row.message)
        running = open.filter { !$0.needsYou }.sorted(by: urgent).map(row)
            + own.filter { !$0.isWaiting }.sorted(by: newest).map(Row.agent)
    }

    /// Whether a task waits on the user by the To-dos page's rule (`TodoTriageLogic.triage`) where its state alone
    /// does not say so: its Waiting section (an agent asks or was cut off, the owner decides, a failed start to retry)
    /// or a review waiting for the user.
    static func waitsOnUser(_ triage: TodoTriage, review: ReviewState?) -> Bool {
        triage.section == .waiting || review == .pendingReview || review == .needsUser
    }

    func rows(_ tray: ActionTray) -> [Row] {
        switch tray {
        case .needsYou: needsYou
        case .running: running
        }
    }

    /// The number its entry shows: its rows.
    func count(_ tray: ActionTray) -> Int { rows(tray).count }
}

extension ActionTray {
    var title: String {
        switch self {
        case .needsYou: String(localized: "Needs You [Action Bar]")
        case .running: String(localized: "In Progress [Action Bar]")
        }
    }

    var empty: String {
        switch self {
        case .needsYou: String(localized: "All Caught Up")
        case .running: String(localized: "Nothing in progress right now")
        }
    }
}

/// The bar and the tray open over it.
struct AssistantActionBar: View {
    var conversation: String
    /// The conversation's own session: its cards (Persona asking, an approval) wait in Needs You.
    var key: SessionKey? = nil
    /// The conversation's model operating a Mac (#1343): its pill after the entries.
    var computer: ComputerActivity? = nil
    @State private var open: ActionTray?
    /// The open tray's rows, measured: the tray is as tall as they are, up to its cap.
    @State private var listHeight: CGFloat = 0
    @State private var removing: TodoItem?
    private var store: TasksStore { .shared }

    var body: some View {
        let content = Self.content(conversation, key: key)
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let open {
                tray(open, content)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            HStack(spacing: Theme.Space.s) {
                ForEach(ActionTray.allCases) { entry($0, count: content.count($0)) }
                if ComputerActivityPill.shows(computer) {
                    ComputerActivityPill(activity: computer)
                }
            }
            .padding(.horizontal, Theme.Space.xs)
            #if !os(macOS)
            .padding(.top, Theme.Space.s)
            .dockChatFade()
            // Drawn under the question and the @ list over it in the footer, so the fade reaches only the chat.
            .zIndex(-1)
            #endif
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .onChange(of: conversation) { open = nil }
        .confirmTaskRemoval($removing)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("action-bar")
    }

    static func content(_ conversation: String, key: SessionKey? = nil) -> ActionTrayContent {
        let store = TasksStore.shared
        let model = AppModel.shared
        // the main conversation watches every task (docs/assistant/unified-entry.md): its entries cover the To-dos page's
        // open to-dos, whatever conversation a to-do came from (#3331)
        let isMain = model.assistantMain?.sessionID == conversation
        // Only tasks: a to-do with agents or handed out by a conversation; the user's own errands are not work going on.
        let tasks = isMain ? store.todos.values.filter { !($0.origin ?? "").isEmpty || !$0.agents.isEmpty } : store.tasks(of: conversation)
        let group = model.hub?.group ?? ""
        let lookup = TodosLogic.makeSessionLookup(model.allSessions(inFront: true))
        let restored = TodoRestores.load()
        let now = Date.now
        let rows = tasks.map { todo in
            let triage = TodoTriageLogic.triage(todo, lookup: lookup, now: now, restoredAt: restored[todo.id])
            let bucket: TodoBucket = TodoTriageLogic.countsAsOpen(todo, triage) ? .open : (triage.section == .shelved ? .shelved : .closed)
            return ActionTrayContent.TaskRow(id: todo.id, state: store.state(todo), createdAt: todo.createdAt, bucket: bucket,
                                             waitsOnUser: ActionTrayContent.waitsOnUser(triage, review: todo.reviewState),
                                             agentKeys: Set(todo.activeAgents.map { SessionKey(group: group, machineID: $0.machineID, sessionID: $0.sessionID) }))
        }
        let messages: [ActionTrayContent.Message] = store.feed(conversation).compactMap { item in
            guard item.kind == .message, let id = item.ref, !store.laterMessages.contains(id),
                  store.messageState(item, itemID: id) == .open else { return nil }
            return ActionTrayContent.Message(itemID: id, feedID: item.id, at: item.at)
        }
        let asks = key.map { model.interactions(for: $0) } ?? []
        let plans = store.plans.values.filter { $0.conversation == conversation }
        return ActionTrayContent(tasks: rows, own: store.agents(of: conversation), plans: Array(plans), messages: messages,
                                 asks: asks.map { .init(id: $0.id, title: $0.askLine(includeToolName: false) ?? $0.title, createdAt: $0.createdAt) })
    }

    private func entry(_ tray: ActionTray, count: Int) -> some View {
        let selected = open == tray
        // Needs You with something in it takes the waiting pill's tint; empty, it is as quiet as In Progress.
        let look: DockCapsule.Look = selected ? .selected : (tray == .needsYou && count > 0 ? .waiting(.needsYou) : .plain)
        return Button {
            Haptics.tap()
            withAnimation(Theme.Motion.state) { open = selected ? nil : tray }
        } label: {
            HStack(spacing: Theme.Space.xs) {
                Text(tray.title)
                if count > 0 { Text(count > 99 ? "99+" : "\(count)").monospacedDigit() }
            }
            .dockCapsule(look)
            .fixedSize()
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("action-bar.\(tray.rawValue)")
    }

    private func tray(_ tray: ActionTray, _ content: ActionTrayContent) -> some View {
        let rows = content.rows(tray)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Theme.Space.s) {
                Text(tray.title).font(Theme.Font.sectionHeader).foregroundStyle(.secondary).lineLimit(1)
                Spacer(minLength: Theme.Space.s)
                // Finished things are in neither tray: the board has everything.
                Button {
                    open = nil
                    store.showBoard = true
                } label: {
                    Text("All").dockCapsule().fixedSize()
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("action-tray.board")
                Button {
                    withAnimation(Theme.Motion.state) { open = nil }
                } label: {
                    ThemedIcon(Theme.Icon.close).font(Theme.Font.labelStrong).foregroundStyle(.secondary)
                        .scaledFrame(width: Theme.Size.badge, height: Theme.Size.badge)
                        .contentShape(.circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Close")
                .accessibilityIdentifier("action-tray.close")
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.s)
            if rows.isEmpty {
                Text(tray.empty).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.bottom, Theme.Space.m)
                    .accessibilityIdentifier("action-tray.empty")
            } else {
                // As tall as its rows; past the cap they scroll inside the tray, the bar stays where it is.
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(rows) { row($0) }
                    }
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { listHeight = $0 }
                }
                .scrollBounceBehavior(.basedOnSize)
                .frame(height: min(listHeight, Theme.Size.box280))
                .padding(.bottom, Theme.Space.xs)
            }
        }
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("action-tray.\(tray.rawValue)")
    }

    @ViewBuilder private func row(_ row: ActionTrayContent.Row) -> some View {
        switch row {
        case .ask(let ask):
            // The card itself is right below the bar: the row closes the tray to show it.
            Button {
                withAnimation(Theme.Motion.state) { open = nil }
            } label: {
                line(title: ask.title, detail: StateName.needsYou.text, waiting: true) {
                    ThemedIcon(Theme.Icon.askFirst).font(Theme.Font.labelStrong).foregroundStyle(AgentState.needsYou.tone.text)
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("action-tray.ask")
        case .task(let id, let state, let agents):
            if let todo = store.todos[id] {
                if let ask = todo.openAsk {
                    BriefAcceptRow(row: ask.row(for: todo), group: AppModel.shared.hub?.group ?? "")
                        .padding(.horizontal, Theme.Space.m)
                } else {
                    Button {
                        open = nil
                        store.detailTask = id
                    } label: {
                        line(title: todo.title, detail: detail(todo, state: state, agents: agents), waiting: state == .needsYou) {
                            TaskMark(state: state)
                        }
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("action-tray.task")
                    .contextMenu { TaskRemoveMenuItem(todo: todo, state: state, pending: $removing) }
                }
            }
        case .agent(let agent):
            Button {
                open = nil
                store.detailAgent = agent.key
            } label: {
                line(title: agent.title, detail: agent.isWaiting ? StateName.needsYou.text : StateName.working.text, waiting: agent.isWaiting) {
                    StatusDot(state: agent.state, size: Theme.Size.dotL)
                }
            }
            .buttonStyle(.plain)
            .accessibilityIdentifier("action-tray.agent")
        case .plan(let id):
            // the plan card itself: Start, Edit and Don't Start are right here
            if let plan = store.plans[id] {
                PlanCard(plan: plan)
                    .padding(.horizontal, Theme.Space.s)
                    .padding(.bottom, Theme.Space.s)
                    .accessibilityIdentifier("action-tray.plan")
            }
        case .message(let message):
            // the message card itself: Send, Edit and Later are right here
            if let item = store.feed(conversation).first(where: { $0.id == message.feedID }) {
                MessageCard(item: item, itemID: message.itemID)
                    .padding(.horizontal, Theme.Space.s)
                    .padding(.bottom, Theme.Space.s)
                    .accessibilityIdentifier("action-tray.message")
            }
        }
    }

    /// A task row's words: its state, what a waiting task waits for (#3274), why it waits on the user when its state
    /// does not say (a review, a decision), and how many agents it has when more than one (they are listed in its details).
    private func detail(_ todo: TodoItem, state: TaskState, agents: Int) -> String {
        let review = todo.reviewState
        let decides = state != .needsYou && state != .failed && (review == .pendingReview || review == .needsUser)
        var parts = [decides ? todo.reviewBadge?.label ?? state.label : state.label]
        if state == .waiting, let waits = store.waitLine(todo) { parts.append(waits) }
        if agents > 1 { parts.append(String(localized: "\(agents) agents")) }
        return parts.joined(separator: " · ")
    }

    private func line<Mark: View>(title: String, detail: String, waiting: Bool, @ViewBuilder mark: () -> Mark) -> some View {
        HStack(spacing: Theme.Space.s) {
            mark().scaledFrame(width: Theme.Size.markM)
            Text(title).font(Theme.Font.body).lineLimit(1)
            Spacer(minLength: Theme.Space.s)
            Text(detail).font(Theme.Font.caption).lineLimit(1).fixedSize()
                .foregroundStyle(waiting ? AnyShapeStyle(AgentState.needsYou.tone.text) : AnyShapeStyle(.secondary))
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .contentShape(.rect)
        .accessibilityElement(children: .combine)
    }
}
