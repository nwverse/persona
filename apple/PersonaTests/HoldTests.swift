import Foundation
import Testing
@testable import Persona

/// `Hold`'s policy (docs/apple/ios-state.md「Timely」), with short times: scrolling holds until the list rests, what waits
/// lands within the cap while it still scrolls, out of sight has no cap and lands when seen again.
@MainActor struct HoldTests {
    private final class Landings {
        var count = 0
    }

    private func hold(_ landings: Landings, cap: Duration = .milliseconds(300)) -> Hold {
        let h = Hold("test", settle: .milliseconds(50), cap: cap)
        h.land = { [weak h] in landings.count += 1; h?.landed() }
        return h
    }

    @Test func notHeldShowsAtOnce() {
        let l = Landings()
        let h = hold(l)
        #expect(!h.wait())
        #expect(l.count == 0)
    }

    @Test func aScrollHoldsUntilTheListRests() async throws {
        let l = Landings()
        let h = hold(l)
        h.set(.scrolling, true)
        #expect(h.wait())
        h.set(.scrolling, false)
        #expect(h.held) // resting, not yet rested
        #expect(l.count == 0)
        try await Task.sleep(for: .milliseconds(400))
        #expect(!h.held)
        #expect(l.count == 1)
    }

    @Test func aMoveWhileRestingKeepsHolding() async throws {
        let l = Landings()
        let h = hold(l, cap: .seconds(10)) // the cap is another test's
        h.set(.scrolling, true)
        #expect(h.wait())
        h.set(.scrolling, false)
        h.set(.scrolling, true) // the finger came back before the list rested
        try await Task.sleep(for: .milliseconds(400))
        #expect(h.held)
        #expect(l.count == 0)
    }

    @Test func whatWaitsWhileScrollingLandsWithinTheCap() async throws {
        let l = Landings()
        let h = hold(l)
        h.set(.scrolling, true)
        #expect(h.wait())
        try await Task.sleep(for: .milliseconds(1200))
        #expect(h.held) // still scrolling
        #expect(l.count == 1)
    }

    @Test func outOfSightHasNoCapAndLandsWhenSeen() async throws {
        let l = Landings()
        let h = hold(l)
        h.set(.outOfSight, true)
        #expect(h.wait())
        try await Task.sleep(for: .milliseconds(1200))
        #expect(l.count == 0)
        h.set(.outOfSight, false)
        #expect(l.count == 1)
    }

    /// A page kept behind another one (the Mac's sections): as out of sight, no cap; the two reasons come and go on
    /// their own.
    @Test func behindHasNoCapAndLandsInFrontOnlyWhenNothingElseHolds() async throws {
        let l = Landings()
        let h = hold(l)
        h.set(.behind, true)
        h.set(.outOfSight, true)
        #expect(h.wait())
        try await Task.sleep(for: .milliseconds(500))
        #expect(l.count == 0)
        h.set(.behind, false)
        #expect(l.count == 0) // still out of sight
        h.set(.outOfSight, false)
        #expect(l.count == 1)
    }

    @Test func nothingWaitingLandsNothing() {
        let l = Landings()
        let h = hold(l)
        h.set(.outOfSight, true)
        h.set(.outOfSight, false)
        #expect(l.count == 0)
    }

    @Test func aScrollEndingOutOfSightStopsTheCap() async throws {
        let l = Landings()
        let h = hold(l)
        h.set(.scrolling, true)
        h.set(.outOfSight, true) // a chat opened from the list mid-glide
        #expect(h.wait())
        h.set(.scrolling, false)
        try await Task.sleep(for: .milliseconds(1200))
        #expect(l.count == 0)
        h.set(.outOfSight, false)
        #expect(l.count == 1)
    }
}
