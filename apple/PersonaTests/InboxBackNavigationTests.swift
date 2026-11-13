import Foundation
import Testing
@testable import Persona

/// The navigation decisions behind Inbox Back: a pushed route carries the item's manager, every nil-push path keeps its
/// previous destination, automatic main-chat selection preserves pushed pages, and a real hub-group change clears them.
@MainActor @Suite(.serialized)
struct InboxBackNavigationTests {
    private static let main = SessionKey(machineID: "server", sessionID: "main")
    private static let other = SessionKey(machineID: "server", sessionID: "other")

    private func item(manager: String?) throws -> InboxItem {
        let stamp = manager.map { #","manager":"\#($0)""# } ?? ""
        return try JSON.decoder.decode(InboxItem.self, from: Data(#"""
        {"id":"mail:a1:ci:77","source":"mail","kind":"mail","account":"a1","account_name":"me@example.com",
         "title":"Build failed","preview":"","count":1\#(stamp)}
        """#.utf8))
    }

    private func keepingDefaults(_ body: () throws -> Void) rethrows {
        let keys = ["assistant.current", "dev.openChat", "app.tab"]
        let saved = keys.map { (key: $0, value: UserDefaults.standard.object(forKey: $0)) }
        defer { for entry in saved { UserDefaults.standard.set(entry.value, forKey: entry.key) } }
        body()
    }

    @Test func automaticMainSelectionKeepsPagesButUserSelectionsDoNot() {
        #expect(AssistantCurrentNavigation.keepsPushedPages(old: nil, new: Self.main, main: Self.main))
        #expect(!AssistantCurrentNavigation.keepsPushedPages(old: nil, new: Self.other, main: Self.main))
        #expect(!AssistantCurrentNavigation.keepsPushedPages(old: Self.other, new: Self.main, main: Self.main))
        #expect(!AssistantCurrentNavigation.keepsPushedPages(old: nil, new: nil, main: nil))
        #expect(!AssistantCurrentNavigation.keepsPushedPages(old: nil, new: nil, main: Self.main))
        #expect(!AssistantCurrentNavigation.keepsPushedPages(old: nil, new: Self.main, main: nil))
    }

    @Test func onlyARealHubGroupChangeClearsPages() {
        #expect(AssistantCurrentNavigation.clearsPushedPages(oldHub: "one", newHub: "two"))
        #expect(!AssistantCurrentNavigation.clearsPushedPages(oldHub: nil, newHub: "one"))
        #expect(!AssistantCurrentNavigation.clearsPushedPages(oldHub: "one", newHub: nil))
        #expect(!AssistantCurrentNavigation.clearsPushedPages(oldHub: "one", newHub: "one"))
        #expect(!AssistantCurrentNavigation.clearsPushedPages(oldHub: nil, newHub: nil))
    }

    /// Both waiting-Agent rows (the inbox list and the new-chat page) share the chain's existing session decision.
    @Test func waitingAgentRowsPushWithoutMovingEitherTab() {
        keepingDefaults {
            let model = AppModel()
            model.selectedTab = .assistant
            model.openChat = nil
            let tab = model.selectedTab, current = model.assistantCurrent
            var pushed: [InboxChatRoute] = []
            InboxEventOpen.session(Self.other, manager: false, owner: nil, model: model) { pushed.append($0) }
            #expect(pushed == [InboxChatRoute(key: model.inHub(Self.other), manager: nil)])
            #expect(model.selectedTab == tab && model.assistantCurrent == current && model.openChat == nil)

            InboxEventOpen.session(Self.other, manager: false, owner: nil, model: model)
            #expect(model.selectedTab == .chats && model.openChat == Self.other)
        }
    }

    /// The direct Thread row and a to-do sheet's Agent row use the same route and the item's manager.
    @Test func threadAgentRowsPushTheItemsManagerOrKeepTheTodoFallback() {
        keepingDefaults {
            let model = AppModel()
            model.openChat = nil
            var pushed: [InboxChatRoute] = []
            InboxLineOpen.agent(Self.other, owner: "m2", model: model) { pushed.append($0) }
            #expect(pushed == [InboxChatRoute(key: model.inHub(Self.other), manager: "m2")])
            #expect(model.openChat == nil)

            InboxLineOpen.agent(Self.other, owner: "m2", model: model)
            #expect(model.selectedTab == .chats && model.openChat == model.inHub(Self.other))
        }
    }

    @Test func connectorSummaryPushesItsManagerOrUsesTheOriginalAssistantOpen() {
        keepingDefaults {
            let model = AppModel()
            let current = model.assistantCurrent, reveal = model.assistantReveal
            var pushed: [InboxChatRoute] = []
            LarkChatView.openSummary(Self.other, owner: "m2", model: model) { pushed.append($0) }
            #expect(pushed == [InboxChatRoute(key: model.inHub(Self.other), manager: "m2")])
            #expect(model.assistantCurrent == current && model.assistantReveal == reveal)

            LarkChatView.openSummary(Self.other, owner: "m2", model: model)
            #expect(model.assistantCurrent == Self.other && model.selectedTab == .assistant)
            #expect(model.assistantReveal == reveal + 1)
            #expect(model.assistantItemManagers[Self.other] == nil, "the old fallback did not name an item manager")
        }
    }

    @Test func handOffViewPushesEveryTargetAndKeepsItsFallback() throws {
        try keepingDefaults {
            let model = AppModel(), item = try item(manager: "m2")
            let agent = SessionKey(machineID: "computer", sessionID: "agent")
            let targets: [InboxHandOffModel.Target] = [.main, .newChat, .agent(agent)]
            for target in targets {
                var pushed: [InboxChatRoute] = []
                var fallback = 0
                InboxHandOffBar.open(sent: (target, Self.other), item: item, model: model,
                                     push: { pushed.append($0) }, fallback: { fallback += 1 })
                #expect(pushed == [InboxChatRoute(key: model.inHub(Self.other), manager: "m2")])
                #expect(fallback == 0)
            }

            var fallback = 0
            for target in targets {
                InboxHandOffBar.open(sent: (target, Self.other), item: item, model: model,
                                     push: nil, fallback: { fallback += 1 })
            }
            #expect(fallback == targets.count)
            InboxHandOffBar.open(sent: nil, item: item, model: model,
                                 push: { _ in Issue.record("nothing sent must not push") }, fallback: { fallback += 1 })
            #expect(fallback == targets.count + 1)
        }
    }
}
