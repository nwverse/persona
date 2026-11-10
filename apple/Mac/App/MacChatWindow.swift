import SwiftUI

/// A conversation in a window of its own (docs/apple/mac.md「Separate chat windows」: `WindowGroup(for: SessionKey.self)`): the same chat page
/// as in Agents / Persona, so a few agents can sit side by side or on another screen. The window's title is the chat's.
struct MacChatWindow: View {
    static let id = "chat"
    @Environment(AppModel.self) private var model
    var key: SessionKey?

    var body: some View {
        Group {
            if let key {
                NavigationStack {
                    ChatView(key: key).id(key)
                        // The first page of its own window: nothing to go back to (no back arrow).
                        .environment(\.splitDetail, SplitDetail(isFirstPage: true, ownWindow: true))
                }
                .navigationTitle(model.session(key)?.displayTitle ?? String(localized: "Chat"))
            } else {
                EmptyStateView(symbol: Theme.Icon.agents, title: String(localized: "No Chat"), message: String(localized: "The chat this window opened is gone."))
            }
        }
        .frame(minWidth: Theme.Size.macWindowMinWidth, minHeight: Theme.Size.macWindowMinHeight)
        .macLook(model)
        .overlay(alignment: .top) { ToastOverlay() }
    }
}

/// 「在新窗口打开」 in a row's menu (the Agents list, ⌘K's results go through MacShellState).
struct MacOpenInWindowButton: View {
    @Environment(\.openWindow) private var ownOpenWindow
    @Environment(\.shellOpenWindow) private var shellOpenWindow
    /// The window scene's: the page is in a hosting view of its own (`MacPageStack`), whose own one opens nothing.
    private var openWindow: OpenWindowAction { shellOpenWindow ?? ownOpenWindow }
    var key: SessionKey

    var body: some View {
        Button { openWindow(id: MacChatWindow.id, value: key) } label: {
            Label("Open in New Window", systemImage: Theme.Icon.newWindow)
        }
    }
}

/// Opens what the menus ask for (`MacShellState.chatWindow`): Commands have no `openWindow` of their own, the menu
/// bar icon's view (always there, unlike the main window) does it for them.
struct MacWindowOpener: ViewModifier {
    @Environment(\.openWindow) private var openWindow

    func body(content: Content) -> some View {
        content.onChange(of: MacShellState.shared.chatWindow) { _, key in
            guard let key else { return }
            MacShellState.shared.chatWindow = nil
            openWindow(id: MacChatWindow.id, value: key)
        }
        .onChange(of: MacShellState.shared.terminalWindow) { _, key in
            guard let key else { return }
            MacShellState.shared.terminalWindow = nil
            openWindow(id: MacTerminalWindow.id, value: key)
        }
    }
}

/// An Agent's terminal in a window of its own (docs/apple/mac.md「Separate chat windows」: the terminal can open in its own window too), beside its chat or on another
/// screen. The same panel the chat's Terminal sheet shows; dragging it down does nothing here (the window closes with ⌘W).
struct MacTerminalWindow: View {
    static let id = "terminal"
    @Environment(AppModel.self) private var model
    var key: SessionKey?

    var body: some View {
        Group {
            if let key {
                TerminalPanel(key: key, switchTo: nil, grab: TerminalGrab(dragged: { _ in }, released: { _, _ in }, close: { NSApp.keyWindow?.close() }))
                    .padding(Theme.Space.xs)
                    .navigationTitle(String(localized: "Terminal · \(model.session(key)?.displayTitle ?? key.sessionID)"))
            } else {
                EmptyStateView(symbol: Theme.Icon.terminal, title: String(localized: "No Terminal"), message: String(localized: "The agent this window opened is gone."))
            }
        }
        .frame(minWidth: Theme.Size.macWindowMinWidth, minHeight: Theme.Size.macWindowMinHeight)
        .macLook(model)
        .overlay(alignment: .top) { ToastOverlay() }
    }
}
