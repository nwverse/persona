import SwiftUI

#if os(iOS) || os(visionOS)
/// Keeps what you tapped under your finger while it opens or closes. The chat's list is flipped (the newest end is
/// its origin), so a row that grows pushes everything above it up, the tapped header included: a thought jumped
/// 40 pt on opening, and a work line, whose steps load after the tap, went 440 pt up and its header out of reach
/// (2026-10-31, measured frame by frame). Rows report where they are (`chatPinned`); a tap holds that key for a
/// moment (`hold`), and every time layout moves the header the list scrolls it back, until it settles, the time is
/// up or the finger takes the list.
@MainActor final class ChatPin {
    /// The chat list's scroll view (`ChatPin.Finder` in its content). Moved directly: asking `ScrollPosition` for a
    /// point landed on it, then 10 pt off a moment later (the content margin), and back, frame after frame.
    weak var list: UIScrollView?
    /// The flipped list's actual distance from its newest end; SwiftUI geometry can lag a completed jump.
    var measuredNewestGap: CGFloat? {
        guard let list else { return nil }
        return list.contentOffset.y + list.adjustedContentInset.top
    }
    /// Set by the chat: forget the position it last asked its list for. At the newest that is the newest end, and the
    /// list went back to it for a frame when a work line's steps arrived.
    var letGo: () -> Void = {}
    /// Set by the chat: a held header began, or ended, rising to show what opened under it (`rising`). Meanwhile the
    /// newest end is off the screen for a moment, and ↓ came up for that moment only (#1123).
    var onRising: (Bool) -> Void = { _ in }
    /// Set by the chat: a row is opening or closing under the finger (`toggleInPlace`). True when the chat would take the
    /// list to whatever comes in next and now holds it instead (`ChatFollow.toggledRow`): the row is kept where it is
    /// until the user takes the list, taps ↓ or sends (#3005).
    var anchor: () -> Bool = { false }

    /// The coordinate space of the list's content: a place in it does not change when the list scrolls.
    nonisolated static let space = "chatContent"
    /// How long a tapped header is held at most.
    nonisolated static let holdTime: Double = 1.5

    /// Where a header is: on screen, and in the list's content. Both come from one layout pass, so the offset that
    /// puts it back is worked out from the content alone; correcting by the screen and the reported offset went past
    /// the mark, and back, when one of the two lagged the other by a frame.
    nonisolated struct Spot: Equatable, Sendable { var y: CGFloat; var content: CGFloat }

    private struct Held {
        var key: String; var at: Spot; var base: CGFloat; var until: Date; var slow: Bool; var rise: CGFloat
        /// The content's height when the hold began, and whether the header has reported since (`watch`).
        var height: CGFloat = 0
        var reported = false
        /// It has glided once to where the list can be (`moved`): from then on the list follows the header at once.
        var glided = false
        /// How far it has risen so far (`wanted`), and when that was worked out.
        var risen: CGFloat = 0
        var risenAt: CFTimeInterval = 0
        var rising = false
    }
    private var held: Held?
    /// The offset that puts the held header back, when the list could not go there yet (`settle`).
    private var want: CGFloat?
    /// How far into the content the held header's far edge is now: the content is at least that long, even before the
    /// scroll view's content size says so.
    private var reach: CGFloat = 0
    private var last: [String: Spot] = [:]
    /// The list is gliding to where the held header goes (`moved`): nothing puts it meanwhile.
    private var gliding: Date?
    /// A kept hold a tap above its header interrupted: taken up again once that one ends (`ended`).
    private var resume: String?

    /// Before opening or closing: keep `key` where `from` (the header before, if it changes id) is now. `slow`: what
    /// opens comes later (a work line's steps load from the hub), so the hold lasts its full 1.5 s; otherwise it ends
    /// as soon as the header is back. `rise`: how far up the header may go to show what opens under it. Opened at the
    /// newest end, the steps came in under the composer and the header held still: the tap seemed to do nothing.
    /// `keep`: held until the finger takes the list or `release()`, not for 1.5 s: a running work line opened at the newest
    /// end stays under the top bar while its steps keep coming in under it (#1046), instead of each one pushing it up.
    func hold(_ key: String, from: String? = nil, slow: Bool = false, rise: CGFloat = 0, keep: Bool = false) {
        guard let list, let at = last[from ?? key] else { return }
        if !keep, let k = held, k.until == .distantFuture {
            // A row under the kept header (a step of the open work line): the kept hold has it in place already, as it
            // has all between the header and what grows. Replaced by this one, it let go when this one ended, and what
            // streamed in from then on pushed the screen up and back (#1046).
            // From now on the kept hold keeps the header where it is: the rise it was allowed (to show its steps) is
            // over. Left to it, what opened under a header not yet at the top went up by its own height, the tapped
            // row with it (a thought, BeautifulUI: 46 pt, #1123).
            if let kept = last[k.key], at.content < kept.content {
                held = Held(key: k.key, at: kept, base: list.contentOffset.y, until: k.until, slow: true, rise: 0,
                            height: list.contentSize.height, reported: k.reported)
                return
            }
            resume = k.key
        } else if keep {
            resume = nil
        }
        held = Held(key: key, at: at, base: list.contentOffset.y, until: keep ? .distantFuture : .now.addingTimeInterval(Self.holdTime),
                    slow: slow || keep, rise: max(rise, 0), height: list.contentSize.height)
        want = nil
        reach = 0
        letGo()
        if slow || keep { watch() }
    }

    /// A header pushed far off screen in one pass (a work line's cached steps all laid out at once) leaves the lazy list:
    /// it reports no more, and nothing put it back (BeautifulUI, 2026-11-04: the header gone, the steps
    /// from the sixth on). Until it reports, the list moves by what the content grew: all that opens is newer than it.
    private func watch() {
        Task { @MainActor [weak self] in
            for _ in 0..<45 {
                try? await Task.sleep(for: .milliseconds(16))
                guard let self, let h = self.held, !h.reported, let list = self.list else { return }
                if list.isTracking || list.isDecelerating { return }
                let grown = list.contentSize.height - h.height
                guard grown > 0.5 else { continue }
                self.rising(grown)
                guard let h = self.held else { return }
                let target = Self.within(list, self.wanted(h, grown: grown))
                if abs(target - list.contentOffset.y) >= 0.5 { list.contentOffset.y = target }
            }
        }
    }

    /// Something is held: following the newest must wait (it scrolled to the end as soon as a work line's steps
    /// arrived, and the header went with it).
    var holding: Bool { held.map { Date.now <= $0.until } ?? false }

    /// Lets go of what is held (a kept hold: ↓, sending, the user's scrolling).
    func release() {
        held = nil
        gliding = nil
        want = nil
        resume = nil
    }

    /// The list got taller or shorter by `by` at its bottom edge (what sits under it came or went: the queue strip, a
    /// card, the mode bar). Drawn upside down from that edge, everything on it moved by as much; a held header is put back
    /// where it was. The queued message taken as the next turn started moved the rows the user had open 60 pt down (#3005).
    func resized(by d: CGFloat) {
        guard var h = held, Date.now <= h.until, abs(d) >= 0.5, let list, !list.isTracking, !list.isDecelerating,
              let spot = last[h.key] else { return }
        h.base -= d
        held = h
        want = wanted(h, grown: spot.content - h.at.content)
        put(list)
    }

    /// The kept hold is no longer kept (the line folded, another one opened): it lasts the usual time from now, so what
    /// was tapped settles first.
    func unkeep() {
        resume = nil
        if var h = held, h.until == .distantFuture {
            h.until = .now.addingTimeInterval(Self.holdTime)
            held = h
        }
    }

    /// A hold is over. One that interrupted a kept hold hands the list back to it, unless a finger has the list.
    private func ended(_ list: UIScrollView) {
        held = nil
        guard let key = resume else { return }
        resume = nil
        if !list.isTracking, !list.isDecelerating { hold(key, slow: true, keep: true) }
    }

    /// A header moved (layout or scrolling).
    #if DEBUG
    /// Where `key` last reported itself on screen (`UnfoldTrace`).
    func screenY(_ key: String) -> CGFloat? { last[key]?.y }
    /// How tall the list's content is (`UnfoldTrace`).
    var contentHeight: CGFloat? { list?.contentSize.height }
    /// What is held, for `ScrollTrace`: its key, and whether it is kept.
    var traceHeld: String { held.map { "\($0.key)\($0.until == .distantFuture ? "+kept" : "")" } ?? "-" }
    #endif

    func moved(_ key: String, to spot: Spot) {
        last[key] = spot
        guard var h = held, h.key == key, let list else { return }
        if Date.now > h.until || list.isTracking || list.isDecelerating { return ended(list) }
        if !h.reported, abs(spot.content - h.at.content) >= 0.5 { h.reported = true; held = h } // it moved, and says where
        if !h.slow, abs(spot.y - h.at.y) < 0.5 { return ended(list) }
        // The content runs newest first: rows opening under the header on screen push it further into the content,
        // and the offset has to grow by as much to keep it on screen where it was (as far as the list can go). Allowed to
        // rise, the header goes up by that much first: the list moves only for what opened past it.
        let grown = spot.content - h.at.content
        reach = spot.content
        held = h
        rising(grown)
        guard let h = held else { return }
        let goal = wanted(h, grown: grown)
        if let g = gliding, Date.now < g { want = goal; return }
        // Folded at the newest end, the list cannot keep the header where it was: it stays for this frame and the list
        // glides to where it can be. In one frame it went 400–470 pt down (#1123).
        let still = h.base + grown, target = Self.within(list, goal, content: spot.content)
        if !h.glided, target - still >= 0.5, !Theme.Motion.reduced {
            var h = h
            h.glided = true
            held = h
            want = nil
            glide(list, from: still, to: target)
            return
        }
        let settling = want != nil
        want = goal
        put(list)
        if !settling { settle() }
    }

    /// Where the list keeps the held header: where it was, but for how far it may rise to show what opened under it
    /// (`rise`, as far as that has grown). That rise takes `glideTime` from when the growth began (`rising`), worked out
    /// again each frame from what has grown by then: the steps come in more than one pass, and a rise in one frame went
    /// 400–470 pt up at once on opening at the newest end (#1123).
    private func wanted(_ h: Held, grown: CGFloat) -> CGFloat {
        grown > 0 ? h.base + grown - h.risen : h.base + grown
    }

    /// How far the header may rise by now (`rise`, as far as what opened has grown) is reached a part at a time: each
    /// frame closes the gap with a time constant of `riseTau` (90 % in a quarter second), so however the steps come in,
    /// in one pass or several, the header goes up smoothly. With Reduce Motion, at once.
    private func rising(_ grown: CGFloat) {
        guard var h = held else { return }
        let full = min(max(grown, 0), h.rise)
        if Theme.Motion.reduced || full <= h.risen + 0.5 {
            if h.risen > full || Theme.Motion.reduced { h.risen = full; held = h }
            return
        }
        guard !h.rising else { return }
        h.rising = true
        h.risenAt = CACurrentMediaTime()
        held = h
        onRising(true)
        Task { @MainActor [weak self] in
            defer { self?.onRising(false) }
            while true {
                try? await Task.sleep(for: .milliseconds(8))
                guard let self, var h = self.held, let list = self.list, !list.isTracking, !list.isDecelerating else { return }
                let grown = h.reported ? self.reach - h.at.content : list.contentSize.height - h.height
                let full = min(max(grown, 0), h.rise), now = CACurrentMediaTime()
                // At most a frame's worth at a time: after a long frame (the steps laid out) it went most of the way at once.
                h.risen += (full - h.risen) * (1 - exp(-min(now - h.risenAt, 1.0 / 60) / Self.riseTau))
                h.risenAt = now
                if full - h.risen < 0.5 { h.risen = full; h.rising = false }
                self.held = h
                self.want = self.wanted(h, grown: grown)
                self.put(list)
                if !h.rising { return }
            }
        }
    }

    /// The rise's time constant (`rising`).
    nonisolated static let riseTau: Double = 0.1

    /// The list from `from` (where it keeps the header still, beyond its ends if need be) to `to`, as UIKit glides a
    /// scroll view (`setContentOffset(_:animated:)`, about 0.3 s): offset by offset, so the lazy list draws the rows
    /// each one shows. Then anything that came meanwhile.
    private func glide(_ list: UIScrollView, from: CGFloat, to: CGFloat) {
        list.contentOffset.y = from
        list.setContentOffset(CGPoint(x: list.contentOffset.x, y: to), animated: true)
        gliding = .now.addingTimeInterval(Self.glideTime)
        Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(Self.glideTime))
            guard let self, let list = self.list else { return }
            self.gliding = nil
            if self.want != nil, !list.isTracking { self.put(list) }
        }
    }

    /// How long UIKit's animated scroll takes.
    nonisolated static let glideTime: Double = 0.35

    /// After something opened under `key`'s header: brings its far edge (`bottom`, on screen) above the list's bottom
    /// edge (the chat's last row: to the newest end), the header staying on screen. The header is held where it was, so a row at the newest end grows past the
    /// composer: the notes under a compaction divider, the last row on a short screen, were half hidden until you
    /// scrolled (2026-11-04).
    func show(_ key: String, bottom: CGFloat) {
        guard let list, let top = last[key]?.y, !list.isTracking, !list.isDecelerating else { return }
        held = nil
        want = nil
        // The list is upside down: its content top inset is the composer side on screen, its bottom inset the far side.
        let frame = list.convert(list.bounds, to: nil)
        let inset = list.adjustedContentInset
        let edge = frame.maxY - inset.top, gap = list.contentOffset.y + inset.top
        let over = bottom - edge, room = top - (frame.minY + inset.bottom)
        guard room >= 0.5 else { return }
        // The chat's last row (nothing but a row's spacing between it and the newest end): back to the newest end, as it
        // was before the tap. Left where the hold put it, a little short of it, the end marker was off screen and ↓
        // came up over the notes.
        let last = edge + gap - bottom <= Theme.Space.xxl
        let by = last && gap <= room ? gap : min(over, room)
        guard by >= 0.5 else { return }
        // A smaller offset is toward the newest: what is on screen moves up.
        list.setContentOffset(CGPoint(x: list.contentOffset.x, y: Self.within(list, list.contentOffset.y - by)), animated: true)
    }

    /// The list goes as near `want` as it can now.
    private func put(_ list: UIScrollView) {
        guard let want else { return }
        let target = Self.within(list, want, content: reach)
        if abs(target - list.contentOffset.y) >= 0.5 { list.contentOffset.y = target }
        if abs(target - want) < 0.5 { self.want = nil }
    }

    /// The list's content size follows its rows a pass late: just after a work line's steps came in, the list could not
    /// go as far as the header needed (a conversation that fit on one screen could not move at all), and the header
    /// moved no more, so nothing asked again: one blank frame on the steps still fading in, then the header gone off
    /// the top (#754). Asked again on the next passes, while the hold lasts.
    private func settle() {
        Task { @MainActor [weak self] in
            for _ in 0..<30 {
                try? await Task.sleep(for: .milliseconds(16))
                guard let self, self.want != nil, let h = self.held, let list = self.list else { return }
                if let g = self.gliding, Date.now < g { continue }
                if h.rising { continue } // `rising` puts it
                if Date.now > h.until || list.isTracking || list.isDecelerating { self.want = nil; return self.ended(list) }
                self.put(list)
            }
            self?.want = nil
        }
    }

    /// An offset the list can be at. `content`: how long the content is known to be (the header's far edge in it): the
    /// rows are laid out a pass before the scroll view's content size follows, and capped by the old size the list
    /// stayed at the newest end for a frame, on steps still fading in: blank (#754).
    private static func within(_ list: UIScrollView, _ y: CGFloat, content: CGFloat = 0) -> CGFloat {
        let inset = list.adjustedContentInset
        let height = max(list.contentSize.height, content)
        return min(max(y, -inset.top), max(-inset.top, height + inset.bottom - list.bounds.height))
    }

    /// Sits in the list's content and hands the scroll view around it to the pin.
    struct Finder: UIViewRepresentable {
        let pin: ChatPin

        func makeUIView(context: Context) -> Probe {
            let v = Probe()
            v.pin = pin
            v.isUserInteractionEnabled = false
            return v
        }

        func updateUIView(_ v: Probe, context: Context) { v.pin = pin }

        final class Probe: UIView {
            weak var pin: ChatPin?

            override func didMoveToWindow() {
                super.didMoveToWindow()
                var v = superview
                while let s = v, !(s is UIScrollView) { v = s.superview }
                if let list = v as? UIScrollView { pin?.list = list }
            }
        }
    }
}

#endif

extension EnvironmentValues {
    /// The chat list's pin; nil outside the chat (sheets, other pages), where rows animate as before.
    @Entry var chatPin: ChatPin? = nil
}

extension View {
    /// Reports this row's top to the chat's pin under `key`, so opening or closing it keeps it in place.
    func chatPinned(_ key: String) -> some View { modifier(ChatPinReport(key: key)) }
}

private struct ChatPinReport: ViewModifier {
    let key: String
    @Environment(\.chatPin) private var pin

    func body(content: Content) -> some View {
        if let pin {
            // The content is upside down: the row's top on screen is its far edge there.
            content.onGeometryChange(for: ChatPin.Spot.self, of: {
                ChatPin.Spot(y: $0.frame(in: .global).minY, content: $0.frame(in: .named(ChatPin.space)).maxY)
            }) { pin.moved(key, to: $0) }
        } else {
            content
        }
    }
}

/// Opens or closes something in a row (`change`). In the chat the height changes at once, with the row's top held in
/// place (`ChatPin`): a height animating in the flipped lazy list slid everything above it. What opens fades in over
/// the space it takes (`unfold`, shown with `.unfolding`); what closes goes at once, its space with it. It used to
/// fold away first, its space held, and then leave: for a fifth of a second the rows under it sat below a fading copy
/// of the output and a blank (#3005). Where the chat would follow what comes in next, the tapped row is kept where it
/// is instead (`ChatPin.anchor`). `closing` names the tap for the trace. Elsewhere: the animation as before.
@MainActor func toggleInPlace(_ pin: ChatPin?, _ key: String, animation: Animation, unfold: Binding<Unfolding>? = nil,
                              closing: Bool = false, _ change: @escaping () -> Void) {
    #if DEBUG
    UnfoldTrace.shared.begin("\(key.split(separator: "-").first ?? "row") \(closing ? "fold" : "toggle")",
                             height: { [weak pin] in pin?.contentHeight }) { [weak pin] in pin?.screenY(key) }
    #endif
    guard let pin else { withAnimation(animation, change); return }
    pin.hold(key, keep: pin.anchor())
    let reveals = !closing && unfold != nil && !Theme.Motion.reduced
    if reveals { unfold?.wrappedValue.revealing = true }
    var t = Transaction()
    t.disablesAnimations = true
    withTransaction(t, change)
    guard reveals, let unfold else { return }
    Task { @MainActor in
        try? await Task.sleep(for: .seconds(Theme.Motion.revealTime))
        unfold.wrappedValue.revealing = false
    }
}

/// What a row shows when opened, as it comes in the chat (`toggleInPlace`).
struct Unfolding: Equatable {
    /// Just opened: it fades in.
    var revealing = false
}

extension View {
    /// What a row shows when opened: it fades in when it appears while `revealing`, over all the space it takes from the
    /// first frame. Its height never animates, so the list's layout changes once and the pin holds the header in that
    /// same pass. It used to be revealed from the top by a mask growing down: until the mask got there, the rows pushed
    /// under it sat below a blank (#3005).
    func unfolding(_ u: Unfolding) -> some View { modifier(Reveal(revealing: u.revealing)) }
}

private struct Reveal: ViewModifier {
    @State private var shown: Double

    init(revealing: Bool) {
        _shown = State(initialValue: revealing ? 0 : 1)
    }

    /// Reduce Motion: it is there at once.
    @Environment(\.accessibilityReduceMotion) private var still

    func body(content: Content) -> some View {
        if still {
            content
        } else {
            content
                .opacity(shown)
                .onAppear { if shown < 1 { withAnimation(Theme.Motion.reveal) { shown = 1 } } }
        }
    }
}
