import CoreGraphics
import Foundation

/// Where the small window's source goes (#6333). The system's Picture in Picture transition starts from that layer's
/// frame and, on Restore, ends there: it used to sit in the bottom-left corner of the main window, so a small copy of
/// the picture popped up there before it jumped into the small window. With the page up it lies exactly over the page's
/// picture (the whole framebuffer as the page draws it, in the window's points), so the picture itself shrinks into the
/// small window and grows back; with no page, in the top trailing corner, where the small window comes up.
nonisolated enum VNCPiPPlacement {
    /// The source's frame in a window of `bounds`: over `page` (the page's picture, in the same window's points) when
    /// there is one, else `size` in the top trailing corner, `margin` in from the side and under the safe area.
    static func source(page: CGRect?, in bounds: CGRect, safeTop: CGFloat, size: CGSize, margin: CGFloat) -> CGRect {
        if let page, page.width >= 1, page.height >= 1 { return page }
        return CGRect(x: bounds.maxX - size.width - margin, y: bounds.minY + safeTop + margin, width: size.width, height: size.height)
    }
}

/// Restore's completion (#6333): the system grows the small window back into the source once it is called, so it waits
/// until the page has come back and the source lies over its picture, or `wait` at the latest. Called once.
@MainActor final class VNCPiPRestore {
    static let wait: Duration = .milliseconds(600)
    private var done: ((Bool) -> Void)?

    var pending: Bool { done != nil }

    /// A restore began; one still waiting (it cannot, but) is answered first.
    func arm(_ done: @escaping (Bool) -> Void) {
        finish()
        self.done = done
    }

    /// The page's picture is in place, or the wait is over: the system may animate.
    func finish() {
        let d = done
        done = nil
        d?(true)
    }
}
