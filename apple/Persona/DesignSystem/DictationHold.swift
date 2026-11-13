import CoreGraphics

/// Hold the composer's mic to dictate (Telegram's voice button, its thresholds in `Theme.Hold`): a touch held past
/// `Theme.Hold.delay` starts dictating; sliding left drops what was heard, sliding up locks it on, letting go keeps it.
/// Only left and up count: a finger drifting right or down is where it started.
nonisolated enum DictationHold {
    enum Outcome: Equatable {
        /// What was heard goes into the draft.
        case keep
        /// What was heard is dropped; the draft is as it was.
        case cancel
        /// Dictation goes on after the finger lifts, as a tapped mic's does (a tap on the mic ends it).
        case lock
    }

    /// How far left and up the finger is from where it went down, never below zero.
    static func travel(_ translation: CGSize) -> (left: CGFloat, up: CGFloat) {
        (max(0, -translation.width), max(0, -translation.height))
    }

    /// Decided while the finger is still down, as soon as it goes far enough; nil: not yet.
    static func whileHeld(_ translation: CGSize) -> Outcome? {
        let (left, up) = travel(translation)
        if left > Theme.Hold.cancelDistance { return .cancel }
        if up > Theme.Hold.lockDistance { return .lock }
        return nil
    }

    /// Decided when the finger lifts: only the axis it went further along counts, by distance or by a flick.
    /// `velocity` is in points per second, negative leftward and upward, as a drag gesture reports it.
    static func onRelease(_ translation: CGSize, velocity: CGSize) -> Outcome {
        let (left, up) = travel(translation)
        if left >= up {
            if -velocity.width > Theme.Hold.flickSpeed || left > Theme.Hold.releaseCancelDistance { return .cancel }
        } else {
            if -velocity.height > Theme.Hold.flickSpeed || up > Theme.Hold.releaseLockDistance { return .lock }
        }
        return .keep
    }

    /// How far the "Slide to cancel" label has moved left: still for the first few points, then at half the finger's pace.
    static func labelShift(_ translation: CGSize) -> CGFloat {
        max(0, travel(translation).left - Theme.Hold.labelDeadZone) * Theme.Hold.labelFollow
    }

    /// The label is gone by the time the slide drops what was heard, fading over the last `Theme.Hold.labelFade` points.
    static func labelOpacity(_ translation: CGSize) -> CGFloat {
        let atDrop = labelShift(CGSize(width: -Theme.Hold.cancelDistance, height: 0))
        return min(1, max(0, (atDrop - labelShift(translation)) / Theme.Hold.labelFade))
    }

    /// From 0 to 1 as the finger goes up toward the lock.
    static func lockProgress(_ translation: CGSize) -> CGFloat {
        min(1, travel(translation).up / Theme.Hold.lockTravel)
    }

    /// The circle under the finger shrinks as the finger goes toward cancel, never below `Theme.Hold.circleSmallest`.
    static func circleScale(_ translation: CGSize) -> CGFloat {
        max(Theme.Hold.circleSmallest, 1 - travel(translation).left / Theme.Hold.circleSlide)
    }

    /// The draft with what was heard after it, a space between when the draft does not end in one.
    static func joined(_ draft: String, _ heard: String) -> String {
        guard !heard.isEmpty else { return draft }
        return (draft.isEmpty || draft.last?.isWhitespace == true ? draft : draft + " ") + heard
    }
}
