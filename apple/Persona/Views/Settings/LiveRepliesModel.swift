import Foundation
import Observation

/// Settings › Claude Code › Live Replies (#1309, docs/integrations/claude-integration.md "Live replies (MessageDisplay)", the
/// web's `LiveReplies.tsx`): whether the Claude Code sessions Persona starts on a computer send their replies while Claude
/// writes them, so the app and the web show an agent's words before its transcript has them. It costs that computer's
/// terminal something (a reply shows a batch of lines at a time instead of word by word), so it is a switch per
/// computer, on by default, kept by the hub (`GET` / `PUT /api/v1/machines/{mid}/claude-live-replies` `{"on"}`), which
/// tells the node at once. A change reaches the sessions started after it. An older hub has no such route (404): no
/// switch.
@Observable final class LiveRepliesModel {
    struct Deps {
        /// The hub that answers for a computer (its group's); nil when not connected to it.
        var api: (MachineRef) -> APIClient?
        /// A computer's name, for a failure on one of several.
        var name: (MachineRef) -> String
        var toast: (Toast) -> Void

        static func live(_ app: AppModel) -> Deps {
            Deps(api: { app.api($0.group) }, name: { app.machine($0)?.title ?? $0.id }, toast: { app.toast = $0 })
        }
    }

    let ref: MachineRef
    /// Nil until the hub answered.
    private(set) var on: Bool?
    private(set) var saving = false
    /// The hub has no such switch (404, older than it): the page shows none.
    private(set) var unsupported = false
    /// Why the setting could not be read; the section offers Retry in its place.
    private(set) var loadError: String?
    /// Why the last change did not save, on this computer or on the others it went to.
    private(set) var error: String?
    @ObservationIgnored private let deps: Deps

    init(ref: MachineRef, deps: Deps) {
        self.ref = ref
        self.deps = deps
    }

    func load() async {
        guard let api = deps.api(ref) else {
            loadError = ClaudeDirsLogic.noHub
            return
        }
        do {
            on = try await api.claudeLiveReplies(machine: ref.id)
            loadError = nil
            unsupported = false
        } catch let e as APIError where e.status == 404 {
            unsupported = true
        } catch {
            // A cancelled read (the page went away) says nothing; one that fails with the switch already shown keeps it.
            guard on == nil, !error.isCancellation else { return }
            let why = error.shown ?? String(localized: "Can't reach the hub")
            loadError = String(localized: "Couldn't read this computer's setting: \(why)")
        }
    }

    /// Shows the new value at once and sends it to this computer, and to `others` too (Apply to All Online Machines). A
    /// failure on this computer puts the old value back; every failure is said under the switch.
    func set(_ next: Bool, others: [MachineRef] = []) async {
        guard let before = on, before != next, !saving else { return }
        on = next
        saving = true
        error = nil
        defer { saving = false }
        let targets = [ref] + others.filter { $0 != ref }
        var failed: [String] = []
        for target in targets {
            do {
                guard let api = deps.api(target) else { throw APIError(status: 0, message: ClaudeDirsLogic.noHub) }
                let kept = try await api.setClaudeLiveReplies(machine: target.id, on: next)
                if target == ref { on = kept }
            } catch {
                if error.isCancellation { continue }
                let why = error.shown ?? String(localized: "Can't reach the hub")
                if target == ref {
                    on = before
                    failed.insert(String(localized: "Couldn't change it: \(why) [Messages Views 87cfe2]"), at: 0)
                } else {
                    failed.append(String(localized: "\(deps.name(target)): \(why)"))
                }
            }
        }
        if failed.isEmpty {
            deps.toast(Toast(targets.count > 1 ? String(localized: "Applied to \(targets.count) machines") : String(localized: "Saved"), style: .success))
        } else {
            error = failed.joined(separator: "\n")
        }
    }
}
