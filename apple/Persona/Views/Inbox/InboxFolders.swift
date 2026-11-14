import Foundation
import Observation

/// Inbox folders (docs/assistant/inbox.md "Folders", #2463), the web's `lib/inbox-folders.ts`: each item can be filed
/// under a "/"-separated path ("work/alerts/engine"), every segment a folder level. The hub's triage model files them
/// when the user turned it on, and the user can move one by hand. The tree is built here from the items' paths.
nonisolated enum InboxFolders {
    /// The folder filter's value for items in no folder (a real path never starts with "/").
    static let unfiled = "/"

    private static let maxDepth = 6
    private static let maxSegment = 40
    private static let maxBytes = 200

    /// A path as the hub keeps it (`assistant.normalizeFolder`): segments trimmed, inner whitespace collapsed, control
    /// characters, empty, "." and ".." segments dropped, each at most 40 characters, at most 6 levels and 200 bytes.
    /// "" is no folder.
    static func normalize(_ s: String) -> String {
        var out: [String] = []
        var bytes = 0
        for raw in s.replacingOccurrences(of: "\\", with: "/").split(separator: "/", omittingEmptySubsequences: false) {
            let cleaned = String(String.UnicodeScalarView(raw.unicodeScalars.map {
                $0.value < 0x20 || $0.value == 0x7f ? " " : $0
            }))
            var seg = cleaned.split(whereSeparator: \.isWhitespace).joined(separator: " ")
            if seg.isEmpty || seg == "." || seg == ".." { continue }
            if seg.unicodeScalars.count > maxSegment { // by code points, as the hub counts runes
                seg = String(String.UnicodeScalarView(seg.unicodeScalars.prefix(maxSegment))).trimmingCharacters(in: .whitespaces)
            }
            if out.count == maxDepth { break }
            let added = seg.utf8.count + (out.isEmpty ? 0 : 1)
            if bytes + added > maxBytes { break }
            bytes += added
            out.append(seg)
        }
        return out.joined(separator: "/")
    }

    /// Whether an item is in the folder filter: in the folder or under it, or in none for `unfiled`; "" is every item.
    static func contains(_ item: InboxItem, filter folder: String) -> Bool {
        guard !folder.isEmpty else { return true }
        let path = item.folder ?? ""
        if folder == unfiled { return path.isEmpty }
        return path == folder || path.hasPrefix(folder + "/")
    }

    /// A folder's name: its last segment.
    static func name(_ path: String) -> String {
        path.split(separator: "/").last.map(String.init) ?? path
    }

    /// The crumbs down to a folder: each level's name and path ("work", "work/alerts"…); none for all or unfiled.
    static func crumbs(_ path: String) -> [Crumb] {
        guard !path.isEmpty, path != unfiled else { return [] }
        let segs = path.split(separator: "/").map(String.init)
        return segs.indices.map { Crumb(name: segs[$0], path: segs[...$0].joined(separator: "/")) }
    }

    nonisolated struct Crumb: Equatable, Hashable, Sendable {
        var name: String
        var path: String
    }

    /// A folder one level under another, with how many items are in it and under it, and how many of those are open.
    nonisolated struct Node: Equatable, Identifiable, Sendable {
        var path: String
        var name: String
        var count: Int
        var open: Int
        var id: String { path }
    }

    /// What the folder bar counts of one item: its folder and whether it is still open.
    nonisolated struct Filed: Equatable, Sendable {
        var folder: String?
        var isOpen: Bool

        init(folder: String?, isOpen: Bool) {
            self.folder = folder
            self.isOpen = isOpen
        }

        init(_ item: InboxItem) {
            folder = item.folder
            isOpen = !item.isDone
        }
    }

    /// The folders right under `parent` ("" the top), each with the items in and under it, by name.
    static func children(_ items: [Filed], of parent: String) -> [Node] {
        let prefix = parent.isEmpty ? "" : parent + "/"
        var by: [String: Node] = [:]
        for it in items {
            let path = it.folder ?? ""
            guard !path.isEmpty, path.hasPrefix(prefix), path != parent else { continue }
            let rest = path.dropFirst(prefix.count)
            let name = String(rest.split(separator: "/").first ?? rest)
            let at = prefix + name
            var n = by[at] ?? Node(path: at, name: name, count: 0, open: 0)
            n.count += 1
            if it.isOpen { n.open += 1 }
            by[at] = n
        }
        return by.values.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }

    /// How many of the items are in no folder, and how many of those are open.
    static func unfiledCount(_ items: [Filed]) -> (count: Int, open: Int) {
        var count = 0, open = 0
        for it in items where (it.folder ?? "").isEmpty {
            count += 1
            if it.isOpen { open += 1 }
        }
        return (count, open)
    }

    /// Every folder the items name, with the levels above each, in order: what "Move to Folder…" suggests.
    static func known(_ items: [InboxItem]) -> [String] {
        var all = Set<String>()
        for it in items { for c in crumbs(it.folder ?? "") { all.insert(c.path) } }
        return all.sorted { $0.localizedStandardCompare($1) == .orderedAscending }
    }

    /// The folders offered under the path field: those in use that contain what is typed (all when nothing is), not
    /// the one typed itself, at most 8.
    static func suggestions(_ known: [String], typed path: String) -> [String] {
        Array(known.filter { $0 != path && (path.isEmpty || $0.localizedCaseInsensitiveContains(path)) }.prefix(8))
    }

    /// The item as it shows while a move is on its way: filed by hand (a path or no folder), or handed back to the model.
    static func moved(_ item: InboxItem, to folder: String?) -> InboxItem {
        var it = item
        it.folder = folder.flatMap { $0.isEmpty ? nil : $0 }
        it.folderBy = folder == nil ? nil : "user"
        return it
    }
}

/// "Move to Folder…" (a row's menu, the item's ⋯): the path field with the folders in use to pick from, No Folder and
/// Let AI Decide. Shown at once; a failure puts the item back and says why.
@MainActor @Observable
final class InboxFolderMoveModel {
    struct Deps {
        /// The item's manager's client (nil: not connected).
        var api: (InboxItem) -> APIClient?
        var items: () -> [InboxItem]
        /// Shows items changed (optimistically, then as the hub answered, or put back), matched by `InboxItem.key`.
        var show: ([InboxItem]) -> Void
        var toast: (Toast) -> Void
        var fail: (Error) -> Void

        static var live: Deps {
            .init(api: { AppModel.shared.api?.manager(AppModel.shared.itemManager($0.manager)) },
                  items: { InboxStore.shared.items },
                  show: { InboxStore.shared.showRuleChange($0) },
                  toast: { AppModel.shared.toast = $0 },
                  fail: { AppModel.shared.show($0) })
        }
    }

    let item: InboxItem
    var text: String
    private(set) var busy = false
    @ObservationIgnored private let deps: Deps
    /// The folders in use, read once when the sheet opens.
    @ObservationIgnored let known: [String]

    init(item: InboxItem, deps: Deps = .live) {
        self.item = item
        self.deps = deps
        text = item.folder ?? ""
        known = InboxFolders.known(deps.items())
    }

    /// What Move files it under.
    var path: String { InboxFolders.normalize(text) }
    var suggestions: [String] { InboxFolders.suggestions(known, typed: path) }
    var canMove: Bool { !busy && !path.isEmpty }
    /// Let AI Decide: only for an item someone filed (the model or the user), as the web offers it.
    var canAuto: Bool { !busy && (item.folderBy != nil || item.folder != nil) }
    /// No Folder: not for an item the user already took out of every folder.
    var canClear: Bool { !busy && !(item.folderBy == "user" && (item.folder ?? "").isEmpty) }

    /// Files the item: a path by hand, "" in no folder, nil back to the model. Resolves to whether the hub took it.
    func move(_ folder: String?) async -> Bool {
        guard !busy, let api = deps.api(item) else { return false }
        let current = deps.items().first { $0.key == item.key } ?? item
        busy = true
        defer { busy = false }
        deps.show([InboxFolders.moved(current, to: folder)])
        do {
            var fresh = try await api.setInboxFolder(item.id, folder)
            // The answer carries neither the listing's version nor the manager the list stamped.
            if fresh.manager == nil { fresh.manager = current.manager }
            if fresh.version == nil { fresh.version = current.version }
            if fresh.key == item.key { deps.show([fresh]) }
            let words = switch folder {
            case nil: String(localized: "AI will file it")
            case let f? where f.isEmpty: String(localized: "Taken out of its folder")
            case let f?: String(localized: "Moved to \(f)")
            }
            deps.toast(Toast(words, style: .success))
            return true
        } catch {
            // back to the folder it had, on the copy now shown
            if let now = deps.items().first(where: { $0.key == item.key }) {
                var back = now
                back.folder = current.folder
                back.folderBy = current.folderBy
                deps.show([back])
            }
            deps.fail(error)
            return false
        }
    }
}

/// Settings › Inbox Folders (the web's "Folders…" dialog): automatic filing on or off and the user's folder rules,
/// kept on the hub (`PUT /api/v1/inbox/folders`), so the same on every device. Off until turned on here.
@MainActor @Observable
final class InboxFolderSettingsModel {
    struct Deps {
        var load: () async throws -> InboxFolderSettings
        var save: (InboxFolderSettings) async throws -> InboxFolderSettings
        /// The settings the hub answered with: the Inbox shows or hides its folders by them.
        var saved: (InboxFolderSettings) -> Void

        static var live: Deps {
            .init(load: {
                guard let api = InboxStore.shared.folderOwner else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
                let s = try await api.inboxFolders().settings
                InboxStore.shared.folderSettingsRead(s)
                return s
            }, save: { next in
                guard let api = InboxStore.shared.folderOwner else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
                return try await api.setInboxFolders(next).settings
            }, saved: { InboxStore.shared.folderSettingsRead($0) })
        }
    }

    /// As the hub has them; nil until read.
    private(set) var settings: InboxFolderSettings?
    /// The rules being written (saved with Save, not on every keystroke).
    var rules = ""
    private(set) var saving = false
    private(set) var error: String?
    /// The hub has no `/api/v1/inbox/folders` (404 / 405): it is older than folders.
    private(set) var unsupported = false
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    func load() async {
        do {
            let s = try await deps.load()
            settings = s
            rules = s.rules
            error = nil
            unsupported = false
        } catch let e as APIError where e.status == 404 || e.status == 405 {
            unsupported = true
        } catch {
            if settings == nil, !error.isCancellation {
                let why = error.shown ?? String(localized: "Can't reach the hub")
                self.error = String(localized: "Couldn't load: \(why)")
            }
        }
    }

    var canChange: Bool { settings != nil && !saving }
    /// The rules typed differ from the hub's.
    var rulesChanged: Bool { settings.map { $0.rules != rules } ?? false }

    /// Automatic filing on or off, saved at once with the rules as the hub has them; a failure puts it back.
    func setEnabled(_ on: Bool) async {
        guard let before = settings, canChange, before.enabled != on else { return }
        await save(InboxFolderSettings(enabled: on, rules: before.rules), before: before)
    }

    /// Saves the rules typed.
    func saveRules() async {
        guard let before = settings, canChange, rulesChanged else { return }
        await save(InboxFolderSettings(enabled: before.enabled, rules: rules), before: before)
    }

    private func save(_ next: InboxFolderSettings, before: InboxFolderSettings) async {
        settings = next
        saving = true
        error = nil
        defer { saving = false }
        do {
            let kept = try await deps.save(next)
            settings = kept
            if rules == next.rules { rules = kept.rules }
            deps.saved(kept)
        } catch {
            settings = before
            let why = error.shown ?? String(localized: "Can't reach the hub")
            self.error = String(localized: "Couldn't change it: \(why) [Messages Views 87cfe2]")
        }
    }
}
