#if DEBUG
import AppKit
import Observation
import SwiftUI

/// A single, non-activating real-app capture run. The plan and pairing must both name the loopback fixture.
/// `-macSnapshot <plan.json>` also activates the existing no-keychain snapshot boundary.
@MainActor @Observable
final class MacRichSnapshotBatch {
    static let shared = MacRichSnapshotBatch()
    var scheme: ColorScheme = .light

    private struct Plan: Decodable { var hub: URL; var conversation: String; var output: URL }
    private struct Shot: Codable { var sample: String; var appearance: String; var file: String }
    private enum Failure: Error { case invalidPlan, request, notReady, capture, focusChanged }

    func run() async {
        do {
            let path = UserDefaults.standard.string(forKey: "macSnapshot") ?? ""
            let plan = try JSONDecoder().decode(Plan.self, from: Data(contentsOf: URL(fileURLWithPath: path)))
            let outputPath = plan.output.standardizedFileURL.path
            guard plan.hub.scheme == "http", plan.hub.host == "127.0.0.1", plan.hub.port != nil,
                  plan.hub.user == nil, plan.hub.password == nil, plan.hub.query == nil, plan.hub.fragment == nil,
                  plan.hub.path.isEmpty || plan.hub.path == "/", plan.output.isFileURL,
                  plan.output.host == nil || plan.output.host == "" || plan.output.host == "localhost",
                  outputPath.hasPrefix("/tmp/") || outputPath.hasPrefix("/private/tmp/"),
                  !FileManager.default.fileExists(atPath: outputPath),
                  AppModel.shared.config?.url == plan.hub,
                  AppModel.shared.assistantCurrent?.sessionID == plan.conversation,
                  NSApp.activationPolicy() == .prohibited, !NSApp.isActive else { throw Failure.invalidPlan }
            try FileManager.default.createDirectory(at: plan.output, withIntermediateDirectories: true)
            var shots: [Shot] = []
            for appearance in ["light", "dark"] {
                scheme = appearance == "dark" ? .dark : .light
                NSApp.appearance = NSAppearance(named: appearance == "dark" ? .darkAqua : .aqua)
                for sample in ["decision", "compare", "progress", "form", "chart", "html"] {
                    var request = URLRequest(url: plan.hub.appending(path: "mock/rich/sample/" + sample))
                    request.httpMethod = "POST"
                    request.httpBody = Data("{}".utf8)
                    request.timeoutInterval = 10
                    let (_, response) = try await URLSession.shared.data(for: request)
                    guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw Failure.request }
                    TasksStore.shared.forget()
                    guard let load = TasksStore.shared.load(plan.conversation, refreshTodos: false) else { throw Failure.request }
                    await load.value
                    let envelope = AssistantViewsStore.shared.view("view_" + sample, conversation: plan.conversation,
                                                                   group: AppModel.shared.api?.group ?? "")
                    guard let title = envelope?.view.title else { throw Failure.notReady }
                    let window = try await readyWindow(title: title, sample: sample, fallback: envelope?.view.fallback)
                    let url = plan.output.appending(path: "\(appearance)-\(sample).png")
                    guard MacSnapshot.capture(to: url, window: window) else { throw Failure.capture }
                    shots.append(Shot(sample: sample, appearance: appearance, file: url.lastPathComponent))
                    FileHandle.standardError.write(Data("rich snapshot wrote \(url.lastPathComponent)\n".utf8))
                }
            }
            let encoder = JSONEncoder(); encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
            try encoder.encode(shots).write(to: plan.output.appending(path: "captures.json"), options: .atomic)
            FileHandle.standardError.write(Data("RESULT Mac rich snapshots: 12 captures; no activation\n".utf8))
            NSApp.terminate(nil)
            exit(0)
        } catch {
            FileHandle.standardError.write(Data("RESULT Mac rich snapshots: FAILED \(error)\n".utf8))
            NSApp.terminate(nil)
            exit(1)
        }
    }

    private func readyWindow(title: String, sample: String, fallback: String?) async throws -> NSWindow {
        let deadline = ContinuousClock.now.advanced(by: .seconds(20))
        var reason = "main window unavailable"
        while ContinuousClock.now < deadline {
            guard !NSApp.isActive, NSWorkspace.shared.frontmostApplication?.processIdentifier != getpid() else {
                throw Failure.focusChanged
            }
            if let window = NSApp.windows.first(where: { $0.identifier?.rawValue.hasPrefix("main") == true && $0.contentView != nil && !($0 is NSPanel) }),
               let content = window.contentView {
                if content.bounds.size != NSSize(width: 1280, height: 900) {
                    window.setContentSize(NSSize(width: 1280, height: 900))
                }
                if !window.isVisible { window.orderBack(nil) }
                guard !window.isKeyWindow, !NSApp.isActive else { throw Failure.focusChanged }
                content.layoutSubtreeIfNeeded(); content.displayIfNeeded()
                let entries = MacSnapshot.accessibilityEntries(content)
                let frame = entries.first(where: { $0.identifier == "assistant-view-card" })?.frame
                reason = "body=\(bodyReady(entries, sample: sample, fallback: fallback)) frame=\(String(describing: frame)) labels=\(entries.compactMap(\.label).suffix(16))"
                if entries.contains(where: { $0.identifier == "assistant-view-card" }),
                   entries.contains(where: { $0.label == title }), bodyReady(entries, sample: sample, fallback: fallback),
                   !entries.contains(where: { $0.identifier == "chat-empty-placeholder" || $0.identifier == "chat-loading-placeholder" }) {
                    let cardFrame = entries.first(where: { $0.identifier == "assistant-view-card" })?.frame
                    // The renderer can expose its title before the inverted lazy list settles its full row height.
                    try await Task.sleep(for: .milliseconds(600))
                    content.layoutSubtreeIfNeeded(); content.displayIfNeeded()
                    let settledFrame = MacSnapshot.accessibilityEntries(content).first(where: { $0.identifier == "assistant-view-card" })?.frame
                    if let cardFrame, cardFrame.height > 0, cardFrame == settledFrame {
                        return window
                    }
                }
            }
            try await Task.sleep(for: .milliseconds(100))
        }
        let count = NSApp.windows.filter { $0.contentView != nil && !($0 is NSPanel) }.count
        FileHandle.standardError.write(Data("rich snapshot readiness timed out: title=\(title), windows=\(count), \(reason)\n".utf8))
        throw Failure.notReady
    }

    private func bodyReady(_ entries: [MacSnapshot.AccessibilityEntry], sample: String, fallback: String?) -> Bool {
        switch sample {
        case "decision": entries.contains { $0.identifier == "plugin-ui-action-choose-b" }
        case "compare": entries.contains { $0.identifier == "plugin-ui-action-details" }
        case "form": entries.contains { $0.identifier == "plugin-ui-field-task" }
            && entries.contains { $0.identifier == "plugin-ui-action-submit" }
        case "progress": entries.contains { $0.label?.contains("完成 1／3 步") == true }
        case "chart": entries.contains { $0.label?.contains("第一次") == true }
        case "html": fallback != nil && entries.contains { $0.identifier == "html-card-fallback" && $0.label == fallback }
        default: false
        }
    }

}
#endif
