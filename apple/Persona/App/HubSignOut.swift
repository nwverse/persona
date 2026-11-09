import Foundation

// Disconnecting in Settings signs this device out of each hub it forgets (#1824). Only a revoked credential makes the
// hub drop the device's push and Live Activity registrations (hub `Pusher.signedOut`); forgetting the hub here does not.
// The local cleanup runs either way, and a toast names each hub that may still reach the device.

extension AppModel {
    /// Settings › Disconnect and Forget Server / Disconnect All Hubs: every hub.
    func disconnectAll() async {
        await signingOut(registry.hubs) { $0.unpair() }
    }

    /// Settings › hub › Disconnect This Group, and Settings › Devices › Revoke and Unpair: every hub of the group (the
    /// last group: `unpair`).
    func disconnectGroup(_ group: String) async {
        await signingOut(registry.members(group)) { $0.removeGroup(group) }
    }

    /// Settings › hub › Disconnect This Hub: that hub alone. The group's push registration and the watch's credential
    /// were made at its first hub: when that one goes, the next first hub gets them.
    func disconnectHub(_ id: String) async {
        guard let hub = registry.hubs.first(where: { $0.id == id }) else { return }
        let wasFirst = registry.primary(hub.group)?.id == id
        await signingOut([hub]) { $0.removeHub(id) }
        guard wasFirst, !registry.hubs.contains(where: { $0.id == id }), link(hub.group) != nil else { return }
        PushRegistrar.shared.registerIfPossible()
        await upgradeCredentials()
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
        toast = Toast(String(localized: "Couldn't sign out of \(names). To stop notifications, revoke this device in Settings › Devices on another device."),
                      style: .error)
    }

    /// Signs out of every hub at once. Returns the hubs this device may still be signed in to, in the order given: out
    /// of reach, refusing, or paired with the hub's master token (no id, so it cannot be revoked). Demo hubs are skipped.
    nonisolated static func signOut(_ hubs: [PairedHub], client: @escaping @Sendable (PairedHub) -> APIClient,
                                    timeout: TimeInterval = 10) async -> [PairedHub] {
        let missed = await withTaskGroup(of: (Int, Bool).self) { group in
            for (i, hub) in hubs.enumerated() where !hub.config.isDemo {
                group.addTask { (i, await Self.signOut(at: client(hub), watch: hub.watchDeviceID, timeout: timeout)) }
            }
            var out = Set<Int>()
            for await (i, done) in group where !done { out.insert(i) }
            return out
        }
        return hubs.indices.filter(missed.contains).map { hubs[$0] }
    }

    /// Revokes this device's credential at one hub (`current` of GET /auth/tokens), and before it the watch's
    /// answer-only one made there. true: the hub no longer holds this device's credential.
    nonisolated static func signOut(at api: APIClient, watch: String?, timeout: TimeInterval) async -> Bool {
        let current: String
        do {
            current = try await api.deviceTokens(timeout: timeout).current ?? ""
        } catch let error as APIError where error.status == 401 {
            return true // revoked already (Settings › Devices on another device), and the hub dropped its pushes then
        } catch {
            return false
        }
        guard !current.isEmpty else { return false }
        if let watch, !watch.isEmpty, watch != current {
            // Best effort: the watch registers no pushes, and the phone stops handing it this group's credential.
            try? await api.revokeToken(watch, timeout: timeout)
        }
        do {
            try await api.revokeToken(current, timeout: timeout)
            return true
        } catch let error as APIError where error.status == 404 {
            return true // revoked meanwhile
        } catch {
            return false
        }
    }
}
