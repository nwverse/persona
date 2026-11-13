#if DEBUG
import AppKit

/// Scrolling the chat while it streams, on a Mac that may be locked (docs/apple/mac.md「Scroll experiments」): no real trackpad and no
/// screen recording, so the app scrolls itself and draws itself.
///
/// - `-macScrollGestures "6:-300,14:120"`: at 6 s after launch a two-finger swipe of 300 pt, at 14 s one of 120 pt,
///   each as a trackpad sends it: the fingers' events (began, changed, ended over 120 ms), then the glide's (momentum
///   begin, changes decaying over about a second, end), handed to the chat's scroll view. The sign is the wheel's:
///   negative moves the content up, as fingers moving up do with natural scrolling.
/// - `-macScrollFrames <dir> -macScrollFramesFrom 5 -macScrollFramesFor 20 -macScrollFramesEvery 0.1`: the window
///   drawn into `<dir>/m-<seconds>.png` every 0.1 s for 20 s from 5 s after launch.
/// - The scroll trace (`-scrollTrace YES`, `ScrollTrace`) gets the gesture's phases from here, and from real scroll
///   events through a local monitor.
@MainActor enum MacScrollLab {
    private static var start = Date.now

    static func scheduleIfAsked() {
        let d = UserDefaults.standard
        start = .now
        if ScrollTrace.enabled {
            NSEvent.addLocalMonitorForEvents(matching: .scrollWheel) { e in
                note(e)
                return e
            }
        }
        if let spec = d.string(forKey: "macScrollGestures") {
            for part in spec.split(separator: ",") {
                let f = part.split(separator: ":").compactMap { Double($0) }
                guard f.count == 2 else { continue }
                DispatchQueue.main.asyncAfter(deadline: .now() + f[0]) { swipe(f[1]) }
            }
        }
        if let dir = d.string(forKey: "macScrollFrames") {
            try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
            let from = d.object(forKey: "macScrollFramesFrom") != nil ? d.double(forKey: "macScrollFramesFrom") : 5
            let span = d.object(forKey: "macScrollFramesFor") != nil ? d.double(forKey: "macScrollFramesFor") : 20
            let every = d.object(forKey: "macScrollFramesEvery") != nil ? d.double(forKey: "macScrollFramesEvery") : 0.1
            DispatchQueue.main.asyncAfter(deadline: .now() + from) { frames(dir, until: .now + span, every: every) }
        }
    }

    private static func note(_ e: NSEvent) {
        if e.phase.contains(.began) || e.phase.contains(.changed) { ScrollTrace.shared.gesture = true }
        if e.phase.contains(.ended) || e.phase.contains(.cancelled) { ScrollTrace.shared.gesture = false }
        if e.momentumPhase.contains(.began) || e.momentumPhase.contains(.changed) { ScrollTrace.shared.momentum = true }
        if e.momentumPhase.contains(.ended) || e.momentumPhase.contains(.cancelled) { ScrollTrace.shared.momentum = false }
    }

    /// The chat's list: the one its pin holds (`ScrollTrace`, so this needs `-scrollTrace YES`).
    private static var list: NSScrollView? { ScrollTrace.shared.currentList }

    /// One scroll event: `scroll` / `momentum` phases as CGEvent values, `dy` in points.
    private static func event(_ dy: Double, scroll: Int64, momentum: Int64, at view: NSView) -> NSEvent? {
        guard let cg = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 1, wheel1: Int32(dy.rounded()), wheel2: 0, wheel3: 0) else { return nil }
        cg.setIntegerValueField(.scrollWheelEventIsContinuous, value: 1)
        cg.setIntegerValueField(.scrollWheelEventScrollPhase, value: scroll)
        cg.setIntegerValueField(.scrollWheelEventMomentumPhase, value: momentum)
        cg.setDoubleValueField(.scrollWheelEventPointDeltaAxis1, value: dy)
        cg.setDoubleValueField(.scrollWheelEventFixedPtDeltaAxis1, value: dy)
        if let w = view.window {
            // Over the list, in the event system's coordinates (top-left origin).
            let r = w.convertToScreen(view.convert(view.bounds, to: nil))
            let h = NSScreen.screens.first?.frame.height ?? r.maxY
            cg.location = CGPoint(x: r.midX, y: h - r.midY)
            // The window it is for, as the window server marks a real one (a locked Mac has no key window to fall to).
            cg.setIntegerValueField(.mouseEventWindowUnderMousePointer, value: Int64(w.windowNumber))
            cg.setIntegerValueField(.mouseEventWindowUnderMousePointerThatCanHandleThisEvent, value: Int64(w.windowNumber))
        }
        return NSEvent(cgEvent: cg)
    }

    /// A two-finger swipe of `total` points and its glide (CGScrollPhase: began 1, changed 2, ended 4;
    /// CGMomentumScrollPhase: begin 1, continue 2, end 3).
    private static func swipe(_ total: Double) {
        guard let list else { FileHandle.standardError.write(Data("MacScrollLab: no list\n".utf8)); return }
        var steps: [(Double, Int64, Int64, Double)] = [] // (dy, scroll, momentum, delay before it)
        let fingers = 8
        steps.append((0, 1, 0, 0))
        for _ in 0..<fingers { steps.append((total * 0.6 / Double(fingers), 2, 0, 0.015)) }
        steps.append((0, 4, 0, 0.015))
        // The glide: what is left of the distance, each event 0.9 of the one before, 16 ms apart.
        var v = total * 0.4 * 0.1
        steps.append((v, 0, 1, 0.016))
        while abs(v) > 0.5 { v *= 0.9; steps.append((v, 0, 2, 0.016)) }
        steps.append((0, 0, 3, 0.016))
        func send(_ i: Int) {
            guard i < steps.count else { return }
            let s = steps[i]
            DispatchQueue.main.asyncAfter(deadline: .now() + s.3) {
                if let e = event(s.0, scroll: s.1, momentum: s.2, at: list) {
                    // Straight to the list, and to `MacWheelWatch` (a locked Mac delivers no posted events); through the
                    // app's queue with `-macScrollPost YES` (an unlocked one).
                    if UserDefaults.standard.bool(forKey: "macScrollPost") {
                        NSApp.postEvent(e, atStart: false)
                    } else {
                        note(e)
                        MacWheelWatch.feed?(e)
                        list.scrollWheel(with: e)
                    }
                }
                send(i + 1)
            }
        }
        FileHandle.standardError.write(Data(String(format: "MacScrollLab swipe %.0f at %.3f\n", total, Date.now.timeIntervalSince1970).utf8))
        send(0)
    }

    /// The window drawn as the snapshot draws it (`MacSnapshot.capture`: views draw themselves, a locked screen works);
    /// `<dir>/list.txt` says where the chat's list is in it (x y width height, points from the top left).
    private static func frames(_ dir: String, until end: Date, every: Double) {
        guard Date.now < end else { return }
        if let list, let w = list.window, let content = w.contentView {
            let r = list.convert(list.bounds, to: content)
            let top = content.isFlipped ? r.minY : content.bounds.height - r.maxY
            try? String(format: "%.0f %.0f %.0f %.0f\n", r.minX, top, r.width, r.height)
                .write(toFile: dir + "/list.txt", atomically: true, encoding: .utf8)
            _ = MacSnapshot.capture(to: URL(fileURLWithPath: String(format: "%@/m-%.3f.png", dir, Date.now.timeIntervalSince1970)), window: w)
        }
        DispatchQueue.main.asyncAfter(deadline: .now() + every) { frames(dir, until: end, every: every) }
    }
}
#endif
