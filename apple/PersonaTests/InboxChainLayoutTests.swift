import Foundation
import SwiftUI
import Testing
@testable import Persona

/// Measures the real chain section, whose closed rows used all to be capped at two lines. The four-line plain reported
/// cases are therefore red before that cap is lifted and green after it; the other kinds, links and message batches are
/// controls whose heights must stay equal. Explicit newlines make the line counts independent of wrapping and fonts.
@MainActor
struct InboxChainLayoutTests {
    private let model = AppModel(stateFolder: FileManager.default.temporaryDirectory
        .appending(path: "inbox-chain-layout-\(UUID().uuidString)"))

    @Test(arguments: [320.0, 744.0] as [CGFloat])
    func plainReportedDetailGrowsBeyondTwoLines(width: CGFloat) throws {
        let one = try height(entry(lines: 1, detail: Self.lines(1)), width: width)
        let two = try height(entry(lines: 2, detail: Self.lines(2)), width: width)
        let four = try height(entry(lines: 4, detail: Self.lines(4)), width: width)

        // Sensitivity: this same measurement must distinguish even the two heights allowed by the old cap.
        #expect(two > one, "two lines did not grow beyond one at width \(width)")
        #expect(four > two, "plain reported detail stayed capped at width \(width)")

        let emptyMessagesTwo = try height(entry(lines: 2, detail: Self.lines(2), messages: []), width: width)
        let emptyMessagesFour = try height(entry(lines: 4, detail: Self.lines(4), messages: []), width: width)
        #expect(emptyMessagesFour > emptyMessagesTwo, "an empty message array recapped the row at width \(width)")
    }

    @Test(arguments: [320.0, 744.0] as [CGFloat])
    func everyOtherClosedRowKeepsTheTwoLineCap(width: CGFloat) throws {
        let link = InboxItem.Link(type: "source", url: "https://example.com/report")
        let messages = [InboxItem.ChainEntry.Message(sender: "Agent", text: "Context", at: nil)]
        let cases: [(name: String, two: InboxItem.ChainEntry, four: InboxItem.ChainEntry)] = [
            ("non-reported", entry(kind: "sent", lines: 2, detail: Self.lines(2)),
             entry(kind: "sent", lines: 4, detail: Self.lines(4))),
            ("reported with a link", entry(lines: 2, detail: Self.lines(2), link: link),
             entry(lines: 4, detail: Self.lines(4), link: link)),
            ("reported with messages", entry(lines: 2, detail: Self.lines(2), messages: messages),
             entry(lines: 4, detail: Self.lines(4), messages: messages)),
            ("reported with nil detail", entry(lines: 2), entry(lines: 4)),
            ("reported with empty detail", entry(lines: 2, detail: ""), entry(lines: 4, detail: "")),
        ]

        for c in cases {
            let two = try height(c.two, width: width)
            let four = try height(c.four, width: width)
            #expect(two == four, "\(c.name) lost its closed two-line cap at width \(width)")
        }
    }

    private func entry(kind: String = "reported", lines: Int, detail: String? = nil,
                       messages: [InboxItem.ChainEntry.Message]? = nil,
                       link: InboxItem.Link? = nil) -> InboxItem.ChainEntry {
        InboxItem.ChainEntry(id: "step", kind: kind, text: Self.lines(lines), detail: detail,
                             messages: messages, link: link)
    }

    private func height(_ entry: InboxItem.ChainEntry, width: CGFloat) throws -> Int {
        var item = InboxItem(id: "matter", source: "lark", kind: "dm", account: "example", accountName: "Example",
                             title: "Matter", preview: "", count: 1)
        item.chain = [entry]
        let renderer = ImageRenderer(content: InboxChainSection(item: item)
            .frame(width: width, alignment: .leading)
            .environment(model)
            .environment(\.colorScheme, .light)
            .environment(\.dynamicTypeSize, .large))
        renderer.scale = 1
        return try #require(renderer.cgImage).height
    }

    private static func lines(_ count: Int) -> String {
        ["First line", "Second line", "Third line", "Fourth line"].prefix(count).joined(separator: "\n")
    }
}
