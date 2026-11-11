import Foundation
import Testing
@testable import Persona

/// The window's sidebar: the iPhone's four tabs plus 「知识库」, 「机器」 and 「待办」 (docs/apple/mac.md「Main window」). The sidebar and the
/// ⌘ order are two orders: ⌘1…⌘6 keep their sections, 「待办」 is ⌘7.
@MainActor
struct MacSectionTests {
    @Test func orderAndShortcuts() {
        #expect(MacSection.allCases.map(\.title) == ["Persona", "Agents", "待办", "消息", "收件箱", "知识库", "机器", "设置"])
        #expect(MacSection.shortcutOrder.map(\.title) == ["Persona", "Agents", "收件箱", "知识库", "机器", "设置", "待办", "消息"])
        #expect(MacSection.assistant.shortcut == 1)
        #expect(MacSection.inbox.shortcut == 3)
        #expect(MacSection.todos.shortcut == 7)
        #expect(MacSection.messages.shortcut == 8)
    }

    /// An old hub has no todos: the window does not offer the section at all.
    @Test func todosAreOfferedOnlyWhenTheHubHasThem() {
        #expect(!MacSection.visible(supportsTodos: false).contains(.todos))
        #expect(MacSection.visible(supportsTodos: true).contains(.todos))
        #expect(MacSection.visible(supportsTodos: false).count == 6)
    }

    /// Messages is the iPhone's extra tab (Settings › Lark › Show Messages on Home, #392): only while it is on, before Inbox as on the phone.
    @Test func messagesAreOfferedOnlyWhileTheirSwitchIsOn() {
        #expect(!MacSection.visible(supportsTodos: true).contains(.messages))
        let on = MacSection.visible(supportsTodos: true, showsMessages: true)
        #expect(on.contains(.messages))
        #expect(on.firstIndex(of: .messages)! + 1 == on.firstIndex(of: .inbox)!)
        #expect(MacSection.messages.tab == nil)
        #expect(MacSection.pick(.messages) == (tab: nil, override: .messages))
    }

    @Test func tabSectionsFollowTheTab() {
        // Nothing picked on the Mac side: the section is the tab's (a notification, ⌘K, a deep link moved it).
        #expect(MacSection.shown(override: nil, tab: .mail) == .inbox)
        #expect(MacSection.shown(override: .machines, tab: .mail) == .machines)
    }

    @Test func pickingASectionMovesTheTabOrTheOverride() {
        #expect(MacSection.pick(.agents) == (tab: .chats, override: nil))
        #expect(MacSection.pick(.knowledge) == (tab: nil, override: .knowledge))
        #expect(MacSection.pick(.machines) == (tab: nil, override: .machines))
    }

    @Test func quickOpenFindsTheNewSections() {
        let t = MacQuickOpen.sections("监控", sessions: [], machines: [], inbox: []).flatMap(\.hits).map(\.title)
        #expect(t == ["机器"])
        let k = MacQuickOpen.sections("knowledge", sessions: [], machines: [], inbox: []).flatMap(\.hits).map(\.title)
        #expect(k == ["知识库"])
        let off = MacQuickOpen.sections("飞书私聊", sessions: [], machines: [], inbox: []).flatMap(\.hits).map(\.title)
        #expect(!off.contains("消息"))
        let m = MacQuickOpen.sections("私聊", sessions: [], machines: [], inbox: [], showsMessages: true).flatMap(\.hits).map(\.title)
        #expect(m == ["消息"])
    }

    /// Knowledge / Machines are not iPhone tabs, so AppModel does not remember them: the shell keeps the pick itself, and a
    /// stored name that is not one of those two (an old build's, a tab's) restores nothing.
    @Test func theMacSidePickIsRemembered() {
        let d = UserDefaults(suiteName: "MacSectionTests-\(UUID())")!
        MacSection.remember(.machines, in: d)
        #expect(MacSection.remembered(in: d) == .machines)
        MacSection.remember(nil, in: d)
        #expect(MacSection.remembered(in: d) == nil)
        d.set("agents", forKey: MacSection.storageKey)
        #expect(MacSection.remembered(in: d) == nil)
    }
}
