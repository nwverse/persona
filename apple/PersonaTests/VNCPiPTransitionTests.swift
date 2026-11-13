import CoreGraphics
import Testing
@testable import Persona

/// The small window's transition (#6333) without the system's picture in picture: where its source lies (over the
/// page's picture, else the top trailing corner, never the bottom-left corner it popped up from), and Restore's
/// completion, answered once.
@MainActor struct VNCPiPTransitionTests {
    private let window = CGRect(x: 0, y: 0, width: 402, height: 874)
    private let size = CGSize(width: 192, height: 120)

    @Test func overThePagesPicture() {
        let picture = CGRect(x: 0, y: 311, width: 402, height: 251)
        #expect(VNCPiPPlacement.source(page: picture, in: window, safeTop: 62, size: size, margin: 16) == picture)
        // Zoomed in, the whole picture runs off the screen: the source does too, so what shows is where it starts.
        let zoomed = CGRect(x: -402, y: 60, width: 1206, height: 754)
        #expect(VNCPiPPlacement.source(page: zoomed, in: window, safeTop: 62, size: size, margin: 16) == zoomed)
    }

    @Test func withNoPageInTheTopTrailingCorner() {
        let r = VNCPiPPlacement.source(page: nil, in: window, safeTop: 62, size: size, margin: 16)
        #expect(r == CGRect(x: 402 - 192 - 16, y: 62 + 16, width: 192, height: 120))
        #expect(r.maxY < window.midY, "not the bottom-left corner it used to pop up from")
        // A page not laid out yet (no size) counts as none.
        #expect(VNCPiPPlacement.source(page: .zero, in: window, safeTop: 62, size: size, margin: 16) == r)
    }

    @Test func restoreAnswersOnce() {
        let restore = VNCPiPRestore()
        var answers: [Bool] = []
        #expect(!restore.pending)
        restore.arm { answers.append($0) }
        #expect(restore.pending)
        restore.finish() // the page's picture is in place
        restore.finish() // the wait runs out after it
        #expect(answers == [true])
        #expect(!restore.pending)
    }

    @Test func aSecondRestoreAnswersTheFirst() {
        let restore = VNCPiPRestore()
        var first = 0, second = 0
        restore.arm { _ in first += 1 }
        restore.arm { _ in second += 1 }
        #expect(first == 1 && second == 0)
        restore.finish()
        #expect(first == 1 && second == 1)
    }
}
