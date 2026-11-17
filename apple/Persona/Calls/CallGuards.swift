#if os(iOS)
import Foundation
import WebKit

// Nothing in the app ends a live call by itself (docs/apple/calls.md, "Nothing in the app ends a call"): while a call to
// Persona or an urgent call is live, what would take the microphone or set up the audio session under it is refused
// with a note, asks first, or carries on without touching the session. The call manager answers for the app here.

extension PersonaCalls {
    /// A system call is live for the app's guards: a call to Persona from "Calling…" to its end, an urgent call ringing
    /// (one the system showed) or answered. DEBUG samples are pictures, not calls: they guard nothing.
    var guardsLive: Bool {
        calls.contains { call in
            guard !call.sample, call.phase.isLive else { return false }
            return !call.kind.isUrgent || !(call.unshown && call.phase == .ringing)
        }
    }

    /// The assistant's name while a call to Persona is live (from "Calling…"), else nil: what a voice chat elsewhere asks
    /// before it ends the call ("End the call with <name>…?"), and CarPlay's says it is on the phone.
    var liveOwnerCallName: String? {
        guard let call = ownerCall, !call.sample, call.phase.isLive else { return nil }
        return call.name
    }

    /// `key` is the conversation the live call to Persona talks in (its main conversation): its handset and Voice Chat
    /// show the call screen instead of starting anything. While the call is still finding its conversation ("Calling…",
    /// no engine yet), the selected hub's main conversation is taken for it.
    func isOwnerCallConversation(_ key: SessionKey) -> Bool {
        guard let call = ownerCall, call.phase.isLive else { return false }
        if let owner = call.engine as? OwnerCallEngine { return owner.key == key }
        guard case .owner(let group) = call.kind else { return false }
        return key.group == group && AppModel.shared.assistantMain == key
    }

    /// The app's own guards ask this manager (the shared one only: a test's manager guards nothing of the app's).
    func guardTheApp() {
        LiveCallGate.isLive = { [weak self] in self?.guardsLive ?? false }
        onLiveChange = { live in CallMediaGuard.shared.callLive(live) }
    }
}

/// Web pages' media stays still while a system call is live: WebKit sets up its own audio for a page's video or sound,
/// which would interrupt the call's session (the call goes on hold). Every web view the app makes that could play
/// media registers here; while a call is live their media playback is suspended (`setAllMediaPlaybackSuspended`), and
/// it is let go when the last call ends.
@MainActor final class CallMediaGuard {
    static let shared = CallMediaGuard()
    private let views = NSHashTable<WKWebView>.weakObjects()
    private(set) var suspended = false

    /// A web view that can play media: held weakly, suspended at once while a call is live.
    func register(_ web: WKWebView) {
        views.add(web)
        if suspended { web.setAllMediaPlaybackSuspended(true) }
    }

    func callLive(_ live: Bool) {
        guard live != suspended else { return }
        suspended = live
        for web in views.allObjects { web.setAllMediaPlaybackSuspended(live) }
    }
}
#endif
