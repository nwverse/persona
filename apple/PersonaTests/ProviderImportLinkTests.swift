import Foundation
import Testing
@testable import Persona

/// Provider import links (#5428): exact deep-link matching and the shared draft's fields carried into the existing wizard.
@MainActor
struct ProviderImportLinkTests {
    @Test func onlyThePersonaProviderImportRouteMatches() {
        #expect(ProviderImportLink.text(URL(string: "persona://provider/import?v=1&data=fixture")!) == "persona://provider/import?v=1&data=fixture")
        for text in ["cindy://provider/import?v=1&data=fixture", "https://provider/import?v=1&data=fixture",
                     "persona://provider@evil.invalid/import?v=1&data=fixture", "persona://provider/other?v=1&data=fixture"] {
            #expect(ProviderImportLink.text(URL(string: text)!) == nil)
        }
    }

    @Test func anImportDraftPrefillsTheWholeConnectionWithoutSaving() throws {
        let json = #"{"format":"link","drafts":[{"provider":{"kind":"api","api":"chat","agent":"codex","name":"Example Gateway","display_name":"Example Gateway","base_url":"https://example.invalid/v1","auth":"bearer","api_key":"FAKE-EXAMPLE-KEY","model":"m1","models":[{"id":"m1"},{"id":"m2"}],"args":["--verbose"],"custom_model":"custom","custom_model_name":"Custom","custom_model_description":"Synthetic"}}]}"#
        let result = try JSON.decoder.decode(ProviderImportResult.self, from: Data(json.utf8))
        let provider = try #require(result.drafts?.first?.provider)
        let wizard = ProviderWizardModel(deps: .init(api: { nil }))
        wizard.applyImport(provider)
        #expect(wizard.name == "example-gateway")
        #expect(wizard.baseURL == "https://example.invalid/v1" && wizard.defaultModel == "m1")
        #expect(wizard.models.map(\.id) == ["m1", "m2"] && wizard.isAPI)
        let input = wizard.input
        #expect(input.kind == "api" && input.api == "chat" && input.agent == "codex" && input.displayName == "Example Gateway")
        #expect(input.args == ["--verbose"] && input.customModel == "custom")
        #expect(input.customModelName == "Custom" && input.customModelDescription == "Synthetic")
        #expect(input.apiKey == "FAKE-EXAMPLE-KEY" && input.auth == "bearer")
    }

    @Test func aSixtyCharacterDisplayNameIsAValidProviderID() throws {
        let displayName = String(repeating: "a", count: 60) // cindy's link limit; Persona has no 40-character provider-ID limit.
        let provider = try JSON.decoder.decode(ProviderImportProvider.self, from: Data(#"{"display_name":"\#(displayName)"}"#.utf8))
        let wizard = ProviderWizardModel(deps: .init(api: { nil }))
        wizard.applyImport(provider)
        #expect(wizard.name == displayName)
        #expect(wizard.nameProblem == nil)
    }
}
