import Foundation

/// A system call is live on the iPhone: a call to Persona or an urgent call (docs/apple/calls.md, "Nothing in the app
/// ends a call"). Nothing in the app may take the microphone or set up the audio session under it, so what would
/// (dictation, a recording, the screen viewer's picture in picture) asks here first: it is refused with the line given,
/// or carries on without touching the session, and the call goes on untouched. The iPhone's call manager answers
/// (`PersonaCalls.guardTheApp`, from the moment it exists: before that no call can be live); on the watch, Vision Pro and
/// in the extensions nothing has a system call and every answer is "no call".
@MainActor enum LiveCallGate {
    /// Whether a system call is live now. Set once by the iPhone's call manager.
    static var isLive: @MainActor () -> Bool = { false }

    static var callIsLive: Bool { isLive() }

    /// Why dictation does not start now, or nil when it may: its own audio session would cut the call's microphone.
    static var dictationRefusal: String? {
        callIsLive ? String(localized: "On a call — dictation is off until it ends") : nil
    }

    /// Why a recording (a meeting, a note, all day) does not start or go on now, or nil when it may.
    static var recordingRefusal: String? {
        callIsLive ? String(localized: "End the call first") : nil
    }
}
