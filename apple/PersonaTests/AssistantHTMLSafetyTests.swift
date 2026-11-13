import Testing
@testable import Persona

struct AssistantHTMLSafetyTests {
    @Test func executionRequiresPlatformAcceptance() {
        #if os(visionOS)
        #expect(!AssistantHTMLSafety.executionVerified)
        #elseif os(iOS)
        #expect(AssistantHTMLSafety.executionVerified)
        #endif
    }
}
