import Foundation
import Testing
import UserNotifications
@testable import Persona

/// Pairing receipts (#4199, docs/protocol/protocol.md「Computer pairing codes」): the hub's payloads, the buttons, the requests they send,
/// and what the user is told.
struct PairReceiptActionsTests {
    /// The payloads as the hub sends them (server/internal/hub/pair_receipt_push_test.go), in userInfo form.
    static func info(_ json: String) -> [AnyHashable: Any] {
        (try? JSONSerialization.jsonObject(with: Data(json.utf8)) as? [String: Any]) ?? [:]
    }

    static var computerCode: [AnyHashable: Any] { info(#"{"aps":{"alert":{"title":"Persona","body":"iPhone made a pairing code for the computer “Office Mac”"},"category":"pair_receipt","sound":"default"},"pair":{"device_id":"cred_issuer","kind":"computer_code","machine":"mach_office"},"hub_id":"hub_b"}"#) }
    static var joining: [AnyHashable: Any] { info(#"{"aps":{"alert":{"title":"Persona","body":"A new computer, “Office Mac”, wants to join"},"category":"computer_joining","sound":"default"},"pair":{"kind":"computer_joining","machine":"mach_office"}}"#) }
    static var device: [AnyHashable: Any] { info(#"{"aps":{"alert":{"title":"Persona","body":"New device paired: iPad"},"category":"pair_receipt","sound":"default"},"pair":{"device_id":"cred_new","kind":"device"}}"#) }

    @Test func theHubsPayloadsParse() throws {
        let code = try #require(PairPush(Self.computerCode))
        #expect(code == PairPush(kind: .computerCode, deviceID: "cred_issuer", machine: "mach_office", hubID: "hub_b"))
        #expect(PairPush(Self.joining) == PairPush(kind: .computerJoining, machine: "mach_office"))
        #expect(PairPush(Self.device) == PairPush(kind: .device, deviceID: "cred_new"))
    }

    /// An older hub's push (no `pair`), a kind the app does not know, or one without the id its kind needs: not a
    /// receipt, so the notification stays a plain one.
    @Test func anythingElseIsAPlainNotification() {
        #expect(PairPush(["aps": ["alert": ["body": "有新设备配对：iPad"]]]) == nil)
        #expect(PairPush(["pair": ["kind": "something_new", "machine": "m"]]) == nil)
        #expect(PairPush(["pair": ["kind": "device"]]) == nil)
        #expect(PairPush(["pair": ["kind": "device", "device_id": ""]]) == nil)
        #expect(PairPush(["pair": ["kind": "computer_joining"]]) == nil)
        #expect(PairPush(["pair": "device"]) == nil)
    }

    @Test func bothCategoriesAreRegisteredWithUnlockedButtons() throws {
        let cats = Dictionary(uniqueKeysWithValues: NotificationActions.categories.map { ($0.identifier, $0) })
        let receipt = try #require(cats["pair_receipt"])
        #expect(receipt.actions.map(\.identifier) == [PairReceiptActions.revoke])
        #expect(receipt.actions[0].options.contains(.destructive))
        let joining = try #require(cats["computer_joining"])
        #expect(joining.actions.map(\.identifier) == [PairReceiptActions.confirm, PairReceiptActions.reject])
        #expect(!joining.actions[0].options.contains(.destructive))
        #expect(joining.actions[1].options.contains(.destructive))
        for a in receipt.actions + joining.actions {
            #expect(a.options.contains(.authenticationRequired))
            #expect(!a.options.contains(.foreground))
            #expect(!a.title.isEmpty)
        }
    }

    @Test func eachButtonAndKindMakesItsRequest() throws {
        let code = try #require(PairPush(Self.computerCode))
        let joining = try #require(PairPush(Self.joining))
        let device = try #require(PairPush(Self.device))
        #expect(PairReceiptActions.request(action: PairReceiptActions.revoke, device) == .revokeDevice("cred_new"))
        // A code made for a computer: Undo rejects the computer (still pending), not the device that made it.
        #expect(PairReceiptActions.request(action: PairReceiptActions.revoke, code) == .rejectComputer("mach_office"))
        #expect(PairReceiptActions.request(action: PairReceiptActions.confirm, joining) == .confirmComputer("mach_office"))
        #expect(PairReceiptActions.request(action: PairReceiptActions.reject, joining) == .rejectComputer("mach_office"))
        // A button that does not fit the kind, a tap, a dismissal: nothing is sent.
        #expect(PairReceiptActions.request(action: PairReceiptActions.confirm, device) == nil)
        #expect(PairReceiptActions.request(action: PairReceiptActions.revoke, joining) == nil)
        for a in [UNNotificationDefaultActionIdentifier, UNNotificationDismissActionIdentifier, NotificationActions.allow] {
            #expect(PairReceiptActions.request(action: a, device) == nil)
            #expect(PairReceiptActions.request(action: a, joining) == nil)
        }
    }

    @Test func theRequestsGoToTheHub() async {
        let hub = FakeHub()
        hub.on("DELETE", "/api/v1/auth/tokens/cred_new", json: #"{"ok":true}"#)
        hub.on("POST", "/api/v1/machines/mach_office/confirm", json: #"{"ok":true}"#)
        hub.on("POST", "/api/v1/machines/mach_office/reject", json: #"{"ok":true}"#)
        #expect(await PairReceiptActions.perform(.revokeDevice("cred_new"), client: hub.client) == .revoked)
        #expect(await PairReceiptActions.perform(.confirmComputer("mach_office"), client: hub.client) == .confirmed)
        #expect(await PairReceiptActions.perform(.rejectComputer("mach_office"), client: hub.client) == .rejected)
        #expect(hub.sent("DELETE", "/api/v1/auth/tokens/cred_new").count == 1)
        #expect(hub.sent("POST", "/api/v1/machines/mach_office/confirm").count == 1)
        #expect(hub.sent("POST", "/api/v1/machines/mach_office/reject").count == 1)
    }

    /// Undo on a computer that was confirmed meanwhile: its page opens instead (in the group the client is for), to
    /// decide there; the follow-up notification carries what to open.
    @Test func aConfirmedComputerOpensItsPage() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/mach_office/reject") { _ in
            throw APIError(status: 409, message: "not pending", fields: ["code": "not_pending"])
        }
        var client = hub.client
        client.group = "work"
        let push = try #require(PairPush(Self.computerCode))
        let outcome = await PairReceiptActions.perform(.rejectComputer("mach_office"), client: client)
        #expect(outcome == .alreadyConfirmed(MachineRef(group: "work", id: "mach_office")))
        let note = PairReceiptActions.content(outcome, request: .rejectComputer("mach_office"), push: push)
        let follow = try #require(PairPush(note.userInfo))
        #expect(follow.kind == .machine && follow.machine == "mach_office" && follow.hubID == "hub_b")
        #expect(PairReceiptActions.destination(follow, group: "work") == .machine(MachineRef(group: "work", id: "mach_office")))
    }

    @Test func goneAndOfflineSayWhy() async {
        let hub = FakeHub() // no routes: every request is a 404
        guard case .failed(let title, let reason) = await PairReceiptActions.perform(.revokeDevice("cred_new"), client: hub.client) else {
            Issue.record("a revoke of a gone device should fail")
            return
        }
        #expect(title == String(localized: "没能撤销"))
        #expect(reason == String(localized: "这台设备已经撤销了，或者已经不在了。"))
        let gone = await PairReceiptActions.perform(.confirmComputer("mach_office"), client: hub.client)
        #expect(gone == .failed(title: String(localized: "没能确认"), reason: String(localized: "这台电脑已经不在了。")))

        let offline = FakeHub()
        offline.on("POST", "/api/v1/machines/mach_office/reject") { _ in throw URLError(.notConnectedToInternet) }
        let out = await PairReceiptActions.perform(.rejectComputer("mach_office"), client: offline.client)
        #expect(out == .failed(title: String(localized: "没能拒绝"), reason: IntentFailure.sentence(for: URLError(.notConnectedToInternet))))
    }

    /// The button asks the hub that sent the push (its `hub_id`), else the group whose key opened it, else the first.
    @Test func itGoesToThePushsGroup() {
        let groups = ["hub_b": "work"]
        let lookup: (String) -> String? = { groups[$0] }
        #expect(PairPush(kind: .device, deviceID: "c", hubID: "hub_b").group(groupOfHub: lookup) == "work")
        #expect(PairPush(kind: .device, deviceID: "c", hubID: "hub_x", keyGroup: "home").group(groupOfHub: lookup) == "home")
        #expect(PairPush(kind: .device, deviceID: "c", keyGroup: "").group(groupOfHub: lookup) == nil)
        #expect(PairPush(kind: .device, deviceID: "c").group(groupOfHub: lookup) == nil)
    }

    @Test func tappingOpensWhatItIsAbout() throws {
        let joining = try #require(PairPush(Self.joining))
        #expect(PairReceiptActions.destination(joining, group: "work") == .confirmComputer(MachineRef(group: "work", id: "mach_office")))
        #expect(PairReceiptActions.destination(joining, group: nil) == .confirmComputer(MachineRef(id: "mach_office")))
        #expect(PairReceiptActions.destination(try #require(PairPush(Self.device)), group: nil) == .devices)
        #expect(PairReceiptActions.destination(try #require(PairPush(Self.computerCode)), group: nil) == .devices)
    }

    /// What the user reads never has an id in it: ids only travel in `pair`.
    @Test func noIdsInWhatIsShown() throws {
        let push = try #require(PairPush(Self.computerCode))
        let ref = MachineRef(group: "work", id: "mach_office")
        let outcomes: [PairReceiptActions.Outcome] = [.revoked, .rejected, .confirmed, .alreadyConfirmed(ref)]
        for o in outcomes {
            let c = PairReceiptActions.content(o, request: .rejectComputer("mach_office"), push: push)
            for text in [c.title, c.subtitle, c.body] {
                #expect(!text.contains("mach_office") && !text.contains("cred_") && !text.contains("hub_b"), "\(o): \(text)")
            }
            #expect(!c.title.isEmpty && !c.body.isEmpty)
        }
        for info in [Self.computerCode, Self.joining, Self.device] {
            let body = ((info["aps"] as? [String: Any])?["alert"] as? [String: Any])?["body"] as? String ?? ""
            #expect(!body.contains("mach_") && !body.contains("cred_"))
        }
    }
}
