import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Contact-style coding Agent avatar identity and hosting-engine badge resolution tests.
struct CodingAgentAvatarTests {
    private static func session(_ fields: String) -> Session {
        let json = #"{"machine_id":"m","session_id":"s","title":"t","cwd":"/w","status":"idle","state":"idle"\#(fields)}"#
        return try! JSON.decoder.decode(Session.self, from: Data(json.utf8))
    }

    @Test func mapsModelsIncludingGatewayPrefixesToVendorBrands() {
        #expect(CodingAgentIdentity.modelVendor(named: "claude-opus-5-5")?.brand == Theme.Brand.claude)
        #expect(CodingAgentIdentity.modelVendor(named: "anthropic/claude-sonnet-4-6[1m]")?.brand == Theme.Brand.claude)
        #expect(CodingAgentIdentity.modelVendor(named: "gpt-5.4")?.brand == Theme.Brand.openAI)
        #expect(CodingAgentIdentity.modelVendor(named: "o3-mini")?.brand == Theme.Brand.openAI)
        #expect(CodingAgentIdentity.modelVendor(named: "openai/codex-mini")?.brand == Theme.Brand.openAI)
        #expect(CodingAgentIdentity.modelVendor(named: "gemini-2.5-pro")?.brand == Theme.Brand.gemini)
        #expect(CodingAgentIdentity.modelVendor(named: "google-antigravity/gemini-4-argon:high")?.brand == Theme.Brand.gemini)
        #expect(CodingAgentIdentity.modelVendor(named: "deepseek-r1")?.brand == Theme.Brand.dsh)
        #expect(CodingAgentIdentity.modelVendor(named: "moonshot/kimi-k2")?.brand == Theme.Brand.kimi)
        #expect(CodingAgentIdentity.modelVendor(named: "xai/grok-3")?.brand == Theme.Brand.grok)
        #expect(CodingAgentIdentity.modelVendor(named: "glm-4.7") == nil)
        #expect(CodingAgentIdentity.modelVendor(named: "") == nil)
    }

    @Test func resolvesKnownCodingAgentsAndModelOverrides() {
        #expect(CodingAgentIdentity(Self.session(#","agent":"claude","model":"claude-opus-5-5""#)).brand == Theme.Brand.claude)
        #expect(CodingAgentIdentity(Self.session(#","agent":"codex","model":"gpt-5.4""#)).brand == Theme.Brand.openAI)
        #expect(CodingAgentIdentity(Self.session(#","agent":"agy","model":"gemini-3-pro""#)).brand == Theme.Brand.agy)
        #expect(CodingAgentIdentity(Self.session(#","agent":"gemini","model":"gemini-2.5-pro""#)).brand == Theme.Brand.gemini)
        #expect(CodingAgentIdentity(Self.session(#","agent":"cursor","model":"claude-sonnet-4-6""#)).brand == Theme.Brand.cursor)
        #expect(CodingAgentIdentity(Self.session(#","agent":"pi","model":"gpt-5""#)).brand == Theme.Brand.pi)
        #expect(CodingAgentIdentity(Self.session(#","agent":"traex","model":"claude-sonnet-4-6""#)).brand == Theme.Brand.trae)
        #expect(CodingAgentIdentity(Self.session(#","agent":"opencode""#)).brand == Theme.Brand.opencode)
        #expect(CodingAgentIdentity(Self.session(#","agent":"paseo","launcher":"cursor","agent_label":"Cursor""#)).brand == Theme.Brand.cursor)

        // Gateway model override on Claude Code resolves to the model vendor.
        let proxied = CodingAgentIdentity(Self.session(#","agent":"claude","model":"deepseek-r1""#))
        #expect(proxied.brand == Theme.Brand.dsh && proxied.name == "DeepSeek")

        // Custom unmarked harness running a recognized model uses the model vendor brand.
        let custom = CodingAgentIdentity(Self.session(#","agent":"my-cli","agent_label":"My CLI","model":"kimi-k2""#))
        #expect(custom.brand == Theme.Brand.kimi && custom.name == "Kimi")
    }

    @Test func degradesUnknownAgentAndModelToSingleUppercaseLetter() {
        let unknown = CodingAgentIdentity(Self.session(#","agent":"acme-bot","agent_label":"Acme Bot","model":"internal-v2""#))
        #expect(unknown.brand == nil && unknown.letter == "A" && unknown.name == "Acme Bot")

        let omp = CodingAgentIdentity(Self.session(#","agent":"omp","agent_label":"Oh My Pi""#))
        #expect(omp.brand == nil && omp.letter == "O")
    }

    @Test func resolvesHostingEnginesFromHostSpacePaneAndRuntime() {
        for engine in HostingEngine.allKnown {
            let s = Self.session(",\"host\":\"\(engine.id)\"")
            #expect(HostingEngine.of(s) == engine)
            #expect(!engine.displayName.isEmpty)
        }
        #expect(HostingEngine.of(Self.session(#","space_kind":"orca""#)) == .orca)
        #expect(HostingEngine.of(Self.session(#","pane_id":"orca:w1:p2""#)) == .orca)
        #expect(HostingEngine.of(Self.session(#","pane_id":"w9:p1""#)) == .herdr)
        #expect(HostingEngine.of(Self.session(#","pane_id":"herdr-core:t1""#)) == .herdrCore)
        #expect(HostingEngine.of(Self.session(#","source_kind":"runtime","source_instance_id":"orca""#)) == .orca)
        #expect(HostingEngine.of(Self.session(#","space_kind":"codex_app""#)) == nil)
        #expect(HostingEngine.of(Self.session("")) == nil)

        let custom = HostingEngine.of(Self.session(#","host":"nebula""#))
        #expect(custom == .custom("nebula"))
        #expect(custom?.letter == "N" && custom?.displayName == "nebula")
    }

    @Test func spokenLabelCombinesModelAndHostingEngine() {
        let hosted = CodingAgentAvatarModel(Self.session(#","agent":"claude","model":"claude-opus-5-5","host":"orca""#))
        #expect(hosted.spokenLabel == "Claude Code · Orca")

        let unhosted = CodingAgentAvatarModel(Self.session(#","agent":"codex","model":"gpt-5.4""#))
        #expect(unhosted.spokenLabel == "Codex")

        let suppressed = CodingAgentAvatarModel(Self.session(#","agent":"claude","host":"herdr""#), showEngineBadge: false)
        #expect(suppressed.engine == nil && suppressed.spokenLabel == "Claude Code")
    }
}
