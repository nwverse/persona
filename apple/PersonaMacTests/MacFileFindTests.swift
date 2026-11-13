import Foundation
import Testing
@testable import Persona

/// ⌘K's Files (docs/apple/mac.md「Quick Open and New」): the matcher, ported from the web's `file-find.ts` (same cases).
struct MacFileFindTests {
    private let files = ["src/AppView.tsx", "src/app/index.ts", "src/lib/shapes.ts", "docs/approach.md", "README.md",
                         "web/src/lib/palette.ts", "web/src/views/Palette.tsx", "web/src/views/PaletteFiles.test.tsx"]

    private func paths(_ hits: [MacFileFind.Hit]) -> [String] { hits.map(\.path) }
    private func marked(_ h: MacFileFind.Hit) -> String {
        let u = Array(h.path.utf16)
        return String(utf16CodeUnits: h.matched.map { u[$0] }, count: h.matched.count)
    }

    @Test func anEmptyQueryMatchesNothing() {
        #expect(MacFileFind.match(files, "").isEmpty)
        #expect(MacFileFind.match(files, "   ").isEmpty)
    }

    @Test func lettersInOrderAnyCase() {
        #expect(paths(MacFileFind.match(files, "READ")) == ["README.md"])
        #expect(MacFileFind.match(files, "mdr").isEmpty)
    }

    @Test func wordStartsBeatLettersInsideWords() {
        let got = paths(MacFileFind.match(files, "ap"))
        let shapes = try! #require(got.firstIndex(of: "src/lib/shapes.ts"))
        #expect(shapes > got.firstIndex(of: "src/AppView.tsx")!)
        #expect(shapes > got.firstIndex(of: "docs/approach.md")!)
    }

    @Test func camelHumpsAreWordStarts() {
        #expect(paths(MacFileFind.match(["src/xAppView.tsx", "src/xapplication.tsx"], "av")) == ["src/xAppView.tsx"])
        let hits = MacFileFind.match(["src/mapview.ts", "src/MapView.ts"], "mv")
        #expect(hits.first?.path == "src/MapView.ts")
        #expect(hits.first.map(marked) == "MV")
    }

    @Test func theNameBeatsTheFolders() {
        #expect(paths(MacFileFind.match(["palette/other.txt", "web/palette.ts"], "palette")) == ["web/palette.ts", "palette/other.txt"])
        let list = ["a/very/deep/folder/index.ts", "index.ts", "index.test.ts", "indexes/readme.md"]
        let got = paths(MacFileFind.match(list, "index"))
        #expect(got.first == "index.ts")
        #expect(got.firstIndex(of: "index.test.ts")! < got.firstIndex(of: "a/very/deep/folder/index.ts")!)
    }

    @Test func deeperFoldersAreNotPunished() {
        let list = ["Sources/App/Views/ForecastView.swift", "Tests/AppTests/ForecastViewModelTests.swift", "Sources/App/Models/ForecastViewModel.swift"]
        #expect(paths(MacFileFind.match(list, "fore")) == ["Sources/App/Views/ForecastView.swift", "Sources/App/Models/ForecastViewModel.swift",
                                                         "Tests/AppTests/ForecastViewModelTests.swift"])
        #expect(MacFileFind.match(list, "fvm").first?.path == "Sources/App/Models/ForecastViewModel.swift")
    }

    @Test func everyTermInAnyOrder() {
        #expect(Set(paths(MacFileFind.match(files, "views palette"))) == ["web/src/views/Palette.tsx", "web/src/views/PaletteFiles.test.tsx"])
        #expect(MacFileFind.match(files, "palette views").count == 2)
        #expect(paths(MacFileFind.match(files, "palette lib")) == ["web/src/lib/palette.ts"])
        #expect(MacFileFind.match(files, "palette docs").isEmpty)
    }

    @Test func listsWhatMatched() throws {
        let hit = try #require(MacFileFind.match(["web/src/views/Palette.tsx"], "pal tsx").first)
        #expect(marked(hit) == "Paltsx")
        #expect(hit.matched == hit.matched.sorted())
        #expect(Set(hit.matched).count == hit.matched.count)
    }

    @Test func atMostTheLimit() {
        let many = (0..<120).map { "src/file\($0).swift" }
        #expect(MacFileFind.match(many, "file").count == MacFileFind.maxHits)
    }

    /// Typing on narrows the last matches; going back (or another query) searches everything again.
    @Test func narrowingAndBack() {
        let search = MacFileFind.Search(files)
        #expect(search.search("pal").count == 3)
        #expect(paths(search.search("pal lib")) == ["web/src/lib/palette.ts"])
        #expect(search.search("pal").count == 3)
        #expect(paths(search.search("READ")) == ["README.md"])
    }

    @Test func nonASCIINames() throws {
        let hit = try #require(MacFileFind.match(["docs/会议纪要.md", "docs/周报.md"], "周报").first)
        #expect(hit.path == "docs/周报.md")
        #expect(marked(hit) == "周报")
    }

    @Test func runsKeepWholeCharacters() {
        let runs = MacFileFind.runs("a👍b", offset: 5, matched: [6])
        #expect(runs.map(\.text) == ["a", "👍", "b"])
        #expect(runs.map(\.hit) == [false, true, false])
        #expect(MacFileFind.join("/w/p", "a.swift") == "/w/p/a.swift")
        #expect(MacFileFind.join("/w/p/", "a.swift") == "/w/p/a.swift")
    }
}
