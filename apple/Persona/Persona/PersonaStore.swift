import Foundation
import ImageIO
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

/// The personas on this phone (docs/apple/persona.md「On the phone」): which one is chosen, what the hub offers, and the packages
/// downloaded, one directory per rev under Application Support/Personas/<id>/<rev>. A package is used only once all of
/// it is there (it is downloaded beside and moved in whole), and the chosen one shows from disk at launch, before
/// the hub answers or without it.
@MainActor @Observable
final class PersonaStore {
    /// Mochi first, then valid packages the hub has listed (kept across launches).
    private(set) var offered: [Persona] = Persona.builtins
    /// The chosen one as it can be shown now: the built-in until its package is on the phone.
    private(set) var current: Persona = .builtin {
        didSet { if current != oldValue { onChange?() } }
    }
    /// Ids being downloaded.
    private(set) var downloading: Set<String> = []
    /// Why the last list or download failed (Settings › Avatar shows it).
    private(set) var error: String?
    /// Bumped when pictures land on disk (a package, a thumbnail): the views that asked for one before ask again.
    private(set) var pictures = 0

    /// `current` changed (AppModel lays the colors over the style).
    @ObservationIgnored var onChange: (() -> Void)?
    @ObservationIgnored let root: URL
    @ObservationIgnored private let defaults: UserDefaults
    /// The refresh under way: another caller waits for it instead of starting a second one (connecting to the hub
    /// and Settings opening come together; a second refresh used to clean away the package the first was downloading).
    @ObservationIgnored private var refreshing: Task<Void, Never>?
    @ObservationIgnored private let frameCache = NSCache<NSString, PersonaFrames>()
    @ObservationIgnored private let imageCache = NSCache<NSString, UIImage>()

    static let chosenKey = NativeStateMigration.personaKey
    static var defaultRoot: URL {
        URL.applicationSupportDirectory.appending(path: "Personas", directoryHint: .isDirectory)
    }

    /// The user's choice; kept when its package is gone from the hub, so it comes back with it.
    var chosenID: String { PersonaSelection.resolve(NativeStateMigration.readPersonaID(from: defaults)) }

    /// The hub's list came this launch.
    @ObservationIgnored private var listed = false

    /// The home screen icon the choice puts on (`Persona.appIcon`): a package's is known from the list before the
    /// package is on the phone, so the icon does not go to the default's and back while it downloads. Nil while the
    /// chosen one is not listed yet; once the hub has answered without it, the built-in's on screen.
    var appIcon: AppIconChoice? {
        if let chosen = offered.first(where: { $0.id == chosenID }) { return chosen.appIcon }
        return listed ? current.appIcon : nil
    }

    init(root: URL = PersonaStore.defaultRoot, defaults: UserDefaults = .standard) {
        self.root = root
        self.defaults = defaults
        _ = NativeStateMigration.defaults(defaults)
        frameCache.countLimit = 6
        if let data = try? Data(contentsOf: indexURL), let list = try? JSONDecoder().decode([PersonaInfo].self, from: data) {
            offered = Persona.builtins + list.filter(Self.supportedPackage).map(Persona.init(info:))
        }
        #if DEBUG
        // `-openPersona <id>`: start with that one (on the phone already, or downloaded when the list comes).
        if let id = defaults.string(forKey: "openPersona"), !id.isEmpty { NativeStateMigration.writePersonaID(id, defaults: defaults) }
        #endif
        if let p = offered.first(where: { $0.id == chosenID }), isOnPhone(p) { current = p }
    }

    private static func supportedPackage(_ info: PersonaInfo) -> Bool {
        info.manifest.id == info.id && info.manifest.problem == nil
    }

    private var indexURL: URL { root.appending(path: "index.json") }

    func directory(_ p: Persona) -> URL { root.appending(path: p.id).appending(path: p.rev, directoryHint: .isDirectory) }

    func isOnPhone(_ p: Persona) -> Bool {
        p.isBuiltin || FileManager.default.fileExists(atPath: directory(p).path(percentEncoded: false))
    }

    // MARK: The hub

    /// What the hub offers now; the chosen one is downloaded when its rev is new, and revs nobody uses are removed.
    func refresh(_ api: APIClient?) async {
        guard let api, !api.config.isDemo else { return }
        if let running = refreshing { return await running.value }
        let task = Task { await load(api) }
        refreshing = task
        await task.value
        refreshing = nil
    }

    private func load(_ api: APIClient) async {
        let list: [PersonaInfo]
        do {
            list = try await api.personas().filter(Self.supportedPackage)
        } catch let e as APIError where e.status == 404 {
            error = String(localized: "This hub can't host avatars yet. Update the hub and check again.")
            return
        } catch {
            if !error.isCancellation { self.error = String(localized: "Couldn't load the avatar list: \(error.localizedDescription)") }
            return
        }
        self.error = nil
        listed = true
        offered = Persona.builtins + list.filter(Self.supportedPackage).map(Persona.init(info:))
        try? FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try? JSONEncoder().encode(list).write(to: indexURL, options: .atomic)
        if let chosen = offered.first(where: { $0.id == chosenID }) {
            if !isOnPhone(chosen) { await download(chosen, api) }
            if isOnPhone(chosen) { current = chosen }
        } else {
            current = .builtin
        }
        // The others' faces for Settings' cards, one small picture each (the package itself comes when chosen).
        for p in offered where !isOnPhone(p) && !hasThumbnail(p) {
            guard let file = p.manifest?.avatarFile(points: Self.thumbnailPoints, scale: 3),
                  let data = try? await api.personaFile(p.id, file) else { continue }
            try? FileManager.default.createDirectory(at: thumbnails, withIntermediateDirectories: true)
            try? data.write(to: thumbnail(p), options: .atomic)
            pictures += 1
        }
        prune(keeping: Set(offered.map { "\($0.id)/\($0.rev)" }))
    }

    /// Settings' cards show faces 64 pt wide.
    static let thumbnailPoints: CGFloat = 64
    private var thumbnails: URL { root.appending(path: ".thumbnails", directoryHint: .isDirectory) }
    private func thumbnail(_ p: Persona) -> URL { thumbnails.appending(path: "\(p.id)-\(p.rev).png") }
    private func hasThumbnail(_ p: Persona) -> Bool { FileManager.default.fileExists(atPath: thumbnail(p).path(percentEncoded: false)) }

    /// Picks a persona; a package is downloaded first (on failure, or with no hub to download from, the choice stays
    /// as it was and `error` says why).
    func choose(_ p: Persona, api: APIClient?) async {
        guard offered.contains(p) else { return }
        if !isOnPhone(p) {
            guard let api else {
                error = String(localized: "“\(p.name)” isn’t on this phone yet. Connect to the hub, then choose it.")
                return
            }
            await download(p, api)
            guard isOnPhone(p) else { return }
        }
        error = nil
        NativeStateMigration.writePersonaID(p.id, defaults: defaults)
        current = p
        WatchSync.shared.push(persona: p.id)
    }

    /// Fetches the package's files beside its place and moves them in whole.
    func download(_ p: Persona, _ api: APIClient) async {
        guard let m = p.manifest, !downloading.contains(p.id) else { return }
        downloading.insert(p.id)
        defer { downloading.remove(p.id) }
        let fm = FileManager.default
        let part = root.appending(path: p.id).appending(path: ".\(p.rev).part", directoryHint: .isDirectory)
        try? fm.removeItem(at: part)
        do {
            for file in m.files {
                let data = try await api.personaFile(p.id, file)
                let dest = part.appending(path: file)
                try fm.createDirectory(at: dest.deletingLastPathComponent(), withIntermediateDirectories: true)
                try data.write(to: dest, options: .atomic)
            }
            // Every file there before it is used (nothing else may touch the .part, but a package missing a face
            // would show the built-in's for good).
            if let lost = m.files.first(where: { !fm.fileExists(atPath: part.appending(path: $0).path(percentEncoded: false)) }) {
                throw APIError(status: 0, message: String(localized: "Missing \(lost)"))
            }
            let final = directory(p)
            try? fm.removeItem(at: final)
            try fm.moveItem(at: part, to: final)
            pictures += 1
            error = nil
        } catch {
            try? fm.removeItem(at: part)
            if !error.isCancellation { self.error = String(localized: "Couldn’t download “\(p.name)”: \(error.localizedDescription)") }
        }
    }

    /// Removes the revs that are neither offered nor on screen; never while a download runs, never a download's .part.
    private func prune(keeping keep: Set<String>) {
        let fm = FileManager.default
        guard downloading.isEmpty, let ids = try? fm.contentsOfDirectory(atPath: root.path(percentEncoded: false)) else { return }
        for t in (try? fm.contentsOfDirectory(atPath: thumbnails.path(percentEncoded: false))) ?? []
        where !keep.contains(where: { t == $0.replacingOccurrences(of: "/", with: "-") + ".png" }) {
            try? fm.removeItem(at: thumbnails.appending(path: t))
        }
        for id in ids where !id.hasPrefix(".") && id != "index.json" {
            let dir = root.appending(path: id)
            for rev in (try? fm.contentsOfDirectory(atPath: dir.path(percentEncoded: false))) ?? [] where !rev.hasPrefix(".") {
                let key = "\(id)/\(rev)"
                if !keep.contains(key), key != "\(current.id)/\(current.rev)" { try? fm.removeItem(at: dir.appending(path: rev)) }
            }
            if ((try? fm.contentsOfDirectory(atPath: dir.path(percentEncoded: false))) ?? []).isEmpty { try? fm.removeItem(at: dir) }
        }
    }

    // MARK: Pictures

    /// The face `points` wide, from the package, or its thumbnail while the package is not on the phone (nil for the
    /// built-in, or when there is neither).
    func avatar(_ p: Persona, points: CGFloat, scale: CGFloat) -> UIImage? {
        _ = pictures
        guard let file = p.manifest?.avatarFile(points: points, scale: scale) else { return nil }
        let url = isOnPhone(p) ? directory(p).appending(path: file) : thumbnail(p)
        let key = url.path(percentEncoded: false) as NSString
        if let img = imageCache.object(forKey: key) { return img }
        guard let raw = UIImage(contentsOfFile: url.path(percentEncoded: false)) else { return nil }
        let img = raw.preparingForDisplay() ?? raw
        imageCache.setObject(img, forKey: key)
        return img
    }

    /// The state's frames, decoded once (a few dozen 144-pixel images) and kept for the states last shown.
    func frames(_ p: Persona, _ state: PersonaState) -> PersonaFrames? {
        _ = pictures
        guard let spec = p.manifest?.dotState(state), isOnPhone(p) else { return nil }
        let url = directory(p).appending(path: spec.file)
        let key = url.path(percentEncoded: false) as NSString
        if let f = frameCache.object(forKey: key) { return f }
        guard let f = PersonaFrames(url: url, fps: spec.fps) else { return nil }
        frameCache.setObject(f, forKey: key)
        return f
    }
}

/// A dot state's frames and the time one loop takes.
final class PersonaFrames: Sendable {
    let images: [UIImage]
    let duration: TimeInterval

    /// From an APNG (ImageIO composes each frame whole) or a still PNG; `fps` when the file has no frame delays.
    nonisolated init?(url: URL, fps: Double?) {
        guard let src = CGImageSourceCreateWithURL(url as CFURL, nil) else { return nil }
        let opts = [kCGImageSourceShouldCacheImmediately: true] as CFDictionary
        var images: [UIImage] = []
        var total = 0.0
        for i in 0..<CGImageSourceGetCount(src) {
            guard let cg = CGImageSourceCreateImageAtIndex(src, i, opts) else { continue }
            #if os(iOS) || os(visionOS)
            images.append(UIImage(cgImage: cg))
            #else
            images.append(NSImage(cgImage: cg, size: NSSize(width: cg.width, height: cg.height))) // AppKit's own init: callable off the main actor
            #endif
            let props = CGImageSourceCopyPropertiesAtIndex(src, i, nil) as? [CFString: Any]
            let png = props?[kCGImagePropertyPNGDictionary] as? [CFString: Any]
            let delay = (png?[kCGImagePropertyAPNGUnclampedDelayTime] as? Double) ?? (png?[kCGImagePropertyAPNGDelayTime] as? Double) ?? 0
            total += delay > 0.005 ? delay : 1 / max(1, fps ?? 8)
        }
        guard !images.isEmpty else { return nil }
        self.images = images
        duration = total
    }
}

nonisolated extension APIClient {
    /// GET /api/v1/personas: the packages on the hub that check out (docs/protocol/protocol.md「REST (App → hub)」).
    func personas() async throws -> [PersonaInfo] {
        struct List: Decodable { var personas: [PersonaInfo] }
        return try JSONDecoder().decode(List.self, from: try await request("GET", "/api/v1/personas")).personas
    }

    /// A file of a package (`PersonaManifest.files`), past the system's URL cache: the store keeps it on disk.
    func personaFile(_ id: String, _ file: String) async throws -> Data {
        let path = "/api/v1/personas/\(id)/\(file)"
        if transport != nil || config.isDemo { return try await request("GET", path) }
        var req = URLRequest(url: url(path), timeoutInterval: 60)
        req.setValue("Bearer \(config.token)", forHTTPHeaderField: "Authorization")
        req.setValue(LanguageStore.shared.launched, forHTTPHeaderField: "Accept-Language")
        let (data, resp) = try await Self.uncachedSession.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            throw APIError(status: status, message: APIError.message(in: data) ?? HTTPURLResponse.localizedString(forStatusCode: status))
        }
        return data
    }
}
