import XCTest

/// Your own long messages, folded (#5676), scrolled: an Agent chat of forty exchanges, each opened by a long pasted
/// report. Before / after in one optimized build: `Whole` draws every bubble whole, as before #5676
/// (`-showWholeBubbles YES`); the cases alternate (folded, whole, whole, folded) so the machine's load falls on both.
/// Mock: MOCK_LONG_INPUT=perf at TEST_RUNNER_PERSONA_PERF_HUB (`PerfTestCase`); without it the tests skip.
final class PerfLongInputTests: PerfTestCase {
    static let chat = "macbook/a1f3c2d4-0000-4000-8000-000005676a00"

    @MainActor private func open(whole: Bool) {
        launch(["-app.tab", "chats", "-tab", "dev", "-dev.openChat", "", "-openSession", Self.chat, "-holdChromeOpen", "YES",
                "-loadAll", "YES"] + (whole ? ["-showWholeBubbles", "YES"] : []))
        see(app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", "最后一行在这里")).firstMatch,
            "the chat", timeout: 30)
        sleep(2)
    }

    /// Fast flicks back into the history and to the newest again.
    @MainActor func testScroll1Folded() throws {
        open(whole: false)
        measureScroll(mainScroller(), first: .down)
    }

    @MainActor func testScroll2Whole() throws {
        open(whole: true)
        measureScroll(mainScroller(), first: .down)
    }

    @MainActor func testScroll3Whole() throws {
        open(whole: true)
        measureScroll(mainScroller(), first: .down)
    }

    @MainActor func testScroll4Folded() throws {
        open(whole: false)
        measureScroll(mainScroller(), first: .down)
    }
}
