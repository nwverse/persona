import CoreGraphics
import Foundation
import Testing
@testable import Persona

/// The avatar chosen on the hub and followed by every device (#2406, docs/apple/persona.md "On the phone"): read from
/// GET /api/v1/looks (an older hub's /api/v1/personas, the choice then this device's own), this device's old choice
/// moved up once with `if_unset`, a choice made here sent with PUT /api/v1/looks/current (taken back when the hub does
/// not take it), another device's `look` push shown once its package is here, an upload's choice not sent twice, and
/// with several hubs, the current one's.
@MainActor
struct AvatarSyncTests {
    private func store() -> (PersonaStore, URL, UserDefaults) {
        let root = FileManager.default.temporaryDirectory.appending(path: "avatar-sync-\(UUID().uuidString)")
        let defaults = UserDefaults(suiteName: "avatar-sync-\(UUID().uuidString)")!
        return (PersonaStore(root: root, defaults: defaults), root, defaults)
    }

    private func pick(_ s: PersonaStore, _ id: String) throws -> Persona {
        try #require(s.offered.first { $0.id == id })
    }

    // MARK: Reading

    @Test func showsTheHubsChoice() async throws {
        let hub = try LookHub(chosen: "nwverse")
        let (s, root, defaults) = store()
        await s.refresh(hub.client)
        #expect(s.current.id == "nwverse" && s.isOnPhone(s.current) && s.chosenID == "nwverse")
        #expect(hub.fake.sent("GET", "/api/v1/personas").isEmpty) // the looks list has the packages
        #expect(hub.puts.isEmpty) // nothing of this device's to move up
        #expect(defaults.bool(forKey: PersonaStore.followsHubKey))
        // The next launch shows it from disk, before the hub answers.
        #expect(PersonaStore(root: root, defaults: defaults).current.id == "nwverse")

        // Chosen elsewhere while this device heard no push: the next read takes it.
        hub.chosen = "persona"
        await s.refresh(hub.client)
        #expect(s.current.isBuiltin && s.chosenID == "persona" && hub.puts.isEmpty)
    }

    @Test func anOlderHubKeepsTheChoiceOnThisDevice() async throws {
        let hub = FakeHub() // no /api/v1/looks: 404
        hub.on("GET", "/api/v1/personas", json: #"{"personas":[{"id":"nwverse","rev":"r1","bytes":10,"manifest":\#(LookHub.manifest("nwverse"))}]}"#)
        for f in try PersonaTests.decode().files { hub.on("GET", "/api/v1/personas/nwverse/\(f)", json: "png:\(f)") }
        let (s, _, defaults) = store()
        await s.refresh(hub.client)
        #expect(hub.sent("GET", "/api/v1/looks").count == 1 && hub.sent("GET", "/api/v1/personas").count == 1)
        #expect(s.offered.map(\.id) == ["persona", "nwverse"] && s.error == nil)
        await s.choose(try pick(s, "nwverse"), api: hub.client)
        #expect(s.current.id == "nwverse" && defaults.string(forKey: PersonaStore.chosenKey) == "nwverse")
        #expect(hub.sent("PUT", "/api/v1/looks/current").isEmpty)
        #expect(!defaults.bool(forKey: PersonaStore.followsHubKey))
    }

    // MARK: This device's old choice

    @Test func thisDevicesOldChoiceMovesUpOnce() async throws {
        let hub = try LookHub(chosen: nil)
        let (s, _, defaults) = store()
        defaults.set("nwverse", forKey: PersonaStore.chosenKey) // chosen by an older app (or with an older hub)
        await s.refresh(hub.client)
        #expect(hub.puts.count == 1 && hub.puts.first?["id"]?.string == "nwverse" && hub.puts.first?["if_unset"]?.bool == true)
        #expect(hub.chosen == "nwverse" && s.current.id == "nwverse" && s.isOnPhone(s.current))

        await s.refresh(hub.client)
        #expect(hub.puts.count == 1) // once
        // The hub lost it (a new database): this device's copy of it is not sent again.
        hub.chosen = nil
        await s.refresh(hub.client)
        #expect(hub.puts.count == 1 && s.current.isBuiltin && s.chosenID == "persona")
    }

    /// Its package is not on the hub now (404): the default shows, the choice is kept, and it moves up when it is back.
    @Test func anOldChoiceTheHubDoesNotListWaitsForIt() async throws {
        let hub = try LookHub(ids: [], chosen: nil)
        let (s, _, defaults) = store()
        defaults.set("nwverse", forKey: PersonaStore.chosenKey)
        await s.refresh(hub.client)
        #expect(hub.puts.count == 1 && hub.chosen == nil)
        #expect(s.current.isBuiltin && s.chosenID == "nwverse" && !defaults.bool(forKey: PersonaStore.followsHubKey))
        hub.ids = ["nwverse"]
        try hub.serveFiles()
        await s.refresh(hub.client)
        #expect(hub.puts.count == 2 && hub.chosen == "nwverse" && s.current.id == "nwverse" && s.isOnPhone(s.current))
    }

    @Test func anotherDevicesChoiceThatGotThereFirstWins() async throws {
        let hub = try LookHub(ids: ["nwverse", "aurora"], chosen: nil)
        let (s, _, defaults) = store()
        defaults.set("nwverse", forKey: PersonaStore.chosenKey)
        // The list says none is chosen; by the time this device's arrives, another device's has.
        hub.afterList = { [unowned hub] in hub.chosen = "aurora" }
        await s.refresh(hub.client)
        #expect(hub.puts.count == 1 && hub.puts.first?["if_unset"]?.bool == true)
        #expect(hub.chosen == "aurora" && s.current.id == "aurora" && s.chosenID == "aurora")
    }

    @Test func nothingChosenHereSendsNothing() async throws {
        for saved in [nil, "persona", "persona-visor"] { // never chosen, the default, a retired drawing (shows the default)
            let hub = try LookHub(chosen: nil)
            let (s, _, defaults) = store()
            if let saved { defaults.set(saved, forKey: PersonaStore.chosenKey) }
            await s.refresh(hub.client)
            #expect(hub.puts.isEmpty && hub.chosen == nil && s.current.isBuiltin)
        }
    }

    // MARK: Choosing here

    @Test func aChoiceGoesToTheHub() async throws {
        let hub = try LookHub(chosen: "persona")
        let (s, _, defaults) = store()
        await s.refresh(hub.client)
        await s.choose(try pick(s, "nwverse"), api: hub.client)
        #expect(hub.puts.map { $0["id"]?.string } == ["nwverse"] && hub.puts[0]["if_unset"] == nil)
        #expect(hub.chosen == "nwverse" && s.current.id == "nwverse" && s.error == nil)
        #expect(defaults.string(forKey: PersonaStore.chosenKey) == "nwverse")
        await s.choose(.builtin, api: hub.client)
        #expect(hub.chosen == "persona" && s.current.isBuiltin && hub.puts.count == 2)
    }

    @Test func aChoiceTheHubDoesNotTakeGoesBack() async throws {
        let hub = try LookHub(chosen: "persona")
        let (s, _, defaults) = store()
        await s.refresh(hub.client)
        let frog = try pick(s, "nwverse")
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.method == "PUT" }
        let choosing = Task { await s.choose(frog, api: api) }
        try await gate.untilHeld()
        #expect(s.current.id == "nwverse") // shown at once, while the hub is asked
        hub.refuse = URLError(.notConnectedToInternet)
        gate.open()
        await choosing.value
        #expect(s.current.isBuiltin && s.chosenID == "persona" && defaults.string(forKey: PersonaStore.chosenKey) == "persona")
        #expect(s.error?.contains("奶蛙") == true)
        #expect(hub.chosen == "persona")

        // The hub no longer lists it (404): the same.
        hub.refuse = nil
        hub.ids = []
        await s.choose(frog, api: hub.client)
        #expect(s.current.isBuiltin && s.chosenID == "persona" && s.error != nil)
        // A choice it takes clears the message.
        await s.choose(.builtin, api: hub.client)
        #expect(s.error == nil)
    }

    /// Two quick choices go one at a time, in order; the first one's push, back while the second waits, changes nothing.
    @Test func quickChoicesReachTheHubInOrder() async throws {
        let hub = try LookHub(ids: ["nwverse", "aurora"], chosen: "persona")
        let (s, _, _) = store()
        await s.refresh(hub.client)
        for id in ["nwverse", "aurora", "persona"] { await s.choose(try pick(s, id), api: hub.client) } // both on the phone
        let sent = hub.puts.count
        let frog = try pick(s, "nwverse"), aurora = try pick(s, "aurora")
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.method == "PUT" && String(decoding: $0.body ?? Data(), as: UTF8.self).contains("nwverse") }
        let first = Task { await s.choose(frog, api: api) }
        try await gate.untilHeld()
        let second = Task { await s.choose(aurora, api: api) }
        try await Task.sleep(for: .milliseconds(50))
        #expect(s.current.id == "aurora")
        await s.follow("nwverse", effective: "nwverse", api: api)
        #expect(s.current.id == "aurora")
        gate.open()
        await first.value
        await second.value
        #expect(hub.puts.dropFirst(sent).map { $0["id"]?.string } == ["nwverse", "aurora"])
        #expect(hub.chosen == "aurora" && s.current.id == "aurora" && s.error == nil)
    }

    /// The first tap the hub took is what a later refused one goes back to, not the choice from before both.
    @Test func aRefusedChoiceGoesBackToTheOneTheHubTookBeforeIt() async throws {
        let hub = try LookHub(ids: ["nwverse", "aurora"], chosen: "persona")
        let (s, _, defaults) = store()
        await s.refresh(hub.client)
        for id in ["nwverse", "aurora", "persona"] { await s.choose(try pick(s, id), api: hub.client) } // both on the phone
        let frog = try pick(s, "nwverse"), aurora = try pick(s, "aurora")
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.method == "PUT" && String(decoding: $0.body ?? Data(), as: UTF8.self).contains("nwverse") }
        let first = Task { await s.choose(frog, api: api) }
        try await gate.untilHeld()
        let second = Task { await s.choose(aurora, api: api) }
        try await Task.sleep(for: .milliseconds(50))
        hub.refuse = URLError(.networkConnectionLost)
        hub.refuseOnly = "aurora"
        gate.open()
        await first.value
        await second.value
        #expect(hub.chosen == "nwverse")
        #expect(s.current.id == "nwverse" && s.chosenID == "nwverse" && defaults.string(forKey: PersonaStore.chosenKey) == "nwverse")
        #expect(s.error != nil)
    }

    /// A refused choice leaves the old local choice as it was: still this device's, still moved up once the hub takes it.
    @Test func aRefusedChoiceKeepsTheOldLocalChoiceToMoveUp() async throws {
        let hub = try LookHub(ids: ["nwverse", "aurora"], chosen: nil)
        let (s, _, defaults) = store()
        defaults.set("nwverse", forKey: PersonaStore.chosenKey) // chosen by an older app
        hub.refuse = URLError(.notConnectedToInternet)
        await s.refresh(hub.client) // the if_unset does not get there
        #expect(hub.chosen == nil && s.current.id == "nwverse" && !defaults.bool(forKey: PersonaStore.followsHubKey))
        await s.choose(try pick(s, "aurora"), api: hub.client) // nor does this
        #expect(s.current.id == "nwverse" && s.chosenID == "nwverse" && s.error != nil)
        #expect(!defaults.bool(forKey: PersonaStore.followsHubKey))
        hub.refuse = nil
        await s.refresh(hub.client)
        #expect(hub.chosen == "nwverse" && hub.puts.last?["if_unset"]?.bool == true)
        #expect(s.current.id == "nwverse" && s.chosenID == "nwverse")
    }

    // MARK: Another device's

    /// Saved once its package is all here: a launch while it downloads still shows the one on screen, not the default.
    @Test func theHubsChoiceIsSavedOnceItsPackageIsHere() async throws {
        let hub = try LookHub(ids: ["nwverse", "aurora"], chosen: "nwverse")
        let (s, root, defaults) = store()
        await s.refresh(hub.client)
        #expect(s.current.id == "nwverse")

        // A list read brings another device's choice.
        hub.chosen = "aurora"
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.path.hasPrefix("/api/v1/personas/aurora/") }
        let reading = Task { await s.refresh(api) }
        try await gate.untilHeld()
        #expect(s.current.id == "nwverse")
        let relaunched = PersonaStore(root: root, defaults: defaults) // killed while it downloads
        #expect(relaunched.current.id == "nwverse")
        gate.open()
        await reading.value
        #expect(s.current.id == "aurora" && s.chosenID == "aurora")
        #expect(PersonaStore(root: root, defaults: defaults).current.id == "aurora")
    }

    /// The same, for the `look` push.
    @Test func aPushedChoiceIsSavedOnceItsPackageIsHere() async throws {
        let hub = try LookHub(ids: ["nwverse", "aurora"], chosen: "nwverse")
        let (s, root, defaults) = store()
        await s.refresh(hub.client)
        hub.chosen = "aurora"
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.path.hasPrefix("/api/v1/personas/aurora/") }
        let following = Task { await s.follow("aurora", effective: "aurora", api: api) }
        try await gate.untilHeld()
        #expect(PersonaStore(root: root, defaults: defaults).current.id == "nwverse")
        gate.open()
        await following.value
        #expect(s.current.id == "aurora" && PersonaStore(root: root, defaults: defaults).current.id == "aurora")
    }

    /// The push comes twice (the hub broadcasts every PUT, the same one too) while the package downloads: it shows.
    @Test func aPushRepeatedWhileItsPackageDownloadsShows() async throws {
        let hub = try LookHub(chosen: "persona")
        let (s, _, _) = store()
        await s.refresh(hub.client)
        hub.chosen = "nwverse"
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.path.hasSuffix("/dot/idle.png") }
        let first = Task { await s.follow("nwverse", effective: "nwverse", api: api) }
        try await gate.untilHeld()
        let second = Task { await s.follow("nwverse", effective: "nwverse", api: api) }
        try await Task.sleep(for: .milliseconds(50))
        #expect(s.current.isBuiltin)
        gate.open()
        await first.value
        await second.value
        #expect(s.current.id == "nwverse" && s.isOnPhone(s.current) && s.chosenID == "nwverse")
    }

    /// A list asked for before the push, without the look pushed, never puts the default on screen in between.
    @Test func aListOlderThanThePushDoesNotShowTheDefault() async throws {
        let hub = try LookHub(chosen: "nwverse")
        let (s, _, _) = store()
        await s.refresh(hub.client)
        #expect(s.current.id == "nwverse")
        var shown: [String] = []
        s.onChange = { shown.append(s.current.id) }
        let gate = Gate()
        let stale = gate.client(hub.fake, answerFirst: true) { $0.path == "/api/v1/looks" }
        let reading = Task { await s.refresh(stale) } // its list is the one before
        try await gate.untilHeld()
        hub.ids = ["nwverse", "u-abc12345"] // made and chosen on another device
        hub.chosen = "u-abc12345"
        try hub.serveFiles()
        let following = Task { await s.follow("u-abc12345", effective: "u-abc12345", api: hub.client) }
        try await Task.sleep(for: .milliseconds(50))
        gate.open()
        await reading.value
        await following.value
        #expect(shown == ["u-abc12345"])
        #expect(s.current.id == "u-abc12345" && s.chosenID == "u-abc12345")
    }

    @Test func anotherDevicesChoiceShowsOnceItsPackageIsHere() async throws {
        let hub = try LookHub(chosen: "persona")
        let (s, root, defaults) = store()
        await s.refresh(hub.client)
        #expect(s.current.isBuiltin)
        hub.chosen = "nwverse" // on another device
        let gate = Gate()
        let api = gate.client(hub.fake) { $0.path.hasSuffix("/dot/idle.png") }
        let following = Task { await s.follow("nwverse", effective: "nwverse", api: api) }
        try await gate.untilHeld()
        #expect(s.current.isBuiltin) // never half a package
        #expect(!FileManager.default.fileExists(atPath: root.appending(path: "nwverse/r1").path(percentEncoded: false)))
        gate.open()
        await following.value
        #expect(s.current.id == "nwverse" && s.isOnPhone(s.current) && s.chosenID == "nwverse")
        #expect(defaults.string(forKey: PersonaStore.chosenKey) == "nwverse")
        #expect(hub.puts.isEmpty) // followed, not sent back

        // The hub went back to the default (the chosen one's extension is off): at once.
        await s.follow("nwverse", effective: "persona", api: hub.client)
        #expect(s.current.isBuiltin && s.chosenID == "persona")
    }

    @Test func aPushForALookNotListedHereYetReadsTheList() async throws {
        let hub = try LookHub(ids: [], chosen: "persona")
        let (s, _, _) = store()
        await s.refresh(hub.client)
        // Made from an image and chosen on another device.
        hub.ids = ["u-abc12345"]
        hub.chosen = "u-abc12345"
        try hub.serveFiles()
        await s.follow("u-abc12345", effective: "u-abc12345", api: hub.client)
        #expect(s.current.id == "u-abc12345" && s.isOnPhone(s.current))
        #expect(hub.puts.isEmpty)
    }

    // MARK: Uploads

    /// The upload's `choose` chose it on the hub, and a delete of the chosen one brings the default back there: neither
    /// is sent again.
    @Test func anUploadOrADeleteIsNotSentAgain() async throws {
        let hub = try LookHub(ids: [], chosen: "persona")
        let (s, _, _) = store()
        await s.refresh(hub.client)
        let model = AvatarUploadModel(image: Self.image(), deps: .init(api: { hub.client }, store: { s }))
        model.name = "豆豆"
        hub.fake.on("POST", "/api/v1/looks/uploads") { [unowned hub] _ in
            hub.ids = ["u-abc12345"]
            hub.chosen = "u-abc12345"
            try hub.serveFiles()
            return Data(#"{"look":{"id":"u-abc12345","source":"hub","name":"奶蛙","app_icon":"persona","rev":"r1","bytes":10,"uploaded":true},"current":{"id":"u-abc12345","effective":"u-abc12345"}}"#.utf8)
        }
        #expect(await model.upload())
        #expect(s.current.id == "u-abc12345" && s.chosenID == "u-abc12345")
        // Its push comes too: nothing more.
        await s.follow("u-abc12345", effective: "u-abc12345", api: hub.client)
        #expect(hub.puts.isEmpty && s.current.id == "u-abc12345")

        let uploads = AvatarUploadsModel(deps: .init(api: { hub.client }, features: { [AvatarUploadsModel.feature] }, isDemo: { false },
                                                     store: { s }))
        hub.fake.on("DELETE", "/api/v1/looks/uploads/u-abc12345") { [unowned hub] _ in
            hub.ids = []
            hub.chosen = "persona"
            return Data(#"{"current":{"id":"persona","effective":"persona"}}"#.utf8)
        }
        #expect(await uploads.delete(try pick(s, "u-abc12345")))
        #expect(s.current.isBuiltin && hub.puts.isEmpty)
    }

    // MARK: Several hubs

    /// The avatar is a hub-level setting (docs/protocol/multi-hub.md): the current hub's shows, a choice goes to it alone,
    /// and this device's old choice moves up only to the first hub that keeps the choice.
    @Test func withSeveralHubsTheCurrentOnesShows() async throws {
        let home = try LookHub(url: "https://home.invalid", chosen: nil)
        let work = try LookHub(url: "https://work.invalid", ids: ["nwverse", "aurora"], chosen: nil)
        let (s, _, defaults) = store()
        defaults.set("nwverse", forKey: PersonaStore.chosenKey) // chosen by an older app
        await s.refresh(home.client)
        #expect(home.chosen == "nwverse" && s.current.id == "nwverse")

        // Work's group becomes the current hub: its choice (none yet: the default); home's is not sent there.
        await s.refresh(work.client)
        #expect(work.puts.isEmpty && work.chosen == nil && s.current.isBuiltin && s.chosenID == "persona")
        await s.choose(try pick(s, "aurora"), api: work.client)
        #expect(work.chosen == "aurora" && home.chosen == "nwverse" && home.puts.count == 1)

        // Back to home's: home's.
        await s.refresh(home.client)
        #expect(s.current.id == "nwverse" && home.puts.count == 1)

        // The current hub changed while the one before was still answering: that answer never shows.
        await s.refresh(work.client)
        #expect(s.current.id == "aurora")
        var changes = 0
        s.onChange = { changes += 1 }
        let gate = Gate()
        let slowHome = gate.client(home.fake) { $0.path == "/api/v1/looks" }
        let before = Task { await s.refresh(slowHome) }
        try await gate.untilHeld()
        let now = Task { await s.refresh(work.client) }
        try await Task.sleep(for: .milliseconds(50))
        gate.open()
        await before.value
        await now.value
        #expect(s.current.id == "aurora" && s.chosenID == "aurora" && changes == 0)
    }

    /// The current hub changes while the old choice is on its way to the first: it is not moved up to the second too.
    @Test func theOldChoiceOnItsWayToOneHubIsNotSentToTheNext() async throws {
        let home = try LookHub(url: "https://home.invalid", chosen: nil)
        let work = try LookHub(url: "https://work.invalid", chosen: nil)
        let (s, _, defaults) = store()
        defaults.set("nwverse", forKey: PersonaStore.chosenKey) // chosen by an older app
        let gate = Gate()
        let slowHome = gate.client(home.fake) { $0.method == "PUT" }
        let before = Task { await s.refresh(slowHome) }
        try await gate.untilHeld()
        let now = Task { await s.refresh(work.client) }
        try await Task.sleep(for: .milliseconds(50))
        gate.open()
        await before.value
        await now.value
        #expect(home.chosen == "nwverse" && home.puts.count == 1)
        #expect(work.puts.isEmpty && work.chosen == nil && s.current.isBuiltin)
    }

    /// What a hub that is no longer the current one answers a choice made on it does not change the current hub's.
    @Test func theHubBeforeAnsweringLateChangesNothingHere() async throws {
        let home = try LookHub(url: "https://home.invalid", ids: ["nwverse", "aurora"], chosen: "persona")
        let work = try LookHub(url: "https://work.invalid", ids: ["nwverse", "aurora"], chosen: "aurora")
        let (s, _, _) = store()
        await s.refresh(home.client)
        for id in ["nwverse", "aurora", "persona"] { await s.choose(try pick(s, id), api: home.client) } // both on the phone
        let frog = try pick(s, "nwverse")

        // Refused, after work's became the current hub.
        var gate = Gate()
        var slowHome = gate.client(home.fake) { $0.method == "PUT" }
        var choosing = Task { await s.choose(frog, api: slowHome) }
        try await gate.untilHeld()
        await s.refresh(work.client)
        #expect(s.current.id == "aurora")
        home.refuse = URLError(.networkConnectionLost)
        gate.open()
        await choosing.value
        #expect(s.current.id == "aurora" && s.chosenID == "aurora" && home.chosen == "persona")

        // Taken, after work's became the current hub: what a refused choice on work's goes back to is still work's.
        home.refuse = nil
        await s.refresh(home.client)
        #expect(s.current.isBuiltin)
        gate = Gate()
        slowHome = gate.client(home.fake) { $0.method == "PUT" }
        choosing = Task { await s.choose(frog, api: slowHome) }
        try await gate.untilHeld()
        await s.refresh(work.client)
        gate.open()
        await choosing.value
        #expect(home.chosen == "nwverse" && s.current.id == "aurora")
        work.refuse = URLError(.networkConnectionLost)
        await s.choose(.builtin, api: work.client)
        #expect(work.chosen == "aurora" && s.current.id == "aurora" && s.chosenID == "aurora")
    }

    // MARK: Demo Mode and the push

    @Test func demoModeReadsAndSendsNothing() async throws {
        let hub = try LookHub(url: "https://demo.invalid", chosen: "nwverse")
        let (s, _, _) = store()
        await s.refresh(hub.client)
        await s.follow("nwverse", effective: "nwverse", api: hub.client)
        await s.choose(.builtin, api: hub.client)
        #expect(hub.fake.requests.isEmpty && s.current.isBuiltin)
    }

    @Test func decodesTheLookPush() throws {
        let data = Data(#"{"type":"look","id":"aurora","effective":"persona"}"#.utf8)
        guard case .look(let id, let effective) = try #require(ServerEvent.decode(data)) else {
            Issue.record("not look")
            return
        }
        #expect(id == "aurora" && effective == "persona")
        #expect(ServerEvent.decode(Data(#"{"type":"look"}"#.utf8)) == nil)
        guard case .look = try #require(ServerEvent.decode(data).map({ $0.stamped("g1") })) else {
            Issue.record("not look in another group")
            return
        }
    }

    private static func image() -> CGImage {
        let ctx = CGContext(data: nil, width: 200, height: 200, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                            bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
        ctx.setFillColor(CGColor(srgbRed: 0.4, green: 0.6, blue: 0.8, alpha: 1))
        ctx.fill(CGRect(x: 0, y: 0, width: 200, height: 200))
        return ctx.makeImage()!
    }
}

/// A hub that keeps the choice, as server/internal/hub/looks.go does: GET /api/v1/looks lists Mochi and the packages
/// `ids` with `chosen`; PUT /api/v1/looks/current keeps one it lists (404 otherwise), with `if_unset` only when none is.
@MainActor
private final class LookHub {
    let fake: FakeHub
    var ids: [String]
    var chosen: String?
    /// Every PUT /api/v1/looks/current fails with it (only the one for `refuseOnly` when that is set).
    var refuse: (any Error)?
    var refuseOnly: String?
    /// Runs once a list has been answered (another device choosing right then).
    var afterList: (() -> Void)?

    init(url: String = "https://fake.invalid", ids: [String] = ["nwverse"], chosen: String?) throws {
        fake = FakeHub(url: url)
        self.ids = ids
        self.chosen = chosen
        fake.on("GET", "/api/v1/looks") { [unowned self] _ in
            defer { afterList?() }
            return Data(listing.utf8)
        }
        fake.on("PUT", "/api/v1/looks/current") { [unowned self] req in try put(req) }
        try serveFiles()
    }

    var client: APIClient { fake.client }

    /// The bodies of the PUTs so far.
    var puts: [[String: JSONValue]] {
        fake.sent("PUT", "/api/v1/looks/current").compactMap { $0.body.flatMap { try? JSONDecoder().decode([String: JSONValue].self, from: $0) } }
    }

    /// persona.json of `id`: PersonaTests' Milk Frog under that id; made from an upload for a `u-` id.
    static func manifest(_ id: String) -> String {
        let m = PersonaTests.manifest.replacingOccurrences(of: #""id":"nwverse""#, with: #""id":"\#(id)""#)
        return id.hasPrefix("u-") ? m.replacingOccurrences(of: #""name":"奶蛙","#, with: #""name":"奶蛙","source":{"kind":"upload"},"#) : m
    }

    func serveFiles() throws {
        for id in ids {
            for f in try PersonaTests.decode(Self.manifest(id)).files { fake.on("GET", "/api/v1/personas/\(id)/\(f)", json: "png:\(f)") }
        }
    }

    private var current: String {
        let effective = chosen.flatMap { c in c == "persona" || ids.contains(c) ? c : nil } ?? "persona"
        guard let chosen else { return #"{"id":null,"effective":"persona"}"# }
        return #"{"id":"\#(chosen)","effective":"\#(effective)","updated_at":"2026-11-09T08:00:00Z"}"#
    }

    private var listing: String {
        let looks = [#"{"id":"persona","source":"builtin","name":"团子","names":{"en":"Mochi"},"app_icon":"persona"}"#]
            + ids.map { #"{"id":"\#($0)","source":"hub","name":"奶蛙","app_icon":"persona","rev":"r1","bytes":10,"manifest":\#(Self.manifest($0))}"# }
        return #"{"looks":[\#(looks.joined(separator: ","))],"current":\#(current)}"#
    }

    private func put(_ req: LocalRequest) throws -> Data {
        let body = try JSONDecoder().decode([String: JSONValue].self, from: req.body ?? Data())
        if let refuse, refuseOnly == nil || refuseOnly == body["id"]?.string { throw refuse }
        if body["if_unset"]?.bool == true, chosen != nil { return Data(current.utf8) }
        guard let id = body["id"]?.string, id == "persona" || ids.contains(id) else { throw APIError(status: 404, message: "no look") }
        chosen = id
        return Data(current.utf8)
    }
}

/// Holds the requests `holding` picks until `open()`: a request still on its way. `answerFirst`: the hub answers it
/// at once and the answer is held on its way back (what it says is from before whatever happens meanwhile).
@MainActor
private final class Gate {
    private var waiting: [CheckedContinuation<Void, Never>] = []
    private var opened = false
    private(set) var held = 0

    func client(_ hub: FakeHub, answerFirst: Bool = false, holding: @escaping @Sendable (LocalRequest) -> Bool) -> APIClient {
        var c = hub.client
        let answer = c.transport!
        c.transport = { [self] req in
            guard holding(req) else { return try await answer(req) }
            if !answerFirst { await hold() }
            let data = try await answer(req)
            if answerFirst { await hold() }
            return data
        }
        return c
    }

    private func hold() async {
        guard !opened else { return }
        held += 1
        await withCheckedContinuation { waiting.append($0) }
    }

    func open() {
        opened = true
        waiting.forEach { $0.resume() }
        waiting = []
    }

    func untilHeld() async throws {
        for _ in 0..<300 where held == 0 { try await Task.sleep(for: .milliseconds(10)) }
        #expect(held > 0, "nothing was asked for")
    }
}
