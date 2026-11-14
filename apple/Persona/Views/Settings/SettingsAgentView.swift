import SwiftUI

/// Agent tools and automation retain their existing routes for saved links and launch arguments.
struct SettingsAgentView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        SettingsForm {
            Section {
                NavigationLink(value: SettingsPage.claude) {
                    SettingsRow(title: String(localized: "Claude Code Settings"), symbol: Theme.Icon.options, tint: Theme.Hue.orange)
                }
                .lineLimit(1)
                .accessibilityIdentifier("settings-agent-claude")
                NavigationLink(value: SettingsPage.codex) {
                    SettingsRow(title: String(localized: "Codex Settings"), symbol: Theme.Icon.options, tint: Theme.Hue.gray)
                }
                .lineLimit(1)
                .accessibilityIdentifier("settings-agent-codex")
                NavigationLink(value: SettingsPage.triage) {
                    SettingsRow(title: String(localized: "Session Triage"), symbol: Theme.Icon.triage, tint: Theme.ai)
                }
                .lineLimit(1)
                .accessibilityIdentifier("settings-triage")
            } footer: {
                Text("Agent tools and automation for this assistant's computers.")
            }
            readSection
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Agent"))
        .navigationBarTitleDisplayMode(.inline)
        .task { await model.refreshReadSettings() }
    }

    /// Manual mark as read (#7356): the hub's switch, the same on every device; off unless turned on.
    private var readSection: some View {
        let state = model.readSettingsState
        return Section {
            Toggle(isOn: Binding(get: { model.manualRead }, set: { on in Task { await model.setManualRead(on) } })) {
                Text("Manual Mark as Read").lineLimit(1)
            }
            .disabled(state.map { !$0.supported || !$0.canWrite } ?? true)
            .accessibilityIdentifier("settings-manual-read")
        } footer: {
            if state?.supported == false {
                Text("This hub doesn't support it yet. Update the hub to use it.")
            } else if state?.canWrite == false {
                Text("A read-only device can't change this.")
            } else {
                Text("Opening a conversation or an Agent no longer clears Unread; use Mark as Read in the chat's More menu or the row's menu. Off by default. Saved on the hub, so every device matches.")
            }
        }
    }
}

/// A settings page keeps the assistant and hub it opened for, even while a request is suspended.
/// This is only request routing and list ownership; the existing models still own all data and saves.
nonisolated struct AssistantSettingsRequestScope: Equatable, Sendable {
    var group: String
    var manager: String?
    var roster: ManagerRoster?

    @MainActor init(_ app: AppModel) {
        group = app.hub?.group ?? ""
        manager = app.assistantManagerID
        roster = app.managerRoster
    }

    init(group: String, manager: String?, roster: ManagerRoster? = nil) {
        self.group = group
        self.manager = manager
        self.roster = roster
    }

    @MainActor func api(_ app: AppModel) -> APIClient? { app.api(group)?.manager(manager) }

    func machines(_ all: [Machine]) -> [Machine] {
        all.filter { machine in
            guard machine.ref.group == group else { return false }
            guard let roster else { return manager == nil || machine.manager == manager }
            return roster.owner(machine.manager) == roster.owner(manager)
        }
    }

    @MainActor func machines(_ app: AppModel) -> [Machine] { machines(app.machines) }
}
