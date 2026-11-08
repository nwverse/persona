#if DEBUG
import AppKit
import SwiftUI

/// Captures the actual extension pages without activating the app or replacing its renderer.
/// The fixture advertises shared snapshots as nav_page contributions (MOCK_EXTENSION_SHOWCASE=1).
@MainActor enum MacExtensionSnapshotBatch {
    private(set) static var isCapturing = false

    struct Plan: Decodable {
        var hub: URL
        var output: URL
        var extensionShowcase: [Scene]
    }

    struct Scene: Decodable {
        var name: String
        var title: String
        var bottom: Bool = false
    }

    private struct Capture: Encodable {
        var sample: String
        var appearance: String
        var position: String
        var file: String
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
            await ExtensionSlots.shared.reload(for: plan.hub.absoluteString)
            let available = Set(ExtensionSlots.shared.pages.filter { $0.owner == "showcase" }.map(\.id))
            guard plan.extensionShowcase.allSatisfy({ available.contains($0.name) }) else { throw Failure.invalidPlan }
            isCapturing = true
            defer { isCapturing = false }
            var captures: [Capture] = []
            for appearance in ["light", "dark"] {
                MacRichSnapshotBatch.shared.scheme = appearance == "dark" ? .dark : .light
                NSApp.appearance = NSAppearance(named: appearance == "dark" ? .darkAqua : .aqua)
                for scene in plan.extensionShowcase {
                    // Tear down the previous page before waiting for this page's HTTP-backed committed view.
                    try await clearPage()
                    MacShellState.shared.pluginPage = "showcase/" + scene.name
                    let window = try await readyWindow(title: scene.title)
                    let url = output.appending(path: "mac-showcase-\(scene.name)-\(appearance).png")
                    guard MacSnapshot.capture(to: url, window: window) else { throw Failure.capture }
                    captures.append(Capture(sample: scene.name, appearance: appearance, position: "top", file: url.lastPathComponent))
                    FileHandle.standardError.write(Data("extension snapshot wrote \(url.lastPathComponent)\n".utf8))
                    if scene.bottom {
                        MacSnapshot.scroll(by: Double.greatestFiniteMagnitude)
                        try await Task.sleep(for: .milliseconds(600))
                        guard !NSApp.isActive, !window.isKeyWindow else { throw Failure.focusChanged }
                        let bottom = output.appending(path: "mac-showcase-\(scene.name)-bottom-\(appearance).png")
                        guard MacSnapshot.capture(to: bottom, window: window) else { throw Failure.capture }
                        captures.append(Capture(sample: scene.name, appearance: appearance, position: "bottom", file: bottom.lastPathComponent))
                        FileHandle.standardError.write(Data("extension snapshot wrote \(bottom.lastPathComponent)\n".utf8))
                    }
                }
            }
            let encoder = JSONEncoder(); encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            try encoder.encode(captures).write(to: output.appending(path: "captures.json"), options: .atomic)
            FileHandle.standardError.write(Data("RESULT Mac extension snapshots: \(captures.count) captures; no activation\n".utf8))
            NSApp.terminate(nil)
            exit(0)
        } catch {
            FileHandle.standardError.write(Data("RESULT Mac extension snapshots: FAILED \(error)\n".utf8))
            NSApp.terminate(nil)
            exit(1)
        }
    }

    private static func clearPage() async throws {
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

    private static func readyWindow(title: String) async throws -> NSWindow {
        let deadline = ContinuousClock.now.advanced(by: .seconds(20))
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
                if entries.contains(where: { $0.identifier == "extension-ui-ready" }),
                   !entries.contains(where: { $0.identifier == "extension-ui-waiting" }),
                   entries.contains(where: { $0.label == title }) {
                    try await Task.sleep(for: .milliseconds(600))
                    content.layoutSubtreeIfNeeded(); content.displayIfNeeded()
                    return window
                }
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        throw Failure.notReady
    }

}
#endif
