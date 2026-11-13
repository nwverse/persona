import CoreLocation
import Foundation

// Walking navigation inside Persona (#6672): the pure part. Where a fix falls on the route, which step the walker is on,
// how far the next maneuver and the end are, when they have left the route or arrived, and what to say when. No
// Core Location manager, MapKit request or timer here, so all of it runs in unit tests (WalkGuideTests).

/// One step of a walking route as `MKDirections` gives it: the line to walk, and the instruction for the maneuver
/// where that line ends ("Turn right onto Nanjing Road"), in the phone's language. Apple's walking routes read that
/// way: a first step with no length ("start the route"), each instruction turning the way the line turns at the step's
/// end, and a last step whose end is the destination ("arrive at the destination").
nonisolated struct WalkStep: Equatable, Sendable {
    var instruction: String
    /// The step's own line, from the maneuver before to its own.
    var line: [CLLocationCoordinate2D]

    static func == (a: WalkStep, b: WalkStep) -> Bool {
        a.instruction == b.instruction && a.line.elementsEqual(b.line) { $0.latitude == $1.latitude && $0.longitude == $1.longitude }
    }
}

/// How a maneuver turns, from the angle between the way in and the way out. `MKRoute.Step` has no maneuver type, so
/// the arrow on the screen is read off the line itself.
nonisolated enum WalkManeuver: Equatable, Sendable {
    case straight, slightLeft, slightRight, left, right, sharpLeft, sharpRight, uTurn, arrive

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
/// and ends along it.
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
    /// Meters along `line` where each step ends: where its maneuver is.
    let stepEnds: [Double]
    /// The index in `line` where each step ends.
    let stepLastPoint: [Int]

    var length: Double { along.last ?? 0 }

    /// Drops what `MKDirections` adds that guides nobody: steps without a line (the first one, "start the route").
    /// Nil when nothing walkable is left.
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
        let lasts = Array(firsts.dropFirst()) + [line.count - 1]
        self.stepLastPoint = lasts
        self.stepEnds = lasts.map { along[$0] }
    }

    /// The compass bearing of the line's segment `i` (from point `i` to the next), the way the walker faces there.
    func bearing(at segment: Int) -> Double {
        let i = max(0, min(segment, line.count - 2))
        return WalkGeo.bearing(line[i], line[i + 1])
    }

    /// The point `meters` along the line, and the bearing of the segment it is on.
    func point(at meters: Double) -> (coordinate: CLLocationCoordinate2D, bearing: Double) {
        let d = max(0, min(meters, length))
        let i = min((along.lastIndex { $0 <= d }) ?? 0, line.count - 2)
        let span = along[i + 1] - along[i]
        let t = span > 0 ? (d - along[i]) / span : 0
        let a = line[i], b = line[i + 1]
        let at = CLLocationCoordinate2D(latitude: a.latitude + t * (b.latitude - a.latitude),
                                        longitude: a.longitude + t * (b.longitude - a.longitude))
        return (at, WalkGeo.bearing(a, b))
    }

    /// Where the marks that show the way go on the line ahead: every `every` meters from `from` (leaving room
    /// around the walker and before the end), at most `most` of them.
    func arrows(from meters: Double, every: Double = 45, most: Int = 24)
        -> [(coordinate: CLLocationCoordinate2D, bearing: Double, meters: Double)] {
        guard every > 0 else { return [] }
        var out: [(CLLocationCoordinate2D, Double, Double)] = []
        var d = meters + every / 2
        while d < length - every / 3, out.count < most {
            let p = point(at: d)
            out.append((p.coordinate, p.bearing, d))
            d += every
        }
        return out
    }

    /// The line between `from` and `to` meters along it.
    func slice(from a: Double, to b: Double) -> [CLLocationCoordinate2D] {
        let lo = max(0, min(a, length)), hi = max(lo, min(b, length))
        var out = [point(at: lo).coordinate]
        for i in line.indices where along[i] > lo && along[i] < hi { out.append(line[i]) }
        out.append(point(at: hi).coordinate)
        return out
    }

    /// Chevrons lying on the road ahead, pointing the way, as navigation apps print a route's texture: a close row of
    /// them, each its back-left point, tip and back-right point, `size` meters, every `every` meters from `from`, none
    /// within `clear` (the maneuver arrow's stretch).
    func chevrons(from meters: Double, every: Double = 11, size: Double = 2.2, clear: ClosedRange<Double>? = nil)
        -> [[CLLocationCoordinate2D]] {
        arrows(from: meters, every: every, most: 90).filter { clear?.contains($0.meters) != true }.map { p in
            let back = WalkGeo.offset(p.coordinate, meters: size, bearing: p.bearing + 180)
            return [WalkGeo.offset(back, meters: size, bearing: p.bearing - 90), p.coordinate,
                    WalkGeo.offset(back, meters: size, bearing: p.bearing + 90)]
        }
    }

    /// The stretch of the route the maneuver arrow at step `i`'s end covers: `before` meters up to it, `after` past it.
    func arrowStretch(_ i: Int, before: Double = 25, after: Double = 15) -> ClosedRange<Double>? {
        guard i >= 0, i < steps.count - 1 else { return nil }
        let at = stepEnds[i]
        return max(0, at - before)...min(length, at + after)
    }

    /// The arrow painted on the road at step `i`'s maneuver, as navigation apps draw the next turn: a shaft along the
    /// route over `arrowStretch`, and a head, a triangle `head` meters long pointing the way out. None for arriving.
    func maneuverArrow(_ i: Int, head: Double = 9) -> (shaft: [CLLocationCoordinate2D], head: [CLLocationCoordinate2D])? {
        guard let stretch = arrowStretch(i), stretch.upperBound > stretch.lowerBound else { return nil }
        let shaft = slice(from: stretch.lowerBound, to: stretch.upperBound)
        let end = point(at: stretch.upperBound).coordinate
        // The way out at the very end, measured just before it: at a corner `point(at:)` already faces the next leg.
        let way = point(at: max(stretch.lowerBound, stretch.upperBound - 0.5)).bearing
        let tip = WalkGeo.offset(end, meters: head, bearing: way)
        let left = WalkGeo.offset(end, meters: head * 0.8, bearing: way - 90)
        let right = WalkGeo.offset(end, meters: head * 0.8, bearing: way + 90)
        return (shaft, [left, tip, right])
    }

    /// Where step `i`'s maneuver is, on the line: where the step ends.
    func maneuverPoint(_ i: Int) -> CLLocationCoordinate2D {
        line[stepLastPoint[max(0, min(i, steps.count - 1))]]
    }

    /// The maneuver at the end of step `i`: the last step's is arriving; the others turn by the angle between the end of
    /// their line and the start of the next.
    func maneuver(at i: Int) -> WalkManeuver {
        guard i >= 0, i < steps.count - 1 else { return .arrive }
        let p = stepLastPoint[i]
        guard let before = WalkGeo.pointBack(line, from: p, meters: 8),
              let after = WalkGeo.pointAhead(line, from: p, meters: 8) else { return .straight }
        let into = WalkGeo.bearing(before, line[p])
        let out = WalkGeo.bearing(line[p], after)
        return .turn(WalkGeo.signedTurn(from: into, to: out))
    }
}

/// Where the walker is on the route.
nonisolated struct WalkProgress: Equatable, Sendable {
    /// The step being walked; its instruction is the next maneuver.
    var step: Int
    /// Meters along the route of the fix's nearest point.
    var along: Double
    /// Meters from the fix to the route.
    var offRoute: Double
    /// Meters to the next maneuver: the end of the step being walked.
    var toNext: Double
    /// Meters left to the destination.
    var remaining: Double
    /// Seconds left, Apple's estimate scaled by the meters left.
    var remainingSeconds: Double
    /// The segment of the line the fix fell on, the hint for the next fix.
    var segment: Int

    /// The step after the one being walked, whose maneuver comes after the next; nil on the last step.
    func thenStep(in route: WalkRoute) -> Int? { step + 1 < route.steps.count ? step + 1 : nil }
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
        let next = route.stepEnds[step]
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

/// What the voice says and when: that the walk starts, each maneuver once as it comes within `ahead` meters (at once
/// on a step shorter than that), and the arrival.
nonisolated struct WalkAnnouncer: Sendable {
    /// How far ahead of a maneuver it is said: about half a minute's walk.
    static let ahead = 40.0

    /// What is due.
    nonisolated enum Line: Equatable, Sendable {
        /// The walk starts.
        case start
        /// The maneuver at the end of `step`, `meters` ahead.
        case maneuver(step: Int, meters: Double)
        case arrived
    }

    private(set) var said: Set<Int> = []
    private(set) var saidStart = false
    private(set) var saidArrival = false

    /// The line due for `progress`, if any, marked as said.
    mutating func due(_ progress: WalkProgress, on route: WalkRoute, arrived: Bool) -> Line? {
        if arrived {
            guard !saidArrival else { return nil }
            saidArrival = true
            return .arrived
        }
        if !saidStart {
            saidStart = true
            return .start
        }
        let step = progress.step
        guard !said.contains(step), progress.toNext <= Self.ahead else { return nil }
        said.insert(step)
        return .maneuver(step: step, meters: progress.toNext)
    }

    /// Forgets the maneuvers said, for a new route (the start was said already).
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

    /// The point `meters` from `p` toward `bearing`.
    static func offset(_ p: CLLocationCoordinate2D, meters: Double, bearing: Double) -> CLLocationCoordinate2D {
        let r = bearing * .pi / 180
        let scale = cos(p.latitude * .pi / 180)
        return CLLocationCoordinate2D(latitude: p.latitude + meters * cos(r) / metersPerDegree,
                                      longitude: p.longitude + meters * sin(r) / (metersPerDegree * max(scale, 0.01)))
    }

    /// Every point of `points` moved `meters` toward `bearing`: the side of a mark standing on the road, seen from a
    /// tilted camera.
    static func shifted(_ points: [CLLocationCoordinate2D], meters: Double, bearing: Double) -> [CLLocationCoordinate2D] {
        points.map { offset($0, meters: meters, bearing: bearing) }
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
