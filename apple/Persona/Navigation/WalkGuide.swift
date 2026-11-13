import CoreLocation
import Foundation

// Walking navigation inside Persona (#6672): the pure part. Where a fix falls on the route, which step the walker is on,
// how far the next maneuver and the end are, when they have left the route or arrived, and what to say when. No
// Core Location manager, MapKit request or timer here, so all of it runs in unit tests (WalkGuideTests).

/// One step of a walking route as `MKDirections` gives it: the instruction for the maneuver where the step starts
/// ("Turn right onto Nanjing Road"), in the phone's language, and the line walked after it.
nonisolated struct WalkStep: Equatable, Sendable {
    var instruction: String
    /// The step's own line, from its maneuver to the next one.
    var line: [CLLocationCoordinate2D]

    static func == (a: WalkStep, b: WalkStep) -> Bool {
        a.instruction == b.instruction && a.line.elementsEqual(b.line) { $0.latitude == $1.latitude && $0.longitude == $1.longitude }
    }
}

/// How a maneuver turns, from the angle between the way in and the way out. `MKRoute.Step` has no maneuver type, so
/// the arrow on the screen is read off the line itself.
nonisolated enum WalkManeuver: Equatable, Sendable {
    case start, straight, slightLeft, slightRight, left, right, sharpLeft, sharpRight, uTurn, arrive

    /// A turn of `degrees` (positive: clockwise, to the right).
    static func turn(_ degrees: Double) -> WalkManeuver {
        let a = abs(degrees)
        switch a {
        case ..<20: return .straight
        case ..<60: return degrees > 0 ? .slightRight : .slightLeft
        case ..<135: return degrees > 0 ? .right : .left
        case ..<165: return degrees > 0 ? .sharpRight : .sharpLeft
        default: return .uTurn
        }
    }
}

/// A walking route ready to guide: its steps with the empty ones dropped, the whole line, and where each step starts
/// along it.
nonisolated struct WalkRoute: Sendable {
    var name: String
    var destination: CLLocationCoordinate2D
    var steps: [WalkStep]
    /// Apple's estimate for the whole walk, in seconds.
    var expectedSeconds: Double
    /// The steps' lines joined, without repeating the point where one ends and the next starts.
    let line: [CLLocationCoordinate2D]
    /// Meters along `line` at each of its points.
    let along: [Double]
    /// Meters along `line` where each step starts.
    let stepStarts: [Double]
    /// The index in `line` where each step starts.
    let stepFirstPoint: [Int]

    var length: Double { along.last ?? 0 }

    /// Drops what `MKDirections` adds that guides nobody: steps without a line, and the first step's empty instruction
    /// when it has no length ("start here"). Nil when nothing walkable is left.
    init?(name: String, destination: CLLocationCoordinate2D, steps: [WalkStep], expectedSeconds: Double) {
        let kept = steps.filter { $0.line.count >= 2 }
        guard !kept.isEmpty else { return nil }
        var line: [CLLocationCoordinate2D] = []
        var firsts: [Int] = []
        for step in kept {
            if let last = line.last, let first = step.line.first, WalkGeo.meters(last, first) < 0.5 {
                firsts.append(line.count - 1)
                line += step.line.dropFirst()
            } else {
                firsts.append(line.count)
                line += step.line
            }
        }
        var along = [0.0]
        for i in 1..<line.count { along.append(along[i - 1] + WalkGeo.meters(line[i - 1], line[i])) }
        self.name = name
        self.destination = destination
        self.steps = kept
        self.expectedSeconds = max(0, expectedSeconds)
        self.line = line
        self.along = along
        self.stepFirstPoint = firsts
        self.stepStarts = firsts.map { along[$0] }
    }

    /// The maneuver where step `i` starts: the first is where the walk starts; the others turn by the angle between
    /// the end of the line before and the start of the line after.
    func maneuver(at i: Int) -> WalkManeuver {
        guard i > 0, i < steps.count else { return i >= steps.count ? .arrive : .start }
        let p = stepFirstPoint[i]
        guard let before = WalkGeo.pointBack(line, from: p, meters: 8),
              let after = WalkGeo.pointAhead(line, from: p, meters: 8) else { return .straight }
        let into = WalkGeo.bearing(before, line[p])
        let out = WalkGeo.bearing(line[p], after)
        return .turn(WalkGeo.signedTurn(from: into, to: out))
    }
}

/// Where the walker is on the route.
nonisolated struct WalkProgress: Equatable, Sendable {
    /// The step being walked.
    var step: Int
    /// Meters along the route of the fix's nearest point.
    var along: Double
    /// Meters from the fix to the route.
    var offRoute: Double
    /// Meters to the next maneuver (the start of the next step), or to the end on the last step.
    var toNext: Double
    /// Meters left to the destination.
    var remaining: Double
    /// Seconds left, Apple's estimate scaled by the meters left.
    var remainingSeconds: Double
    /// The segment of the line the fix fell on, the hint for the next fix.
    var segment: Int

    /// The next maneuver's step, or nil when the next thing is arriving.
    func nextStep(in route: WalkRoute) -> Int? { step + 1 < route.steps.count ? step + 1 : nil }
}

nonisolated enum WalkGuide {
    /// How far back a fix may fall from the last one's segment before it is taken as walking back: GPS jitters.
    static let backtrack = 30.0
    /// A walker this close to the destination has arrived.
    static let arrival = 20.0
    /// A fix this far from the route counts as off it, unless its own accuracy is worse.
    static let offRoute = 40.0

    /// Where `fix` falls on `route`, searching from the last fix's segment (`hint`) onward, and a little back. A route
    /// can pass the same corner twice; searching forward keeps the walker on the leg they are on.
    static func progress(of fix: CLLocationCoordinate2D, on route: WalkRoute, hint: Int = 0) -> WalkProgress {
        let line = route.line
        let start = max(0, min(hint, line.count - 2))
        var best = (segment: start, along: route.along[start], distance: Double.infinity)
        // Back from the hint by up to `backtrack` meters, then forward to the end.
        var first = start
        while first > 0, route.along[start] - route.along[first - 1] <= backtrack { first -= 1 }
        for i in first..<(line.count - 1) {
            let (t, d) = WalkGeo.project(fix, on: line[i], line[i + 1])
            if d < best.distance - 0.01 {
                best = (i, route.along[i] + t * (route.along[i + 1] - route.along[i]), d)
            }
        }
        let along = best.along
        let step = max(0, (route.stepStarts.lastIndex { $0 <= along + 0.01 }) ?? 0)
        let next = step + 1 < route.stepStarts.count ? route.stepStarts[step + 1] : route.length
        let remaining = max(0, route.length - along)
        let seconds = route.length > 0 ? route.expectedSeconds * remaining / route.length : 0
        return WalkProgress(step: step, along: along, offRoute: best.distance, toNext: max(0, next - along),
                            remaining: remaining, remainingSeconds: seconds, segment: best.segment)
    }

    /// Whether `fix` has reached the destination: close to it, or at the end of the line.
    static func arrived(_ fix: CLLocationCoordinate2D, _ progress: WalkProgress, on route: WalkRoute) -> Bool {
        WalkGeo.meters(fix, route.destination) <= arrival || (progress.remaining <= arrival && progress.offRoute <= offRoute)
    }

    /// Whether a fix `progress` with `accuracy` meters of uncertainty is off the route.
    static func isOff(_ progress: WalkProgress, accuracy: Double) -> Bool {
        progress.offRoute > max(offRoute, accuracy)
    }
}

/// When to work the route out again: off the route for a while (one bad fix is GPS, not the walker), and not more often
/// than Apple's directions allow.
nonisolated struct WalkReroute: Sendable {
    /// Fixes in a row off the route before a new route is asked for.
    static let fixes = 3
    /// Seconds off the route before a new route is asked for.
    static let after: TimeInterval = 8
    /// Seconds between two requests: `MKDirections` throttles an app that asks often.
    static let spacing: TimeInterval = 15

    private(set) var offSince: Date?
    private(set) var offFixes = 0
    private(set) var lastAsked: Date?

    /// Takes a fix: true when it is time to ask for a new route (and counts the request as made).
    mutating func take(off: Bool, at now: Date) -> Bool {
        guard off else { offSince = nil; offFixes = 0; return false }
        if offSince == nil { offSince = now }
        offFixes += 1
        guard offFixes >= Self.fixes, let since = offSince, now.timeIntervalSince(since) >= Self.after else { return false }
        if let lastAsked, now.timeIntervalSince(lastAsked) < Self.spacing { return false }
        lastAsked = now
        offSince = nil
        offFixes = 0
        return true
    }
}

/// What the voice says and when: the first instruction at the start, each maneuver once as it comes within `ahead`
/// meters, and the arrival. A step shorter than that is said as soon as the walker is on the step before.
nonisolated struct WalkAnnouncer: Sendable {
    /// How far ahead of a maneuver it is said: about half a minute's walk.
    static let ahead = 40.0

    /// What is due.
    nonisolated enum Line: Equatable, Sendable {
        /// The walk starts: step 0's instruction.
        case start(step: Int)
        /// The maneuver starting `step`, `meters` ahead.
        case maneuver(step: Int, meters: Double)
        case arrived
    }

    private(set) var said: Set<Int> = []
    private(set) var saidArrival = false

    /// The line due for `progress`, if any, marked as said.
    mutating func due(_ progress: WalkProgress, on route: WalkRoute, arrived: Bool) -> Line? {
        if arrived {
            guard !saidArrival else { return nil }
            saidArrival = true
            return .arrived
        }
        if !said.contains(0) {
            said.insert(0)
            // Already past the start's step (a new route from where the walker is), straight to what comes next.
            if progress.step == 0 { return .start(step: 0) }
        }
        guard let next = progress.nextStep(in: route), !said.contains(next), progress.toNext <= Self.ahead else { return nil }
        said.insert(next)
        return .maneuver(step: next, meters: progress.toNext)
    }

    /// Forgets what was said, for a new route.
    mutating func reset() { said = []; saidArrival = false }
}

/// Plane geometry good for a walk: a few kilometers, where the earth is flat enough.
nonisolated enum WalkGeo {
    private static let metersPerDegree = 111_195.0

    static func meters(_ a: CLLocationCoordinate2D, _ b: CLLocationCoordinate2D) -> Double {
        let (x, y) = xy(b, around: a)
        return hypot(x, y)
    }

    /// `p` in meters east and north of `origin`.
    static func xy(_ p: CLLocationCoordinate2D, around origin: CLLocationCoordinate2D) -> (Double, Double) {
        let scale = cos(origin.latitude * .pi / 180)
        return ((p.longitude - origin.longitude) * metersPerDegree * scale, (p.latitude - origin.latitude) * metersPerDegree)
    }

    /// Where `p` falls on the segment from `a` to `b`: how far along it (0…1) and how far off it, in meters.
    static func project(_ p: CLLocationCoordinate2D, on a: CLLocationCoordinate2D, _ b: CLLocationCoordinate2D) -> (Double, Double) {
        let (bx, by) = xy(b, around: a)
        let (px, py) = xy(p, around: a)
        let length = bx * bx + by * by
        guard length > 0 else { return (0, hypot(px, py)) }
        let t = max(0, min(1, (px * bx + py * by) / length))
        return (t, hypot(px - t * bx, py - t * by))
    }

    /// The compass bearing from `a` to `b`, 0…360, clockwise from north.
    static func bearing(_ a: CLLocationCoordinate2D, _ b: CLLocationCoordinate2D) -> Double {
        let (x, y) = xy(b, around: a)
        let degrees = atan2(x, y) * 180 / .pi
        return degrees < 0 ? degrees + 360 : degrees
    }

    /// The turn from heading `a` to heading `b`, -180…180, positive clockwise.
    static func signedTurn(from a: Double, to b: Double) -> Double {
        var d = (b - a).truncatingRemainder(dividingBy: 360)
        if d > 180 { d -= 360 }
        if d <= -180 { d += 360 }
        return d
    }

    /// The first point at least `meters` back along `line` from point `i`, or the line's start; nil at the start.
    static func pointBack(_ line: [CLLocationCoordinate2D], from i: Int, meters: Double) -> CLLocationCoordinate2D? {
        guard i > 0 else { return nil }
        var j = i - 1
        while j > 0, Self.meters(line[j], line[i]) < meters { j -= 1 }
        return line[j]
    }

    /// The first point at least `meters` ahead along `line` from point `i`, or the line's end; nil at the end.
    static func pointAhead(_ line: [CLLocationCoordinate2D], from i: Int, meters: Double) -> CLLocationCoordinate2D? {
        guard i < line.count - 1 else { return nil }
        var j = i + 1
        while j < line.count - 1, Self.meters(line[i], line[j]) < meters { j += 1 }
        return line[j]
    }
}
