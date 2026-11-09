import Foundation

// Settings' disconnect buttons sign this device out of each hub they forget (#1824). Only revoking its credential makes
// the hub drop the device's push and Live Activity registrations (hub signedOut); forgetting the hub locally does not.
// The local cleanup goes ahead either way, and a hub that could not be signed out of is named in a toast.

extension AppModel {
    /// Settings › Disconnect and Forget Server, Disconnect All Hubs: every hub.
    func disconnectAll() async {
        await signingOut(registry.hubs) { $0.unpair() }
    }

    /// Settings › hub › Disconnect This Group: every hub of the group (the last group: `unpair`).
    func disconnectGroup(_ group: String) async {
        await signingOut(registry.members(group)) { $0.removeGroup(group) }
    }

    /// Settings › hub › Disconnect This Hub: that hub alone.
    func disconnectHub(_ id: String) async {
        await signingOut(registry.hubs.filter { $0.id == id }) { $0.removeHub(id) }
    }

    /// Signs out of `hubs`, then runs the local cleanup. A second tap while one runs does nothing.
    private func signingOut(_ hubs: [PairedHub], then forget: (AppModel) -> Void) async {
        guard !signOutRunning else { return }
        signOutRunning = true
        defer { signOutRunning = false }
        let traded = tradedTokens
        let missed = await Self.signOut(hubs) { hub in
            APIClient(config: DeviceCredentialTrade.current(hub.config, traded: traded), group: hub.group)
        }
        forget(self)
        guard !missed.isEmpty else { return }
        let names = ListFormatter.localizedString(byJoining: missed.map(\.displayName))
        toast = Toast(String(localized: "Couldn't sign this device out of \(names). It may still get notifications until you remove it under Settings › Devices on another device."),
                      style: .error)
    }

    /// Revokes, at each hub, the watch's answer-only credential and then this device's own (`current` of
    /// GET /auth/tokens), all hubs at once. Returns the hubs this device is still signed in to: out of reach, refused, or
    /// paired with the hub's master token (which has no id and cannot be revoked). Demo hubs are skipped.
    nonisolated static func signOut(_ hubs: [PairedHub], client: @escaping @Sendable (PairedHub) -> APIClient,
                                    timeout: TimeInterval = 10) async -> [PairedHub] {
        let signedIn = await withTaskGroup(of: (Int, Bool).self) { group in
            for (i, hub) in hubs.enumerated() where !hub.config.isDemo {
                group.addTask {
                    let api = client(hub)
                    if let watch = hub.watchDeviceID, !watch.isEmpty {
                        // Best effort: the watch registers no pushes, so a watch credential left behind is not reported.
                        try? await api.revokeToken(watch, timeout: timeout)
                    }
                    do {
                        return (i, try await !api.revokeOwnCredential(timeout: timeout))
                    } catch {
                        return (i, true)
                    }
                }
            }
            var out = Set<Int>()
            for await (i, still) in group where still { out.insert(i) }
            return out
        }
        return hubs.indices.filter(signedIn.contains).map { hubs[$0] }
    }
}
