import Foundation
import Testing
@testable import Persona

struct ExtensionConversationLinkTests {
    @Test func onlyASpaceConversationOpens() throws {
        let url = try #require(URL(string: "persona://tracks/computer-example/reading/session-example"))
        #expect(ExtensionConversationLink.session(url) == SessionKey(machineID: "computer-example", sessionID: "session-example"))
        #expect(ExtensionViewModel.opens(url, item: nil, owner: "space-conversations"))
        #expect(!ExtensionViewModel.opens(url, item: nil, owner: "other"))
        #expect(ExtensionConversationLink.session(URL(string: "persona://tracks/computer-example/health/session-example")!) == nil)
        #expect(ExtensionConversationLink.session(URL(string: "persona://settings/extensions/space-conversations")!) == nil)
        #expect(ExtensionConversationLink.session(URL(string: "https://example.com/tracks/computer-example/reading/session-example")!) == nil)
    }
}
