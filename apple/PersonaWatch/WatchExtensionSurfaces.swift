import Foundation
import SwiftUI

nonisolated struct WatchExtensionDestination: Identifiable {
    var link: ExtensionSystemLink
    var id: String { link.url?.absoluteString ?? "" }
}

extension WatchModel {
    /// This is the wrist's own configuration, not the phone's registry; empty replaces and clears all groups.
    func syncExtensionSurfaces() {
        try? ExtensionSurfaceSync.credentials(configs, names: names, devices: extensionDeviceIDs)
        if let destination = extensionSurface, configs[destination.link.group] == nil { extensionSurface = nil }
    }
    func refreshExtensionSurfaces() async {
        syncExtensionSurfaces()
        await ExtensionSurfaceSync.refresh()
    }
    func openExtensionSurface(_ url: URL) {
        guard let link = ExtensionSystemLink(url: url), configs[link.group] != nil else { return }
        extensionSurface = WatchExtensionDestination(link: link)
    }
}

/// A complication tap opens only the named group's page; unpaired groups do not fall back to another hub.
/// The wrist presents a read-only projection. Interactive extension controls remain in the containing phone app.
struct WatchExtensionSurfacePage: View {
    var destination: WatchExtensionDestination
    @Environment(WatchModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State private var view: ExtensionView?
    @State private var failed = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                if let view { ExtensionSurfaceView(view: view) }
                else if failed { Text("This extension page is unavailable").font(Theme.Font.body) }
                else if destination.link.ui != nil { ProgressView() }
                Text("Open Persona on iPhone to use extension controls").font(Theme.Font.caption).foregroundStyle(.secondary)
            }
            .padding(Theme.Space.s)
        }
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button { dismiss() } label: { Text("Done").lineLimit(1) }
            }
        }
        .task(id: destination.id) {
            guard let ui = destination.link.ui, let api = model.api(destination.link.group) else { failed = true; return }
            do {
                let places: ExtensionUIPlaces = try await api.get("/api/v1/extensions/-/ui")
                guard places.ui.contains(where: { $0.extension == destination.link.owner && $0.id == ui && ["nav_page", "extension_page"].contains($0.placement) }) else { failed = true; return }
                let body = ExtensionUIRequest(context: .here(size: "large"), state: destination.link.state)
                let result = try JSON.decoder.decode(ExtensionActionResult.self, from: try await api.request("POST", "/api/v1/extensions/\(destination.link.owner)/-/ui/\(ui)", body: body))
                guard !Task.isCancelled, model.configs[destination.link.group] == api.config else { return }
                view = result.view
                failed = view == nil
            } catch {
                if !Task.isCancelled { failed = true }
            }
        }
    }
}
