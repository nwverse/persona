import Foundation
import Testing
@testable import Persona

/// A stopped Agent's manager work card can still be put off or dismissed, which send the Agent nothing; Continue, a typed
/// answer and every other card stay closed (#6839, web #6753).
@MainActor
struct StoppedWorkCardTests {
    nonisolated static let ask = "模型请求失败了，要怎么处理？" // i18n-ignore: a hub card's words

    private func card(_ id: String = "work", tool: String? = "persona_triage", kind: InteractionKind = .question,
                      multiSelect: Bool = false) -> Interaction {
        let options = [QuestionOption(label: "继续"), QuestionOption(label: "稍后再说", action: "defer"), // i18n-ignore
                       QuestionOption(label: "不用管了", action: "defer")] // i18n-ignore
        return Interaction(
            id: id, machineID: "m", sessionID: "s", sessionTitle: nil, cwd: nil,
            kind: kind, createdAt: Date(timeIntervalSince1970: 1), expiresAt: nil, toolName: tool,
            toolUseID: nil, questions: kind == .question ? [Question(question: Self.ask, header: nil, multiSelect: multiSelect, options: options)] : nil,
            plan: nil, toolInput: nil, summary: nil, screen: nil, dialog: nil
        )
    }

    /// An Agent that has stopped: the node says it can't take approvals any more.
    private func stopped() -> Session {
        let json = #"{"machine_id":"m","session_id":"s","title":"s","cwd":"/tmp/p","status":"idle","state":"needs_you","source_kind":"claude","capabilities":{"approve":false}}"#
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) } catch { fatalError("bad fixture: \(error)") }
    }

    @Test func decodesTheDeferMarker() throws {
        let json = #"{"label":"稍后再说","action":"defer"}"# // i18n-ignore
        #expect(try JSON.decoder.decode(QuestionOption.self, from: Data(json.utf8)).action == "defer")
        #expect(try JSON.decoder.decode(QuestionOption.self, from: Data(#"{"label":"继续"}"#.utf8)).action == nil) // i18n-ignore
    }

    @Test func onlyDeferChoicesAndDismissAreLocal() {
        let ix = card()
        #expect(ix.isManagerWork)
        #expect(ix.isLocalResolution(.dismissed))
        #expect(ix.isLocalResolution(Resolution(answers: [Self.ask: "稍后再说"]))) // i18n-ignore
        #expect(!ix.isLocalResolution(Resolution(answers: [Self.ask: "继续"])), "Continue messages the Agent") // i18n-ignore
        #expect(!ix.isLocalResolution(Resolution(answers: [Self.ask: "帮我重试一下"])), "a typed answer goes to the Agent") // i18n-ignore
        #expect(!ix.isLocalResolution(Resolution(answers: [:])))
        #expect(!ix.isLocalResolution(Resolution(message: "x", dismiss: true)), "a dismiss carrying more is not bare")
    }

    @Test func otherCardsHaveNoLocalAnswers() {
        #expect(!card(tool: nil).isLocalResolution(Resolution(answers: [Self.ask: "稍后再说"])), "an Agent's own question") // i18n-ignore
        #expect(!card(tool: nil).isLocalResolution(.dismissed))
        #expect(!card(kind: .permission).isLocalResolution(.dismissed), "a permission card stays closed")
        #expect(!card(multiSelect: true).isLocalResolution(Resolution(answers: [Self.ask: "稍后再说"]))) // i18n-ignore
    }

    @Test(arguments: [Resolution.dismissed, Resolution(answers: [StoppedWorkCardTests.ask: "稍后再说"])]) // i18n-ignore
    func aStoppedAgentsWorkCardIsPutAway(_ r: Resolution) async {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/interactions/work/resolve", json: #"{"ok":true}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        model.applySessions([MachineRef(group: "", id: "m"): [stopped()]])
        let ix = card()
        model.replaceInteractions("", [ix])
        #expect(!model.canAnswer(ix))
        await model.resolve(ix, r)
        #expect(hub.sent("POST", "/api/v1/interactions/work/resolve").count == 1)
        #expect(model.pendingInteractions.isEmpty)
    }

    @Test func continueOnAStoppedAgentSendsNothing() async {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/interactions/work/resolve", json: #"{"ok":true}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        model.applySessions([MachineRef(group: "", id: "m"): [stopped()]])
        let ix = card()
        model.replaceInteractions("", [ix])
        await model.resolve(ix, Resolution(answers: [Self.ask: "继续"])) // i18n-ignore
        #expect(hub.sent("POST", "/api/v1/interactions/work/resolve").isEmpty)
    }
}
