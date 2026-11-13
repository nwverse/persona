import AppKit
import Foundation
import Testing
@testable import Persona

/// The composer's send history (docs/apple/mac.md「Details」): the web's `send-history.test.ts` cases, on the Mac's keys.
struct MacSendHistoryTests {
    private typealias H = MacSendHistory
    private func entries(_ texts: [String]) -> [H.Entry] {
        texts.enumerated().map { H.Entry(text: $1, at: Date(timeIntervalSince1970: Double($0))) }
    }

    @Test func keepsTheLastHundredWithoutBlanksOrRepeats() {
        var list: [H.Entry] = []
        for i in 0..<130 { list = H.withSent(list, "m\(i)", at: Date(timeIntervalSince1970: Double(i))) }
        #expect(list.count == H.historyMax)
        #expect(list.first?.text == "m30")
        #expect(H.withSent(list, "  \n", at: .now) == list)
        let moved = H.withSent(entries(["a", "b", "c"]), "a", at: Date(timeIntervalSince1970: 9))
        #expect(moved.map(\.text) == ["b", "c", "a"])
    }

    @Test func searchNeedsEveryWordNewestFirstOncePerText() {
        let all = entries(["fix the Login bug", "deploy", "login page", "fix the login bug"]).reversed().map { $0 }
        #expect(H.search(all, "LOGIN fix") == ["fix the login bug", "fix the Login bug"])
        #expect(H.search(all, "") == ["fix the login bug", "login page", "deploy", "fix the Login bug"])
        #expect(H.search(all, "nothing").isEmpty)
        #expect(H.search(entries(["a", "a", "b"]).reversed(), "").count == 2)
        #expect(H.search(entries((0..<150).map { "m\($0)" }), "m").count == H.searchMax)
    }

    @Test func recallsOnlyFromAnEmptyBoxOrTheRecalledMessage() {
        #expect(H.canRecall(draft: "", shown: nil, .up, firstLine: true, lastLine: true))
        #expect(!H.canRecall(draft: "typed", shown: nil, .up, firstLine: true, lastLine: true))
        #expect(!H.canRecall(draft: "edited", shown: "sent", .up, firstLine: true, lastLine: true))
        #expect(H.canRecall(draft: "a\nb", shown: "a\nb", .up, firstLine: true, lastLine: false))
        #expect(!H.canRecall(draft: "a\nb", shown: "a\nb", .down, firstLine: true, lastLine: false))
    }

    @Test func stepsThroughTheHistory() {
        #expect(H.step(count: 0, index: -1, .up) == -1)
        #expect(H.step(count: 3, index: -1, .up) == 2)
        #expect(H.step(count: 3, index: 2, .up) == 1)
        #expect(H.step(count: 3, index: 0, .up) == 0)
        #expect(H.step(count: 3, index: 1, .down) == 2)
        #expect(H.step(count: 3, index: 2, .down) == -1)
        #expect(H.step(count: 3, index: -1, .down) == -1)
    }

    @Test func linesOfTheCursor() {
        #expect(H.lines("one", cursor: NSRange(location: 1, length: 0)) == (true, true))
        #expect(H.lines("a\nb", cursor: NSRange(location: 0, length: 0)) == (true, false))
        #expect(H.lines("a\nb", cursor: NSRange(location: 3, length: 0)) == (false, true))
        #expect(H.lines("a\nb", cursor: NSRange(location: 0, length: 3)) == (true, true))
    }

    @Test func composerKeys() {
        #expect(H.key(keyCode: H.upArrow, modifiers: [], composing: false) == .recall(.up))
        #expect(H.key(keyCode: H.downArrow, modifiers: [.numericPad, .function], composing: false) == .recall(.down))
        #expect(H.key(keyCode: H.upArrow, modifiers: .shift, composing: false) == nil)
        #expect(H.key(keyCode: H.upArrow, modifiers: [], composing: true) == nil)
        #expect(H.key(keyCode: 15, modifiers: .control, composing: false) == .openSearch)
        #expect(H.key(keyCode: 15, modifiers: .command, composing: false) == nil) // ⌘R is Refresh
    }

    @Test func searchKeys() {
        #expect(H.searchAction(keyCode: H.upArrow, modifiers: [], composing: false) == .up)
        #expect(H.searchAction(keyCode: 15, modifiers: .control, composing: false) == .up)
        #expect(H.searchAction(keyCode: 35, modifiers: .control, composing: false) == .up)
        #expect(H.searchAction(keyCode: 1, modifiers: .control, composing: false) == .down)
        #expect(H.searchAction(keyCode: 45, modifiers: .control, composing: false) == .down)
        #expect(H.searchAction(keyCode: 36, modifiers: [], composing: false) == .pick)
        #expect(H.searchAction(keyCode: 48, modifiers: [], composing: false) == .pick)
        #expect(H.searchAction(keyCode: 53, modifiers: [], composing: false) == .cancel)
        #expect(H.searchAction(keyCode: 5, modifiers: .control, composing: false) == .cancel)
        #expect(H.searchAction(keyCode: 36, modifiers: [], composing: true) == nil)
        #expect(H.searchAction(keyCode: 0, modifiers: [], composing: false) == nil) // typing "a"
        #expect(H.move(0, count: 3, .up) == 2)
        #expect(H.move(2, count: 3, .down) == 0)
        #expect(H.move(0, count: 0, .down) == 0)
    }
}
