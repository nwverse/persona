import Foundation
import Testing
@testable import Persona

/// Which Agents get the Terminal button in an empty chat (#6170): a CLI in a terminal pane whose chat Persona cannot draw.
struct TerminalOnlyAgentTests {
    private func session(_ extra: String = "") throws -> Session {
        try JSON.decoder.decode(Session.self, from: Data(#"{"machine_id":"box","session_id":"s1","status":"working","state":"working"\#(extra)}"#.utf8))
    }

    @Test func anAgentOtherThanClaudeCodexAndTraexInATerminalIsTerminalOnly() throws {
        #expect(try session(#","agent":"kiro""#).isTerminalOnly)
    }

    @Test func agentsWithAStructuredChatKeepTheMenuEntry() throws {
        #expect(!(try session().isTerminalOnly)) // an older hub names no agent: Claude Code
        #expect(!(try session(#","agent":"claude""#).isTerminalOnly))
        #expect(!(try session(#","agent":"codex""#).isTerminalOnly))
        #expect(!(try session(#","agent":"traex""#).isTerminalOnly))
        #expect(!(try session(#","agent":"kiro","assistant":true"#).isTerminalOnly))
        #expect(!(try session(#","agent":"kiro","source_kind":"runtime""#).isTerminalOnly))
        #expect(!(try session(#","agent":"kiro","capabilities":{"read":true}"#).isTerminalOnly)) // no terminal
    }
}
