import SwiftUI
import Testing
@testable import Persona

/// Got It on a Just Finished to-do row sits at the end of the status line, under the time (#6578), not on a line of its
/// own: the row is exactly as tall as the same row without it, on a phone's width and a narrow one, at the default, a
/// large and an accessibility text size, light and dark. Where the words and Got It can't share the line (the largest
/// text size on a narrow row), Got It takes a line under them instead of squeezing them.
@MainActor struct TodoRowSeenLayoutTests {
    private static let todo = TodoItem(id: "seen-layout", title: "Write the release notes", doneAt: Date(), createdAt: Date(),
                                       updatedAt: Date())
    private static let triage = TodoTriage(section: .justDone, rule: .closed, last: Date())

    private func render(seen: Bool, width: CGFloat, text: DynamicTypeSize, look: ColorScheme) throws -> (size: CGSize, pixels: Data) {
        let row = TodoRow(todo: Self.todo, triage: Self.triage, onToggleDone: {}, onSeen: seen ? {} : nil)
            .frame(width: width)
            .environment(AppModel.shared)
            .environment(\.dynamicTypeSize, text)
            .environment(\.colorScheme, look)
        let renderer = ImageRenderer(content: row)
        renderer.scale = 2
        let image = try #require(renderer.cgImage)
        let pixels = try #require(image.dataProvider?.data) as Data
        return (CGSize(width: CGFloat(image.width) / renderer.scale, height: CGFloat(image.height) / renderer.scale), pixels)
    }

    @Test(arguments: [CGFloat(390), 320], [DynamicTypeSize.large, .xxxLarge, .accessibility2])
    func gotItAddsNoHeight(width: CGFloat, text: DynamicTypeSize) throws {
        for look in [ColorScheme.light, .dark] {
            let with = try render(seen: true, width: width, text: text, look: look)
            let without = try render(seen: false, width: width, text: text, look: look)
            #expect(with.size == without.size,
                    "\(width) pt wide at \(text), \(look): \(with.size.height) pt with Got It, \(without.size.height) pt without")
            #expect(with.pixels != without.pixels, "Got It is not drawn (\(width) pt, \(text), \(look))")
        }
    }

    /// At the largest text size on a 320 pt row the row grows by Got It's own line and nothing else: the status words keep
    /// theirs, unsqueezed.
    @Test func gotItGoesUnderTheWordsWhereTheyCantShareTheLine() throws {
        let with = try render(seen: true, width: 320, text: .accessibility5, look: .light)
        let without = try render(seen: false, width: 320, text: .accessibility5, look: .light)
        let chip = Chip(text: String(localized: "Got It [To-do Seen]"), symbol: Theme.Icon.seen, look: .tint(Theme.accent), compact: true)
            .padding(.vertical, -Chip.compactInset)
            .environment(\.dynamicTypeSize, .accessibility5)
        let renderer = ImageRenderer(content: chip)
        renderer.scale = 2
        let line = CGFloat(try #require(renderer.cgImage).height) / renderer.scale
        #expect(abs(with.size.height - (without.size.height + line + Theme.Space.xs)) <= 0.5,
                "\(with.size.height) pt with Got It, \(without.size.height) pt without, its line \(line) pt")
    }
}
