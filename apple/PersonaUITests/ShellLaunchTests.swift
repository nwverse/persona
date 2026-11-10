import XCTest

/// Launching into a conversation makes it, and only it, appear once (docs/apple/shell.md「Launch」). The ovis lane saw a
/// conversation's `onAppear` run several times at launch (2026-11-02): the one opened appeared once, but the shell had
/// built Assistant behind Agents and its conversation appeared too, unseen: subscribed, marked seen, and the DEBUG switches
/// that hang off `onAppear` ran twice. `probe.chat`'s `appeared` and the shell's trace count them.
final class ShellLaunchTests: XCTestCase {
    private struct Chat: Decodable {
        var key: String
        var appeared: Int
    }

    /// An agent's conversation in the demo (weather's 「补齐网络层的单元测试」).
    private static let agentChat = "macbook/a1f3c2d4-0000-4000-8000-000000000002"
    /// An Assistant conversation in the demo (「整理这个季度的读书笔记」).
    private static let assistantChat = "server/b2e4d6f8-0000-4000-8000-000000000004"

    override func setUp() {
        continueAfterFailure = true
    }

    @MainActor private func expectOnce(_ args: [String], page: String, _ what: String, hub: String? = nil) {
        let app = XCUIApplication()
        // Paired with scripts/mock/mock_hub.py, or Demo Mode.
        let source = hub.map { ["-pairURL", "persona://pair?url=" + $0.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"] }
            ?? ["-demo.on", "YES"]
        app.launchArguments = source + ["-probes", "YES", "-dev.folded", ""] + args
        app.launchPinned()
        defer { app.terminate() }
        let start = Date()
        var chat: Chat?
        while Date().timeIntervalSince(start) < 15 {
            chat = app.probe("chat", as: Chat.self, page: page)
            if chat != nil { break }
            usleep(200_000)
        }
        guard chat != nil else { return XCTFail("no conversation on \(page) after launch (\(what)): [\(app.shellPage)] \(app.shellTrace)") }
        // Whatever goes back and forth does so within the first moments: give it time, then count.
        sleep(3)
        chat = app.probe("chat", as: Chat.self, page: page)
        XCTAssertEqual(chat?.appeared, 1, "the conversation appeared \(chat?.appeared ?? 0) times (\(what)): \(app.shellTrace)")
        // No other conversation either: the page behind is not built, so its conversation does not appear unseen.
        let appeared = app.shellTrace.components(separatedBy: " | ").filter { $0.contains("chat appeared") }
        XCTAssertEqual(appeared.count, 1, "conversations that appeared at launch (\(what)): \(appeared) \(app.shellTrace)")
        let behind = page == "agents" ? "assistant" : "agents"
        XCTAssertNil(app.probeEnvelope("chat", page: behind), "a conversation on \(behind), behind \(page) (\(what))")
        app.expectShell(agents: page == "agents", "after launching into a conversation (\(what))")
    }

    /// `-openSession`, what a notification's tap or a test does at launch.
    @MainActor func testAChatOpenedAtLaunchAppearsOnce() {
        expectOnce(["-openSession", Self.agentChat], page: "agents", "-openSession")
    }

    /// The Agents chat that was open last time comes back at launch.
    @MainActor func testTheLastChatComesBackOnce() {
        expectOnce(["-app.tab", "chats", "-dev.openChat", Self.agentChat], page: "agents", "the last chat restored")
    }

    /// Against a hub the link says Connecting… at launch: the status row opens and folds under the open chat (the page
    /// makes way through the window's safe area) and must not make it appear again.
    @MainActor func testAChatOpenedAtLaunchAppearsOnceWhileTheRowComesAndGoes() {
        expectOnce(["-openSession", Self.agentChat, "-showConnectionCycle", "connecting:1:1"], page: "agents", "连接中… coming and going")
    }

    /// Paired with a hub (scripts/mock/mock_hub.py, PERSONA_MOCK_HUB): what ConnectionTests launches into, where the chat was
    /// seen to appear three times.
    @MainActor func testAChatOpenedAtLaunchAgainstAHubAppearsOnce() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        expectOnce(["-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000001"], page: "agents", "paired with a hub", hub: hub)
    }

    /// Assistant's conversation, the page the shell opens on. Named: what Assistant shows is kept across launches, and a test before
    /// may have left it on a new conversation (`-newAssistant`).
    @MainActor func testTheAssistantsConversationAppearsOnce() {
        expectOnce(["-tab", "assistant", "-openAssistant", Self.assistantChat], page: "assistant", "助手")
    }
}
