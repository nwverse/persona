import Foundation
import Testing
@testable import Persona

/// UI version 5's code review on Apple (docs/extensions/extensions.md "UI version 5: code review", #3136, #827): the
/// words that changed, whitespace ignored, generated and big files folded at first, viewed marks (the extension's,
/// changed at once and sent with `mark_viewed`; or kept on the device), the toolbar's counts, and an action's `items`
/// drawn in place. On the schema's shared snapshot (schema/extension/examples/showcase/code-review-v5.json).
@MainActor
struct ExtensionCodeReviewTests {
    private static let snapshot = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().appending(path: "schema/extension/examples/showcase/code-review-v5.json")
    private static let path = "/api/v1/plugins/forge/-/ui/review"

    private static func view() throws -> ExtensionView {
        try JSONDecoder().decode(ExtensionView.self, from: Data(contentsOf: snapshot))
    }

    private static func mains(_ v: ExtensionView) -> [Int] { v.sections.indices.filter { v.sections[$0].pane != "side" } }

    private static func diff(_ v: ExtensionView, _ path: String) throws -> ExtensionDiffComponent {
        try #require(v.sections.flatMap(\.items).compactMap { if case .diff(let d) = $0, d.path == path { d } else { nil } }.first)
    }

    // MARK: The logic

    @Test func theWordsThatChangedAreMarked() {
        let lines = CodeDiffModel.lines(oldStart: 1, newStart: 1, raw: ["-\t\treturn err", "+\t\treturn fmt.Errorf(\"upload: %w\", err)", " }"])
        var budget = 1_000_000
        let marked = CodeReview.markWords(lines, budget: &budget)
        #expect(!marked[0].changed.isEmpty || !marked[1].changed.isEmpty, "a changed pair is compared word by word")
        let words = marked[1].changed.map { r in
            let u = marked[1].text.utf16
            return String(u[u.index(u.startIndex, offsetBy: r.lowerBound)..<u.index(u.startIndex, offsetBy: r.upperBound)])!
        }
        #expect(words.joined().contains("fmt"), "the new words are the changed ones: \(words)")
        #expect(!words.joined().contains("return"), "the words both have are not")
        #expect(marked[2].changed.isEmpty, "an unchanged line has none")
    }

    @Test func whitespaceOnlyChangesReadAsUnchanged() {
        let lines = CodeDiffModel.lines(oldStart: 10, newStart: 10, raw: ["-  a := 1", "-  b := 2", "+\ta := 1", "+\tb := 3"])
        let out = CodeReview.ignoringWhitespace(lines)
        #expect(out.map(\.kind) == [.context, .del, .add], "only the line that changed in more than whitespace stays changed")
        #expect(out[0].old == 10 && out[0].new == 10 && out[0].text == "\ta := 1", "both numbers, the new text")
        #expect(CodeReview.onlyWhitespace([CodeDiffModel.lines(oldStart: 1, newStart: 1, raw: ["-  x", "+\tx"])]))
        #expect(!CodeReview.onlyWhitespace([lines]))
    }

    @Test func generatedFilesAreKnownByTheirPaths() {
        for p in ["go.sum", "web/package-lock.json", "vendor/github.com/x/y.go", "api/user.pb.go", "a/b_gen.go", "dist/app.min.js"] {
            #expect(CodeReview.isGenerated(p), "\(p)")
        }
        for p in ["main.go", "docs/upload.md", "src/vendors.go", "generated.md"] {
            #expect(!CodeReview.isGenerated(p), "\(p)")
        }
        #expect(CodeReview.isGenerated("schema.json", said: true), "or as the extension says")
    }

    @Test func whyADiffStartsFolded() {
        let hunk = ExtensionHunk(oldStart: 1, newStart: 1, lines: ["+x"])
        #expect(CodeReview.foldedAtFirst(.init(path: "go.sum", collapsed: true, hunks: [hunk]), viewed: true) == .generated)
        #expect(CodeReview.foldedAtFirst(.init(path: "a.go", additions: 300, deletions: 101), viewed: false) == .big)
        #expect(CodeReview.foldedAtFirst(.init(path: "a.go", collapsed: true, hunks: [hunk]), viewed: true) == .collapsed)
        #expect(CodeReview.foldedAtFirst(.init(path: "a.go", collapsed: true), viewed: false) == nil, "collapsed without hunks: to be loaded")
        #expect(CodeReview.foldedAtFirst(.init(path: "a.go", hunks: [hunk]), viewed: true) == .viewed)
        #expect(CodeReview.foldedAtFirst(.init(path: "a.go", hunks: [hunk]), viewed: false) == nil)
    }

    @Test func aViewedMarkKeptHereHoldsForThisChangeOnly() {
        let a = ExtensionDiffComponent(path: "a.go", hunks: [ExtensionHunk(oldStart: 1, newStart: 1, lines: ["+x"])])
        var b = a
        b.hunks = [ExtensionHunk(oldStart: 1, newStart: 1, lines: ["+y"])]
        #expect(CodeReview.changeID(a) != CodeReview.changeID(b), "the file changed: the mark goes")
        #expect(CodeReview.changeID(.init(path: "a.go", revision: "r1")) == "a.go@r1")
        let marks = CodeViewedMarks(saved: [])
        let state = CodePageState(marks: marks)
        #expect(!state.isViewed(a))
        state.setViewed(a, key: "diff:a.go", true)
        #expect(state.isViewed(a) && !state.isViewed(b) && marks.ids == [CodeReview.changeID(a)])
    }

    // MARK: The page

    @Test func theSnapshotsPageHasItsToolbarAndFolds() throws {
        let v = try Self.view()
        let state = CodePageState(marks: CodeViewedMarks(saved: []))
        let blocks = CodePageBlocks.of(v, sections: Self.mains(v), state: state, look: .unified, canComment: true)
        // The toolbar over the first diff: 1 of 5 viewed (retry.go, the extension's mark), 2 threads open (t1, t3;
        // t2 is a draft).
        guard let tools = blocks.firstIndex(where: { if case .reviewTools = $0 { true } else { false } }),
              case .reviewTools(_, let files, let viewed, let open) = blocks[tools] else { Issue.record("no toolbar"); return }
        #expect(files == 5 && viewed == 1 && open.map { $0.block } == ["th:diff:src/upload/retry.go:t1", "th:diff:src/upload/client.go:t3"])
        if case .diffHeader = blocks[tools + 1] {} else { Issue.record("the toolbar is right over the first diff") }
        // retry.go is viewed: folded at first, and says why.
        #expect(blocks.contains { if case .diffHeader(_, _, let d, true) = $0 { d.path == "src/upload/retry.go" } else { false } })
        #expect(blocks.contains { if case .diffFolded(_, "diff:src/upload/retry.go", let note) = $0 { note == String(localized: "Viewed") } else { false } })
        // go.sum is generated but has no hunks: its Load Diff shows, nothing loads it by itself.
        #expect(blocks.contains { if case .diffLoad(_, let d) = $0 { d.path == "go.sum" } else { false } })
        // client.go's changed words are marked.
        #expect(blocks.contains { if case .diffLine(_, _, let d, let l, _, _) = $0 { d.path == "src/upload/client.go" && l.kind == .add && !l.changed.isEmpty } else { false } })
    }

    @Test func checkingViewedFoldsAndUncheckingLeavesItSo() throws {
        let v = try Self.view()
        let state = CodePageState(marks: CodeViewedMarks(saved: []))
        let client = try Self.diff(v, "src/upload/client.go")
        let key = "diff:src/upload/client.go"
        func folded() -> Bool {
            CodePageBlocks.of(v, sections: Self.mains(v), state: state, look: .unified, canComment: true)
                .contains { if case .diffHeader(_, key, _, true) = $0 { true } else { false } }
        }
        #expect(!folded())
        let first = state.setViewed(client, key: key, true)
        #expect(state.isViewed(client) && folded(), "checked: viewed and folded")
        state.setViewed(client, key: key, false)
        #expect(!state.isViewed(client) && folded(), "unchecked: still folded, as on the web")
        // The extension's mark_viewed failing for an older change puts back nothing.
        state.viewedFailed(client.path, attempt: first.attempt, before: first.before)
        #expect(!state.isViewed(client))
        let last = state.setViewed(client, key: key, true)
        state.viewedFailed(client.path, attempt: last.attempt, before: last.before)
        #expect(!state.isViewed(client), "the latest failing: the mark it had before")
    }

    @Test func whitespaceIgnoredLeavesOnlyRealChanges() throws {
        var v = try Self.view()
        let state = CodePageState(marks: CodeViewedMarks(saved: []))
        let was = state.whitespace
        defer { state.setWhitespace(was) }
        for i in v.sections.indices {
            v.sections[i].items = v.sections[i].items.map { c in
                guard case .diff(var d) = c, d.path == "src/upload/limits.go" else { return c }
                d.hunks = [ExtensionHunk(oldStart: 1, newStart: 1, lines: ["-  x := 1", "+\tx := 1"])]
                return .diff(d)
            }
        }
        state.setWhitespace(true)
        let blocks = CodePageBlocks.of(v, sections: Self.mains(v), state: state, look: .unified, canComment: true)
        #expect(blocks.contains { if case .diffNote(let id, _) = $0 { id == "ws:diff:src/upload/limits.go" } else { false } })
        state.setWhitespace(false)
        let again = CodePageBlocks.of(v, sections: Self.mains(v), state: state, look: .unified, canComment: true)
        #expect(again.contains { if case .diffLine(_, _, let d, let l, _, _) = $0 { d.path == "src/upload/limits.go" && l.kind == .del } else { false } })
    }

    // MARK: The model

    @Test func anAnswersItemsReplaceOnlyTheirComponents() async throws {
        let hub = FakeHub()
        let v = try Self.view()
        let page = try String(contentsOf: Self.snapshot, encoding: .utf8)
        var loaded = try Self.diff(v, "docs/upload.md")
        loaded.hunks = [ExtensionHunk(oldStart: 1, newStart: 1, lines: ["-old", "+new"])]
        loaded.collapsed = nil
        // A component's payload is written without its type.
        var object = try JSON.decoder.decode([String: JSONValue].self, from: JSONEncoder().encode(loaded))
        object["type"] = .string("diff")
        let item = String(decoding: try JSONEncoder().encode(object), as: UTF8.self)
        let draws = Recorded()
        hub.on("POST", Self.path) { r in
            let body = try JSON.decoder.decode([String: JSONValue].self, from: r.body ?? Data())
            if body["action"] != nil { return Data(#"{"items":[\#(item)]}"#.utf8) }
            draws.count += 1
            return Data(#"{"view":\#(page)}"#.utf8)
        }
        let m = ExtensionViewModel(plugin: "forge", ui: "review", deps: .init(api: { hub.client }))
        await m.load()
        let load = try #require(try Self.diff(#require(m.view), "docs/upload.md").load)
        _ = await m.perform(load, with: ["path": "docs/upload.md"])
        #expect(draws.count == 1, "the page is not drawn again")
        #expect(try Self.diff(#require(m.view), "docs/upload.md").hunks?.first?.lines == ["-old", "+new"], "the loaded diff in its place")
        // Drawn again (ui.invalidate, a refresh): the extension gives the diff without its lines; the loaded one stays.
        await m.refresh()
        #expect(draws.count == 2)
        #expect(try Self.diff(#require(m.view), "docs/upload.md").hunks?.isEmpty == false, "kept loaded")
    }

    @Test func markViewedIsSentQuietly() async throws {
        let hub = FakeHub()
        let page = try String(contentsOf: Self.snapshot, encoding: .utf8)
        let draws = Recorded()
        hub.on("POST", Self.path) { r in
            let body = try JSON.decoder.decode([String: JSONValue].self, from: r.body ?? Data())
            if body["action"] != nil { return Data("{}".utf8) }
            draws.count += 1
            return Data(#"{"view":\#(page)}"#.utf8)
        }
        let m = ExtensionViewModel(plugin: "forge", ui: "review", deps: .init(api: { hub.client }))
        await m.load()
        let mark = try #require(try Self.diff(#require(m.view), "src/upload/client.go").markViewed)
        let o = await m.quiet(mark, with: ["path": "src/upload/client.go", "viewed": "true"])
        #expect(o.error == nil && draws.count == 1 && m.running == nil, "an empty answer draws nothing, nothing waited")
        let sent = try #require(hub.sent("POST", Self.path).last)
        let body = try JSON.decoder.decode([String: JSONValue].self, from: sent.body ?? Data())
        guard case .object(let values)? = body["values"] else { Issue.record("no values"); return }
        #expect(values["path"] == .string("src/upload/client.go") && values["viewed"] == .string("true"))
    }
}

@MainActor private final class Recorded {
    var count = 0
}
