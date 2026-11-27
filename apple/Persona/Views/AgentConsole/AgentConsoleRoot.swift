import SwiftUI

/// The opt-in iPhone Agent console (#17129): Main chat, Agents and Settings. External conversations are a segment in
/// Agents, never a fourth tab. The existing native pages keep their navigation, search and chat routes; only the root
/// and their presentation choices change after a template is applied.
@MainActor @Observable final class AgentConsoleRouter: TabRouter {
    private weak var model: AppModel?
    private var front = AppTab.assistant

    func attach(to model: AppModel) {
        self.model = model
        model.tabRouter = self
        front = model.selectedTabNow == .chats || model.selectedTabNow == .settings ? model.selectedTabNow : .assistant
        model.selectedTab = front
    }

    func detach(from model: AppModel) {
        guard model.tabRouter === self else { return }
        model.tabRouter = nil
    }

    func show(_ page: ShellPage) {
        if page == .messages { model?.selectedTab = .chats }
        else { model?.selectedTab = .assistant }
    }

    func route(_ tab: AppTab, in model: AppModel) {
        switch tab {
        case .assistant, .chats, .settings:
            front = tab
        case .feishu:
            front = .chats
            model.selectedTab = .chats
        case .mail:
            front = .assistant
            model.selectedTab = .assistant
        }
    }
}

struct AgentConsoleRoot: View {
    @Environment(AppModel.self) private var model
    @State private var router = AgentConsoleRouter()
    @State private var agentsMode: AgentConsoleAgentsMode = .running

    var body: some View {
        TabView(selection: Binding(get: { model.selectedTab }, set: { model.selectHomeTab($0) })) {
            Tab(String(localized: "Main Chat"), systemImage: Theme.Icon.ai, value: AppTab.assistant) {
                AssistantView()
                    .modifier(TabActivityScope(tab: .assistant))
            }
            .badge(model.badges.assistant)

            Tab("Agents", systemImage: Theme.Icon.agents, value: AppTab.chats) {
                VStack(spacing: 0) {
                        Picker("Agent conversations", selection: $agentsMode) {
                            Text("Running").tag(AgentConsoleAgentsMode.running)
                            Text("External").tag(AgentConsoleAgentsMode.external)
                        }
                        .pickerStyle(.segmented)
                        .padding(.horizontal, Theme.Space.m)
                        .padding(.vertical, Theme.Space.s)
                        .accessibilityIdentifier("agent-console-running-external")

                        if agentsMode == .running {
                            ChatsView()
                                .environment(\.agentConsole, true)
                        } else {
                            LarkConversationsView()
                        }
                }
                .modifier(TabActivityScope(tab: .chats))
            }
            .badge(model.badges.agents)

            Tab(String(localized: "Settings"), systemImage: Theme.Icon.settings, value: AppTab.settings) {
                SettingsView()
                    .modifier(TabActivityScope(tab: .settings))
            }
        }
        .environment(\.agentConsole, true)
        .onAppear { router.attach(to: model) }
        .onDisappear { router.detach(from: model) }
        .onChange(of: NativeTemplateStore.shared.enabled) { _, enabled in
            if !enabled, model.selectedTab == .settings {
                model.settingsPath = NavigationPath()
                model.selectedTab = .assistant
            }
        }
    }
}

enum AgentConsoleAgentsMode: Hashable { case running, external }

extension EnvironmentValues {
    @Entry var agentConsole = false
}
