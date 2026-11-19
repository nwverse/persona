#if os(iOS)
import QuartzCore
import SwiftUI
import UIKit

/// The mark before a row's second line (#10465; docs/design/proposals/todos-redesign.md 3.4): ● needs you breathes
/// exactly as on Agents rows (`StatusDot`, DESIGN_SYSTEM 2.7), ◐ working turns its amber arc, ✓ just finished in the
/// unread green, ◷ waiting an idle clock. Both loops pause off screen and hold still with Reduce Motion.
struct TodoMarkView: View {
    var mark: TodoMark

    var body: some View {
        // A letter's room in the text beside it, the mark over its middle: the row lines up on that letter's baseline.
        // A drawn mark has none of its own, and a UIKit one (the arc) reports its top as its baseline.
        Text(verbatim: "M").hidden()
            .overlay { drawn }
            .accessibilityHidden(true) // the row's label says the state in words
    }

    @ViewBuilder private var drawn: some View {
        switch mark {
        case .needs:
            StatusDot(state: .needsYou, size: Theme.Size.dot)
        case .working:
            TodoWorkingArc()
        case .ready:
            ThemedIcon(Theme.Icon.done).font(Theme.Font.labelHeavy).foregroundStyle(StatusPalette.unread.dot)
        case .waiting:
            ThemedIcon(Theme.Icon.time).font(Theme.Font.label).foregroundStyle(StatusPalette.idle.dot)
        }
    }
}

/// ◐: Working's amber arc, turning in the compositor while it can be seen (`VisibleWork`), still with Reduce Motion.
struct TodoWorkingArc: View {
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VisibleWork { eligible in
            TodoArcRing(color: StatusPalette.working.dot, turning: eligible && !reduceMotion)
        }
        .scaledFrame(width: Theme.Size.markXS, height: Theme.Size.markXS)
        .allowsHitTesting(false)
    }
}

/// The arc as a Core Animation layer: a SwiftUI rotation would redraw its row on every frame (P9).
private struct TodoArcRing: UIViewRepresentable {
    var color: Color
    var turning: Bool

    func makeUIView(context: Context) -> TodoArcRingView { TodoArcRingView() }

    func updateUIView(_ view: TodoArcRingView, context: Context) {
        view.configure(color: color.resolve(in: context.environment).cgColor, displayScale: context.environment.displayScale,
                       turning: turning)
    }

    static func dismantleUIView(_ view: TodoArcRingView, coordinator: ()) {
        view.stop()
    }
}

final class TodoArcRingView: UIView {
    private let arc = CAShapeLayer()
    private static let key = "turn"
    private var turning = false

    override init(frame: CGRect) {
        super.init(frame: frame)
        isUserInteractionEnabled = false
        arc.fillColor = nil
        arc.lineWidth = Theme.Size.workingArcLine
        arc.lineCap = .round
        arc.strokeEnd = Theme.Size.workingArcSweep
        layer.addSublayer(arc)
    }

    @available(*, unavailable) required init?(coder: NSCoder) { fatalError() }

    override func hitTest(_ point: CGPoint, with event: UIEvent?) -> UIView? { nil }

    func configure(color: CGColor, displayScale: CGFloat, turning: Bool) {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        arc.strokeColor = color
        arc.contentsScale = displayScale
        CATransaction.commit()
        self.turning = turning
        updateAnimation()
    }

    override func layoutSubviews() {
        super.layoutSubviews()
        let side = min(bounds.width, bounds.height) - Theme.Size.workingArcLine
        guard side > 0 else { return }
        // A local square around the middle of the view, turned about its own middle (never `frame` on a layer that turns).
        let local = CGRect(origin: .zero, size: bounds.size)
        let circle = CGRect(x: local.midX - side / 2, y: local.midY - side / 2, width: side, height: side)
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        arc.bounds = local
        arc.position = CGPoint(x: bounds.midX, y: bounds.midY)
        arc.path = CGPath(ellipseIn: circle, transform: nil)
        CATransaction.commit()
        updateAnimation()
    }

    override func didMoveToWindow() {
        super.didMoveToWindow()
        updateAnimation()
    }

    private func updateAnimation() {
        guard turning, window != nil, bounds.width > 0 else {
            arc.removeAnimation(forKey: Self.key)
            return
        }
        guard arc.animation(forKey: Self.key) == nil else { return }
        let turn = CABasicAnimation(keyPath: "transform.rotation.z")
        turn.fromValue = 0
        turn.toValue = 2 * Double.pi
        turn.duration = Theme.Motion.workingArcTurn
        turn.repeatCount = .infinity
        turn.preferredFrameRateRange = CAFrameRateRange(minimum: Theme.Motion.breathMinimumFPS, maximum: Theme.Motion.breathPreferredFPS,
                                                        preferred: Theme.Motion.breathPreferredFPS)
        arc.add(turn, forKey: Self.key)
    }

    func stop() {
        turning = false
        arc.removeAnimation(forKey: Self.key)
    }
}
#endif
