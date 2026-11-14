import SwiftUI

/// Settings › Appearance › Sidebar (#4206), where the app has a sidebar beside its pages (iPad, the open iPhone Duo).
/// Merged: one sidebar with Persona's places, its chats and the Agents list, the page beside it. Separate: as before
/// #4206, each tab (Persona, Agents, Messages) its own page with its own sidebar, shown or hidden on its own, the tab bar
/// switching between them. Both slide their sidebar at one width (`SplitColumnControl`).
enum SidebarMode: String, CaseIterable, Identifiable {
    case merged, separate

    static let key = "sidebar.mode"

    var id: String { rawValue }

    var title: String {
        switch self {
        case .merged: String(localized: "Merged [Sidebar]", comment: "Settings › Appearance › Sidebar: one sidebar for Persona, its chats and Agents")
        case .separate: String(localized: "Separate [Sidebar]", comment: "Settings › Appearance › Sidebar: each tab its own sidebar, as before")
        }
    }

    var summary: String {
        switch self {
        case .merged: String(localized: "Persona, your chats and Agents in one sidebar, the page beside it.")
        case .separate: String(localized: "Persona, Agents and Messages each a page of its own with its own sidebar; the tab bar switches between them.")
        }
    }
}

extension EnvironmentValues {
    /// The one sidebar is the layout (iPad, iPhone Duo open; `RootTabs`, Merged). False elsewhere, and on the Mac, whose
    /// shared pages read it too (it lived with the iOS-only one sidebar and the Mac stopped building).
    @Entry var oneSidebar = false
}

#if os(iOS)
extension SidebarMode {
    /// Where the pages have a sidebar beside them: the iPad, and a phone whose window is wide (the open iPhone Duo). Only
    /// there does Settings offer the choice. Read when Settings draws the row, not observed: an observable written from
    /// `RootView` as the width settled redrew the one sidebar's rows on every layout of a turn, and a Debug build on the
    /// phone ran out of the scene's 10 seconds (0x8BADF00D).
    @MainActor static var available: Bool {
        if UIDevice.current.userInterfaceIdiom == .pad { return true }
        return UIApplication.shared.connectedScenes.contains { scene in
            (scene as? UIWindowScene)?.traitCollection.horizontalSizeClass == .regular
        }
    }
}
#endif

#if os(iOS)
/// Settings › Appearance › Sidebar: Merged or Separate, each with what it does.
struct SidebarModeSettingsPage: View {
    @StoredValue(SidebarMode.key) private var mode: SidebarMode = .merged

    var body: some View {
        SettingsForm {
            Section {
                ForEach(SidebarMode.allCases) { choice in
                    Button { mode = choice } label: {
                        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text(choice.title).font(Theme.Font.body).lineLimit(1)
                                Text(choice.summary).font(Theme.Font.caption).foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                            Spacer(minLength: Theme.Space.s)
                            if mode == choice {
                                ThemedIcon(Theme.Icon.done).font(Theme.Font.bodyStrong).foregroundStyle(Theme.accent)
                            }
                        }
                        .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("sidebar-mode-" + choice.rawValue)
                    .accessibilityAddTraits(mode == choice ? .isSelected : [])
                }
            }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Sidebar [Appearance]"))
        .navigationBarTitleDisplayMode(.inline)
    }
}
#endif
