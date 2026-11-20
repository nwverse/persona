import Foundation
import Testing
@testable import Persona
import PersonaWire

@MainActor struct MachineLinkSwitchTests {
    private static let path = "/api/v1/machines/box/link-switch"

    private func machine(_ state: MachineLinkSwitch? = MachineLinkSwitch(available: true)) -> Machine {
        var machine = Machine(id: "box", name: "Box", kind: "ssh", online: true, home: "/home/alice")
        machine.linkSwitch = state
        return machine
    }

    private func at(_ second: Int) -> Date {
        Date(timeIntervalSince1970: 1_795_075_200 + Double(second))
    }

    private func accepted(_ phase: String = "checking", second: Int = 5) -> MachineLinkSwitch {
        MachineLinkSwitch(available: false, reason: "in_progress", phase: phase, at: at(second))
    }

    private var acceptedAnswer: String {
        #"{"link_switch":{"available":false,"reason":"in_progress","phase":"checking","at":"2026-11-19T08:00:05Z"}}"#
    }

    @Test func decodesEligibilityAndLeavesOlderHubsAndPeersOut() throws {
        let data = Data(#"{"id":"box","name":"Box","kind":"ssh","online":true,"link_switch":{"available":false,"reason":"unsupported_os","rollback_unconfirmed":true,"at":"2026-11-19T08:00:05Z"}}"#.utf8)
        let decoded = try JSON.decoder.decode(Machine.self, from: data)
        #expect(decoded.linkSwitch?.available == false)
        #expect(decoded.linkSwitch?.reason == "unsupported_os")
        #expect(decoded.linkSwitch?.rollbackUnconfirmed == true)
        #expect(decoded.linkSwitch?.at != nil)

        let store = MachineLinkSwitchStore()
        #expect(store.shown(machine(nil)) == nil)
        #expect(!store.shouldShow(machine(nil)))
        var peer = machine()
        peer.route = MachineHubRoute(via: "peer")
        #expect(store.shown(peer) == nil)
        #expect(!store.canStart(peer))
        var linkedHub = machine()
        linkedHub.hubID = "hub-b"
        #expect(!MachineLinkSwitchStore.ownHub(linkedHub, selfHubID: "hub-a"))
        #expect(MachineLinkSwitchStore.ownHub(linkedHub, selfHubID: "hub-b"))
        #expect(MachineLinkSwitchStore.ownHub(linkedHub, selfHubID: nil))
        #expect(MachineLinkSwitchStore.ownHub(machine(), selfHubID: "hub-a"))
        #expect(!store.shouldShow(machine(MachineLinkSwitch(available: false, reason: "offline"))))
        #expect(store.shouldShow(machine(MachineLinkSwitch(available: false, reason: "unsupported_os"))))
        #expect(!store.canStart(machine(MachineLinkSwitch(available: false, reason: "unsupported_os"))))
    }

    @MainActor private final class StartGate {
        private var pending: CheckedContinuation<Data, Error>?
        private var arrival: CheckedContinuation<Void, Never>?

        func wait() async throws -> Data {
            try await withCheckedThrowingContinuation { continuation in
                pending = continuation
                arrival?.resume()
                arrival = nil
            }
        }

        func started() async {
            if pending != nil { return }
            await withCheckedContinuation { arrival = $0 }
        }

        func succeed() {
            pending?.resume(returning: Data(#"{"link_switch":{"available":false,"reason":"in_progress","phase":"checking","at":"2026-11-19T08:00:05Z"}}"#.utf8))
            pending = nil
        }
    }

    @Test func confirmationStartsOnePostWithImmediateFeedback() async {
        let hub = FakeHub(), gate = StartGate(), store = MachineLinkSwitchStore()
        hub.onAsync("POST", Self.path) { _ in try await gate.wait() }
        let initial = machine()
        #expect(hub.sent("POST", Self.path).isEmpty, "opening the page and its confirmation sends nothing")
        let first = Task { await store.start(initial, api: hub.client, refresh: { initial }) }
        await gate.started()
        #expect(store.isStarting(initial))
        #expect(!store.canStart(initial))
        var otherHub = initial
        otherHub.group = "two"
        #expect(store.canStart(otherHub), "the same machine id on another hub has its own single flight")
        #expect(!store.isStarting(otherHub))
        await store.start(initial, api: hub.client, refresh: { initial })
        #expect(hub.sent("POST", Self.path).count == 1, "a repeated confirmation while the answer is out does not POST again")
        gate.succeed()
        await first.value
        #expect(!store.isStarting(initial))
        #expect(store.shown(initial)?.phase == "checking")
        #expect(hub.sent("POST", Self.path).count == 1)
    }

    @Test func acceptedStateHoldsUntilTheAuthoritativeListCatchesUp() async {
        let hub = FakeHub(), store = MachineLinkSwitchStore()
        hub.on("POST", Self.path, json: acceptedAnswer)
        let initial = machine(MachineLinkSwitch(available: true, phase: "failed", error: "link_timeout", at: at(1)))
        await store.start(initial, api: hub.client, refresh: { initial })
        #expect(store.shown(initial)?.phase == "checking")
        store.reconcile(initial) // a push from before the start cannot take it back to the old failure
        #expect(store.shown(initial)?.phase == "checking")
        var installing = initial
        installing.linkSwitch = accepted("installing", second: 9)
        store.reconcile([installing])
        #expect(store.shown(installing)?.phase == "installing")
        var succeeded = initial
        succeeded.kind = "link"
        succeeded.linkSwitch = MachineLinkSwitch(available: false, phase: "succeeded", at: at(30))
        store.reconcile(succeeded)
        #expect(store.shown(succeeded)?.phase == "succeeded")
        #expect(!store.canStart(succeeded))
    }

    @Test func acceptedStateWithoutTimestampsWaitsForAChangedSnapshot() async {
        let hub = FakeHub(), store = MachineLinkSwitchStore()
        hub.on("POST", Self.path, json: #"{"link_switch":{"available":false,"reason":"in_progress","phase":"checking"}}"#)
        let initial = machine(MachineLinkSwitch(available: true, phase: "failed", error: "link_timeout"))
        await store.start(initial, api: hub.client, refresh: { initial })
        #expect(store.shown(initial)?.phase == "checking")
        store.reconcile(initial)
        #expect(store.shown(initial)?.phase == "checking")
        var installing = initial
        installing.linkSwitch = MachineLinkSwitch(available: false, reason: "in_progress", phase: "installing")
        store.reconcile(installing)
        #expect(store.shown(installing)?.phase == "installing")
    }

    @Test func runningDisconnectIsNotAnOfflineFailure() {
        for phase in ["checking", "installing", "connecting", "rolling_back"] {
            var offline = machine(MachineLinkSwitch(available: false, phase: phase, error: "link_timeout"))
            offline.online = false
            offline.errorCode = "link_offline"
            offline.error = "raw connection failure"
            #expect(offline.linkSwitchRunning)
            #expect(MachineLinkSwitchStore.running(offline.linkSwitch))
            #expect(AddComputerText.machineLines(offline) == nil)
        }
        for phase in ["succeeded", "failed", "a_future_phase"] {
            #expect(!MachineLinkSwitchStore.running(MachineLinkSwitch(available: false, phase: phase)))
        }
        #expect(!MachineLinkSwitchStore.running(nil))
    }

    @Test func refusalReadsTheListAndFailureKeepsSSHRecovery() async {
        let hub = FakeHub(), store = MachineLinkSwitchStore()
        hub.on("POST", Self.path) { _ in throw APIError(status: 409, message: "raw hub text", fields: ["code": "offline"]) }
        let initial = machine()
        var refreshes = 0
        await store.start(initial, api: hub.client, refresh: { refreshes += 1; return initial })
        #expect(refreshes == 1)
        #expect(store.notice(initial) == .refused("offline"))
        #expect(store.canStart(initial), "a definite refusal did not start work and may be confirmed again")

        var failed = initial
        failed.linkSwitch = MachineLinkSwitch(available: true, phase: "failed", error: "link_timeout")
        let words = MachineLinkSwitchText.failure(failed.linkSwitch!, home: failed.home)
        #expect(words.contains("SSH"))
        #expect(!words.contains("link_timeout"))
        #expect(store.canStart(failed), "a rolled-back failure can be tried again behind confirmation")

        failed.linkSwitch = MachineLinkSwitch(available: false, reason: "offline", phase: "failed", error: "link_timeout", rollbackUnconfirmed: true)
        let uncertain = MachineLinkSwitchText.failure(failed.linkSwitch!, home: failed.home)
        #expect(uncertain != words, "an unconfirmed rollback must not reuse the confirmed SSH-fallback sentence")
        #expect(!uncertain.contains("link_timeout"))
        #expect(!store.canStart(failed))
        #expect(store.needsStatusCheck(failed))
    }

    @Test func uncertainAnswerNeverStartsAgainBeforeTheListChanges() async {
        let hub = FakeHub(), store = MachineLinkSwitchStore()
        hub.on("POST", Self.path) { _ in throw APIError(status: 503, message: "raw hub text") }
        let initial = machine()
        var refreshes = 0
        await store.start(initial, api: hub.client, refresh: { refreshes += 1; return initial })
        #expect(refreshes == 1)
        #expect(store.notice(initial) == .uncertain)
        #expect(store.needsStatusCheck(initial))
        #expect(!store.canStart(initial))
        await store.start(initial, api: hub.client, refresh: { refreshes += 1; return initial })
        #expect(hub.sent("POST", Self.path).count == 1)
        #expect(refreshes == 1, "the blocked second start neither POSTs nor refreshes")

        var running = initial
        running.linkSwitch = accepted("checking", second: 8)
        store.reconcile(running)
        #expect(store.notice(running) == nil)
        #expect(store.shown(running)?.phase == "checking")
        #expect(!store.canStart(running))
        #expect(hub.sent("POST", Self.path).count == 1)
    }

    @Test func forbiddenDoesNotRefreshOrOfferAnotherStart() async {
        let hub = FakeHub(), store = MachineLinkSwitchStore()
        hub.on("POST", Self.path) { _ in throw APIError(status: 403, message: "forbidden", fields: ["code": "forbidden"]) }
        let initial = machine()
        var refreshes = 0
        await store.start(initial, api: hub.client, refresh: { refreshes += 1; return initial })
        #expect(refreshes == 0)
        #expect(store.notice(initial) == .forbidden)
        #expect(!store.canStart(initial))
        #expect(!store.needsStatusCheck(initial))
    }

    @Test func everyKnownCodeHasPlainWordsAndLingerNamesTheAccount() {
        let codes = ["offline", "unsupported_os", "link_unavailable", "hub_old", "pending", "custom_node", "busy",
                     "in_progress", "no_systemd", "existing_link", "no_node", "unsafe_permissions", "no_package",
                     "hub_unanswered", "transfer_failed", "install_failed", "link_timeout", "config_changed", "interrupted", "unknown"]
        for code in codes {
            let words = MachineLinkSwitchText.reason(code)
            #expect(!words.isEmpty)
            #expect(words != code)
        }
        #expect(MachineLinkSwitchText.homeUser("/home/alice") == "alice")
        #expect(MachineLinkSwitchText.homeUser("/root/") == "root")
        #expect(MachineLinkSwitchText.homeUser(nil) == nil)
        #expect(MachineLinkSwitchText.reason("linger_off", home: "/home/alice").contains("alice"))
        #expect(MachineLinkSwitchText.reason("a_future_code") != "a_future_code")
    }
}
