import CoreGraphics
import Testing
@testable import Persona

struct DictationHoldTests {
    @Test func whileHeldOnlyAFarSlideDecides() {
        #expect(DictationHold.whileHeld(CGSize(width: -149, height: 0)) == nil)
        #expect(DictationHold.whileHeld(CGSize(width: -151, height: 0)) == .cancel)
        #expect(DictationHold.whileHeld(CGSize(width: 0, height: -109)) == nil)
        #expect(DictationHold.whileHeld(CGSize(width: 0, height: -111)) == .lock)
    }

    @Test func rightAndDownAreWhereTheFingerStarted() {
        #expect(DictationHold.whileHeld(CGSize(width: 400, height: 400)) == nil)
        #expect(DictationHold.onRelease(CGSize(width: 300, height: 300), velocity: CGSize(width: 2000, height: 2000)) == .keep)
        #expect(DictationHold.labelShift(CGSize(width: 50, height: 0)) == 0)
        #expect(DictationHold.lockProgress(CGSize(width: 0, height: 80)) == 0)
    }

    @Test func lettingGoAShorterSlideOrAFlickCounts() {
        #expect(DictationHold.onRelease(CGSize(width: -101, height: 0), velocity: .zero) == .cancel)
        #expect(DictationHold.onRelease(CGSize(width: -99, height: 0), velocity: .zero) == .keep)
        #expect(DictationHold.onRelease(CGSize(width: -20, height: 0), velocity: CGSize(width: -401, height: 0)) == .cancel)
        #expect(DictationHold.onRelease(CGSize(width: 0, height: -61), velocity: .zero) == .lock)
        #expect(DictationHold.onRelease(CGSize(width: 0, height: -59), velocity: .zero) == .keep)
        #expect(DictationHold.onRelease(CGSize(width: 0, height: -10), velocity: CGSize(width: 0, height: -401)) == .lock)
    }

    @Test func onlyTheLongerAxisCountsOnRelease() {
        // Further left than up: a lock-speed flick up does not lock.
        #expect(DictationHold.onRelease(CGSize(width: -50, height: -40), velocity: CGSize(width: 0, height: -900)) == .keep)
        // Further up than left: a long-enough slide left does not cancel.
        #expect(DictationHold.onRelease(CGSize(width: -101, height: -120), velocity: .zero) == .lock)
    }

    @Test func theLabelFollowsAtHalfThePaceAfterADeadZone() {
        #expect(DictationHold.labelShift(CGSize(width: -8, height: 0)) == 0)
        #expect(DictationHold.labelShift(CGSize(width: -48, height: 0)) == 20)
    }

    @Test func theLabelIsGoneByTheDrop() {
        #expect(DictationHold.labelOpacity(.zero) == 1)
        #expect(DictationHold.labelOpacity(CGSize(width: -120, height: 0)) == 1)
        #expect(DictationHold.labelOpacity(CGSize(width: -140, height: 0)) == 0.5)
        #expect(DictationHold.labelOpacity(CGSize(width: -150, height: 0)) == 0)
    }

    @Test func lockProgressAndCircleAreBounded() {
        #expect(DictationHold.lockProgress(CGSize(width: 0, height: -52.5)) == 0.5)
        #expect(DictationHold.lockProgress(CGSize(width: 0, height: -500)) == 1)
        #expect(DictationHold.circleScale(.zero) == 1)
        #expect(DictationHold.circleScale(CGSize(width: -150, height: 0)) == 0.5)
        #expect(DictationHold.circleScale(CGSize(width: -1000, height: 0)) == 0.4)
    }

    @Test func whatWasHeardGoesAfterTheDraft() {
        #expect(DictationHold.joined("", "hello") == "hello")
        #expect(DictationHold.joined("Hi", "there") == "Hi there")
        #expect(DictationHold.joined("Hi ", "there") == "Hi there")
        #expect(DictationHold.joined("Hi", "") == "Hi")
    }
}
