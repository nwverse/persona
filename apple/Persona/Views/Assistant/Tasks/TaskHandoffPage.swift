#if os(iOS)
import SwiftUI

/// Stable across the trade of a pairing token for its device credential, distinct on re-pairing.
/// The navigation stack and reading-position keys contain no authentication material.
struct TaskHandoffPairing: Hashable {
    let id: String
    let fingerprint: String

    init(_ hub: PairedHub) {
        id = hub.id
        fingerprint = hub.tradedPairing ?? DeviceCredentialTrade.fingerprint(hub.config.token)
    }

    func readingKey(_ key: SessionKey, manager: String?) -> String {
        // Length-prefixed components keep machine and manager IDs containing separators distinct.
        "chat.read.assistant." + [id, fingerprint, key.group, manager ?? "", key.machineID, key.sessionID]
            .map { "\($0.utf8.count):\($0)" }.joined()
    }
}

/// A drill-down on this conversation's stack. A fresh pairing can reuse every hub ID.
struct TaskHandoffRoute: Hashable {
    var destination: DispatchedTasks.Destination
    var origin: SessionKey
    var title: String
    var pairing: TaskHandoffPairing?
    var manager: String?
    var generation: UInt

    @MainActor func belongs(to model: AppModel) -> Bool {
        model.assistantCurrent == origin && model.hub?.group == origin.group &&
            model.assistantManagerID == manager && TasksStore.shared.generation == generation &&
            model.registry.primary(origin.group).map(TaskHandoffPairing.init) == pairing
    }
}

struct TaskHandoffReading: Equatable {
    var line: String
    /// The message's top relative to the visible scroll view, including a partially visible message.
    var offset: CGFloat
}

struct TaskHandoffActions {
    var open: (TaskHandoffRoute, TaskHandoffReading?) -> Void
    var takeReading: (SessionKey) -> TaskHandoffReading?
}

extension EnvironmentValues {
    @Entry var taskHandoffActions: TaskHandoffActions?
}

struct TaskHandoffPage: View {
    var route: TaskHandoffRoute
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var showsOrigin = true

    var body: some View {
        if route.belongs(to: model) {
            switch route.destination {
            case .agent(let agent):
                ChatView(key: agent, exactAgent: true)
                    .id(agent)
                    .safeAreaInset(edge: .top, spacing: 0) {
                        if showsOrigin { origin }
                    }
            case .todo(let id):
                TodoDetailView(todoID: id, fromTask: true)
            }
        } else {
            Color.clear.task { dismiss() }
        }
    }

    private var origin: some View {
        HStack(spacing: Theme.Space.s) {
            Button { dismiss() } label: {
                Label("Came from \(route.title)", systemImage: "arrow.uturn.backward")
                    .font(Theme.Font.captionStrong)
                    .multilineTextAlignment(.leading)
                    .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                    .contentShape(Rectangle())
            }
            .accessibilityHint(Text("Return to conversation"))
            .accessibilityIdentifier("task-handoff.return")
            Button { showsOrigin = false } label: {
                Image(systemName: "xmark").font(Theme.Font.captionStrong)
                    .frame(width: 44, height: 44)
                    .contentShape(Rectangle())
            }
            .accessibilityLabel(Text("Hide conversation link"))
            .accessibilityIdentifier("task-handoff.dismiss")
        }
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
        .padding(.leading, Theme.Space.l)
        .padding(.trailing, Theme.Space.xs)
        .background(Theme.pageBackground)
    }
}
#endif
