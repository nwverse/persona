import Foundation
import Testing
@testable import PersonaKit

/// The reveal lays out the received text to the end of the block it is in and draws a fraction of that block (#7287).
struct RevealPlanTests {
    static let reply = """
    # 如何推行代码评审

    先说结论：评审的目的是**让代码变好**。

    - 提前发现缺陷
    - 知识共享
      继续说明

    | 维度 | 异步 | 结对 |
    | --- | --- | --- |
    | 实时性 | 异步 | 实时 |

    ```python
    def f():

        return 1
    ```

    最后一段 👍🏽 e\u{301} 结束
    """

    func utf16Prefix(_ s: String, _ n: Int) -> String { String(decoding: Array(s.utf16.prefix(n)), as: UTF16.self) }
    func utf16Slice(_ s: String, _ r: Range<Int>) -> String {
        String(decoding: Array(s.utf16.dropFirst(r.lowerBound).prefix(r.count)), as: UTF16.self)
    }

    /// At every length of the text as it grows: the text up to a block's end splits into the blocks before it, with that
    /// block as the tail, exactly as `split` cuts the whole text.
    @Test func blocksAreSplitsPieces() {
        let text = Self.reply
        for n in stride(from: 1, through: text.utf16.count, by: 1) {
            let prefix = utf16Prefix(text, n)
            guard prefix.utf16.count == n else { continue } // inside a surrogate pair
            let blocks = StreamChunks.blocks(prefix)
            let split = StreamChunks.split(prefix)
            #expect(blocks.count == split.done.count + 1)
            for (k, block) in blocks.enumerated() {
                let upTo = StreamChunks.split(utf16Prefix(prefix, block.upperBound))
                #expect(upTo.done == Array(split.done.prefix(k)))
                #expect(upTo.tail == utf16Slice(prefix, block))
            }
        }
    }

    @Test func plansTheBlockTheRevealIsIn() {
        let text = Self.reply
        let blocks = StreamChunks.blocks(text)
        #expect(blocks.count == 6)
        // Inside the second block: laid out to its end, part of it drawn.
        let mid = blocks[1].lowerBound + blocks[1].count / 2
        let plan = RevealPlan.make(blocks: blocks, shown: mid)
        #expect(plan.layoutEnd == blocks[1].upperBound)
        #expect(plan.blockStart == blocks[1].lowerBound)
        #expect(abs(plan.fraction - Double(mid - blocks[1].lowerBound) / Double(blocks[1].count)) < 1e-9)
        // In the blank line after a block: that block, whole; nothing of the next is laid out.
        let gap = RevealPlan.make(blocks: blocks, shown: blocks[1].upperBound + 1)
        #expect(gap.layoutEnd == blocks[1].upperBound && gap.fraction == 1)
        // Everything shown: all of it, drawn whole.
        let all = RevealPlan.make(blocks: blocks, shown: text.utf16.count)
        #expect(all.layoutEnd == text.utf16.count && all.fraction == 1)
        // The last block (still being written) is laid out to the end of what arrived.
        let tail = RevealPlan.make(blocks: blocks, shown: blocks[5].lowerBound + 2)
        #expect(tail.layoutEnd == text.utf16.count && tail.fraction < 1)
    }

    /// As the reveal moves on, what is laid out and drawn only grows: never a block or a character taken back.
    @Test func neverGoesBack() {
        let text = Self.reply
        let blocks = StreamChunks.blocks(text)
        var last = RevealPlan(layoutEnd: 0, blockStart: 0, fraction: 0)
        for shown in 0...text.utf16.count {
            let plan = RevealPlan.make(blocks: blocks, shown: shown)
            #expect(plan.layoutEnd >= last.layoutEnd)
            #expect(plan.blockStart > last.blockStart || (plan.blockStart == last.blockStart && plan.fraction >= last.fraction))
            #expect(plan.layoutEnd >= shown || plan.fraction == 1) // never draws past what is laid out
            last = plan
        }
        #expect(last.fraction == 1 && last.layoutEnd == text.utf16.count)
    }
}
