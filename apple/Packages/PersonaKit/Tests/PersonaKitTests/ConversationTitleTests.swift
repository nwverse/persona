import Testing
@testable import PersonaKit

/// The drawer dims a hub title's 「MMDD | 类型 | 」 (docs/assistant/assistant.md「Chat titles」); any other title is shown as it is.
struct ConversationTitleTests {
    @Test func splitsTheHubsFormat() {
        #expect(ConversationTitle("1101 | 修复 | 搜索框和顶栏重叠") == ConversationTitle(head: "1101 | 修复 | ", topic: "搜索框和顶栏重叠"))
        #expect(ConversationTitle("0102 | 定时 | 早报")?.topic == "早报")
        // A bar inside the topic stays in the topic.
        #expect(ConversationTitle("1101 | 研究 | A | B")?.topic == "A | B")
    }

    @Test func leavesOtherTitlesAlone() {
        for t in ["帮我约周五吃饭", "1101 修复 搜索框", "11/01 | 修复 | 搜索框", "1101 | 修复 | ", "| 修复 | 搜索框", "abcd | 修复 | x", "1101 | 很长的一个类型 | x"] {
            #expect(ConversationTitle(t) == nil, "\(t)")
        }
    }
}

extension ConversationTitle {
    init(head: String, topic: String) {
        self = ConversationTitle("0000 | x | x")!
        self.head = head
        self.topic = topic
    }
}
