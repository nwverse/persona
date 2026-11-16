import Foundation
import Testing
@testable import Persona

/// Urgent calls (docs/assistant/urgent-call.md, #8734): what the owner's words on a call mean, a ring read from its
/// push, and the settings' built-in rules. Made-up values.
struct UrgentCallTests {
    private let options = ["现在停掉 (Recommended)", "再等 10 分钟", "我自己处理"]

    @Test func anOptionByItsWordsOrItsPlace() {
        #expect(UrgentCallReply.answer("现在停掉吧", options: options, approval: false) == .option("现在停掉 (Recommended)"))
        #expect(UrgentCallReply.answer("再等十分钟", options: options, approval: false) != .option("我自己处理"))
        #expect(UrgentCallReply.answer("我自己处理。", options: options, approval: false) == .option("我自己处理"))
        #expect(UrgentCallReply.answer("第二个", options: options, approval: false) == .option("再等 10 分钟"))
        #expect(UrgentCallReply.answer("the third one", options: ["Stop now", "Wait", "I'll handle it"], approval: false) == .option("I'll handle it"))
        #expect(UrgentCallReply.answer("Roll back to 4.2 please", options: ["Roll back", "Roll back to 4.2"], approval: false) == .option("Roll back to 4.2"))
    }

    @Test func approvalAndHangingUp() {
        #expect(UrgentCallReply.answer("批准", options: [], approval: true) == .allow)
        #expect(UrgentCallReply.answer("不批准", options: [], approval: true) == .deny) // the denial holds the approval word: deny is read first
        #expect(UrgentCallReply.answer("go ahead and approve it", options: [], approval: true) == .allow)
        #expect(UrgentCallReply.answer("没有了，挂断吧", options: options, approval: false) == .hangUp)
        #expect(UrgentCallReply.answer("批准", options: [], approval: false) == .words("批准")) // no approval card: words
        #expect(UrgentCallReply.answer("帮我跟财务说一声", options: [], approval: false) == .words("帮我跟财务说一声"))
    }

    @Test func theReadBack() {
        #expect(UrgentCallReply.readBack("确认") == .confirm)
        #expect(UrgentCallReply.readBack("Yes, confirm") == .confirm)
        #expect(UrgentCallReply.readBack("不对，取消") == .cancel)
        #expect(UrgentCallReply.readBack("yes do it now") == .confirm) // "no" is not in "now"
        #expect(UrgentCallReply.readBack("嗯？") == .cancel) // anything else is not a yes
        #expect(UrgentCallReply.readBack("") == .cancel)
    }

    @Test func aRingFromItsPush() {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        let info: [AnyHashable: Any] = ["urgent_call": ["id": "call_1", "title": "云账单扣费失控", "manager": "assistant",
                                                        "conversation_id": "conv", "attempt": 2],
                                        "iat": now.timeIntervalSince1970 - 5, PushCrypto.groupKey: "home"]
        let ring = UrgentCallRing(info: info, now: now)
        #expect(ring == UrgentCallRing(id: "call_1", title: "云账单扣费失控", manager: "assistant", conversationID: "conv", attempt: 2, group: "home"))
        var old = info
        old["iat"] = now.timeIntervalSince1970 - 600 // a replayed old ring
        #expect(UrgentCallRing(info: old, now: now) == nil)
        #expect(UrgentCallRing(info: ["aps": [:]], now: now) == nil)
    }

    @Test func theCallsLanguage() {
        #expect(UrgentCallVoice.speechLanguage("zh-Hans") == "zh-CN")
        #expect(UrgentCallVoice.speechLanguage("ja") == "ja-JP")
        #expect(UrgentCallVoice.speechLanguage("en") == "en-US")
    }

    @Test func theCallsNameFollowsLockScreenDetails() {
        #expect(UrgentCallCenter.callName(title: "云账单扣费失控", details: true).contains("云账单扣费失控"))
        #expect(!UrgentCallCenter.callName(title: "云账单扣费失控", details: false).contains("云账单"))
        #expect(UrgentCallCenter.callName(title: nil, details: true) == UrgentCallCenter.callName(title: "x", details: false))
    }

    @Test func linesEndAsSentences() {
        #expect(UrgentCallSession.sentence("还在涨", spaced: false) == "还在涨。")
        #expect(UrgentCallSession.sentence("要不要停？", spaced: false) == "要不要停？")
        #expect(UrgentCallSession.sentence("still rising", spaced: true) == "still rising.")
    }

    @Test func builtInRulesComeBack() throws {
        let json = #"{"enabled":true,"rules":[{"id":"r_kids","text":"接孩子相关的事"},{"id":"harm","builtin":true}],"daily_cap":3,"quiet":null,"retry_minutes":3}"#
        let set = try JSONDecoder().decode(UrgentCallSettings.self, from: Data(json.utf8))
        #expect(set.missingBuiltins == ["money_loss", "agent_disaster"])
        let back = set.restoringBuiltins()
        #expect(back.rules.map(\.id) == ["money_loss", "harm", "agent_disaster", "r_kids"])
        #expect(back.missingBuiltins.isEmpty)
        let encoded = try #require(String(data: JSONEncoder().encode(back), encoding: .utf8))
        #expect(encoded.contains(#""daily_cap":3"#) && encoded.contains(#""retry_minutes":3"#))
    }
}
