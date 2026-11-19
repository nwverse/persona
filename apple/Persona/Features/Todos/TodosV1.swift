import SwiftUI

extension TodosV1 {
    /// Whether this build may show V1 on this device: once released (#10465's switch-on), on the iPhone and iPad; before
    /// that, in a DEBUG build launched with `-showTodosV1 YES`, for screenshots and tests. Either way only where the hub
    /// lists `todo_view`: the switch stands for the release, not for the hub.
    nonisolated static var allowedHere: Bool { (released || shownByLaunch) && platform }

    #if DEBUG
    nonisolated static var shownByLaunch: Bool { UserDefaults.standard.bool(forKey: "showTodosV1") }
    #else
    nonisolated static let shownByLaunch = false
    #endif
}

extension TodoStore {
    /// The To-dos row's number in the drawer and the sidebar, and the folded Pages heading's: N on the V1 path, the to-dos
    /// that need the owner, as the list's bar and Needs you header say it (`countsSource`, DESIGN_SYSTEM 4.8); today's
    /// open count otherwise (`badgeCount`).
    var sidebarCount: Int { TodosV1.shared.isOn ? countsSource.needsYou : badgeCount }
}

#if os(iOS)
/// The To-dos place's page: the V1 list while the gate is on, else today's list (`TodosView`, frozen). Which one is
/// decided as the page appears: a hello that turns V1 on while the page is up (a hub whose manager connects late) takes
/// effect the next time it appears, never under the owner's eyes (docs/apple/ios.md "To-dos (V1, #10465)").
struct TodosPlacePage: View {
    /// What the page drew when it last appeared; nil before its first appearance, when the gate decides.
    @State private var drawsV1: Bool?

    var body: some View {
        let v1 = drawsV1 ?? TodosV1.shared.isOn
        Group {
            if v1 { TodoListView() } else { TodosView() }
        }
        .onAppear { drawsV1 = TodosV1.shared.isOn }
    }
}
#endif
