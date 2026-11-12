import SwiftUI
import Testing
@testable import Persona

/// Your own long message folds as the web folds it (#5676, `BubbleFoldRule`): past 14 lines to 10, at the accessibility
/// text sizes past 9 to 5; a message too short to fold in the narrowest bubble is never measured; folded, only its start
/// is laid out.
struct BubbleFoldTests {
    @Test func theWebsLinesAndFewerAtAccessibilitySizes() {
        #expect(BubbleFoldRule(.large) == .regular)
        #expect(BubbleFoldRule(.xxxLarge) == .regular)
        #expect(BubbleFoldRule.regular.limit == 14)
        #expect(BubbleFoldRule.regular.shown == 10)
        #expect(BubbleFoldRule(.accessibility1) == .large)
        #expect(BubbleFoldRule(.accessibility5) == .large)
        #expect(BubbleFoldRule.large.limit == 9)
        #expect(BubbleFoldRule.large.shown == 5)
    }

    @Test func linesCountWrappedAndEmptyLines() {
        // "abc" and "" take a line each; ten characters at four a line take three.
        #expect(BubbleFoldRule.lines([3, 0, 10], perLine: 4) == 5)
        #expect(BubbleFoldRule.lengths("ab\n\n中文字") == [2, 0, 3])
    }

    @Test func charactersPerLineShrinkAsTheTextGrows() {
        #expect(BubbleFoldRule.perLine(BubbleFoldRule.narrowest, size: 17) == 9)
        #expect(BubbleFoldRule.perLine(BubbleFoldRule.narrowest, size: 53) == 2)
        #expect(BubbleFoldRule.perLine(BubbleFoldRule.narrowest, size: 500) == 1)
    }

    /// A short question is drawn as before: no fold, no measuring (the web's NARROWEST_CHARS check).
    @Test func shortMessagesNeverFold() {
        let perLine = BubbleFoldRule.perLine(BubbleFoldRule.narrowest, size: 17)
        let short = BubbleFoldRule.lengths("看一下 CI 为什么红了")
        #expect(BubbleFoldRule.lines(short, perLine: perLine) <= BubbleFoldRule.regular.limit)
        let long = BubbleFoldRule.lengths(String(repeating: "一行很长的话", count: 30))
        #expect(BubbleFoldRule.lines(long, perLine: perLine) > BubbleFoldRule.regular.limit)
    }

    @Test func foldedOnlyTheStartIsLaidOut() throws {
        // Lines: the start ends before the line past `lines`.
        let many = (1...40).map { "第 \($0) 行" }.joined(separator: "\n")
        let start = try #require(BubbleFoldRule.start(of: many, lines: 16))
        #expect(start.split(separator: "\n", omittingEmptySubsequences: false).count == 16)
        #expect(many.hasPrefix(start))
        // Characters: one line of 10 000 is cut at `startCharacters`.
        let wide = String(repeating: "字", count: 10_000)
        #expect(BubbleFoldRule.start(of: wide, lines: 16)?.count == BubbleFoldRule.startCharacters)
        // All of it: nothing to cut.
        #expect(BubbleFoldRule.start(of: "短的\n一句", lines: 16) == nil)
        // Never in the middle of a character made of several scalars.
        let flags = String(repeating: "🇨🇳", count: 4_000)
        let cut = try #require(BubbleFoldRule.start(of: flags, lines: 16))
        #expect(cut.count == BubbleFoldRule.startCharacters)
        #expect(cut.unicodeScalars.count == 2 * BubbleFoldRule.startCharacters)
    }
}
