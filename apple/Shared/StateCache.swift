import Foundation

/// The app's last known state on disk (Caches/state/<name>.json): every page paints from it at once — offline, on a
/// cold start, when switching tabs — and the network only refreshes it in the background.
///
/// `folder`: another place for the files than Caches/state, for a test's model to keep its own (`AppModel.stateFolder`).
nonisolated enum StateCache {
    private static func url(_ name: String, in folder: URL? = nil) -> URL? {
        guard let dir = folder ?? FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first?.appending(path: "state")
        else { return nil }
        let safe = String(name.map { $0.isLetter || $0.isNumber || $0 == "-" || $0 == "_" ? $0 : "_" })
        return dir.appending(path: safe + ".json")
    }

    static func load<T: Decodable>(_ name: String, as type: T.Type = T.self, in folder: URL? = nil) -> T? {
        guard let url = url(name, in: folder), let data = try? Data(contentsOf: url) else { return nil }
        return try? JSON.decoder.decode(T.self, from: data)
    }

    static func remove(_ name: String, in folder: URL? = nil) {
        Task.detached(priority: .utility) {
            guard let url = url(name, in: folder) else { return }
            try? FileManager.default.removeItem(at: url)
        }
    }

    /// Removes every entry whose name starts with `prefix`, before returning (a save made after it stays).
    static func removeAll(prefix: String) {
        guard let dir = url(prefix)?.deletingLastPathComponent(), let leaf = url(prefix)?.deletingPathExtension().lastPathComponent,
              let names = try? FileManager.default.contentsOfDirectory(atPath: dir.path) else { return }
        for name in names where name.hasPrefix(leaf) { try? FileManager.default.removeItem(at: dir.appending(path: name)) }
    }

    static func save<T: Encodable & Sendable>(_ name: String, _ value: T, in folder: URL? = nil) {

        Task.detached(priority: .utility) {
            guard let url = url(name, in: folder), let data = try? JSON.encoder.encode(value) else { return }
            try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try? data.write(to: url, options: .atomic)
        }
    }
}

/// Coalesces bursts of changes into one write a moment later.
@MainActor
final class Debouncer {
    private var task: Task<Void, Never>?
    private let delay: Duration
    init(_ delay: Duration = .seconds(1)) { self.delay = delay }

    func run(_ work: @escaping @MainActor () -> Void) {
        task?.cancel()
        task = Task { [delay] in
            try? await Task.sleep(for: delay)
            guard !Task.isCancelled else { return }
            work()
        }
    }
}
