import CoreLocation
import Testing
@testable import Persona

/// Walking navigation's guidance (#6672, `WalkGuide`): where a fix falls on a route, which step it is on, how far the
/// next maneuver and the end are, which way each maneuver turns, when the walker is off the route or has arrived, and
/// what the voice says when. A walk east, south, then east, about 210 m, 210 m and 160 m, and the last few meters into
/// the destination, read as Apple's walking routes are: each step's instruction is the maneuver where its line ends, the
/// first step has no line ("start the route") and the last ends at the destination ("arrive").
struct WalkGuideTests {
    private static let a = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4730)
    private static let b = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4752)
    private static let c = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4752)
    private static let d = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4769)
    private static let e = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4770)

    private func route(start: WalkStep? = WalkStep(instruction: "", line: [a])) -> WalkRoute {
        let steps = [start, WalkStep(instruction: "Right", line: [Self.a, Self.b]),
                     WalkStep(instruction: "Left", line: [Self.b, Self.c]),
                     WalkStep(instruction: "Ahead", line: [Self.c, Self.d]),
                     WalkStep(instruction: "Arrive", line: [Self.d, Self.e])].compactMap { $0 }
        return WalkRoute(name: "Cafe", destination: Self.e, steps: steps, expectedSeconds: 420)!
    }

    /// `t` of the way from `p` to `q`, nudged `off` meters north.
    private func between(_ p: CLLocationCoordinate2D, _ q: CLLocationCoordinate2D, _ t: Double, off: Double = 0) -> CLLocationCoordinate2D {
        CLLocationCoordinate2D(latitude: p.latitude + t * (q.latitude - p.latitude) + off / 111_195,
                               longitude: p.longitude + t * (q.longitude - p.longitude))
    }

    @Test func emptyStepsAreDroppedAndTheLineIsJoined() throws {
        let r = route()
        #expect(r.steps.map(\.instruction) == ["Right", "Left", "Ahead", "Arrive"])
        #expect(r.line.count == 5) // the shared corners once
        #expect(abs(r.length - (WalkGeo.meters(Self.a, Self.b) + WalkGeo.meters(Self.b, Self.c) + WalkGeo.meters(Self.c, Self.d)
                                + WalkGeo.meters(Self.d, Self.e))) < 0.5)
        #expect(r.stepStarts[0] == 0 && abs(r.stepStarts[1] - WalkGeo.meters(Self.a, Self.b)) < 0.5)
        #expect(r.stepEnds[0] == r.stepStarts[1] && r.stepEnds[3] == r.length)
        #expect(WalkRoute(name: "x", destination: Self.d, steps: [WalkStep(instruction: "", line: [Self.a])], expectedSeconds: 1) == nil)
    }

    @Test func maneuversTurnTheWayTheLineTurns() {
        let r = route()
        // Each at the end of its step, the way the line turns there.
        #expect(r.maneuver(at: 0) == .right)    // east, then south
        #expect(r.maneuver(at: 1) == .left)     // south, then east
        #expect(r.maneuver(at: 2) == .straight) // east, and on east
        #expect(r.maneuver(at: 3) == .arrive)   // Apple's last step ends at the destination
        #expect(r.maneuverPoint(0).latitude == Self.b.latitude && r.maneuverPoint(0).longitude == Self.b.longitude)
        #expect(WalkManeuver.turn(10) == .straight && WalkManeuver.turn(-40) == .slightLeft && WalkManeuver.turn(150) == .sharpRight)
        #expect(WalkManeuver.turn(179) == .uTurn && WalkManeuver.turn(-90) == .left)
        #expect(WalkGeo.signedTurn(from: 350, to: 10) == 20 && WalkGeo.signedTurn(from: 10, to: 350) == -20)
    }

    @Test func aFixFallsOnItsStepWithTheDistancesLeft() {
        let r = route()
        let p = WalkGuide.progress(of: between(Self.a, Self.b, 0.5, off: 5), on: r)
        #expect(p.step == 0 && p.thenStep(in: r) == 1)
        #expect(abs(p.toNext - WalkGeo.meters(Self.a, Self.b) / 2) < 2)
        #expect(abs(p.offRoute - 5) < 0.5)
        #expect(abs(p.remaining - (r.length - WalkGeo.meters(Self.a, Self.b) / 2)) < 2)
        #expect(abs(p.remainingSeconds - 420 * p.remaining / r.length) < 0.01)
        let later = WalkGuide.progress(of: between(Self.b, Self.c, 0.5), on: r, hint: p.segment)
        #expect(later.step == 1 && later.thenStep(in: r) == 2)
        let third = WalkGuide.progress(of: between(Self.c, Self.d, 0.5), on: r, hint: later.segment)
        #expect(third.step == 2 && abs(third.toNext - WalkGeo.meters(Self.c, Self.d) / 2) < 2)
        // On the last step what comes next is the destination itself.
        let into = WalkGuide.progress(of: between(Self.d, Self.e, 0.5), on: r, hint: third.segment)
        #expect(into.step == 3 && into.thenStep(in: r) == nil && abs(into.toNext - into.remaining) < 0.01)
    }

    /// The search goes on from the last fix's segment: a fix near an earlier leg (a route that comes back past it) stays
    /// on the leg the walker is on, unless it is only a little behind (GPS jitter).
    @Test func theSearchKeepsTheWalkerOnTheirLeg() {
        // Out east and back west along the same street, a little to the south.
        let out = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4760)
        let back = CLLocationCoordinate2D(latitude: 31.2309, longitude: 121.4730)
        let r = WalkRoute(name: "Loop", destination: back, steps: [WalkStep(instruction: "Out", line: [Self.a, out]),
                                                                   WalkStep(instruction: "Back", line: [out, back])], expectedSeconds: 600)!
        let near = between(Self.a, out, 0.3)
        #expect(WalkGuide.progress(of: near, on: r).step == 0)
        #expect(WalkGuide.progress(of: near, on: r, hint: 1).step == 1)
    }

    @Test func offTheRouteAndArriving() {
        let r = route()
        let off = WalkGuide.progress(of: between(Self.a, Self.b, 0.5, off: 60), on: r)
        #expect(WalkGuide.isOff(off, accuracy: 10))
        #expect(!WalkGuide.isOff(off, accuracy: 80)) // a fix that unsure says nothing
        let near = WalkGuide.progress(of: between(Self.c, Self.d, 0.95), on: r)
        #expect(WalkGuide.arrived(between(Self.c, Self.d, 0.95), near, on: r))
        let notYet = WalkGuide.progress(of: between(Self.c, Self.d, 0.5), on: r)
        #expect(!WalkGuide.arrived(between(Self.c, Self.d, 0.5), notYet, on: r))
    }

    /// A new route is asked for after a few fixes and seconds off the route, not after one; and not again right away.
    @Test func rerouteWaitsAndSpacesItsRequests() {
        var r = WalkReroute()
        let t0 = Date(timeIntervalSince1970: 1_000)
        // (off the route?, seconds after t0) → asks for a new route?
        let fixes: [(Bool, TimeInterval)] = [(true, 0), (true, 4), (false, 5), (true, 6), (true, 10), (true, 15),
                                             (true, 20), (true, 25), (true, 28), (true, 31)]
        var asked: [Bool] = []
        for (off, at) in fixes { asked.append(r.take(off: off, at: t0 + at)) }
        // Not after one fix or when back on; at 15 s (three fixes, 9 s off); not at 28 s (under 15 s since); at 31 s.
        #expect(asked == [false, false, false, false, false, true, false, false, false, true])
    }

    /// The start once, each maneuver once as it comes within reach, the arrival once.
    @Test func theVoiceSaysEachThingOnce() {
        let r = route()
        var say = WalkAnnouncer()
        let start = WalkGuide.progress(of: Self.a, on: r)
        let first = say.due(start, on: r, arrived: false)
        #expect(first == .start)
        let again = say.due(start, on: r, arrived: false)
        #expect(again == nil) // 210 m to the corner
        let close = WalkGuide.progress(of: between(Self.a, Self.b, 0.85), on: r)
        let turn = say.due(close, on: r, arrived: false)
        guard case .maneuver(0, let meters)? = turn else {
            Issue.record("the right turn was not said: \(String(describing: turn))")
            return
        }
        #expect(meters <= WalkAnnouncer.ahead)
        let twice = say.due(close, on: r, arrived: false)
        #expect(twice == nil)
        let end = WalkGuide.progress(of: Self.e, on: r)
        let arrived = say.due(end, on: r, arrived: true)
        let arrivedAgain = say.due(end, on: r, arrived: true)
        #expect(arrived == .arrived && arrivedAgain == nil)
    }

    @Test func theWordsTellHowFarAhead() {
        let r = route()
        #expect(WalkNavigation.words(.maneuver(step: 0, meters: 36), on: r)?.contains("Right") == true)
        #expect(WalkNavigation.words(.maneuver(step: 0, meters: 8), on: r) == "Right") // here: just the instruction
        #expect(WalkNavigation.words(.arrived, on: r)?.contains("Cafe") == true)
        #expect(WalkNavigation.distance(36) == WalkNavigation.distance(40))
    }
}

/// What the navigation screen draws on the map (#6672): the walker's way, arrows along the route ahead, the next
/// maneuver's place, and where the camera looks.
struct WalkMapMarksTests {
    private static let a = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4730)
    private static let b = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4752)
    private static let c = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4752)

    private var route: WalkRoute {
        WalkRoute(name: "Cafe", destination: Self.c, steps: [WalkStep(instruction: "East", line: [Self.a, Self.b]),
                                                              WalkStep(instruction: "Right", line: [Self.b, Self.c])],
                  expectedSeconds: 300)!
    }

    @Test func pointsAndBearingsAlongTheRoute() {
        let r = route
        #expect(abs(r.bearing(at: 0) - 90) < 1 && abs(r.bearing(at: 1) - 180) < 1)
        let half = r.point(at: r.stepStarts[1] / 2)
        #expect(abs(half.bearing - 90) < 1 && abs(half.coordinate.latitude - Self.a.latitude) < 1e-9)
        #expect(abs(WalkGeo.meters(half.coordinate, Self.a) - r.stepStarts[1] / 2) < 1)
        #expect(WalkGeo.meters(r.point(at: r.length + 50).coordinate, Self.c) < 0.5) // clamped to the end
        #expect(r.maneuverPoint(0).latitude == Self.b.latitude && r.maneuverPoint(0).longitude == Self.b.longitude)
    }

    @Test func arrowsGoAheadOfTheWalkerEvenlyAndPointTheWay() {
        let r = route
        let from = 30.0
        let arrows = r.arrows(from: from, every: 45)
        #expect(!arrows.isEmpty && arrows.count <= 24)
        // None behind the walker or on top of the end; evenly spaced; each points along its leg.
        let first = WalkGuide.progress(of: arrows[0].coordinate, on: r).along
        #expect(first > from && first < from + 45)
        let last = WalkGuide.progress(of: arrows[arrows.count - 1].coordinate, on: r).along
        #expect(last < r.length - 10)
        #expect(arrows.allSatisfy { abs($0.bearing - 90) < 1 || abs($0.bearing - 180) < 1 })
        #expect(r.arrows(from: from, every: 5, most: 3).count == 3)
        #expect(r.arrows(from: r.length).isEmpty)
    }

    @Test func theCameraLooksAheadTheWayTheWalkerFaces() {
        let north = WalkGeo.offset(Self.a, meters: 70, bearing: 0)
        #expect(abs(WalkGeo.meters(north, Self.a) - 70) < 0.5 && abs(WalkGeo.bearing(Self.a, north)) < 0.5)
        let east = WalkGeo.offset(Self.a, meters: 70, bearing: 90)
        #expect(abs(WalkGeo.meters(east, Self.a) - 70) < 0.5 && abs(WalkGeo.bearing(Self.a, east) - 90) < 0.5)
    }
}

/// The route as navigation apps paint it (#6672): chevrons on the road and the arrow at the next turn.
struct WalkRoadPaintTests {
    private static let a = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4730)
    private static let b = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4752)
    private static let c = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4752)

    private var route: WalkRoute {
        WalkRoute(name: "Cafe", destination: Self.c, steps: [WalkStep(instruction: "Right", line: [Self.a, Self.b]),
                                                              WalkStep(instruction: "Arrive", line: [Self.b, Self.c])],
                  expectedSeconds: 300)!
    }

    @Test func aSliceFollowsTheRouteRoundItsCorners() {
        let r = route
        let corner = r.stepEnds[0]
        let piece = r.slice(from: corner - 20, to: corner + 20)
        #expect(piece.count == 3) // in, the corner, out
        #expect(WalkGeo.meters(piece[1], Self.b) < 0.01)
        #expect(abs(WalkGeo.meters(piece[0], piece[1]) - 20) < 0.5 && abs(WalkGeo.meters(piece[1], piece[2]) - 20) < 0.5)
        #expect(r.slice(from: -10, to: 5).count == 2 && r.slice(from: r.length - 5, to: r.length + 50).count == 2)
    }

    @Test func theTurnArrowRunsThroughTheCornerAndPointsTheWayOut() throws {
        let r = route
        let arrow = try #require(r.maneuverArrow(0))
        #expect(arrow.shaft.contains { WalkGeo.meters($0, Self.b) < 0.01 })
        #expect(arrow.head.count == 3)
        // The tip is ahead of the shaft's end, south: the way out of the corner.
        let end = arrow.shaft[arrow.shaft.count - 1]
        #expect(abs(WalkGeo.bearing(end, arrow.head[1]) - 180) < 1)
        #expect(abs(WalkGeo.meters(arrow.head[0], arrow.head[2]) - 2 * 9 * 0.8) < 0.5)
        // Its side, seen from a camera looking north: the same shape moved toward the camera.
        let side = WalkGeo.shifted(arrow.head, meters: 1.5, bearing: 180)
        #expect(zip(side, arrow.head).allSatisfy { abs(WalkGeo.meters($0, $1) - 1.5) < 0.01 && abs(WalkGeo.bearing($1, $0) - 180) < 1 })
        #expect(r.maneuverArrow(1) == nil) // arriving: the destination's own marker
    }

    @Test func chevronsLieOnTheRoadAndLeaveTheTurnArrowAlone() {
        let r = route
        let clear = r.arrowStretch(0)
        let marks = r.chevrons(from: 0, clear: clear)
        #expect(!marks.isEmpty && marks.allSatisfy { $0.count == 3 })
        for mark in marks {
            let along = WalkGuide.progress(of: mark[1], on: r).along
            #expect(clear?.contains(along) != true)
            // Symmetric about the route, tip ahead.
            #expect(abs(WalkGeo.meters(mark[0], mark[1]) - WalkGeo.meters(mark[2], mark[1])) < 0.01)
        }
    }
}
