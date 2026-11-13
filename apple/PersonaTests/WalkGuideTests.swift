import CoreLocation
import Testing
@testable import Persona

/// Walking navigation's guidance (#6672, `WalkGuide`): where a fix falls on a route, which step it is on, how far the
/// next maneuver and the end are, which way each maneuver turns, when the walker is off the route or has arrived, and
/// what the voice says when. An L-shaped walk east then south then east, about 210 m, 210 m and 160 m.
struct WalkGuideTests {
    private static let a = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4730)
    private static let b = CLLocationCoordinate2D(latitude: 31.2310, longitude: 121.4752)
    private static let c = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4752)
    private static let d = CLLocationCoordinate2D(latitude: 31.2291, longitude: 121.4769)

    private func route(start: WalkStep? = WalkStep(instruction: "", line: [a])) -> WalkRoute {
        let steps = [start, WalkStep(instruction: "East", line: [Self.a, Self.b]),
                     WalkStep(instruction: "Right", line: [Self.b, Self.c]),
                     WalkStep(instruction: "Left", line: [Self.c, Self.d])].compactMap { $0 }
        return WalkRoute(name: "Cafe", destination: Self.d, steps: steps, expectedSeconds: 420)!
    }

    /// `t` of the way from `p` to `q`, nudged `off` meters north.
    private func between(_ p: CLLocationCoordinate2D, _ q: CLLocationCoordinate2D, _ t: Double, off: Double = 0) -> CLLocationCoordinate2D {
        CLLocationCoordinate2D(latitude: p.latitude + t * (q.latitude - p.latitude) + off / 111_195,
                               longitude: p.longitude + t * (q.longitude - p.longitude))
    }

    @Test func emptyStepsAreDroppedAndTheLineIsJoined() throws {
        let r = route()
        #expect(r.steps.map(\.instruction) == ["East", "Right", "Left"])
        #expect(r.line.count == 4) // the shared corners once
        #expect(abs(r.length - (WalkGeo.meters(Self.a, Self.b) + WalkGeo.meters(Self.b, Self.c) + WalkGeo.meters(Self.c, Self.d))) < 0.5)
        #expect(r.stepStarts[0] == 0 && abs(r.stepStarts[1] - WalkGeo.meters(Self.a, Self.b)) < 0.5)
        #expect(WalkRoute(name: "x", destination: Self.d, steps: [WalkStep(instruction: "", line: [Self.a])], expectedSeconds: 1) == nil)
    }

    @Test func maneuversTurnTheWayTheLineTurns() {
        let r = route()
        #expect(r.maneuver(at: 0) == .start)
        #expect(r.maneuver(at: 1) == .right) // east, then south
        #expect(r.maneuver(at: 2) == .left)  // south, then east
        #expect(r.maneuver(at: 3) == .arrive)
        #expect(WalkManeuver.turn(10) == .straight && WalkManeuver.turn(-40) == .slightLeft && WalkManeuver.turn(150) == .sharpRight)
        #expect(WalkManeuver.turn(179) == .uTurn && WalkManeuver.turn(-90) == .left)
        #expect(WalkGeo.signedTurn(from: 350, to: 10) == 20 && WalkGeo.signedTurn(from: 10, to: 350) == -20)
    }

    @Test func aFixFallsOnItsStepWithTheDistancesLeft() {
        let r = route()
        let p = WalkGuide.progress(of: between(Self.a, Self.b, 0.5, off: 5), on: r)
        #expect(p.step == 0 && p.nextStep(in: r) == 1)
        #expect(abs(p.toNext - WalkGeo.meters(Self.a, Self.b) / 2) < 2)
        #expect(abs(p.offRoute - 5) < 0.5)
        #expect(abs(p.remaining - (r.length - WalkGeo.meters(Self.a, Self.b) / 2)) < 2)
        #expect(abs(p.remainingSeconds - 420 * p.remaining / r.length) < 0.01)
        let later = WalkGuide.progress(of: between(Self.b, Self.c, 0.5), on: r, hint: p.segment)
        #expect(later.step == 1 && later.nextStep(in: r) == 2)
        let last = WalkGuide.progress(of: between(Self.c, Self.d, 0.5), on: r, hint: later.segment)
        #expect(last.step == 2 && last.nextStep(in: r) == nil && abs(last.toNext - last.remaining) < 0.01)
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
        #expect(first == .start(step: 0))
        let again = say.due(start, on: r, arrived: false)
        #expect(again == nil) // 210 m to the corner
        let close = WalkGuide.progress(of: between(Self.a, Self.b, 0.85), on: r)
        let turn = say.due(close, on: r, arrived: false)
        guard case .maneuver(1, let meters)? = turn else {
            Issue.record("the right turn was not said: \(String(describing: turn))")
            return
        }
        #expect(meters <= WalkAnnouncer.ahead)
        let twice = say.due(close, on: r, arrived: false)
        #expect(twice == nil)
        let end = WalkGuide.progress(of: Self.d, on: r)
        let arrived = say.due(end, on: r, arrived: true)
        let arrivedAgain = say.due(end, on: r, arrived: true)
        #expect(arrived == .arrived && arrivedAgain == nil)
    }

    @Test func theWordsTellHowFarAhead() {
        let r = route()
        #expect(WalkNavigation.words(.maneuver(step: 1, meters: 36), on: r)?.contains("Right") == true)
        #expect(WalkNavigation.words(.maneuver(step: 1, meters: 8), on: r) == "Right") // here: just the instruction
        #expect(WalkNavigation.words(.arrived, on: r)?.contains("Cafe") == true)
        #expect(WalkNavigation.distance(36) == WalkNavigation.distance(40))
    }
}
