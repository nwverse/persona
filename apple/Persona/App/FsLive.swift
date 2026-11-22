import Foundation

// Live Files / Git (#2439, #3089; docs/protocol/protocol.md「File auto-refresh」): the panes tell the hub which folders they show, the hub
// watches them on the computer and pushes `fs_changed`; a pane refreshes what changed after a short debounce (200 ms,
// as on the web, web/src/lib/fs-live.ts). One socket message per session folder replaces the whole set, so the Files
// and the Git pane of the same folder (or two windows on the Mac) are merged into it. A pane that is not in front
// stops its watch (`.fsLive`), and the hub is told when nothing is left; a reconnect tells the new socket again and
// refreshes every pane once, since changes were missed. An older hub or node ignores `fs_watch`: the panes just stay
// as they were, refreshed by hand.

/// What a pane is told: the folders (as it named them) that changed, and whether its folder's Git status did.
nonisolated struct FsChange: Equatable, Sendable {
    var dirs: [String]
    var git: Bool
}

@MainActor
final class FsLive {
    /// One session folder on one machine of one group of hubs: the hub keeps one set per (machine, cwd) per socket.
    struct Scope: Hashable {
        var group: String
        var machineID: String
        var cwd: String
    }

    /// A pane's watch: change what it shows, or stop (the hub is told when nothing is left for the folder).
    final class Handle {
        fileprivate weak var live: FsLive?
        fileprivate let id: Int
        fileprivate init(live: FsLive, id: Int) { self.live = live; self.id = id }

        func update(paths: [String], git: Bool) { live?.update(id, paths: paths, git: git) }
        func stop() { live?.stop(id) }
    }

    static let debounce: Duration = .milliseconds(200)

    private struct Watcher {
        var scope: Scope
        var paths: [String]
        var git: Bool
        var onChange: (FsChange) -> Void
        var pending: Set<String> = []
        var pendingGit = false
        var cancel: (() -> Void)?
    }

    private let send: (String, [String: Any]) -> Void
    private let isOpen: (String) -> Bool
    private let schedule: @MainActor (Duration, @escaping @MainActor () -> Void) -> () -> Void
    private var watchers: [Int: Watcher] = [:]
    private var nextID = 0
    /// What the hub was last told per folder ([paths, git]); absent: nothing.
    private var sent: [Scope: [String]] = [:]

    /// send: the message to the group's socket; isOpen: whether that socket is up (the hub is told on its hello
    /// otherwise); schedule: runs the action after the delay, and returns what cancels it.
    init(send: @escaping (String, [String: Any]) -> Void, isOpen: @escaping (String) -> Bool,
         schedule: @escaping @MainActor (Duration, @escaping @MainActor () -> Void) -> () -> Void = FsLive.sleepThen) {
        self.send = send
        self.isOpen = isOpen
        self.schedule = schedule
    }

    static func sleepThen(_ delay: Duration, _ action: @escaping @MainActor () -> Void) -> () -> Void {
        let task = Task { @MainActor in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            action()
        }
        return { task.cancel() }
    }

    /// Starts watching the folders (absolute paths) and, with git, the folder's Git status.
    func watch(_ scope: Scope, paths: [String], git: Bool, onChange: @escaping (FsChange) -> Void) -> Handle {
        nextID += 1
        watchers[nextID] = Watcher(scope: scope, paths: paths, git: git, onChange: onChange)
        sync(scope)
        return Handle(live: self, id: nextID)
    }

    /// An `fs_changed` from a group's socket.
    func received(group: String, machineID: String, cwd: String, dirs: [String], git: Bool) {
        for (id, w) in watchers where w.scope == Scope(group: group, machineID: machineID, cwd: cwd) {
            let mine = dirs.filter { w.paths.contains($0) }
            let gitHit = git && w.git
            if !mine.isEmpty || gitHit { queue(id, dirs: mine, git: gitHit) }
        }
    }

    /// A group's socket connected (again): a new client for the hub, which knows nothing of what was open, and
    /// changes were missed while it was down.
    func reconnected(group: String) {
        for scope in sent.keys where scope.group == group { sent[scope] = nil }
        for scope in Set(watchers.values.map(\.scope)) where scope.group == group { sync(scope) }
        for (id, w) in watchers where w.scope.group == group { queue(id, dirs: w.paths, git: w.git) }
    }

    /// The folders being watched now, for tests and the runtime CLI.
    var watching: [Scope: [String]] { sent }

    private func update(_ id: Int, paths: [String], git: Bool) {
        guard var w = watchers[id], w.paths != paths || w.git != git else { return }
        w.paths = paths
        w.git = git
        // Changes queued for a folder no longer shown are dropped with it.
        w.pending.formIntersection(paths)
        w.pendingGit = w.pendingGit && git
        watchers[id] = w
        sync(w.scope)
    }

    private func stop(_ id: Int) {
        guard let w = watchers.removeValue(forKey: id) else { return }
        w.cancel?()
        sync(w.scope)
    }

    /// Brings the hub's set for one session folder in line with the watchers.
    private func sync(_ scope: Scope) {
        var paths = Set<String>(), git = false
        for w in watchers.values where w.scope == scope {
            paths.formUnion(w.paths)
            git = git || w.git
        }
        let sorted = paths.sorted()
        let want: [String]? = sorted.isEmpty && !git ? nil : sorted + [git ? "git" : ""]
        guard sent[scope] != want, isOpen(scope.group) else { return } // not open: told on the hello
        send(scope.group, ["type": "fs_watch", "machine_id": scope.machineID, "cwd": scope.cwd, "paths": sorted, "git": git])
        sent[scope] = want
    }

    private func queue(_ id: Int, dirs: [String], git: Bool) {
        guard var w = watchers[id] else { return }
        w.pending.formUnion(dirs)
        w.pendingGit = w.pendingGit || git
        w.cancel?()
        w.cancel = schedule(Self.debounce) { [weak self] in self?.flush(id) }
        watchers[id] = w
    }

    private func flush(_ id: Int) {
        guard var w = watchers[id] else { return }
        let change = FsChange(dirs: w.pending.sorted(), git: w.pendingGit)
        w.pending = []
        w.pendingGit = false
        w.cancel = nil
        watchers[id] = w
        if !change.dirs.isEmpty || change.git { w.onChange(change) }
    }
}
