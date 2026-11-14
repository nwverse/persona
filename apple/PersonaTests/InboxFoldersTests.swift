import Foundation
import Testing
@testable import Persona

/// Inbox folders on iOS (docs/assistant/inbox.md "Folders", #2463): paths normalized as the hub and the web do, the
/// folder filter and the folder bar's counts, the list unchanged while folders are off, Move to Folder… (a path, no
/// folder, back to AI; shown at once, put back on failure) and the folder settings (on / off and the rules).
@MainActor
struct InboxFoldersTests {
    private static func item(_ id: String, folder: String? = nil, by: String? = nil, done: Bool = false, manager: String? = nil) throws -> InboxItem {
        var json = #"{"id":"\#(id)","source":"mail","kind":"mail","account":"a","account_name":"me@example.com","title":"示例 \#(id)","preview":"","count":1,"tier":"fyi""#
        if let folder { json += #","folder":"\#(folder)""# }
        if let by { json += #","folder_by":"\#(by)""# }
        if done { json += #","done_at":"2026-11-09T01:00:00Z""# }
        if let manager { json += #","manager":"\#(manager)""# }
        return try JSON.decoder.decode(InboxItem.self, from: Data((json + "}").utf8))
    }

    // MARK: Model

    @Test func itemsDecodeTheirFolderAndWhoFiledIt() throws {
        let filed = try Self.item("1", folder: "work/alerts", by: "model")
        #expect(filed.folder == "work/alerts" && filed.folderBy == "model")
        let plain = try Self.item("2")
        #expect(plain.folder == nil && plain.folderBy == nil) // an older hub
        // kept on disk with the item, read back the same
        let back = try JSON.decoder.decode(InboxItem.self, from: JSON.encoder.encode(filed))
        #expect(back == filed)
    }

    @Test func folderSettingsDecodeAndAnOlderAnswerIsOff() throws {
        let list = try JSON.decoder.decode(InboxFolderList.self, from: Data(#"""
        {"enabled":true,"rules":"构建通知放 work/builds","folders":[{"path":"work/alerts","count":3,"open":2}]}
        """#.utf8))
        #expect(list.settings == InboxFolderSettings(enabled: true, rules: "构建通知放 work/builds"))
        #expect(list.folders == [.init(path: "work/alerts", count: 3, open: 2)])
        let bare = try JSON.decoder.decode(InboxFolderList.self, from: Data("{}".utf8))
        #expect(bare.settings == InboxFolderSettings() && !bare.settings.enabled && bare.folders.isEmpty)
        let sent = try JSONSerialization.jsonObject(with: JSON.encoder.encode(InboxFolderSettings(enabled: true, rules: "r"))) as? [String: Any]
        #expect(sent?.keys.sorted() == ["enabled", "rules"] && sent?["enabled"] as? Bool == true && sent?["rules"] as? String == "r")
    }

    @Test func aMoveSendsNullToHandItBackToTheModel() throws {
        let null = try JSONSerialization.jsonObject(with: JSONEncoder().encode(InboxFolderBody(folder: nil))) as? [String: Any]
        #expect(null?.keys.sorted() == ["folder"] && null?["folder"] is NSNull)
        let none = try JSONSerialization.jsonObject(with: JSONEncoder().encode(InboxFolderBody(folder: ""))) as? [String: String]
        #expect(none == ["folder": ""])
    }

    // MARK: Paths

    /// The same cases as the hub's `normalizeFolder` and the web's `normalizeFolder`.
    @Test func pathsNormalizeAsTheHubKeepsThem() {
        #expect(InboxFolders.normalize("  work / alerts  /engine ") == "work/alerts/engine")
        #expect(InboxFolders.normalize("work\\alerts") == "work/alerts")
        #expect(InboxFolders.normalize("a//./../b") == "a/b")
        #expect(InboxFolders.normalize("big   team\tchat") == "big team chat")
        #expect(InboxFolders.normalize("a\u{0007}b") == "a b")
        #expect(InboxFolders.normalize("/ . /..") == "")
        #expect(InboxFolders.normalize("Work/Alerts") == "Work/Alerts") // case is kept
        #expect(InboxFolders.normalize("1/2/3/4/5/6/7/8") == "1/2/3/4/5/6")
        #expect(InboxFolders.normalize(String(repeating: "x", count: 50)) == String(repeating: "x", count: 40))
        #expect(InboxFolders.normalize(String(repeating: "文", count: 41)) == String(repeating: "文", count: 40))
        // at most 200 bytes, separators counted: five 40-byte levels are 204
        let level = String(repeating: "y", count: 40)
        #expect(InboxFolders.normalize(Array(repeating: level, count: 5).joined(separator: "/")) == Array(repeating: level, count: 4).joined(separator: "/"))
    }

    @Test func namesAndCrumbs() {
        #expect(InboxFolders.name("work/alerts/engine") == "engine")
        #expect(InboxFolders.name("work") == "work")
        #expect(InboxFolders.crumbs("work/alerts") == [.init(name: "work", path: "work"), .init(name: "alerts", path: "work/alerts")])
        #expect(InboxFolders.crumbs("").isEmpty && InboxFolders.crumbs(InboxFolders.unfiled).isEmpty)
    }

    @Test func theFolderFilterTakesAFolderAndEverythingUnderIt() throws {
        let deep = try Self.item("1", folder: "work/alerts/engine")
        let near = try Self.item("2", folder: "work/alertsx")
        let none = try Self.item("3")
        #expect(InboxFolders.contains(deep, filter: ""))
        #expect(InboxFolders.contains(deep, filter: "work") && InboxFolders.contains(deep, filter: "work/alerts"))
        #expect(InboxFolders.contains(deep, filter: "work/alerts/engine"))
        #expect(!InboxFolders.contains(near, filter: "work/alerts")) // a sibling with the same start is not under it
        #expect(InboxFolders.contains(none, filter: InboxFolders.unfiled) && !InboxFolders.contains(deep, filter: InboxFolders.unfiled))
        #expect(!InboxFolders.contains(none, filter: "work"))
    }

    @Test func theBarCountsTheFoldersUnderTheOnePickedAndTheUnfiled() throws {
        let items = [
            try Self.item("1", folder: "work/alerts/engine"),
            try Self.item("2", folder: "work/alerts", done: true),
            try Self.item("3", folder: "work/builds"),
            try Self.item("4", folder: "Home"),
            try Self.item("5"),
            try Self.item("6", done: true),
        ].map(InboxFolders.Filed.init)
        #expect(InboxFolders.children(items, of: "") == [
            .init(path: "Home", name: "Home", count: 1, open: 1),
            .init(path: "work", name: "work", count: 3, open: 2),
        ])
        #expect(InboxFolders.children(items, of: "work") == [
            .init(path: "work/alerts", name: "alerts", count: 2, open: 1),
            .init(path: "work/builds", name: "builds", count: 1, open: 1),
        ])
        #expect(InboxFolders.children(items, of: "work/alerts") == [.init(path: "work/alerts/engine", name: "engine", count: 1, open: 1)])
        #expect(InboxFolders.children(items, of: "work/alerts/engine").isEmpty)
        let none = InboxFolders.unfiledCount(items)
        #expect(none.count == 2 && none.open == 1)
        #expect(InboxPage.folderChipTitle("work", open: 2) == "work 2")
        #expect(InboxPage.folderChipTitle("work", open: 0) == "work")
    }

    @Test func moveSuggestsTheFoldersInUseThatMatch() throws {
        let known = InboxFolders.known([try Self.item("1", folder: "work/alerts/engine"), try Self.item("2", folder: "home"), try Self.item("3")])
        #expect(known == ["home", "work", "work/alerts", "work/alerts/engine"])
        #expect(InboxFolders.suggestions(known, typed: "") == known)
        #expect(InboxFolders.suggestions(known, typed: "ALERTS") == ["work/alerts", "work/alerts/engine"])
        #expect(InboxFolders.suggestions(known, typed: "work/alerts") == ["work/alerts/engine"]) // not the one typed
    }

    // MARK: The list

    /// Off (the default), the list is as it was: every item listed whatever folder it has, nothing collected for a bar.
    @Test func withFoldersOffTheListIsUnchanged() throws {
        let items = [try Self.item("1", folder: "work"), try Self.item("2"), try Self.item("3", folder: "home", done: true)]
        let off = InboxProjection(items: items, filter: .init())
        #expect(off.openItems.map(\.id) == ["1", "2"] && off.doneItems.map(\.id) == ["3"])
        #expect(off.filed.isEmpty)
        // a folder still stored from before, with folders off, is never passed (the page sends "")
        let page = InboxProjection(items: items, filter: .init(folder: "", folders: false))
        #expect(page.openItems == off.openItems && page.doneItems == off.doneItems)
    }

    @Test func withFoldersOnTheListFiltersByFolderAndCountsBesideIt() throws {
        let items = [
            try Self.item("1", folder: "work/alerts"),
            try Self.item("2", folder: "work"),
            try Self.item("3"),
            try Self.item("4", folder: "home", done: true),
        ]
        let work = InboxProjection(items: items, filter: .init(folder: "work", folders: true))
        #expect(work.openItems.map(\.id) == ["1", "2"])
        // the bar counts what the other filters leave, every folder
        #expect(work.filed.count == 4)
        let unfiled = InboxProjection(items: items, filter: .init(folder: InboxFolders.unfiled, folders: true))
        #expect(unfiled.openItems.map(\.id) == ["3"] && unfiled.doneItems.isEmpty)
        // the other filters still apply, to the bar's counts too
        let searched = InboxProjection(items: items, filter: .init(query: "示例 1", folder: "work", folders: true))
        #expect(searched.openItems.map(\.id) == ["1"] && searched.filed == [InboxFolders.Filed(folder: "work/alerts", isOpen: true)])
    }

    // MARK: Move to Folder…

    private final class Inbox {
        var items: [InboxItem]
        var toasts: [Toast] = []
        var failures = 0
        /// What the list showed at each change.
        var shown: [[InboxItem]] = []
        init(_ items: [InboxItem]) { self.items = items }
    }

    private func deps(_ hub: FakeHub, _ inbox: Inbox) -> InboxFolderMoveModel.Deps {
        .init(api: { _ in hub.client }, items: { inbox.items }, show: { changed in
            let byKey = Dictionary(changed.map { ($0.key, $0) }, uniquingKeysWith: { a, _ in a })
            inbox.items = inbox.items.map { byKey[$0.key] ?? $0 }
            inbox.shown.append(inbox.items)
        }, toast: { inbox.toasts.append($0) }, fail: { _ in inbox.failures += 1 })
    }

    @Test func aMoveFilesTheItemByHandAtOnceThenTakesTheHubsCopy() async throws {
        let hub = FakeHub()
        var body: [String: String?] = [:]
        hub.on("PUT", "/api/v1/inbox/m1/folder") { req in
            body = try JSONDecoder().decode([String: String?].self, from: req.body ?? Data())
            return Data(#"{"id":"m1","source":"mail","kind":"mail","account":"a","account_name":"me@example.com","title":"t","preview":"","count":1,"folder":"work/alerts","folder_by":"user"}"#.utf8)
        }
        let it = try Self.item("m1", folder: "inbox", by: "model", manager: "mgr-a")
        var listed = it
        listed.version = "v1"
        let inbox = Inbox([listed, try Self.item("m2")])
        let m = InboxFolderMoveModel(item: listed, deps: deps(hub, inbox))
        #expect(m.text == "inbox" && m.known == ["inbox"])
        m.text = " work / alerts "
        #expect(m.path == "work/alerts" && m.canMove)
        #expect(await m.move(m.path))
        #expect(body == ["folder": "work/alerts"])
        // shown at once as filed by hand, before the hub answered
        #expect(inbox.shown.first?.first?.folder == "work/alerts" && inbox.shown.first?.first?.folderBy == "user")
        // the hub's copy keeps the list's stamp and version
        let now = try #require(inbox.items.first)
        #expect(now.folder == "work/alerts" && now.folderBy == "user" && now.manager == "mgr-a" && now.version == "v1")
        #expect(inbox.items[1].folder == nil)
        #expect(inbox.toasts.count == 1 && inbox.failures == 0)
    }

    @Test func noFolderAndLetAIDecideSendEmptyAndNull() async throws {
        let hub = FakeHub()
        var bodies: [Data] = []
        hub.on("PUT", "/api/v1/inbox/m1/folder") { req in
            bodies.append(req.body ?? Data())
            return Data(#"{"id":"m1","source":"mail","kind":"mail","account":"a","account_name":"me@example.com","title":"t","preview":"","count":1}"#.utf8)
        }
        let it = try Self.item("m1", folder: "work", by: "model")
        let inbox = Inbox([it])
        let m = InboxFolderMoveModel(item: it, deps: deps(hub, inbox))
        #expect(m.canAuto && m.canClear)
        #expect(await m.move(""))
        #expect(inbox.shown.first?.first?.folder == nil && inbox.shown.first?.first?.folderBy == "user")
        let back = InboxFolderMoveModel(item: inbox.items[0], deps: deps(hub, inbox))
        #expect(await back.move(nil))
        #expect(inbox.shown.last?.first?.folderBy == nil)
        let sent = try bodies.map { try JSONSerialization.jsonObject(with: $0) as? [String: Any] }
        #expect(sent.count == 2 && sent[0]?["folder"] as? String == "" && sent[1]?["folder"] is NSNull)
    }

    @Test func aFailedMovePutsTheFolderBack() async throws {
        let hub = FakeHub()
        hub.on("PUT", "/api/v1/inbox/m1/folder") { _ in throw APIError(status: 500, message: "boom") }
        let it = try Self.item("m1", folder: "work", by: "model")
        let inbox = Inbox([it])
        let m = InboxFolderMoveModel(item: it, deps: deps(hub, inbox))
        m.text = "home"
        #expect(!(await m.move(m.path)))
        #expect(inbox.shown.first?.first?.folder == "home") // shown at once
        #expect(inbox.items == [it]) // then put back
        #expect(inbox.failures == 1 && inbox.toasts.isEmpty && !m.busy)
    }

    @Test func theChoicesOfferWhatChangesSomething() throws {
        let hub = FakeHub()
        let unfiled = try Self.item("1")
        let m = InboxFolderMoveModel(item: unfiled, deps: deps(hub, Inbox([unfiled])))
        #expect(!m.canMove && !m.canAuto && m.canClear) // nothing typed; nobody filed it yet
        m.text = " / ./ "
        #expect(m.path.isEmpty && !m.canMove) // a path that normalizes to nothing
        let takenOut = try Self.item("2", by: "user")
        let t = InboxFolderMoveModel(item: takenOut, deps: deps(hub, Inbox([takenOut])))
        #expect(!t.canClear && t.canAuto)
    }

    // MARK: Settings › Inbox Folders

    private struct Boom: Error {}

    @Test func settingsTurnFilingOnAndSaveTheRules() async {
        var saved: [InboxFolderSettings] = []
        var told: [InboxFolderSettings] = []
        let m = InboxFolderSettingsModel(deps: .init(load: { InboxFolderSettings(rules: "旧规则") },
                                                     save: { saved.append($0); return $0 },
                                                     saved: { told.append($0) }))
        #expect(!m.canChange)
        await m.load()
        #expect(m.settings == InboxFolderSettings(rules: "旧规则") && m.rules == "旧规则" && !m.rulesChanged)
        await m.setEnabled(true)
        #expect(saved == [InboxFolderSettings(enabled: true, rules: "旧规则")] && m.settings?.enabled == true)
        m.rules = "新规则"
        #expect(m.rulesChanged)
        await m.saveRules()
        #expect(saved.last == InboxFolderSettings(enabled: true, rules: "新规则") && !m.rulesChanged)
        #expect(told == saved && m.error == nil)
        await m.setEnabled(true) // no change, nothing sent
        #expect(saved.count == 2)
    }

    @Test func aFailedSavePutsTheSwitchBack() async {
        let m = InboxFolderSettingsModel(deps: .init(load: { InboxFolderSettings() }, save: { _ in throw Boom() }, saved: { _ in }))
        await m.load()
        await m.setEnabled(true)
        #expect(m.settings?.enabled == false && m.error != nil && !m.saving)
    }

    @Test func anOlderHubSaysItHasNoFolders() async {
        let m = InboxFolderSettingsModel(deps: .init(load: { throw APIError(status: 404, message: "") }, save: { $0 }, saved: { _ in }))
        await m.load()
        #expect(m.unsupported && m.settings == nil && !m.canChange)
    }

    @Test func theClientReadsAndWritesTheHubsFolderSettings() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/inbox/folders", json: #"{"enabled":false,"rules":"","folders":[]}"#)
        var put: [String: Any]?
        hub.on("PUT", "/api/v1/inbox/folders") { req in
            put = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any]
            return Data(#"{"enabled":true,"rules":"r","folders":[]}"#.utf8)
        }
        #expect(try await hub.client.inboxFolders().settings == InboxFolderSettings())
        let kept = try await hub.client.setInboxFolders(InboxFolderSettings(enabled: true, rules: "r"))
        #expect(kept.settings == InboxFolderSettings(enabled: true, rules: "r"))
        #expect(put?["enabled"] as? Bool == true && put?["rules"] as? String == "r")
    }
}
