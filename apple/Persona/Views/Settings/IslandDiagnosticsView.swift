import SwiftUI

// The Dynamic Island is an iPhone's: a Mac has no island to diagnose.
#if os(iOS)

/// Dynamic Island Diagnostics (hidden: touch and hold the first preview in Settings › Notifications › Dynamic Island): the steps between Settings › Avatar and the
/// island, for a phone that draws the wrong persona — what the app saved for the widget extension, what the shared
/// App Group container holds, what the extension last read (docs/apple/live-activity.md「Diagnostics」). 「复制全部」 is what to send.
struct IslandDiagnosticsView: View {
    @Environment(AppModel.self) private var model
    @State private var sections: [IslandDiagnostics.Section] = []

    var body: some View {
        List {
            ForEach(sections) { section in
                Section(section.title) {
                    ForEach(section.rows) { row in
                        if row.wide {
                            Text(row.value).font(Theme.Font.body).textSelection(.enabled)
                        } else {
                            LabeledContent {
                                Text(row.value).multilineTextAlignment(.trailing).textSelection(.enabled)
                            } label: {
                                Text(row.label).lineLimit(1)
                            }
                            .font(Theme.Font.secondary)
                        }
                    }
                }
            }
            Section {
                Button(action: redraw) {
                    Label("Rewrite and Redraw", systemImage: Theme.Icon.retry).lineLimit(1)
                }
                Button {
                    UIPasteboard.general.string = IslandDiagnostics.text(sections)
                    model.toast = Toast(String(localized: "Copied [V N C Pi P Diagnostics 8f6f8d]"), style: .success)
                } label: {
                    Label("Copy All [V N C Pi P Diagnostics 75c393]", systemImage: Theme.Icon.copy).lineLimit(1)
                }
            } footer: {
                Text("“Extension, last run” updates each time the Dynamic Island redraws (identical content is written at most once every ten minutes). If it's missing, the extension may not be able to use the App Group; then only the system log has its records (Console, subsystem persona, category island).")
            }
        }
        .themedPage()
        .navigationTitle("Dynamic Island Diagnostics")
        .navigationBarTitleDisplayMode(.inline)
        .task { reload() }
        .refreshable { reload() }
    }

    private func redraw() {
        LiveActivityController.shared.look(model.fleetLook, force: true) { model.fleetFaces }
        reload()
    }

    private func reload() {
        let store = FleetLookStore.shared
        var input = IslandDiagnostics.Input(look: model.fleetLook, group: FleetLookStore.group, root: store.root?.path(percentEncoded: false),
                                            writes: FleetLookStore.lastWrites, files: store.files() ?? [], seen: FleetLookReport.seen)
        input.activity = LiveActivityController.shared.runningLook
        sections = IslandDiagnostics.sections(input, now: .now)
        Task {
            guard let api = model.api else { return }
            do { input.hub = .some(try await api.fleetLook(device: PushRegistrar.deviceID)) } catch { return }
            sections = IslandDiagnostics.sections(input, now: .now)
        }
    }
}

/// The page's content, worked out apart from the container so it can be tested.
nonisolated enum IslandDiagnostics {
    struct Row: Identifiable, Hashable {
        var label: String
        var value: String
        /// A sentence on its own (the verdict), not a value beside a label.
        var wide = false
        var id: String { label }
    }

    struct Section: Identifiable, Hashable {
        var title: String
        var rows: [Row]
        var id: String { title }
    }

    /// What the page is made from.
    struct Input {
        var look: FleetLook
        var group: String?
        /// The app's `Fleet/` in the group's container; nil when it has none.
        var root: String? = "/Fleet"
        var writes: [String: (at: Date, error: String?, bytes: Int?)] = [:]
        var files: [FleetLookStore.File] = []
        var seen: FleetLookReport?
        /// The running activity's content: nil without an activity, .some(nil) when its content carries no look.
        var activity: FleetLook??
        /// What the hub keeps for this device: nil until asked, .some(nil) when it has none (an older hub, or not told).
        var hub: FleetLook??
    }

    static func sections(_ i: Input, now: Date) -> [Section] {
        let current = FleetLookReport.describe(i.look)
        func shown(_ l: FleetLook??, none: String, missing: String) -> String {
            switch l { case .none: none; case .some(.none): missing; case .some(.some(let l)): FleetLookReport.describe(l) }
        }
        var out = [Section(title: String(localized: "Conclusion"), rows: [Row(label: String(localized: "Looks like"), value: verdict(i), wide: true)])]
        out.append(Section(title: String(localized: "Avatar"), rows: [
            Row(label: String(localized: "In Settings"), value: current),
            Row(label: String(localized: "Colors [Image Editor 070a80]"), value: String(format: "%06X / %06X", i.look.light, i.look.dark)),
            Row(label: String(localized: "Stored on Hub"), value: shown(i.hub, none: String(localized: "Not asked yet"), missing: String(localized: "None (the hub is too old, or hasn't been told)"))),
            Row(label: String(localized: "In Live Activity Content"), value: shown(i.activity, none: String(localized: "No Live Activity showing"), missing: String(localized: "None [V N C Pi P Diagnostics f12780]"))),
        ]))
        let written = [FleetLookStore.lookFile, FleetLookStore.facesDir].map { name in
            guard let hit = i.writes.first(where: { $0.key == name || $0.key.hasPrefix(name + "/") }) else {
                return Row(label: name, value: String(localized: "Not written yet (skipped when unchanged)"))
            }
            let w = hit.value
            return Row(label: hit.key, value: [w.error.map { String(localized: "Failed: \($0)") } ?? String(localized: "Succeeded"), w.bytes.map { String(localized: "\($0) bytes") }, time(w.at, now)].compactMap { $0 }.joined(separator: " · "))
        }
        out.append(Section(title: String(localized: "Written to App Group This Launch"), rows: [
            Row(label: "App Group", value: i.group ?? String(localized: "None: not in the app's Info.plist")),
            Row(label: String(localized: "Container"), value: i.root ?? String(localized: "None: this App Group isn't in the signature")),
        ] + written))
        let listed = i.files.isEmpty
            ? [Row(label: String(localized: "Files"), value: String(localized: "None [V N C Pi P Diagnostics f12780]"))]
            : i.files.map { Row(label: $0.name, value: String(localized: "\($0.bytes) bytes") + ($0.modified.map { " · " + time($0, now) } ?? "")) }
        out.append(Section(title: String(localized: "In Container"), rows: listed))
        if let seen = i.seen {
            out.append(Section(title: String(localized: "Extension, Last Run"), rows: [
                Row(label: String(localized: "Time"), value: time(seen.at, now)),
                Row(label: String(localized: "Avatar Drawn"), value: seen.content ?? String(localized: "No avatar in content")),
                Row(label: String(localized: "Container"), value: seen.container == false ? String(localized: "None [V N C Pi P Diagnostics f12780]") : String(localized: "Yes [V N C Pi P Diagnostics 90ce0f]")),
                Row(label: String(localized: "Saved Avatar"), value: seen.saved ?? String(localized: "None [V N C Pi P Diagnostics f12780]")),
                Row(label: String(localized: "Avatar Pack Images"), value: seen.facesPackage.map { p in
                    seen.faceFile.map { String(localized: "\($0) · \(seen.faceBytes ?? 0) bytes · \(p)") } ?? String(localized: "No images for \(p)")
                } ?? String(localized: "Not read (built-in avatar)")),
            ]))
        } else {
            out.append(Section(title: String(localized: "Extension, Last Run"), rows: [Row(label: String(localized: "Report"), value: String(localized: "None [V N C Pi P Diagnostics f12780]"))]))
        }
        return out
    }

    /// The first step that does not hold, in the order the look travels: the hub and the activity's content (what
    /// the island draws), then the App Group container (a package's pictures).
    static func verdict(_ i: Input) -> String {
        if case .some(let hub) = i.hub, hub != i.look {
            return hub == nil ? String(localized: "The hub has no avatar for this phone: update the hub, or the app hasn't told it yet (pushes won't carry the avatar)") : String(localized: "The hub's avatar doesn't match Settings (the app failed to tell it)")
        }
        if case .some(let content) = i.activity, content != i.look { return String(localized: "The Live Activity showing doesn't have the avatar from Settings (not redrawn yet)") }
        if i.group == nil || i.root == nil {
            let group = i.group ?? String(localized: "this group")
            return String(localized: "The app can't get the App Group container (\(group) isn't in the signature): avatar pack images can't reach the extension, so it draws the default avatar in the pack's colors")
        }
        if let failed = i.writes.first(where: { $0.key.hasPrefix(FleetLookStore.facesDir) && $0.value.error != nil }) {
            let error = failed.value.error ?? ""
            return String(localized: "The app failed to write avatar pack images to the App Group (\(failed.key)): \(error)")
        }
        guard let seen = i.seen else {
            return i.look.package == nil ? String(localized: "The avatar arrives with the Live Activity content; the extension left no report (it may not be able to write to the App Group; built-in avatars aren't affected)")
                : String(localized: "The extension left no report: it may not be able to use the App Group, so avatar pack images can't be drawn (fine if the island shows the pack's images)")
        }
        if seen.content != FleetLookReport.describe(i.look) { return String(localized: "The extension last drew a different avatar than Settings (not redrawn yet)") }
        if seen.container == false { return String(localized: "The extension can't get the App Group container (the group isn't in its signature): it draws the default avatar in the pack's colors") }
        if let package = i.look.package, seen.facesPackage != package || seen.faceFile == nil {
            return String(localized: "The extension can't read this avatar pack's images, so it draws the default avatar in the pack's colors")
        }
        return String(localized: "The extension's drawing matches Settings")
    }

    static func time(_ date: Date, _ now: Date) -> String {
        let s = Int(now.timeIntervalSince(date))
        return s < 60 ? String(localized: "\(max(s, 0)) seconds ago") : s < 3600 ? String(localized: "\(s / 60) min ago") : date.formatted(date: .abbreviated, time: .standard)
    }

    /// Everything on the page as text, to paste into a message.
    static func text(_ sections: [Section]) -> String {
        sections.map { s in ([s.title] + s.rows.map { "  \($0.label)：\($0.value)" }).joined(separator: "\n") }.joined(separator: "\n") // i18n-ignore: a diagnostics report
    }
}
#endif
