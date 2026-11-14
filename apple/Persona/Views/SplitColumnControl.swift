#if os(iOS)
import OSLog
import SwiftUI
import UIKit

/// The list column of a split, driven from UIKit (#4206; iPad and the open iPhone Duo).
///
/// - **Gestures and the drag only.** Whether the column lies beside the page or over it is SwiftUI's, by the page's least
///   width (`SplitPageMin`), never set here: SwiftUI sets the split's behavior and the page's least width back on every
///   update, and a value set here and taken back each time laid the split out again and again until the scene ran out of
///   its 10 seconds (0x8BADF00D, #4206).
/// - **Slid by a finger, as Feishu's** (`drags`). From the window's leading edge while the column is away, and from the
///   line beside it while it shows, the column follows the finger at its one width; let go, it settles out or away by how
///   far it went and how fast. While it moves the page behind blurs. Only the column's layer moves (an additive slide over
///   UIKit's own placement) and the blur's animator advances: no row is laid out again (the width drag, which laid every row out on every frame, ran at 28.8 fps).
///   Side by side (on its side, the iPad) the page moves with the column, its edge on the column's, never under it, and
///   takes its new width once, under the blur, never in plain sight: putting the column away, once the finger has gone
///   far enough for the blur to hide it (`Theme.Split.reflow`; the strip the page leaves until then is in its color),
///   bringing it out, as the finger lets go, before the blur clears. Upright on the Duo the column lies over the page by
///   design and moves alone.
/// - **Gestures.** A page that can go back keeps the edge for its own swipe back (`SwipeBack`): the column comes from its
///   button there. The line's grab area is a tap target wide and away from the rows, whose swipes (close, delete) stay
///   theirs; a drag starts only sideways. Regular width has no other sideways gesture (the shell's pager is the phone's).
struct SplitColumnControl: UIViewControllerRepresentable {
    /// The finger may slide the column.
    var drags: Bool
    /// The column as UIKit has it, for the split's SwiftUI side: a finger moving it, the split itself.
    var state: SplitColumnState
    /// Where a drag left the column, shown or not, for the split's visibility (it is there already: no animation).
    var settle: (Bool) -> Void

    func makeUIViewController(context: Context) -> Control { Control() }

    func updateUIViewController(_ control: Control, context: Context) {
        control.drags = drags
        control.state = state
        control.settle = settle
        control.apply()
    }

    final class Control: UIViewController, UIGestureRecognizerDelegate {
        var drags = false
        var state = SplitColumnState()
        var settle: (Bool) -> Void = { _ in }
        private weak var split: UISplitViewController?
        private var open: UIPanGestureRecognizer?
        private var close: UIPanGestureRecognizer?
        private var drag: Drag?

        /// A drag under way: where the column started, how wide it is, what the split was, and the blur over the page.
        private struct Drag {
            let opening: Bool
            let width: CGFloat
            let behavior: UISplitViewController.SplitBehavior
            let column: UIView
            /// The page as drawn, moved with the column, its edge on the column's, where the two settle side by side; nil
            /// where the column lies over it.
            let page: UIView?
            /// Behind the page while it is narrower than the window: the strip its move leaves at the trailing edge, in
            /// the page's color.
            var fill: UIView?
            let blur: UIVisualEffectView
            let animator: UIViewPropertyAnimator
            var offset: CGFloat
            /// The page is the window's width, under the column (the split lays the column over it); else beside it, at
            /// its own width.
            var wide: Bool

            /// Where the page is moved to, from where the split lays it: wide it starts at the window's edge, beside the
            /// column at the column's end.
            var pageX: CGFloat { (wide ? width : 0) + offset }
        }

        override func loadView() {
            view = UIView()
            view.isUserInteractionEnabled = false
        }

        override func didMove(toParent parent: UIViewController?) {
            super.didMove(toParent: parent)
            if parent == nil { uninstall() } else { apply() }
        }

        override func viewWillLayoutSubviews() {
            super.viewWillLayoutSubviews()
            apply()
        }

        func apply() {
            guard let split = splitViewController else { return }
            install(on: split)
            state.split = split
            // The split's own edge swipe laid the column over the page and left its state unkept: ours replaces it.
            if split.presentsWithGesture == drags { split.presentsWithGesture = !drags }
            // The drag holds the page at the window's width under the column once it has taken it.
            if let drag, drag.wide, split.preferredSplitBehavior != .overlay { split.preferredSplitBehavior = .overlay }
        }

        // MARK: Gestures

        private func install(on split: UISplitViewController) {
            guard self.split !== split else { return }
            uninstall()
            self.split = split
            let open = UIPanGestureRecognizer(target: self, action: #selector(moved(_:)))
            open.maximumNumberOfTouches = 1
            open.delegate = self
            let close = UIPanGestureRecognizer(target: self, action: #selector(moved(_:)))
            close.maximumNumberOfTouches = 1
            close.delegate = self
            split.view.addGestureRecognizer(open)
            split.view.addGestureRecognizer(close)
            self.open = open
            self.close = close
        }

        private func uninstall() {
            if let open { open.view?.removeGestureRecognizer(open) }
            if let close { close.view?.removeGestureRecognizer(close) }
            open = nil
            close = nil
            split = nil
        }

        private func shown(_ split: UISplitViewController) -> Bool { split.displayMode != .secondaryOnly }

        func gestureRecognizerShouldBegin(_ recognizer: UIGestureRecognizer) -> Bool {
            guard drags, drag == nil, let split, split.view.window != nil, let pan = recognizer as? UIPanGestureRecognizer else { return false }
            if recognizer === open {
                // A page that can go back keeps its edge (SwipeBack); the column comes from its button there.
                // From the window's leading edge, a finger's width in, to the right.
                let v = pan.velocity(in: split.view)
                let x = pan.location(in: split.view).x - pan.translation(in: split.view).x
                return !shown(split) && x <= Theme.Size.tapTarget / 2 && v.x > 0 && abs(v.x) > abs(v.y) * 1.5 && !canGoBack(split)
            }
            guard recognizer === close, shown(split) else { return false }
            let v = pan.velocity(in: split.view)
            // The line beside the column: a tap target wide, half over the column and half over the page.
            let line = split.primaryColumnWidth
            let x = pan.location(in: split.view).x - pan.translation(in: split.view).x
            // To the left only: on the line, a drag to the right is the page's swipe back (its edge is the line).
            guard v.x < 0, abs(v.x) > abs(v.y) * 1.5 else { return false }
            return abs(x - line) <= Theme.Size.tapTarget / 2
        }

        /// The list's scroll, its rows' swipes and the page's own drags wait for ours to fail. The line's drag fails at the
        /// first move anywhere but on the line, or moving up and down; the edge's anywhere but at the screen's edge
        /// (`gestureRecognizerShouldBegin`). Without this the list's pan, or the page's, took the touch first.
        func gestureRecognizer(_ recognizer: UIGestureRecognizer, shouldBeRequiredToFailBy other: UIGestureRecognizer) -> Bool {
            guard let split, recognizer === close || recognizer === open, other !== open, other !== close,
                  other is UIPanGestureRecognizer, let view = other.view else { return false }
            // The page's own swipe back is not made to wait: where it can go back, ours does not begin at all.
            if recognizer === open, other is UIScreenEdgePanGestureRecognizer { return false }
            return view.isDescendant(of: split.view)
        }

        /// A page pushed in the detail column: its stack's swipe back owns the edge.
        private func canGoBack(_ split: UISplitViewController) -> Bool {
            guard let page = split.viewController(for: .secondary) else { return false }
            var queue: [UIViewController] = [page]
            while let next = queue.popLast() {
                if let nav = next as? UINavigationController, nav.viewControllers.count > 1, nav.view.window != nil { return true }
                queue.append(contentsOf: next.children)
            }
            return false
        }

        @objc private func moved(_ pan: UIPanGestureRecognizer) {
            guard let split else { return }
            switch pan.state {
            case .began: begin(split, opening: pan === open)
            case .changed: follow(pan.translation(in: split.view).x)
            case .ended: end(split, velocity: pan.velocity(in: split.view).x)
            case .cancelled, .failed: end(split, velocity: 0)
            default: break
            }
        }

        // MARK: The drag

        private func begin(_ split: UISplitViewController, opening: Bool) {
            let width = split.primaryColumnWidth
            let behavior = split.preferredSplitBehavior
            // Side by side when it settles where the page beside it keeps its least width (`SplitPageMin`); elsewhere (the open
            // Duo, an iPad mini, upright) the column lies over the page.
            let beside = opening ? state.fitsBeside == true : Self.beside(split)
            state.dragging = true
            // Coming out, the column lies over the page, the window's width as it was with the column away: it moves with
            // the column and takes its own width as the finger lets go. Going away beside it, the page keeps its width
            // until the blur can hide the change (`follow`).
            if opening || !beside {
                UIView.performWithoutAnimation {
                    split.preferredSplitBehavior = .overlay
                    if opening { split.show(.primary) }
                    split.view.layoutIfNeeded()
                }
            }
            if opening { settle(true) }
            guard let content = split.viewController(for: .primary)?.view, let pageContent = split.viewController(for: .secondary)?.view else {
                state.dragging = false
                return
            }
            // The column and the page as drawn: the split's own views around them (the column's background, iPadOS 26's
            // floating panel), not only the list inside, which slid out of a column that stayed put.
            let column = Self.drawn(content)
            let page = beside ? Self.drawn(pageContent) : nil
            var fill: UIView?
            if let page, !opening {
                let strip = UIView(frame: split.view.bounds)
                strip.autoresizingMask = [.flexibleWidth, .flexibleHeight]
                strip.isUserInteractionEnabled = false
                strip.backgroundColor = Self.color(of: pageContent) ?? Self.color(of: page) ?? Self.pageColor
                split.view.insertSubview(strip, at: 0)
                fill = strip
            }
            let blur = UIVisualEffectView(effect: nil)
            blur.frame = pageContent.bounds
            blur.autoresizingMask = [.flexibleWidth, .flexibleHeight]
            blur.isUserInteractionEnabled = false
            pageContent.addSubview(blur)
            let animator = UIViewPropertyAnimator(duration: 1, curve: .linear) {
                blur.effect = UIBlurEffect(style: .systemUltraThinMaterial)
            }
            animator.pausesOnCompletion = true
            animator.fractionComplete = 0
            let d = Drag(opening: opening, width: width, behavior: behavior, column: column, page: page, fill: fill, blur: blur,
                         animator: animator, offset: opening ? -width : 0, wide: opening)
            place(d)
            drag = d
        }

        private func follow(_ dx: CGFloat) {
            guard var d = drag, let split else { return }
            d.offset = min(0, max(-d.width, (d.opening ? -d.width : 0) + dx))
            // The page blurs as the finger moves, from nothing: none at the touch, the most a quarter of the way across.
            let blur = min(1, abs(dx) / (d.width * Theme.Split.blurReach))
            d.animator.fractionComplete = blur * Theme.Split.dragBlur
            // Far enough for the blur to hide it, the page going away with the column takes the window's width.
            if d.page != nil, !d.wide, blur >= Theme.Split.reflow { widen(split, &d) }
            place(d)
            drag = d
        }

        /// The page takes the window's width, the column over it: from now its strip at the trailing edge is the page itself.
        private func widen(_ split: UISplitViewController, _ d: inout Drag) {
            drag?.wide = true
            UIView.performWithoutAnimation {
                split.preferredSplitBehavior = .overlay
                split.view.layoutIfNeeded()
            }
            d.wide = true
            d.fill?.removeFromSuperview()
            d.fill = nil
        }

        /// The column and the page where the finger has them.
        private func place(_ d: Drag) {
            Self.place(d.column.layer, at: d.offset)
            if let page = d.page { Self.place(page.layer, at: d.pageX) }
        }

        /// A view's own color, if it paints one.
        private static func color(of view: UIView) -> UIColor? {
            guard let color = view.backgroundColor, color.cgColor.alpha > 0 else { return nil }
            return color
        }

        /// The style's page color (`themedPage`), where the page's views paint none of their own.
        private static var pageColor: UIColor {
            StyleSpec.current.usesGlass ? .systemBackground : UIColor(Theme.pageBackground)
        }

        /// The column's slide, added to where UIKit puts it: UIKit sets the column's own transform as it lays the split out,
        /// every frame while it lays it over the page, so a transform set here did not last to the next frame.
        private static let slide = "transform.translation.x"

        /// The split's own view around a column's content (as wide as it), else the content.
        private static func drawn(_ content: UIView) -> UIView {
            content.superview.map { $0.bounds.width == content.bounds.width ? $0 : content } ?? content
        }

        private static func place(_ layer: CALayer, at x: CGFloat) {
            let still = CABasicAnimation(keyPath: slide)
            still.fromValue = x
            still.toValue = x
            still.duration = .greatestFiniteMagnitude
            still.isAdditive = true
            still.fillMode = .both
            still.isRemovedOnCompletion = false
            layer.add(still, forKey: slide)
        }

        private func end(_ split: UISplitViewController, velocity: CGFloat) {
            guard var d = drag else { return }
            // A fling decides; else past halfway.
            let shows = abs(velocity) > Theme.Split.fling ? velocity > 0 : d.offset > -d.width / 2
            // The page's new width, now, while it is still blurred: beside the column again (the split's own layout back),
            // or the window's (a flick put the column away before the page had taken it).
            if d.page != nil {
                if shows, d.wide {
                    // Not held wide by `apply` as the split lays itself out again.
                    drag?.wide = false
                    UIView.performWithoutAnimation {
                        split.preferredSplitBehavior = d.behavior
                        split.view.layoutIfNeeded()
                    }
                    d.wide = !Self.beside(split)
                } else if !shows, !d.wide {
                    widen(split, &d)
                }
                place(d)
                drag = d
            }
            d.animator.isReversed = true
            d.animator.continueAnimation(withTimingParameters: nil, durationFactor: Theme.Split.settle)
            let target: CGFloat = shows ? 0 : -d.width
            CATransaction.begin()
            CATransaction.setCompletionBlock { self.finish(split, d, shows: shows) }
            d.column.layer.add(Self.settling(from: d.offset, to: target, velocity: velocity, width: d.width), forKey: Self.slide)
            // Wherever it settles, the split lays the page where it ends: away, the window's width from its edge; beside
            // the column, from the column's end; under it, from the edge.
            d.page?.layer.add(Self.settling(from: d.pageX, to: 0, velocity: velocity, width: d.width), forKey: Self.slide)
            CATransaction.commit()
        }

        /// The slide from where the finger left it to where it settles, at the finger's speed.
        private static func settling(from: CGFloat, to: CGFloat, velocity: CGFloat, width: CGFloat) -> CASpringAnimation {
            let spring = CASpringAnimation(keyPath: slide)
            spring.fromValue = from
            spring.toValue = to
            spring.initialVelocity = width > 0 ? velocity / width : 0
            spring.duration = Theme.Split.settle
            spring.isAdditive = true
            spring.fillMode = .forwards
            spring.isRemovedOnCompletion = false
            return spring
        }

        private func finish(_ split: UISplitViewController, _ d: Drag, shows: Bool) {
            // Over before the split lays itself out again: `apply` no longer holds the page wide.
            drag = nil
            d.animator.stopAnimation(true)
            d.blur.removeFromSuperview()
            d.fill?.removeFromSuperview()
            // Back to how the split lays its columns, in one go with the slides removed, where they were drawn already (the
            // page took its width as the finger let go).
            UIView.performWithoutAnimation {
                if !shows { split.hide(.primary) }
                split.preferredSplitBehavior = d.behavior
                split.view.layoutIfNeeded()
            }
            d.column.layer.removeAnimation(forKey: Self.slide)
            d.page?.layer.removeAnimation(forKey: Self.slide)
            state.dragging = false
            settle(shows)
        }

        /// The column beside the page, not over it.
        private static func beside(_ split: UISplitViewController) -> Bool {
            split.displayMode == .oneBesideSecondary || split.displayMode == .twoBesideSecondary
        }
    }
}

/// A split's list column as UIKit has it (`SplitColumnControl`), read where it is needed, not observed: a drag redraws
/// nothing. `fitsBeside` reads the split's own size, as a turn lays it out: SwiftUI's geometry for the new size can come
/// after the split's own change of columns, and a list the turn put away was kept as closed (#4206).
final class SplitColumnState {
    /// A finger is moving the column: the split lays it over the page meanwhile, which is not its posture.
    var dragging = false
    weak var split: UISplitViewController?

    /// The page beside the column is at least its least width at the split's size now (`SplitColumns.fitsBeside`); nil
    /// before the split is laid out.
    @MainActor var fitsBeside: Bool? {
        guard let width = split?.view.bounds.width, width > 0 else { return nil }
        return SplitColumns.fitsBeside(width: width)
    }
}
#endif
