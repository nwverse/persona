import Foundation
import Observation

/// A computer's clipboard section (#3064): its switch, Send Clipboard (the phone's to the computer), Get Clipboard (the
/// computer's onto the phone), and the one-time hint to stop iOS asking before every paste. The hub and the phone's
/// pasteboard come in through `Deps`, so the tests run it with a FakeHub and a pasteboard of their own.
@Observable final class ClipboardModel {
    struct Deps {
        var api: () -> APIClient?
        var pasteboard: PhonePasteboard
        /// The switch's new state, for the app's machine list.
        var updated: (MachineClipboard) -> Void
        var hints: ClipboardHints
    }

    enum Busy: Equatable { case sending, fetching, saving }

    private(set) var busy: Busy?
    /// The switch as last saved here (nil: as the hub's machine list says).
    private(set) var saved: MachineClipboard?
    /// What Get Clipboard put on the phone, for its preview.
    private(set) var fetched: ClipboardContent?
    /// The last result, said under the buttons.
    private(set) var outcome: ClipboardOutcome?
    /// "Asked every time?": after the first paste the user allowed (iOS only), until dismissed.
    private(set) var showsPasteHint = false
    @ObservationIgnored private let deps: Deps

    init(deps: Deps) {
        self.deps = deps
    }

    /// Send Clipboard: reads the phone's clipboard (iOS may ask "Allow Paste?") and puts it on the computer.
    @discardableResult func sendPasteboard(machineID: String) async -> ClipboardOutcome {
        guard busy == nil else { return outcome ?? .nothingToSend }
        guard let content = deps.pasteboard.read() else { return finish(.nothingToSend) }
        // Read, so the user allowed the paste (or never had to): the time to say it can be always allowed.
        if deps.hints.applies && !deps.hints.dismissed() { showsPasteHint = true }
        return await send(content, machineID: machineID)
    }

    /// What the system Paste button handed over (no prompt), or a shortcut's input.
    @discardableResult func send(_ content: ClipboardContent?, machineID: String) async -> ClipboardOutcome {
        guard let content, content.kind == .text || content.kind == .image else { return finish(.nothingToSend) }
        guard busy == nil else { return outcome ?? .nothingToSend }
        busy = .sending
        defer { busy = nil }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            try await api.setClipboard(machine: machineID, content)
            return finish(.sent(content.kind))
        } catch {
            return finish(.failed(ClipboardFailure(error)))
        }
    }

    /// Get Clipboard: the computer's clipboard onto the phone's (no prompt), kept for the preview.
    @discardableResult func fetch(machineID: String) async -> ClipboardOutcome {
        guard busy == nil else { return outcome ?? .nothingToSend }
        busy = .fetching
        defer { busy = nil }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let content = try await api.clipboard(machine: machineID)
            switch content.kind {
            case .text where content.text?.isEmpty == false, .image where content.image?.bytes != nil:
                deps.pasteboard.write(content)
                fetched = content
                return finish(.fetched(content.kind))
            case .concealed:
                return finish(.remoteConcealed)
            default:
                return finish(.remoteEmpty)
            }
        } catch {
            return finish(.failed(ClipboardFailure(error)))
        }
    }

    /// The computer's switch; the result goes to the machine list too.
    @discardableResult func setEnabled(_ on: Bool, machineID: String) async -> Bool {
        guard busy == nil else { return false }
        busy = .saving
        defer { busy = nil }
        do {
            guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            let state = try await api.setClipboardEnabled(machine: machineID, enabled: on)
            saved = state
            deps.updated(state)
            if !state.enabled { fetched = nil }
            outcome = nil
            return true
        } catch {
            outcome = .failed(ClipboardFailure(error))
            return false
        }
    }

    /// The computer's state from the hub now (`GET …/clipboard/settings`), for a page opened on an older machine list;
    /// later changes come with the pushed list. Quiet when it fails: the list's state stays.
    func refresh(machineID: String) async {
        guard busy == nil, let api = deps.api(), let state = try? await api.clipboardSettings(machine: machineID) else { return }
        if busy == nil { saved = state }
    }

    /// "Don't show again", or Open Settings: the hint does not come back.
    func dismissPasteHint() {
        showsPasteHint = false
        deps.hints.dismiss()
    }

    /// The switch as shown: saved here, else the machine list's.
    func state(_ listed: MachineClipboard?) -> MachineClipboard? { saved ?? listed }

    /// The machine list changed (a push, a refresh, or this switch passed on): it is the truth again.
    func synchronize(_ listed: MachineClipboard?) {
        if busy != .saving { saved = nil }
    }

    private func finish(_ result: ClipboardOutcome) -> ClipboardOutcome {
        outcome = result
        return result
    }

    #if DEBUG
    /// Screenshots: a state without a hub (`-showClipboardFetched text|image`, `-showClipboardHint YES`).
    func preview(fetched: ClipboardContent?, hint: Bool) {
        self.fetched = fetched
        if let fetched { outcome = .fetched(fetched.kind) }
        showsPasteHint = hint
    }
    #endif
}

/// The once-only paste hint's flag. Written once, read when a paste is allowed: UserDefaults is fine here.
struct ClipboardHints {
    /// Whether this platform has a paste prompt to stop (iOS: Settings › Persona › Paste from Other Apps).
    var applies: Bool
    var dismissed: () -> Bool
    var dismiss: () -> Void

    static let dismissedKey = "clipboard.pasteHintDismissed"

    static func live(_ defaults: UserDefaults = .standard) -> ClipboardHints {
        #if os(iOS)
        let applies = true
        #else
        let applies = false
        #endif
        return ClipboardHints(applies: applies, dismissed: { defaults.bool(forKey: dismissedKey) },
                              dismiss: { defaults.set(true, forKey: dismissedKey) })
    }
}

/// What Send or Get came to, in one sentence.
nonisolated enum ClipboardOutcome: Equatable, Sendable {
    case sent(ClipboardContent.Kind)
    case fetched(ClipboardContent.Kind)
    /// Nothing on the phone's clipboard, or the paste was not allowed.
    case nothingToSend
    case remoteEmpty
    case remoteConcealed
    case failed(ClipboardFailure)

    var isFailure: Bool {
        switch self {
        case .sent, .fetched: false
        default: true
        }
    }

    func message(machine name: String) -> String {
        switch self {
        case .sent(.image): String(localized: "Image sent to \(name)'s clipboard")
        case .sent: String(localized: "Sent to \(name)'s clipboard")
        case .fetched(.image): String(localized: "Copied the image from \(name) to this phone")
        case .fetched: String(localized: "Copied \(name)'s clipboard to this phone")
        case .nothingToSend: String(localized: "Nothing to send: the clipboard is empty, or pasting wasn't allowed")
        case .remoteEmpty: String(localized: "\(name)'s clipboard is empty")
        case .remoteConcealed: String(localized: "\(name)'s clipboard holds hidden content (such as a password), so it wasn't copied")
        case .failed(let f): f.sentence(machine: name)
        }
    }
}
