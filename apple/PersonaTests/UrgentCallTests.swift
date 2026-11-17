import AVFoundation
import Foundation
import LiveCommunicationKit
import PushKit
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

/// A call confirmed by voice answers its card through the same resolve a tap on the card uses (#8734): the card is then
/// answered everywhere, as if tapped. A read-back the owner turns down answers nothing.
@MainActor
struct UrgentCallCardTests {
    /// A voice that says nothing and hears the owner's lines in turn, then silence.
    final class Owner: UrgentCallSpeech {
        var lines: [String]
        var said: [String] = []
        init(_ lines: [String]) { self.lines = lines }
        func say(_ text: String) async { said.append(text) }
        func listen() async -> String? { lines.isEmpty ? nil : lines.removeFirst() }
        func stop() {}
        var canHear: Bool { true }
    }

    private let ring = UrgentCallRing(id: "call_1", title: "云账单扣费失控", manager: "", conversationID: "conv", attempt: 1, group: "urgent-test")

    /// Runs a call with `card` until it hangs up; returns what was answered.
    private func call(_ card: Interaction, owner: [String]) async -> [(id: String, resolution: Resolution)] {
        var answered: [(id: String, resolution: Resolution)] = []
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            let session = UrgentCallSession(ring: ring, voice: Owner(owner), card: card,
                                            resolve: { ix, r in answered.append((ix.id, r)) }, ended: { _ in done.resume() })
            session.start()
        }
        return answered
    }

    @Test func aConfirmedOptionAnswersTheQuestionCard() async {
        var card = Interaction(id: "ix_1", machineID: "server", sessionID: "conv", kind: .question)
        card.questions = [Question(question: "要不要现在停掉？", header: nil, multiSelect: false,
                                   options: [QuestionOption(label: "现在停掉 (Recommended)"), QuestionOption(label: "再等 10 分钟")])]
        let answered = await call(card, owner: ["现在停掉", "确认"])
        #expect(answered.count == 1)
        #expect(answered.first?.id == "ix_1")
        #expect(answered.first?.resolution == Resolution(answers: ["要不要现在停掉？": "现在停掉 (Recommended)"]))
    }

    @Test func aConfirmedApprovalAllowsThePermissionCard() async {
        let card = Interaction(id: "ix_2", machineID: "server", sessionID: "conv", kind: .permission)
        let answered = await call(card, owner: ["批准", "确认"])
        #expect(answered.map(\.resolution) == [Resolution(allow: true)])
    }

    @Test func aReadBackTurnedDownAnswersNothing() async {
        var card = Interaction(id: "ix_3", machineID: "server", sessionID: "conv", kind: .question)
        card.questions = [Question(question: "要不要现在停掉？", header: nil, multiSelect: false, options: [QuestionOption(label: "现在停掉")])]
        let answered = await call(card, owner: ["现在停掉", "不对"])
        #expect(answered.isEmpty)
    }
}

/// System frameworks call these delegates and callbacks on queues of their own, so none may be the main actor's (this
/// target's default): an Objective-C protocol's witness or a closure handed to an Objective-C API traps in Swift's
/// runtime isolation check when called off the main thread. Each delegate is called here through its protocol from a
/// background queue, as the system does; and launching makes no LiveCommunicationKit manager at all.
struct UrgentCallIsolationTests {
    private func offMain(_ body: @escaping @Sendable () -> Void) async {
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            DispatchQueue.global(qos: .userInitiated).async {
                body()
                done.resume()
            }
        }
    }

    @Test func liveCommunicationKitCallsItsDelegateOffTheMainThread() async {
        let config = ConversationManager.Configuration(ringtoneName: nil, iconTemplateImageData: nil, maximumConversationGroups: 1,
                                                       maximumConversationsPerConversationGroup: 1, includesConversationInRecents: false,
                                                       supportsVideo: false, supportedHandleTypes: [.generic])
        nonisolated(unsafe) let manager = ConversationManager(configuration: config)
        nonisolated(unsafe) let delegate: any ConversationManagerDelegate = UrgentCallLCKDelegate()
        await offMain {
            delegate.conversationManagerDidBegin(manager)
            delegate.conversationManagerDidReset(manager)
            delegate.conversationManager(manager, timedOutPerforming: JoinConversationAction(conversationUUID: UUID()))
            delegate.conversationManager(manager, didDeactivate: AVAudioSession.sharedInstance())
        }
    }

    @Test @MainActor func pushKitCallsItsDelegateOffTheMainThread() async {
        nonisolated(unsafe) let registry = PKPushRegistry(queue: nil)
        nonisolated(unsafe) let delegate: any PKPushRegistryDelegate = UrgentCallPush.shared
        await offMain { delegate.pushRegistry?(registry, didInvalidatePushTokenFor: .voIP) }
        #expect(UrgentCallPush.shared.token == nil)
    }

    /// The call page's cover, on every launch, reads only the presenter: no call center's LiveCommunicationKit manager.
    @Test @MainActor func launchingMakesNoCallManager() {
        _ = UrgentCallPresenter.shared.session
        #expect(!UrgentCallCenter.shared.managerMade)
    }

    /// Every system callback in the call code is written `nonisolated`, so a new one cannot quietly be the main actor's.
    @Test func callCallbacksAreNotTheMainActors() throws {
        let calls = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent().appending(path: "Persona/Calls")
        let files = try FileManager.default.contentsOfDirectory(at: calls, includingPropertiesForKeys: nil).filter { $0.pathExtension == "swift" }
        var seen = 0
        for file in files {
            for line in try String(contentsOf: file, encoding: .utf8).split(separator: "\n")
            where line.contains("func conversationManager") || line.contains("func pushRegistry") {
                seen += 1
                #expect(line.contains("nonisolated"), "\(file.lastPathComponent): \(line.trimmingCharacters(in: .whitespaces))")
            }
        }
        #expect(seen >= 10)
    }
}
