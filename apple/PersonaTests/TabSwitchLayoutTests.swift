#if os(iOS)
import Foundation
import SwiftUI
import Testing
@testable import Persona

/// The tabs' switch on iPhone Duo in the Separate layout (#4231, `TabSwitchLayout`): one upright capsule of the pages'
/// symbols at the bottom-trailing corner, at the foot of the system's side column where the bars run down the side and
/// floating over the page where they run across the top; what sits at the page's foot leaves it room while it shows,
/// and none while the keyboard is up. Settings › Appearance › Sidebar (Merged / Separate) chooses it, kept on this device.
@MainActor
struct TabSwitchLayoutTests {
    /// Where the switch stands in each posture of the open Duo, and nowhere on the iPad, which keeps the system's tab bar.
    @Test func itStandsWhereTheSystemsBarsAre() {
        #expect(TabSwitchLayout.placement(phone: true, barEdge: nil) == .floating) // open, upright: bars across the top
        #expect(TabSwitchLayout.placement(phone: true, barEdge: .trailing) == .rail(.trailing)) // open, on its side
        #expect(TabSwitchLayout.placement(phone: true, barEdge: .leading) == .rail(.leading))
        #expect(TabSwitchLayout.placement(phone: false, barEdge: nil) == nil) // the iPad
        #expect(TabSwitchLayout.placement(phone: false, barEdge: .trailing) == nil)
    }

    /// The design's capsule (#4206, the 11-12 prototype without names): 44 pt segments, 52 pt wide, 140 pt tall with
    /// Messages and 96 pt without, at every text size (a bar of fixed items, DESIGN_SYSTEM.md 6.2).
    @Test func theCapsulesSize() {
        #expect(TabSwitchLayout.width == Theme.Size.tapTarget + Theme.Space.xs * 2)
        #expect(TabSwitchLayout.width == 52)
        #expect(TabSwitchLayout.height(items: 3) == 140)
        #expect(TabSwitchLayout.height(items: 2) == 96)
        #expect(TabSwitchLayout.items(messages: true) == 3)
        #expect(TabSwitchLayout.items(messages: false) == 2)
        #expect(Theme.Size.tapTarget >= 44) // each segment is a whole tap target
        #expect(Theme.Shell.switchTypeSize == .large) // the default text size, as the system's tab bar keeps
        // Floating: the footer's own margins, so its foot is level with the composer's.
        #expect(TabSwitchLayout.trailingInset == Theme.Space.m)
        #expect(TabSwitchLayout.bottomInset == Theme.Space.s)
    }

    /// The composer and the rows at the page's foot leave the floating switch its width and a gap while it shows; with
    /// the keyboard up the switch is away and they take the whole width back; elsewhere they leave nothing.
    @Test func theRoomBesideTheFloatingSwitch() {
        #expect(TabSwitchLayout.trailingRoom(floats: true, keyboardUp: false) == 60)
        #expect(TabSwitchLayout.trailingRoom(floats: true, keyboardUp: true) == 0)
        #expect(TabSwitchLayout.trailingRoom(floats: false, keyboardUp: false) == 0)
        #expect(TabSwitchLayout.trailingRoom(floats: false, keyboardUp: true) == 0)
        // Outside the rows' own 12 pt margin, the room keeps them a gap clear of the capsule 12 pt from the edge.
        let rowsEnd = TabSwitchLayout.trailingInset + TabSwitchLayout.trailingRoom(floats: true, keyboardUp: false)
        let capsuleStart = TabSwitchLayout.trailingInset + TabSwitchLayout.width
        #expect(rowsEnd - capsuleStart == Theme.Space.s)
    }

    /// A list's end scrolls clear of the floating switch: its height, its inset from the foot and a gap.
    @Test func theRoomAtAListsEnd() {
        #expect(TabSwitchLayout.listRoom(floats: true, items: 3) == 156)
        #expect(TabSwitchLayout.listRoom(floats: true, items: 2) == 112)
        #expect(TabSwitchLayout.listRoom(floats: false, items: 3) == 0)
    }

    /// The counts hang off the segment's corner toward the page: leftward with the switch at the right (the side column
    /// on the right, or floating), rightward in a side column on the left.
    @Test func theCountsHangTowardThePage() {
        #expect(UprightBadge.offset(railEdge: nil).width < 0)
        #expect(UprightBadge.offset(railEdge: .trailing).width < 0)
        #expect(UprightBadge.offset(railEdge: .leading).width > 0)
        #expect(UprightBadge.offset(railEdge: nil).height < 0) // above the segment's symbol
    }

    /// The choice is Settings › Appearance › Sidebar's, this device's own: picking Separate is written at once and read
    /// back by a new reader of the same defaults, as the app's root after a relaunch; nothing picked stays Merged on the
    /// open Duo and Separate on its outer display (#4206's default, unchanged).
    @Test func theChoiceIsKept() throws {
        let suite = "persona.tests.tab-switch.\(UUID().uuidString)"
        let store = try #require(UserDefaults(suiteName: suite))
        defer { store.removePersistentDomain(forName: suite) }
        @StoredValue(NavigationLayout.key, store: store) var picked = ""
        #expect(NavigationLayout.effective(picked: picked, regular: true) == .sidebar) // the open Duo: Merged
        #expect(NavigationLayout.effective(picked: picked, regular: false) == .separate) // its outer display
        picked = NavigationLayout.separate.rawValue
        #expect(store.string(forKey: NavigationLayout.key) == "separate")
        let relaunched = try #require(UserDefaults(suiteName: suite))
        @StoredValue(NavigationLayout.key, store: relaunched) var again = ""
        #expect(again == "separate")
        // A pick holds on both displays: folding and unfolding keep the tabs.
        #expect(NavigationLayout.effective(picked: again, regular: true) == .separate)
        #expect(NavigationLayout.effective(picked: again, regular: false) == .separate)
        picked = NavigationLayout.sidebar.rawValue
        #expect(store.string(forKey: NavigationLayout.key) == "sidebar")
    }

    private static let apple = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()

    /// What sits at a page's foot under the open Duo's floating switch leaves it room (`tabSwitchRoom`), and the main lists
    /// keep room at their end (`tabSwitchListRoom`): one taken off covers the composer's send button, the to-do page's
    /// Done or a list's last row.
    @Test(arguments: [
        ("Persona/Views/Chat/ChatView.swift", ".tabSwitchRoom()"), // the chats' footer and Jump to Latest
        ("Persona/Views/Shell/ShellHome.swift", ".tabSwitchRoom(padding:"), // a new conversation's composer and Needs You
        ("Persona/DesignSystem/FloatingPill.swift", ".tabSwitchRoom()"), // the to-do page's buttons
        ("Persona/Views/Inbox/Lark/LarkChatView.swift", ".tabSwitchRoom()"),
        ("Persona/Views/Inbox/InboxHandOffBar.swift", ".tabSwitchRoom()"),
        ("Persona/Views/Inbox/ConnectorThread/ThreadComposer.swift", ".tabSwitchRoom()"),
        ("Persona/Recording/MeetingPage.swift", ".tabSwitchRoom()"), // a meeting's bars and Jump to Latest
        ("Persona/Views/Machines/BrowserLoginsListView.swift", ".tabSwitchBottomBarRoom(shown: selecting)"),
        ("Persona/Views/Inbox/InboxView.swift", ".tabSwitchListRoom()"),
        ("Persona/Views/Todos/TodosView.swift", ".tabSwitchListRoom()"),
        ("Persona/Views/Inbox/Lark/LarkConversationsView.swift", ".tabSwitchListRoom()"),
    ])
    func thePagesFootLeavesTheSwitchRoom(file: String, modifier: String) throws {
        let source = try String(contentsOf: Self.apple.appending(path: file), encoding: .utf8)
        #expect(source.contains(modifier), "\(file) no longer leaves the open Duo's floating tabs' switch room")
    }

    /// The tabs hide the system's tab bar on a phone (the open Duo) and the switch stands in its place; every tab's page
    /// hides it, or the bar comes back on that tab under the switch.
    @Test func everyTabHidesTheSystemsBarOnAPhone() throws {
        let source = try String(contentsOf: Self.apple.appending(path: "Persona/Views/RootView.swift"), encoding: .utf8)
        #expect(source.contains(".modifier(TabSwitchOverlay())"))
        for tab in ["struct AssistantRootTab", "struct AgentsRootTab", "struct FeishuRootTab"] {
            let start = try #require(source.range(of: tab), "\(tab) not in RootView.swift")
            let rest = source[start.upperBound...]
            let body = rest[..<(rest.range(of: "\n}\n")?.upperBound ?? rest.endIndex)]
            #expect(body.contains("PhoneTabBarHidden()"), "\(tab) shows the system's tab bar under the switch")
        }
    }
}
#endif
