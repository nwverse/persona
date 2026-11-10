#if DEBUG || PERF_SIGNPOST
import AppKit
import QuartzCore

/// Measures how smooth the Mac app is, from inside, the same way every run (#1337, docs/apple/mac.md「Measuring hitches on Mac」).
///
/// The Mac this was written on has no Instruments display tracing (Developer Mode off) and posted wheel events from a
/// shell tool are filtered out (no Accessibility grant), so the scroll is driven here and frames are timed here: a
/// display link on the main run loop records when each frame could start. When the main thread is busy past a frame,
/// the next tick comes late; that lateness is the hitch the user feels. Waiting on the render server delays it too: a
/// header rebuilt at every fold left the main thread's CPU time about the same and the frames late (#1337). Frames
/// the render server drops on its own are not seen.
///
/// `-showPerfRun scroll|sections|chats|resize|launch|pushes` (sections: `-showPerfSections a,b` for just those, once; `-showPerfRounds n` times), `-showPerfDelay <s>` (default 6), `-showPerfOut <json>`,
/// `-showPerfQuit YES`; scroll: `-showPerfPoint x,y` (window points from the top left, inside the scroll view to
/// drive), `-showPerfSpeed <points per frame>` (default 12), `-showPerfSeconds <s>` (default 6), `-showPerfGlide
/// YES|up|live`; pushes: `-showPerfChurn <url>` (the mock hub's `/mock/churn?…`), with `-perfCounts YES`.
@MainActor enum MacPerfHarness {
    static func scheduleIfAsked(_ model: AppModel) {
        let d = UserDefaults.standard
        guard let run = d.string(forKey: "showPerfRun") else { return }
        let delay = d.object(forKey: "showPerfDelay") != nil ? d.double(forKey: "showPerfDelay") : 6
        if run == "launch" { MacPerfLaunch.shared.start(); return }
        DispatchQueue.main.asyncAfter(deadline: .now() + delay) {
            switch run {
            case "scroll": MacPerfScroll.shared.start()
            case "sections": MacPerfActions.shared.start(.sections, model)
            case "chats": MacPerfActions.shared.start(.chats, model)
            case "resize": MacPerfResize.shared.start()
            case "pushes": MacPerfPushes.shared.start()
            default: finish(["error": "unknown run \(run)"])
            }
        }
    }

    static var window: NSWindow? {
        NSApp.windows.first { $0.isVisible && $0.identifier?.rawValue.hasPrefix("main") == true }
            ?? NSApp.windows.first { $0.isVisible && !($0 is NSPanel) && $0.contentView != nil }
    }

    /// Writes the result where `-showPerfOut` says (and the log), then quits when asked.
    static func finish(_ result: [String: Any]) {
        let d = UserDefaults.standard
        let data = (try? JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys])) ?? Data()
        if let path = d.string(forKey: "showPerfOut") { try? data.write(to: URL(fileURLWithPath: path)) }
        FileHandle.standardError.write(Data("macPerf \(String(decoding: data, as: UTF8.self))\n".utf8))
        if d.bool(forKey: "showPerfQuit") {
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { NSApp.terminate(nil); exit(0) }
        }
    }
}

/// Counts UserDefaults writes during a run, and which keys ended up different: each write invalidates every view with
/// an `@AppStorage` (CLAUDE.md「Most common pitfalls」).
@MainActor final class MacDefaultsWatch {
    private var token: Any?
    private var before: [String: Any] = [:]
    private(set) var writes = 0

    func start() {
        before = UserDefaults.standard.dictionaryRepresentation()
        token = NotificationCenter.default.addObserver(forName: UserDefaults.didChangeNotification, object: nil, queue: nil) { [weak self] _ in
            MainActor.assumeIsolated { self?.writes += 1 }
        }
    }

    func stop() -> [String: Any] {
        if let token { NotificationCenter.default.removeObserver(token) }
        let after = UserDefaults.standard.dictionaryRepresentation()
        let changed = Set(before.keys).union(after.keys).filter { k in
            switch (before[k], after[k]) {
            case let (a as NSObject, b as NSObject): return a != b
            case (nil, nil): return false
            default: return true
            }
        }
        return ["defaultsWrites": writes, "defaultsChanged": Array(changed).sorted().prefix(20).map { $0 }]
    }
}

/// The main thread's own CPU time, in milliseconds.
nonisolated func macPerfMainThreadCPU() -> Double {
    var info = thread_basic_info()
    var count = mach_msg_type_number_t(MemoryLayout<thread_basic_info_data_t>.size / MemoryLayout<integer_t>.size)
    let port = pthread_mach_thread_np(pthread_self())
    let kr = withUnsafeMutablePointer(to: &info) {
        $0.withMemoryRebound(to: integer_t.self, capacity: Int(count)) { thread_info(port, thread_flavor_t(THREAD_BASIC_INFO), $0, &count) }
    }
    guard kr == KERN_SUCCESS else { return 0 }
    return Double(info.user_time.seconds + info.system_time.seconds) * 1000
        + Double(info.user_time.microseconds + info.system_time.microseconds) / 1000
}

/// The whole process's CPU time (every thread), in milliseconds.
nonisolated func macPerfProcessCPU() -> Double {
    var u = rusage()
    getrusage(RUSAGE_SELF, &u)
    return Double(u.ru_utime.tv_sec + u.ru_stime.tv_sec) * 1000 + Double(u.ru_utime.tv_usec + u.ru_stime.tv_usec) / 1000
}

/// Ticks of the screen's display link on the main run loop: when each frame could start.
@MainActor final class MacFrameClock: NSObject {
    private var link: CADisplayLink?
    private(set) var ticks: [CFTimeInterval] = []
    private(set) var refresh: CFTimeInterval = 1.0 / 60
    var onTick: ((CFTimeInterval) -> Void)?

    func start() {
        guard link == nil, let l = (MacPerfHarness.window?.screen ?? NSScreen.main)?.displayLink(target: self, selector: #selector(tick(_:))) else { return }
        l.preferredFrameRateRange = CAFrameRateRange(minimum: 60, maximum: 120, preferred: 120)
        l.add(to: .main, forMode: .common)
        link = l
    }

    func stop() { link?.invalidate(); link = nil }

    @objc private func tick(_ l: CADisplayLink) {
        let now = CACurrentMediaTime()
        if l.duration > 0 { refresh = l.duration }
        ticks.append(now)
        onTick?(now)
    }

    /// Frame statistics over ticks in [from, to]: the hitch time (beyond one refresh, for frames longer than 1.5
    /// refreshes) per second of the run, frames dropped, the longest frame and the 95th percentile.
    func stats(from: CFTimeInterval, to: CFTimeInterval) -> [String: Any] {
        let ts = ticks.filter { $0 >= from && $0 <= to }
        let gaps = zip(ts.dropFirst(), ts).map { ($0 - $1) * 1000 }
        let r = refresh * 1000
        let long = gaps.filter { $0 > r * 1.5 }
        let hitchMs = long.reduce(0) { $0 + ($1 - r) }
        let dropped = long.reduce(0) { $0 + max(0, Int(($1 / r).rounded()) - 1) }
        let seconds = max(to - from, 0.001)
        let sorted = gaps.sorted()
        let p95 = sorted.isEmpty ? 0 : sorted[min(sorted.count - 1, Int(Double(sorted.count) * 0.95))]
        return ["frames": gaps.count, "refreshMs": round1(r), "hitchMsPerS": round1(hitchMs / seconds), "dropped": dropped,
                "longestMs": round1(sorted.last ?? 0), "p95Ms": round1(p95), "seconds": round1(seconds)]
    }
}

func round1(_ v: Double) -> Double { (v * 10).rounded() / 10 }

/// Drives a scroll view at a steady speed (points per frame) down and back up, ping-pong, for a few seconds.
@MainActor final class MacPerfScroll {
    static let shared = MacPerfScroll()
    private let clock = MacFrameClock()
    private let defaults = MacDefaultsWatch()
    private var scroll: NSScrollView?
    private var direction: CGFloat = 1

    func start() {
        let d = UserDefaults.standard
        guard let window = MacPerfHarness.window, let frameView = window.contentView?.superview ?? window.contentView else {
            return MacPerfHarness.finish(["error": "no window"])
        }
        let parts = (d.string(forKey: "showPerfPoint") ?? "").split(separator: ",").compactMap { Double($0) }
        let point = parts.count == 2 ? CGPoint(x: parts[0], y: parts[1]) : CGPoint(x: 400, y: 500)
        // The smallest scroll view under the point (window coordinates from the top left).
        let inWindow = CGPoint(x: point.x, y: frameView.bounds.height - point.y)
        let candidates = Self.descendants(of: frameView).compactMap { $0 as? NSScrollView }.filter {
            !$0.isHidden && $0.convert($0.bounds, to: nil).contains(inWindow)
                && ($0.documentView?.frame.height ?? 0) > $0.contentView.bounds.height + 1
        }
        guard let sv = candidates.min(by: { $0.bounds.width * $0.bounds.height < $1.bounds.width * $1.bounds.height }) else {
            return MacPerfHarness.finish(["error": "no scroll view at \(point)"])
        }
        scroll = sv
        let speed = CGFloat(d.object(forKey: "showPerfSpeed") != nil ? d.double(forKey: "showPerfSpeed") : 12)
        let seconds = d.object(forKey: "showPerfSeconds") != nil ? d.double(forKey: "showPerfSeconds") : 6
        // `-showPerfGlide YES`: iOS's pattern (docs/operations/testing.md「Whole App」); `-showPerfGlide up` starts toward the top.
        // `-showPerfGlide live`: ping-pong as the user's own scroll, NSScrollView's live-scroll notifications around it
        // (SwiftUI's scroll phase is `.interacting`). Without them SwiftUI does not take it for the user scrolling: the
        // chat kept following its newest end and landing on the saved place against the drive, and a short chat's glide
        // reached its end in half a second, then timed a list standing still (#1337).
        let glideArg = d.string(forKey: "showPerfGlide")
        let glide = glideArg != nil
        let live = glideArg == "live"
        if glideArg == "up" { direction = -1 }
        var sweeps = 0
        let cpu0 = macPerfMainThreadCPU(), all0 = macPerfProcessCPU()
        defaults.start()
        let t0 = CACurrentMediaTime()
        var moved: CGFloat = 0
        clock.onTick = { [weak self] now in
            guard let self, let sv = self.scroll else { return }
            if now - t0 >= seconds {
                if live { NotificationCenter.default.post(name: NSScrollView.didEndLiveScrollNotification, object: sv) }
                self.clock.stop()
                let t1 = CACurrentMediaTime()
                var r = self.clock.stats(from: t0, to: t1)
                r["run"] = "scroll"
                r["pattern"] = live ? "live" : glide ? "glide" : "ping-pong"
                if live { r["sweeps"] = sweeps }
                r["point"] = "\(Int(point.x)),\(Int(point.y))"
                r["speedPtPerFrame"] = Double(speed)
                r["scrolledPt"] = Double(moved)
                r["scrollView"] = String(describing: type(of: sv.documentView ?? sv))
                r["documentHeight"] = Double(sv.documentView?.frame.height ?? 0)
                r["mainCPUMsPerS"] = round1((macPerfMainThreadCPU() - cpu0) / (t1 - t0))
                r["processCPUMsPerS"] = round1((macPerfProcessCPU() - all0) / (t1 - t0))
                r.merge(self.defaults.stop()) { $1 }
                MacPerfHarness.finish(r)
                return
            }
            let clip = sv.contentView
            let maxY = max(0, (sv.documentView?.frame.height ?? 0) - clip.bounds.height)
            var y: CGFloat
            if live {
                if sweeps == 0 { sweeps = 1; NotificationCenter.default.post(name: NSScrollView.willStartLiveScrollNotification, object: sv) }
                y = clip.bounds.origin.y + speed * self.direction
                if y >= maxY { y = maxY; self.direction = -1; sweeps += 1 } else if y <= 0 { y = 0; self.direction = 1; sweeps += 1 }
                moved += abs(y - clip.bounds.origin.y)
                clip.setBoundsOrigin(NSPoint(x: clip.bounds.origin.x, y: y))
                sv.reflectScrolledClipView(clip)
                NotificationCenter.default.post(name: NSScrollView.didLiveScrollNotification, object: sv)
                return
            }
            if glide {
                // As iOS's ListGlide: one way for half the time, back for the other half, stopping at the ends.
                let dir: CGFloat = (now - t0) < seconds / 2 ? 1 : -1
                y = min(max(clip.bounds.origin.y + speed * dir * self.direction, 0), maxY)
            } else {
                y = clip.bounds.origin.y + speed * self.direction
                if y >= maxY { y = maxY; self.direction = -1 } else if y <= 0 { y = 0; self.direction = 1 }
            }
            moved += abs(y - clip.bounds.origin.y)
            clip.setBoundsOrigin(NSPoint(x: clip.bounds.origin.x, y: y))
            sv.reflectScrolledClipView(clip)
        }
        clock.start()
    }

    static func descendants(of v: NSView) -> [NSView] { v.subviews + v.subviews.flatMap(descendants) }
}

/// Section switches or chat openings, one at a time: how long the main thread stays busy before the next frame can
/// start (from the action to the first display tick after it), and the hitch time in the second after.
@MainActor final class MacPerfActions {
    enum Kind { case sections, chats }
    static let shared = MacPerfActions()
    private let clock = MacFrameClock()
    private let defaults = MacDefaultsWatch()

    func start(_ kind: Kind, _ model: AppModel) {
        clock.start()
        defaults.start()
        var steps: [(String, () -> Void)] = []
        switch kind {
        case .sections:
            // `-showPerfSections agents,knowledge` (once each, in that order) instead of every section three times.
            let asked = (UserDefaults.standard.string(forKey: "showPerfSections") ?? "").split(separator: ",").compactMap { MacSection(rawValue: String($0)) }
            let order = asked.isEmpty
                ? MacSection.shortcutOrder.filter { MacSection.visibleNow.contains($0) } : asked
            let rounds = UserDefaults.standard.integer(forKey: "showPerfRounds")
            for _ in 0..<(rounds > 0 ? rounds : asked.isEmpty ? 3 : 1) {
                for s in order { steps.append((s.rawValue, { MacShellState.shared.go(s, model) })) }
            }
        case .chats:
            MacShellState.shared.go(.agents, model)
            let keys = model.sessionsNow.filter { $0.assistant != true }.prefix(6).map(\.key)
            for _ in 0..<2 {
                for k in keys { steps.append((k.path, { MacShellState.shared.open(k, model) })) }
            }
        }
        var results: [[String: Any]] = []
        func next(_ i: Int) {
            guard i < steps.count else {
                clock.stop()
                let firsts = results.compactMap { $0["toFirstFrameMs"] as? Double }.sorted()
                let p = { (q: Double) -> Double in firsts.isEmpty ? 0 : firsts[min(firsts.count - 1, Int(Double(firsts.count) * q))] }
                var r: [String: Any] = ["run": kind == .sections ? "sections" : "chats", "steps": results,
                                        "medianToFirstFrameMs": p(0.5), "p90ToFirstFrameMs": p(0.9), "maxToFirstFrameMs": firsts.last ?? 0]
                r.merge(defaults.stop()) { $1 }
                MacPerfHarness.finish(r)
                return
            }
            let (name, act) = steps[i]
            let bodies0 = PerfCount.bodies
            let cpu0 = macPerfMainThreadCPU()
            let t0 = CACurrentMediaTime()
            act()
            // The first tick after the action: the main thread was free to start a frame.
            var first: CFTimeInterval?
            clock.onTick = { now in if first == nil, now > t0 { first = now } }
            DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) { [self] in
                var r = clock.stats(from: t0, to: t0 + 1.0)
                r["step"] = name
                r["toFirstFrameMs"] = round1(((first ?? t0) - t0) * 1000)
                r["mainCPUMs"] = round1(macPerfMainThreadCPU() - cpu0)
                // With `-perfCounts YES`: the views whose body ran for this step (and how often).
                if PerfCount.enabled {
                    r["bodies"] = PerfCount.bodies.compactMapValues { $0 }.reduce(into: [String: Int]()) { acc, kv in
                        let n = kv.value - (bodies0[kv.key] ?? 0)
                        if n > 0 { acc[kv.key] = n }
                    }
                }
                results.append(r)
                next(i + 1)
            }
        }
        next(0)
    }
}

/// Live-resizes the window narrower and back, as a drag of its edge does, for a few seconds.
@MainActor final class MacPerfResize {
    static let shared = MacPerfResize()
    private let clock = MacFrameClock()
    private let defaults = MacDefaultsWatch()

    func start() {
        guard let w = MacPerfHarness.window else { return MacPerfHarness.finish(["error": "no window"]) }
        let start = w.frame
        let seconds = 4.0, span = max(0, start.width - Theme.Size.macWindowMinWidth)
        let cpu0 = macPerfMainThreadCPU()
        let bodies0 = PerfCount.bodies // `-perfCounts YES`: which bodies the resize evaluates, and how often
        defaults.start()
        let t0 = CACurrentMediaTime()
        clock.onTick = { [weak self] now in
            guard let self else { return }
            let t = now - t0
            if t >= seconds {
                self.clock.stop()
                w.setFrame(start, display: true)
                let t1 = CACurrentMediaTime()
                var r = self.clock.stats(from: t0, to: t1)
                r["run"] = "resize"
                r["mainCPUMsPerS"] = round1((macPerfMainThreadCPU() - cpu0) / (t1 - t0))
                if PerfCount.enabled {
                    var bodies: [String: Int] = [:]
                    for (k, v) in PerfCount.bodies where v - (bodies0[k] ?? 0) > 0 { bodies[k] = v - (bodies0[k] ?? 0) }
                    r["bodies"] = bodies
                }
                r.merge(self.defaults.stop()) { $1 }
                MacPerfHarness.finish(r)
                return
            }
            // Width follows a triangle: full → narrowest → full, twice.
            let phase = (t / (seconds / 2)).truncatingRemainder(dividingBy: 1)
            let f = phase < 0.5 ? phase * 2 : (1 - phase) * 2
            var frame = start
            frame.size.width = start.width - span * f
            w.setFrame(frame, display: true)
        }
        clock.start()
    }
}

/// From the process's start to the first frame with the window up.
@MainActor final class MacPerfLaunch {
    static let shared = MacPerfLaunch()
    private let clock = MacFrameClock()

    func start() {
        clock.onTick = { [weak self] _ in
            guard let self, MacPerfHarness.window != nil else { return }
            self.clock.stop()
            var info = kinfo_proc(), size = MemoryLayout<kinfo_proc>.stride
            var mib: [Int32] = [CTL_KERN, KERN_PROC, KERN_PROC_PID, getpid()]
            sysctl(&mib, 4, &info, &size, nil, 0)
            let started = Double(info.kp_proc.p_starttime.tv_sec) + Double(info.kp_proc.p_starttime.tv_usec) / 1_000_000
            MacPerfHarness.finish(["run": "launch", "toFirstFrameMs": round1((Date().timeIntervalSince1970 - started) * 1000)])
        }
        clock.start()
    }
}
/// Hub pushes while a page stands still (`-showPerfRun pushes`): asks the mock hub for a burst of them
/// (`-showPerfChurn`, its `/mock/churn?n=…&ms=…`), then reports, over the next seconds, which view bodies were evaluated
/// and how often (`PerfCount`, `-perfCounts YES`), the main thread's time per push and the hitches.
@MainActor final class MacPerfPushes {
    static let shared = MacPerfPushes()
    private let clock = MacFrameClock()

    func start() {
        let d = UserDefaults.standard
        guard PerfCount.enabled, let url = d.string(forKey: "showPerfChurn").flatMap(URL.init(string:)) else {
            return MacPerfHarness.finish(["error": "pushes needs -perfCounts YES and -showPerfChurn <url>"])
        }
        let seconds = d.object(forKey: "showPerfSeconds") != nil ? d.double(forKey: "showPerfSeconds") : 8
        let bodies0 = PerfCount.bodies, events0 = PerfCount.events, passes0 = PerfCount.passes, ms0 = PerfCount.totalMS
        let cpu0 = macPerfMainThreadCPU()
        clock.start()
        let t0 = CACurrentMediaTime()
        URLSession.shared.dataTask(with: url).resume()
        DispatchQueue.main.asyncAfter(deadline: .now() + seconds) { [self] in
            clock.stop()
            let t1 = CACurrentMediaTime()
            var r = clock.stats(from: t0, to: t1)
            r["run"] = "pushes"
            var bodies: [String: Int] = [:]
            for (k, v) in PerfCount.bodies where v - (bodies0[k] ?? 0) > 0 { bodies[k] = v - (bodies0[k] ?? 0) }
            r["bodies"] = bodies
            let passes = PerfCount.passes - passes0
            r["pushes"] = PerfCount.events - events0
            r["pushPasses"] = passes
            r["pushMsAvg"] = passes > 0 ? round1((PerfCount.totalMS - ms0) / Double(passes)) : 0
            r["mainCPUMsPerS"] = round1((macPerfMainThreadCPU() - cpu0) / (t1 - t0))
            MacPerfHarness.finish(r)
        }
    }
}
#endif
