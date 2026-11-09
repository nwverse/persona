#if DEBUG
import AppKit
import SwiftUI

/// Captures the actual extension pages without activating the app or replacing its renderer.
/// The fixture advertises shared snapshots as nav_page contributions (MOCK_EXTENSION_SHOWCASE=1).
@MainActor enum MacExtensionSnapshotBatch {
    private(set) static var isCapturing = false
    private static var nativePage: String?
    private static var nativeVersion: Int64?
    private static var nativeFailure: String?
    private static var nativeStream: ExtensionPageStream?
    private static var closedPages: [String] = []
    private static var imageLoads: [UUID: (page: String, url: String)] = [:]
    private static var loadedImages: Set<String> = []

    static func beginImageLoad(_ url: URL) -> UUID? {
        guard isCapturing, let nativePage else { return nil }
        let id = UUID()
        loadedImages.remove(url.absoluteString)
        imageLoads[id] = (nativePage, url.absoluteString)
        return id
    }

    static func finishImageLoad(_ id: UUID?, loaded: Bool) {
        guard let id, let load = imageLoads.removeValue(forKey: id), load.page == nativePage else { return }
        if loaded { loadedImages.insert(load.url) }
    }

    static func observeNativeFailure(owner: String, contribution: String, code: String) {
        guard isCapturing, nativePage == owner + "/" + contribution else { return }
        nativeFailure = code
    }

    // Code-hosting AX containers may omit values; observe only the real validated publisher, never fixture data.
    static func observeNativeCommit(owner: String, contribution: String, version: Int64, stream: ExtensionPageStream) {
        guard isCapturing, nativePage == owner + "/" + contribution, version > 0 else { return }
        nativeVersion = version
        nativeStream = stream
    }

    struct Plan: Decodable {
        var hub: URL
        var output: URL
        var requiresNative: Bool?
        var extensionShowcase: [Scene]
    }

    struct Scene: Decodable {
        var name: String
        var title: String
        var bottom: Bool = false
        var review: Bool?
        var images: [String]?
        var scrollAll: Bool?
    }

    private struct Capture: Encodable {
        var sample: String
        var appearance: String
        var position: String
        var file: String
        var nativeRevision: String? = nil
        var scroll: MacSnapshot.ScrollMetrics? = nil
        var loadedImages: [String]? = nil
    }

    private enum Failure: Error { case invalidPlan, notReady, focusChanged, capture }

    static func plan(at path: String) -> Plan? {
        guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else { return nil }
        return try? JSONDecoder().decode(Plan.self, from: data)
    }

    static func run(_ plan: Plan) async {
        do {
            let output = plan.output.standardizedFileURL
            guard plan.hub.scheme == "http", plan.hub.host == "127.0.0.1", plan.hub.port != nil,
                  plan.hub.user == nil, plan.hub.password == nil, plan.hub.query == nil, plan.hub.fragment == nil,
                  plan.hub.path.isEmpty || plan.hub.path == "/", AppModel.shared.config?.url == plan.hub,
                  NSApp.activationPolicy() == .prohibited, !NSApp.isActive,
                  output.isFileURL, output.host == nil || output.host == "" || output.host == "localhost",
                  output.path.hasPrefix("/tmp/") || output.path.hasPrefix("/private/tmp/"),
                  !FileManager.default.fileExists(atPath: output.path), !plan.extensionShowcase.isEmpty,
                  plan.extensionShowcase.allSatisfy({ $0.name.range(of: "^[a-z0-9_-]+$", options: .regularExpression) != nil })
            else { throw Failure.invalidPlan }
            try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)
            let deadline = ContinuousClock.now.advanced(by: .seconds(30))
            var available: Set<String> = []
            while ContinuousClock.now < deadline {
                guard !NSApp.isActive else { throw Failure.focusChanged }
                if ExtensionSlots.shared.available {
                    await ExtensionSlots.shared.reload(for: plan.hub.absoluteString)
                    available = Set(ExtensionSlots.shared.pages.filter { $0.owner == "showcase" }.map(\.id))
                    if plan.extensionShowcase.allSatisfy({ available.contains($0.name) }) { break }
                }
                try await Task.sleep(for: .milliseconds(200))
            }
            guard plan.extensionShowcase.allSatisfy({ available.contains($0.name) }) else { throw Failure.notReady }
            isCapturing = true
            closedPages = []
            defer { isCapturing = false }
            var captures: [Capture] = []
            for appearance in ["light", "dark"] {
                MacRichSnapshotBatch.shared.scheme = appearance == "dark" ? .dark : .light
                NSApp.appearance = NSAppearance(named: appearance == "dark" ? .darkAqua : .aqua)
                for scene in plan.extensionShowcase {
                    // Tear down the previous page before waiting for this page's HTTP-backed committed view.
                    try await clearPage()
                    nativePage = "showcase/" + scene.name
                    MacShellState.shared.pluginPage = nativePage
                    let window = try await readyWindow(title: scene.title, diagnostic: output.appending(path: "failed-\(scene.name)-\(appearance).png"), requiresNative: plan.requiresNative == true, requiresReview: scene.review == true)
                    try await waitForImages(scene.images ?? [], window: window, diagnostic: output.appending(path: "failed-images-\(scene.name)-\(appearance).png"))
                    let url = output.appending(path: "mac-showcase-\(scene.name)-\(appearance).png")
                    guard MacSnapshot.capture(to: url, window: window) else { throw Failure.capture }
                    captures.append(Capture(sample: scene.name, appearance: appearance, position: "top", file: url.lastPathComponent, nativeRevision: nativeVersion.map { "stream:\($0)" },
                                            scroll: scene.scrollAll == true ? MacSnapshot.captureScrollMetrics(in: window) : nil,
                                            loadedImages: scene.images?.filter { loadedImages.contains($0) }))
                    FileHandle.standardError.write(Data("extension snapshot wrote \(url.lastPathComponent)\n".utf8))
                    if scene.scrollAll == true {
                        var step = 0
                        while MacSnapshot.advanceCapturePage(in: window) {
                            step += 1
                            guard step <= 64 else { throw Failure.capture }
                            try await Task.sleep(for: .milliseconds(600))
                            try await waitForImages(scene.images ?? [], window: window, diagnostic: output.appending(path: "failed-images-\(scene.name)-\(appearance).png"))
                            guard !NSApp.isActive, !window.isKeyWindow,
                                  let scroll = MacSnapshot.captureScrollMetrics(in: window) else { throw Failure.capture }
                            let position = scroll.offset >= scroll.end - 1 ? "bottom" : "middle-\(step)"
                            let tile = output.appending(path: "mac-showcase-\(scene.name)-\(position)-\(appearance).png")
                            guard MacSnapshot.capture(to: tile, window: window) else { throw Failure.capture }
                            captures.append(Capture(sample: scene.name, appearance: appearance, position: position, file: tile.lastPathComponent, nativeRevision: nativeVersion.map { "stream:\($0)" },
                                                    scroll: scroll, loadedImages: scene.images?.filter { loadedImages.contains($0) }))
                            FileHandle.standardError.write(Data("extension snapshot wrote \(tile.lastPathComponent)\n".utf8))
                        }
                    } else if scene.bottom {
                        MacSnapshot.scroll(by: Double.greatestFiniteMagnitude)
                        try await Task.sleep(for: .milliseconds(600))
                        try await waitForImages(scene.images ?? [], window: window, diagnostic: output.appending(path: "failed-images-\(scene.name)-\(appearance).png"))
                        guard !NSApp.isActive, !window.isKeyWindow else { throw Failure.focusChanged }
                        let bottom = output.appending(path: "mac-showcase-\(scene.name)-bottom-\(appearance).png")
                        guard MacSnapshot.capture(to: bottom, window: window) else { throw Failure.capture }
                        captures.append(Capture(sample: scene.name, appearance: appearance, position: "bottom", file: bottom.lastPathComponent, nativeRevision: nativeVersion.map { "stream:\($0)" },
                                                loadedImages: scene.images?.filter { loadedImages.contains($0) }))
                        FileHandle.standardError.write(Data("extension snapshot wrote \(bottom.lastPathComponent)\n".utf8))
                    }
                }
            }
            // Unmount the final page while the socket still exists, so its deferred close can be sent.
            try await clearPage()
            try await Task.sleep(for: .milliseconds(600))
            let encoder = JSONEncoder(); encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            try encoder.encode(captures).write(to: output.appending(path: "captures.json"), options: .atomic)
            try encoder.encode(closedPages).write(to: output.appending(path: "closed-pages.json"), options: .atomic)
            FileHandle.standardError.write(Data("RESULT Mac extension snapshots: \(captures.count) captures; no activation\n".utf8))
            NSApp.terminate(nil)
            exit(0)
        } catch {
            FileHandle.standardError.write(Data("RESULT Mac extension snapshots: FAILED \(error)\n".utf8))
            NSApp.terminate(nil)
            exit(1)
        }
    }

    private static func waitForImages(_ urls: [String], window: NSWindow, diagnostic: URL) async throws {
        guard !urls.isEmpty else { return }
        let required = Set(urls)
        let deadline = ContinuousClock.now.advanced(by: .seconds(30))
        while ContinuousClock.now < deadline {
            guard !NSApp.isActive, !window.isKeyWindow else { throw Failure.focusChanged }
            if required.isSubset(of: loadedImages), !imageLoads.values.contains(where: { required.contains($0.url) }) {
                try await Task.sleep(for: .milliseconds(200))
                window.contentView?.layoutSubtreeIfNeeded()
                window.contentView?.displayIfNeeded()
                return
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        _ = MacSnapshot.capture(to: diagnostic, window: window)
        FileHandle.standardError.write(Data("snapshot images still missing: \(required.subtracting(loadedImages).sorted())\n".utf8))
        throw Failure.notReady
    }

    private static func clearPage() async throws {
        if let nativeStream {
            try await nativeStream.closeForCapture()
            if let nativePage { closedPages.append(nativePage) }
        }
        nativeStream = nil
        nativePage = nil
        nativeVersion = nil
        nativeFailure = nil
        imageLoads = [:]
        loadedImages = []
        MacShellState.shared.pluginPage = nil
        let deadline = ContinuousClock.now.advanced(by: .seconds(10))
        while ContinuousClock.now < deadline {
            guard !NSApp.isActive, NSWorkspace.shared.frontmostApplication?.processIdentifier != getpid() else {
                throw Failure.focusChanged
            }
            if let window = NSApp.windows.first(where: { $0.identifier?.rawValue.hasPrefix("main") == true && $0.contentView != nil && !($0 is NSPanel) }),
               let content = window.contentView {
                content.layoutSubtreeIfNeeded(); content.displayIfNeeded()
                let entries = MacSnapshot.accessibilityEntries(content)
                if !entries.contains(where: { $0.identifier == "extension-ui-ready" || $0.identifier == "extension-ui-waiting" }) { return }
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        throw Failure.notReady
    }

    private static func readyWindow(title: String, diagnostic: URL, requiresNative: Bool, requiresReview: Bool) async throws -> NSWindow {
        let deadline = ContinuousClock.now.advanced(by: .seconds(20))
        var lastWindow: NSWindow?
        var lastEntries: [MacSnapshot.AccessibilityEntry] = []
        while ContinuousClock.now < deadline {
            guard !NSApp.isActive, NSWorkspace.shared.frontmostApplication?.processIdentifier != getpid() else {
                throw Failure.focusChanged
            }
            if let window = NSApp.windows.first(where: { $0.identifier?.rawValue.hasPrefix("main") == true && $0.contentView != nil && !($0 is NSPanel) }),
               let content = window.contentView {
                let size = NSSize(width: 1280, height: 900)
                if content.bounds.size != size { window.setContentSize(size) }
                if !window.isVisible { window.orderBack(nil) }
                guard !window.isKeyWindow else { throw Failure.focusChanged }
                content.layoutSubtreeIfNeeded(); content.displayIfNeeded()
                let entries = MacSnapshot.accessibilityEntries(content)
                lastWindow = window
                lastEntries = entries
                let native = nativeVersion.map { $0 > 0 } == true
                if entries.contains(where: { $0.identifier == "extension-ui-ready" }),
                   !entries.contains(where: { $0.identifier == "extension-ui-waiting" }),
                   (!requiresNative || native),
                   (!requiresReview || entries.contains(where: { $0.identifier == "extension-review-bar" })),
                   entries.contains(where: { $0.label == title }) {
                    try await Task.sleep(for: .milliseconds(600))
                    content.layoutSubtreeIfNeeded(); content.displayIfNeeded()
                    return window
                }
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        if let window = lastWindow, !NSApp.isActive, !window.isKeyWindow {
            _ = MacSnapshot.capture(to: diagnostic, window: window)
        }
        let ready = lastEntries.contains { $0.identifier == "extension-ui-ready" }
        let waiting = lastEntries.contains { $0.identifier == "extension-ui-waiting" }
        let labels = lastEntries.compactMap(\.label).prefix(40)
        let lifecycle = lastEntries.filter { $0.identifier == "extension-ui-ready" || $0.identifier == "extension-ui-waiting" }.compactMap(\.value)
        FileHandle.standardError.write(Data("snapshot readiness title=\(title) ready=\(ready) waiting=\(waiting) nativeVersion=\(nativeVersion.map(String.init) ?? "none") nativeFailure=\(nativeFailure ?? "none") lifecycle=\(lifecycle) labels=\(Array(labels))\n".utf8))
        throw Failure.notReady
    }

}
#endif
