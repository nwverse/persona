import Foundation

// Channels (docs/extensions/channels.md): the wire types of GET /api/v1/channels and where a message came from. Shared with the
// watch, which marks a message that came in from a channel as the phone does; the settings pages are the app's
// (Persona/Views/Settings/ChannelsModel.swift).

/// One of GET /api/v1/channels (the hub's `channel.View`). Every field but the id and name may be missing: a field a
/// newer hub drops, or one an older one never had, leaves its default.
nonisolated struct HubChannel: Decodable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    /// app | im | mention | voice
    var kind: String
    /// builtin | plugin | market
    var source: String
    var plugin: String?
    /// A market's (source market): the market's id, for its catalog page.
    var market: String?
    var installed: Bool
    var enabled: Bool
    /// connected | not_connected | error | disabled | not_installed
    var state: String
    var error: String?
    var account: Account?
    var description: String?
    var capabilities: Capabilities
    var target: ChannelTarget?
    var features: [ChannelFeature]
    /// none | qr | code | fields | config
    var bind: String
    /// The app channel's ends (iPhone, Mac…), for people.
    var surfaces: [String]
    var lastEventAt: Date?
    /// Its settings are elsewhere (hub.json, its own page): shown here, not changed.
    var readOnly: Bool

    nonisolated struct Account: Codable, Hashable, Sendable {
        var id: String?
        var name: String
    }

    nonisolated struct Capabilities: Codable, Hashable, Sendable {
        var imagesIn: Bool?
        var filesIn: Bool?
        var voiceIn: Bool?
        /// markdown | plain
        var format: String?
        var maxText: Int?
        /// none | text | buttons
        var asks: String?
        var push: Bool?
        var typing: Bool?
        var progress: Bool?
        var threads: Bool?

        enum CodingKeys: String, CodingKey {
            case format, asks, push, typing, progress, threads
            case imagesIn = "images_in", filesIn = "files_in", voiceIn = "voice_in", maxText = "max_text"
        }
    }

    enum CodingKeys: String, CodingKey {
        case id, name, kind, source, plugin, market, installed, enabled, state, error, account, description, capabilities,
             target, features, bind, surfaces
        case lastEventAt = "last_event_at", readOnly = "read_only"
    }

    init(id: String, name: String, kind: String = "im", source: String = "builtin", state: String = "not_connected") {
        self.id = id
        self.name = name
        self.kind = kind
        self.source = source
        installed = true
        enabled = true
        self.state = state
        capabilities = Capabilities()
        features = []
        bind = "none"
        surfaces = []
        readOnly = false
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
        kind = try c.decodeIfPresent(String.self, forKey: .kind) ?? "im"
        source = try c.decodeIfPresent(String.self, forKey: .source) ?? "builtin"
        plugin = try c.decodeIfPresent(String.self, forKey: .plugin)
        market = try c.decodeIfPresent(String.self, forKey: .market)
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? (source != "market")
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        state = try c.decodeIfPresent(String.self, forKey: .state) ?? "not_connected"
        error = try c.decodeIfPresent(String.self, forKey: .error)
        account = try c.decodeIfPresent(Account.self, forKey: .account)
        description = try c.decodeIfPresent(String.self, forKey: .description)
        capabilities = try c.decodeIfPresent(Capabilities.self, forKey: .capabilities) ?? Capabilities()
        target = try c.decodeIfPresent(ChannelTarget.self, forKey: .target)
        features = try c.decodeIfPresent([ChannelFeature].self, forKey: .features) ?? []
        bind = try c.decodeIfPresent(String.self, forKey: .bind) ?? "none"
        surfaces = try c.decodeIfPresent([String].self, forKey: .surfaces) ?? []
        lastEventAt = try c.decodeIfPresent(Date.self, forKey: .lastEventAt)
        readOnly = try c.decodeIfPresent(Bool.self, forKey: .readOnly) ?? false
    }

    var isApp: Bool { kind == "app" }
    var isMarket: Bool { source == "market" }
    /// Bound to its owner: connected, or broken with an account still on it.
    var isBound: Bool { state == "connected" || (state == "error" && account != nil) }
    /// It binds from the app (a QR code, a pairing code, fields), it is on and not bound yet.
    var canBind: Bool { bindsHere && enabled && !isBound }
    /// It has a binding step the app goes through (not none / config, not a market's, not read-only).
    var bindsHere: Bool { !readOnly && !isMarket && bind != "none" && bind != "config" }
    /// Its switch, where its messages go and its features are changed here.
    var editable: Bool { !readOnly && !isMarket }
}

/// Where a channel's messages go (`target`).
nonisolated struct ChannelTarget: Codable, Hashable, Sendable {
    /// main | new | conversation | agent (the Feishu bot's /agents, set from Feishu only)
    var kind: String
    var machineID: String?
    var sessionID: String?
    var title: String?

    static let main = ChannelTarget(kind: "main")
    static let new = ChannelTarget(kind: "new")
    static func conversation(_ key: SessionKey) -> ChannelTarget {
        ChannelTarget(kind: "conversation", machineID: key.machineID, sessionID: key.sessionID)
    }

    enum CodingKeys: String, CodingKey {
        case kind, title
        case machineID = "machine_id", sessionID = "session_id"
    }
}

/// A part of a channel switched on and off on its own (drawn as the hub lists them, known or not).
nonisolated struct ChannelFeature: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String
    var description: String?
    var on: Bool
}

/// GET /api/v1/channels/{id}: the channel with what waits on it, the conversations it went to lately, and its own full
/// page if it has one.
nonisolated struct HubChannelDetail: Decodable, Hashable, Sendable {
    var channel: HubChannel
    var waiting: Waiting
    var conversations: [Conversation]
    /// An app route ("settings/connectors/feishu-bot", "settings/plugins/<id>", "settings/model-config"): `ChannelText.page`.
    var settingsURL: String?

    nonisolated struct Waiting: Codable, Hashable, Sendable {
        var answers: Int
        var asks: Int
    }

    nonisolated struct Conversation: Codable, Hashable, Sendable, Identifiable {
        var machineID: String
        var sessionID: String
        var title: String?
        var at: Date?
        var id: String { machineID + "/" + sessionID }

        enum CodingKeys: String, CodingKey {
            case title, at
            case machineID = "machine_id", sessionID = "session_id"
        }
    }

    private enum CodingKeys: String, CodingKey {
        case waiting, conversations
        case settingsURL = "settings_url"
    }

    init(from decoder: Decoder) throws {
        channel = try HubChannel(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        waiting = try c.decodeIfPresent(Waiting.self, forKey: .waiting) ?? Waiting(answers: 0, asks: 0)
        conversations = try c.decodeIfPresent([Conversation].self, forKey: .conversations) ?? []
        settingsURL = try c.decodeIfPresent(String.self, forKey: .settingsURL)
    }
}

extension APIClient {
    /// GET /api/v1/channels (a hub without channels answers 404: callers take nothing).
    func channels() async throws -> [HubChannel] {
        struct List: Decodable { var channels: [HubChannel]? }
        let list: List = try await get("/api/v1/channels")
        return list.channels ?? []
    }
}

/// Where a message came from, by its client_msg_id (docs/extensions/channels.md「Source markers」): a channel's are `<channel>-<message
/// id>`, the app's own a UUID (its first part 8 hex digits, which no channel id can be).
nonisolated enum ChannelSource {
    /// The channel a message came from, when its prefix is a channel the hub has; the apps' channel is never a source.
    static func of(_ clientMsgID: String?, in list: [HubChannel]) -> HubChannel? {
        guard let head = prefix(clientMsgID) else { return nil }
        return list.first { $0.id == head && !$0.isApp }
    }

    /// Whether a message may have come from a channel: its id has a prefix that is not a UUID's.
    static func possible(_ clientMsgID: String?) -> Bool { prefix(clientMsgID) != nil }

    private static func prefix(_ clientMsgID: String?) -> Substring? {
        guard let id = clientMsgID, let dash = id.firstIndex(of: "-"), dash > id.startIndex else { return nil }
        let head = id[..<dash]
        if head.count == 8 && head.allSatisfy(\.isHexDigit) { return nil }
        return head
    }
}
