#if DEBUG && (os(iOS) || os(visionOS))
import SwiftUI
import UIKit

/// Holding the screen still for a screenshot (docs/operations/runtime-cli.md「Screens that never stop moving」): `freeze`.
extension RuntimeCLI {
    static let motionCommands: [String: Command] = [
        "freeze": { a in freeze(a) },
    ]

    /// Reduce Motion as it was before `freeze`, saved: the setting is the simulator's, so it outlives the app. A launch
    /// that finds it (the app killed or a script stopped while frozen) puts it back (`restoreReduceMotion`).
    private static let reduceMotionBeforeKey = "runtimeCLIReduceMotionBefore"
    private static var reduceMotionBefore: Bool? {
        get { UserDefaults.standard.object(forKey: reduceMotionBeforeKey) as? Bool }
        set { UserDefaults.standard.set(newValue, forKey: reduceMotionBeforeKey) }
    }

    /// At launch (`startIfAsked`, every DEBUG launch): Reduce Motion back as it was before a `freeze` never turned off.
    static func restoreReduceMotion() {
        guard let before = reduceMotionBefore else { return }
        setReduceMotion(before)
        reduceMotionBefore = nil
    }

    /// `on` (default true) / false. `reduceMotion` (default true): the simulator's Reduce Motion, which the views that
    /// read it draw still (a needs-you dot stops breathing, BeautifulUI's chat stops); put back as it was by `on: false`
    /// or, when that never came, by the next launch.
    /// `layers`: what Reduce Motion does not reach (a UIKit spinner in a working agent's steps, seen through the glass
    /// bar too): "strip" (default: every running Core Animation animation removed once, layers jump to where they were
    /// going; the same pixels run after run), "pause" (Core Animation's clock stopped in the app's windows: what runs now
    /// stays where it is, at whatever phase; transitions started meanwhile do not end, so unfreeze before navigating) or
    /// "none". Not reached either way: views on a timer (`TimelineView`: "N min ago") and data that keeps coming (a
    /// machine's live metrics), for which `settle` takes `ignore`.
    private static func freeze(_ a: Args) -> Reply {
        let on = a["on"] as? Bool ?? true
        if a["reduceMotion"] as? Bool ?? true {
            if on {
                if reduceMotionBefore == nil { reduceMotionBefore = UIAccessibility.isReduceMotionEnabled }
                setReduceMotion(true)
            } else if let before = reduceMotionBefore {
                setReduceMotion(before)
                reduceMotionBefore = nil
            }
        }
        switch on ? a["layers"] as? String ?? "strip" : "resume" {
        case "pause":
            for w in windows where w.layer.speed != 0 {
                let t = w.layer.convertTime(CACurrentMediaTime(), from: nil)
                w.layer.speed = 0
                w.layer.timeOffset = t
            }
        case "strip":
            func strip(_ l: CALayer) {
                l.removeAllAnimations()
                l.sublayers?.forEach(strip)
            }
            windows.forEach { strip($0.layer) }
        case "resume":
            for w in windows where w.layer.speed == 0 {
                let paused = w.layer.timeOffset
                w.layer.speed = 1
                w.layer.timeOffset = 0
                w.layer.beginTime = 0
                w.layer.beginTime = w.layer.convertTime(CACurrentMediaTime(), from: nil) - paused
            }
        default: break
        }
        return ["frozen": on, "reduceMotion": UIAccessibility.isReduceMotionEnabled,
                "paused": windows.contains { $0.layer.speed == 0 }]
    }

    private static func setReduceMotion(_ on: Bool) {
        guard let h = dlopen("/usr/lib/libAccessibility.dylib", RTLD_NOW),
              let f = dlsym(h, "_AXSSetReduceMotionEnabled") else { return }
        typealias Set = @convention(c) (Int32) -> Void
        unsafeBitCast(f, to: Set.self)(on ? 1 : 0)
    }
}
#endif
