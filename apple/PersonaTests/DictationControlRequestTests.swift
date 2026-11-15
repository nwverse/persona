import Foundation
import Testing
@testable import Persona

/// Dictate to Persona's navigation and one-shot request, with no network or microphone (`FakeHub`, `FakeDictation`).
@MainActor @Suite(.serialized) struct DictationControlRequestTests {
    @Test func intentOpensANewConversationAndLeavesTheRequestBeforeTheShellExists() throws {
        let model = try Self.model()
        defer { Self.close(model) }
        let previous = SessionKey(machineID: "m", sessionID: "previous")
        model.selectedTab = .chats
        model.shellSettings = true
        model.assistantCurrent = previous

        OpenPersonaDictationIntent.open(model)

        #expect(model.tabRouter == nil) // cold launch: no shell or starter has appeared yet
        #expect(model.selectedTab == .assistant)
        #expect(!model.shellSettings)
        #expect(model.assistantCurrent == nil)
        #expect(model.dictationRequest != nil)
        #expect(UserDefaults.standard.string(forKey: "assistant.current") == "")
    }

    @Test func aColdAssistantRefreshKeepsTheNewConversationAndItsRequest() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: #"{"machine_id":"m","launcher":"claude","cwd":"/","conversations":[{"machine_id":"m","session_id":"main","status":"idle","assistant":true}]}"#)
        let model = try Self.model(hub)
        defer { Self.close(model) }
        OpenPersonaDictationIntent.open(model)
        let request = try #require(model.dictationRequest)

        await model.refreshAssistant()

        #expect(model.assistantCurrent == nil)
        #expect(model.dictationRequest == request)
        #expect(hub.sent("GET", "/api/v1/assistant").count == 1)
    }

    @Test func aRefreshStartedBeforeTheControlCannotRestoreTheMainConversation() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/assistant", json: #"{"machine_id":"m","launcher":"claude","cwd":"/","conversations":[{"machine_id":"m","session_id":"main","status":"idle","assistant":true}]}"#)
        let model = try Self.model(hub)
        defer { Self.close(model) }
        await model.refreshAssistant()
        let refreshStartedAt = model.assistantNewChat

        OpenPersonaDictationIntent.open(model)
        model.selectAssistantMain(after: refreshStartedAt)

        #expect(model.assistantMain != nil)
        #expect(model.assistantCurrent == nil)
        #expect(model.dictationRequest != nil)
    }

    @Test func theControlRoutesToTheStarterThroughAnIPadRouter() throws {
        let model = try Self.model()
        defer { Self.close(model) }
        let pad = PadRouter()
        pad.attach(to: model)
        defer { pad.detach(from: model) }

        OpenPersonaDictationIntent.open(model)

        #expect(pad.request?.page == .newConversation)
        #expect(model.assistantCurrent == nil)
        #expect(model.dictationRequest != nil)
    }

    @Test func theNewStarterTakesTheRequestOnlyOnce() throws {
        let model = try Self.model()
        defer { Self.close(model) }
        OpenPersonaDictationIntent.open(model)
        let request = try #require(model.dictationRequest)
        let dictation = FakeDictation()
        let bar = DictationBarModel()

        for _ in 0..<2 {
            if model.takeDictationRequest(request, uploadDest: nil) {
                bar.start(dictation, hints: [], reduceMotion: true)
            }
        }

        #expect(dictation.starts == 1)
        #expect(bar.on)
        #expect(model.dictationRequest == nil)
        #expect(!model.takeDictationRequest(request, uploadDest: nil))
    }

    @Test func anotherStarterOrAStaleRequestCannotTakeIt() throws {
        let model = try Self.model()
        defer { Self.close(model) }
        OpenPersonaDictationIntent.open(model)
        let request = try #require(model.dictationRequest)
        let dictation = FakeDictation()

        #expect(!model.takeDictationRequest(request, uploadDest: Session.healthSpace))
        #expect(!model.takeDictationRequest(DictationRequest(), uploadDest: nil))
        #expect(dictation.starts == 0)
        #expect(model.dictationRequest == request)
        #expect(model.takeDictationRequest(request, uploadDest: nil))
    }

    @Test func openingAnExistingConversationDropsTheRequestWithoutStarting() throws {
        let model = try Self.model()
        defer { Self.close(model) }
        OpenPersonaDictationIntent.open(model)
        let request = try #require(model.dictationRequest)

        model.assistantCurrent = SessionKey(machineID: "m", sessionID: "existing")

        #expect(model.dictationRequest == nil)
        #expect(!model.takeDictationRequest(request, uploadDest: nil))
    }

    @Test func aFailedStartRestoresFocusAndCannotBeRetried() throws {
        let model = try Self.model()
        defer { Self.close(model) }
        OpenPersonaDictationIntent.open(model)
        let request = try #require(model.dictationRequest)
        let dictation = FakeDictation(failure: "synthetic permission denied")
        let bar = DictationBarModel()
        var focused = false

        #expect(model.takeDictationRequest(request, uploadDest: nil))
        bar.start(dictation, hints: [], reduceMotion: true, onFailure: { focused = true })

        #expect(dictation.starts == 1)
        #expect(focused)
        #expect(!bar.on)
        #expect(model.dictationRequest == nil)
        #expect(!model.takeDictationRequest(request, uploadDest: nil))
    }

    private static func model(_ hub: FakeHub = FakeHub()) throws -> AppModel {
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "dictation-control-" + UUID().uuidString))
        model.linkHooksForTests = (transport: try #require(hub.client.transport), socket: { SyncSocket() })
        model.connectOnly(ServerConfig(url: hub.url, token: "t"))
        return model
    }

    private static func close(_ model: AppModel) {
        for link in model.links { link.live.disconnect() }
    }
}

@MainActor private final class FakeDictation: DictationStarting {
    private(set) var starts = 0
    let failure: String?

    init(failure: String? = nil) {
        self.failure = failure
    }

    func start(hints: [String], update: @escaping (String) -> Void, failed: @escaping (String) -> Void) {
        starts += 1
        if let failure { failed(failure) }
    }
}
