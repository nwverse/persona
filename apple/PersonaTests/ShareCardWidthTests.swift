import Foundation
import Testing
@testable import Persona

/// How wide Export as Image draws a message (#2216, `ShareCardWidth`, `ShareCardBlocks`).
struct ShareCardWidthTests {
    @Test func plainTextKeepsAPhonesWidth() {
        #expect(ShareCardWidth.width(natural: 0, height: 600) == ShareCardWidth.base)
        // A table or code that fits in the phone's width changes nothing.
        #expect(ShareCardWidth.width(natural: 250, height: 600) == ShareCardWidth.base)
    }

    @Test func tablesAndCodeWidenItToTheirOwnWidthUpToTheCap() {
        #expect(ShareCardWidth.width(natural: 480, height: 900) == 480 + ShareCardWidth.chrome)
        #expect(ShareCardWidth.width(natural: 2000, height: 900) == ShareCardWidth.cap) // wider: its cells wrap
    }

    @Test func aThinStripWidensTowardAReadableLine() {
        let base = ShareCardWidth.base
        #expect(ShareCardWidth.width(natural: 0, height: ShareCardWidth.tallest * base) == base)
        let tall = ShareCardWidth.width(natural: 0, height: 1400)
        #expect(tall > base && tall < ShareCardWidth.measure + ShareCardWidth.chrome)
        // However tall, text alone never goes past the paragraph's measure.
        #expect(ShareCardWidth.width(natural: 0, height: 20000) == ShareCardWidth.measure + ShareCardWidth.chrome)
        #expect(ShareCardWidth.width(natural: 560, height: 20000) == 560 + ShareCardWidth.chrome)
    }

    @Test func theWideBlocksAreTablesAndFencedCode() {
        let text = """
        Intro line | not a table

        | A | B |
        | --- | :-: |
        | 1 | 2 |

        - a list
        ```swift
        let a = 1
        ```
        After.
        """
        #expect(ShareCardBlocks.wide(text) == ["| A | B |\n| --- | :-: |\n| 1 | 2 |", "```swift\nlet a = 1\n```"])
        #expect(ShareCardBlocks.wide("Just words\nand more") == [])
        #expect(ShareCardBlocks.wide("```\nnever closed") == ["```\nnever closed"])
        #expect(ShareCardBlocks.isDelimiter("---|---") && ShareCardBlocks.isDelimiter("| :--- | ---: |"))
        #expect(!ShareCardBlocks.isDelimiter("| a | b |") && !ShareCardBlocks.isDelimiter("---"))
    }
}
