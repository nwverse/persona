import Testing
@testable import Persona

/// The "/" list over the composer (`SlashMenuRow`): the web's rows in the web's order, filtered as the web filters them.
@MainActor
struct SlashMenuTests {
    @Test func rowsFollowTheWebsOrder() {
        let words = SlashMenuRow.rows(model: true, clear: true).map(\.word)
        #expect(words == ["/继续", "/提交", "/汇报", "/compact", "/context", "/model", "/clear"])
        #expect(SlashMenuRow.rows(model: false, clear: false).map(\.word) == ["/继续", "/提交", "/汇报", "/compact", "/context"])
        // /loop between /model and /clear, as on the web, only where it is offered (a Claude agent)
        #expect(SlashMenuRow.rows(model: true, loop: true, clear: true).map(\.word)
            == ["/继续", "/提交", "/汇报", "/compact", "/context", "/model", "/loop", "/clear"])
        #expect(SlashMenuRow.matching("/lo", in: SlashMenuRow.rows(model: true, loop: true, clear: true)).map(\.text) == ["/loop "])
    }

    @Test func onlyALoneSlashWordOpensTheList() {
        let rows = SlashMenuRow.rows(model: true, clear: true)
        #expect(SlashMenuRow.matching("/", in: rows).count == rows.count)
        #expect(SlashMenuRow.matching("", in: rows).isEmpty)
        #expect(SlashMenuRow.matching("看下 /co", in: rows).isEmpty)
        #expect(SlashMenuRow.matching("/model sonnet", in: rows).isEmpty)
        #expect(SlashMenuRow.matching("/nothing", in: rows).isEmpty)
    }

    @Test func filtersByTheWordOrItsDescription() {
        let rows = SlashMenuRow.rows(model: true, clear: true)
        #expect(SlashMenuRow.matching("/co", in: rows).map(\.word) == ["/compact", "/context"])
        #expect(SlashMenuRow.matching("/CL", in: rows).map(\.word) == ["/clear"])
        #expect(SlashMenuRow.matching("/汇", in: rows).map(\.word) == ["/汇报"])
        #expect(SlashMenuRow.matching("/上下文", in: rows).map(\.word) == ["/compact", "/context"])
    }

    @Test func theMainChatOffersOnlyClear() {
        let rows = SlashMenuRow.matching("/", in: [.clear])
        #expect(rows.map(\.word) == ["/clear"])
        #expect(SlashMenuRow.matching("/cl", in: [.clear]).map(\.word) == ["/clear"])
        #expect(SlashMenuRow.matching("/继续", in: [.clear]).isEmpty)
        #expect(SlashMenuRow.matching("/clear now", in: [.clear]).isEmpty)
    }

    @Test func aQuickMessageShowsWhatItSends() {
        let rows = SlashMenuRow.rows(model: true, clear: true)
        #expect(rows.first { $0.word == "/提交" }?.preview == "把当前改动按规范提交并推送")
        #expect(rows.first { $0.word == "/compact" }?.preview == nil)
        #expect(SlashMenuRow.model.preview == nil)
    }
}
