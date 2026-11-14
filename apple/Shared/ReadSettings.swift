import Foundation

/// Manual mark as read (#7356, docs/protocol/agent-states.md "Seen"): the hub's switch, the same on every device
/// (GET/PUT /api/v1/read-settings and the `read_settings` event). Off, the default, opening a conversation or an Agent
/// marks it seen as always; on, only Mark as read does.
nonisolated struct ReadSettings: Decodable, Equatable, Sendable {
    var manual: Bool
    /// This device may change it (an answer-only device may not).
    var canWrite: Bool

    enum CodingKeys: String, CodingKey { case manual, canWrite = "can_write" }

    init(manual: Bool, canWrite: Bool) {
        self.manual = manual
        self.canWrite = canWrite
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        manual = (try? c.decodeIfPresent(Bool.self, forKey: .manual)) ?? false
        canWrite = (try? c.decodeIfPresent(Bool.self, forKey: .canWrite)) ?? false
    }
}

/// What Settings shows of the hub's answer: `supported` is false on a hub from before the switch.
nonisolated struct ReadSettingsState: Equatable, Sendable {
    var supported: Bool
    var canWrite: Bool
}

extension APIClient {
    /// The switch; nil from a hub from before it (404), where it is off and cannot be turned on.
    func readSettings() async throws -> ReadSettings? {
        do {
            return try await get("/api/v1/read-settings", as: ReadSettings.self)
        } catch let e as APIError where e.status == 404 {
            return nil
        }
    }

    func setManualRead(_ on: Bool) async throws -> ReadSettings {
        try JSON.decoder.decode(ReadSettings.self, from: try await request("PUT", "/api/v1/read-settings", body: ["manual": on]))
    }
}
