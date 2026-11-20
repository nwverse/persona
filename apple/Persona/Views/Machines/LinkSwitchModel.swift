import Foundation

/// Switching an SSH computer to Persona Link (#10662, docs/operations/link.md "Switching from SSH").
///
/// The machine list is authoritative. A start made here is held in this app-lifetime store, so leaving and reopening
/// the page cannot send a second start while the first answer (or the status read after an unclear one) is still out.
@Observable final class MachineLinkSwitchStore {
    enum Notice: Equatable {
        case refused(String)
        case forbidden
        case uncertain
    }

    private struct Attempt: Equatable {
        var starting: Bool
        var seen: MachineLinkSwitch?
        var accepted: MachineLinkSwitch?
        var notice: Notice?
    }

    private var attempts: [MachineRef: Attempt] = [:]

    static let steps = ["checking", "installing", "connecting"]

    static func running(_ state: MachineLinkSwitch?) -> Bool {
        guard let phase = state?.phase else { return false }
        return steps.contains(phase) || phase == "rolling_back"
    }

    /// A linked hub's computer is switched from that hub. Unknown ids preserve compatibility with an older hello.
    static func ownHub(_ machine: Machine, selfHubID: String?) -> Bool {
        machine.hubID == nil || selfHubID == nil || machine.hubID == selfHubID
    }

    /// Only a computer of the hub the page talks to can use its route; a peer's switch belongs on that peer.
    private static func eligible(_ machine: Machine) -> Bool {
        machine.kind == "ssh" && machine.route?.via != "peer"
    }

    private static func caughtUp(_ listed: MachineLinkSwitch?, _ attempt: Attempt) -> Bool {
        guard let accepted = attempt.accepted else { return false }
        guard let listed else { return true }
        if let acceptedAt = accepted.at, let listedAt = listed.at { return listedAt >= acceptedAt }
        return listed != attempt.seen
    }

    /// What the page draws. A 202 speaks only until the pushed/read machine list is at least as new by the hub's clock;
    /// without timestamps, until that list differs from what it said when the button was pressed.
    func shown(_ machine: Machine) -> MachineLinkSwitch? {
        guard machine.route?.via != "peer", let listed = machine.linkSwitch else { return nil }
        guard let attempt = attempts[machine.ref], attempt.accepted != nil, !Self.caughtUp(listed, attempt) else { return listed }
        return attempt.accepted
    }

    func isStarting(_ machine: Machine) -> Bool { attempts[machine.ref]?.starting == true }

    func hasAttempt(_ machine: Machine) -> Bool { attempts[machine.ref] != nil }

    /// The last start's own notice while the list still says what it said when pressed. Once it moves, its state says more.
    func notice(_ machine: Machine) -> Notice? {
        guard let attempt = attempts[machine.ref], !attempt.starting, attempt.seen == machine.linkSwitch else { return nil }
        return attempt.notice
    }

    func canStart(_ machine: Machine) -> Bool {
        guard Self.eligible(machine), let state = shown(machine), state.available, !Self.running(state) else { return false }
        guard let attempt = attempts[machine.ref] else { return true }
        if attempt.starting { return false }
        if attempt.accepted != nil, !Self.caughtUp(machine.linkSwitch, attempt) { return false }
        // An uncertain answer may mean the hub started. Only a changed authoritative list can unlock another start.
        if attempt.notice != nil, attempt.seen == machine.linkSwitch {
            switch attempt.notice {
            case .uncertain, .forbidden: return false
            case .refused, .none: break
            }
        }
        return true
    }

    /// A status read, never another start, is the recovery for an uncertain answer or an unconfirmed rollback.
    func needsStatusCheck(_ machine: Machine) -> Bool {
        if notice(machine) == .uncertain { return true }
        let state = shown(machine)
        return state?.phase == "failed" && state?.rollbackUnconfirmed == true
    }

    /// Passing conditions the rest of the page already explains stay out of this card. Other refusals say why here.
    func shouldShow(_ machine: Machine) -> Bool {
        guard let state = shown(machine) else { return false }
        let passing = state.reason == "offline" || state.reason == "busy"
        return !passing || state.phase != nil || hasAttempt(machine)
    }

    /// Reconcile every newly arrived machine snapshot. In-flight starts are left alone; the end of `start` reconciles
    /// them against the status read it awaited, which may have arrived while the attempt was still marked starting.
    func reconcile(_ machines: [Machine]) {
        for machine in machines { reconcile(machine) }
    }

    func reconcile(_ machine: Machine) {
        guard let attempt = attempts[machine.ref], !attempt.starting else { return }
        if attempt.accepted != nil, Self.caughtUp(machine.linkSwitch, attempt) {
            attempts.removeValue(forKey: machine.ref)
        } else if attempt.notice != nil, machine.linkSwitch != attempt.seen {
            attempts.removeValue(forKey: machine.ref)
        }
    }

    /// Starts once after the view's confirmation. A definite refusal is recoverable. A lost, unreadable, cancelled or
    /// 5xx answer is uncertain: read the list, never POST again, and keep the start locked until that list changes.
    func start(_ machine: Machine, api: APIClient, refresh: @MainActor () async -> Machine?) async {
        guard canStart(machine) else { return }
        let ref = machine.ref
        let seen = machine.linkSwitch
        attempts[ref] = Attempt(starting: true, seen: seen, accepted: nil, notice: nil)
        do {
            let accepted = try await api.startLinkSwitch(machine: machine.id)
            attempts[ref] = Attempt(starting: false, seen: seen, accepted: accepted, notice: nil)
        } catch {
            let status = (error as? APIError)?.status ?? 0
            if status == 403 {
                attempts[ref] = Attempt(starting: false, seen: seen, accepted: nil, notice: .forbidden)
                return
            }
            let notice: Notice
            if status == 409 || (400..<500).contains(status) && status != 408 && status != 429 {
                notice = .refused((error as? APIError)?.code ?? "unknown")
            } else {
                notice = .uncertain
            }
            let latest = await refresh()
            attempts[ref] = Attempt(starting: false, seen: seen, accepted: nil, notice: notice)
            if let latest { reconcile(latest) }
        }
    }
}
