import Foundation

/// Where requests for a hub actually go (docs/operations/network.md「经 SSH 隧道连 hub」). A hub the app reaches through its SSH
/// tunnel keeps its own address everywhere — in the registry, in `LiveConnection.currentConfig`, in every comparison —
/// and only the URLs put on the wire (`APIClient`, the socket, port forwards) are turned into the tunnel's loopback
/// address here. Nothing routed: every URL is what it was.
///
/// The app sets the routes (SSHTunnels); everything else only reads them, from any thread. The watch and the
/// extensions never route anything.
nonisolated enum HubTransport {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var routes: [String: URL] = [:]

    /// A hub address as the routes key it: without the trailing slash.
    static func key(_ url: URL) -> String {
        url.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    }

    /// The tunnel's address for requests to `hub` from now on; nil: straight to the hub again.
    static func route(_ hub: URL, via local: URL?) {
        lock.lock()
        defer { lock.unlock() }
        routes[key(hub)] = local
    }

    /// Requests to `hub` go straight to it again, unless another tunnel routes them by now: a hub can have more than
    /// one way through, and closing one must not take the route of the one that replaced it.
    static func unroute(_ hub: URL, from local: URL) {
        lock.lock()
        defer { lock.unlock() }
        if routes[key(hub)] == local { routes[key(hub)] = nil }
    }

    /// The tunnel requests to `hub` go through, nil when they go straight to it.
    static func via(_ hub: URL) -> URL? {
        lock.lock()
        defer { lock.unlock() }
        return routes[key(hub)]
    }

    static func clearAll() {
        lock.lock()
        defer { lock.unlock() }
        routes = [:]
    }

    /// `url` (the hub's address or anything under it) as it is reached: the tunnel's scheme, host and port when the hub
    /// is routed through one, the path and query kept.
    static func resolve(_ url: URL, hub: URL) -> URL {
        guard let local = via(hub), var comps = URLComponents(url: url, resolvingAgainstBaseURL: false),
              let target = URLComponents(url: local, resolvingAgainstBaseURL: false) else { return url }
        // A WebSocket keeps its kind: ws(s) to the hub is ws through the tunnel (the tunnel itself is plain TCP).
        let ws = comps.scheme == "ws" || comps.scheme == "wss"
        comps.scheme = ws ? (target.scheme == "https" ? "wss" : "ws") : target.scheme
        comps.host = target.host
        comps.port = target.port
        return comps.url ?? url
    }
}

/// A paired hub's SSH tunnel (Settings › hub › SSH Tunnel): the server, who logs in, where the hub is from there, and the
/// server's key the user confirmed. Kept with the pairing (the keychain); this device's own key is kept apart
/// (SSHKeys), one per device.
nonisolated struct SSHRoute: Codable, Hashable, Sendable {
    var host: String
    var port: Int = 22
    /// As typed; a Warpgate login (`user:target`) included.
    var user: String
    /// The hub's address as the SSH server sees it.
    var targetHost: String = SSHRoute.defaultTargetHost
    var targetPort: Int = SSHRoute.defaultTargetPort
    /// `SHA256:…` of the server's key, once the user confirmed it; nil before (the tunnel is not used then).
    var hostKey: String?
    /// The key's algorithm as OpenSSH prints it (`ED25519`), for showing the fingerprint.
    var hostKeyAlgorithm: String?
    /// Through the tunnel every time, not only when the hub's own addresses do not answer.
    var always = false

    static let defaultTargetHost = "127.0.0.1"
    static let defaultTargetPort = 18660

    init(host: String, port: Int = 22, user: String, targetHost: String = SSHRoute.defaultTargetHost,
         targetPort: Int = SSHRoute.defaultTargetPort, hostKey: String? = nil, hostKeyAlgorithm: String? = nil, always: Bool = false) {
        self.host = host
        self.port = port
        self.user = user
        self.targetHost = targetHost
        self.targetPort = targetPort
        self.hostKey = hostKey
        self.hostKeyAlgorithm = hostKeyAlgorithm
        self.always = always
    }

    /// Ready to carry traffic: a server, a user and a confirmed key.
    var isUsable: Bool { !host.isEmpty && !user.isEmpty && (1...65535).contains(port) && hostKey?.isEmpty == false }

    /// `user@host` (`:port` when not 22), what lists show.
    var label: String { "\(user)@\(host)" + (port == 22 ? "" : ":\(port)") }

    /// The same server, user and target: a confirmed key still belongs to it. Another server needs its key confirmed.
    func sameServer(_ other: SSHRoute) -> Bool { host == other.host && port == other.port }

    enum CodingKeys: String, CodingKey {
        case host, port, user, always
        case targetHost = "target_host", targetPort = "target_port", hostKey = "host_key", hostKeyAlgorithm = "host_key_algorithm"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        host = try c.decode(String.self, forKey: .host)
        user = try c.decode(String.self, forKey: .user)
        port = try c.decodeIfPresent(Int.self, forKey: .port) ?? 22
        targetHost = try c.decodeIfPresent(String.self, forKey: .targetHost) ?? Self.defaultTargetHost
        targetPort = try c.decodeIfPresent(Int.self, forKey: .targetPort) ?? Self.defaultTargetPort
        hostKey = try c.decodeIfPresent(String.self, forKey: .hostKey)
        hostKeyAlgorithm = try c.decodeIfPresent(String.self, forKey: .hostKeyAlgorithm)
        always = try c.decodeIfPresent(Bool.self, forKey: .always) ?? false
    }
}

/// A paired hub's in-app tunnel (Settings › hub › the tunnel's name, docs/operations/network.md「经 App 内隧道连 hub」): what the hub's
/// tunnel extension says (its name, where the hub is from inside the network the tunnel reaches) and whether to go
/// through it every time. The account the tunnel signs in with is the core's own (one per device, in the app's
/// container), not part of the pairing.
nonisolated struct InAppTunnelRoute: Codable, Hashable, Sendable {
    /// The hub's address as the tunnel's network sees it: a host name (resolved by that network's DNS) or an IP.
    var targetHost: String
    var targetPort: Int = InAppTunnelRoute.defaultTargetPort
    /// The name the hub's extension gives the tunnel (contributes.tunnels[].name); empty when it gave none.
    var name: String = ""
    /// Through the tunnel every time, not only when the hub's own addresses do not answer.
    var always = false

    static let defaultTargetPort = 18660

    init(targetHost: String, targetPort: Int = InAppTunnelRoute.defaultTargetPort, name: String = "", always: Bool = false) {
        self.targetHost = targetHost
        self.targetPort = targetPort
        self.name = name
        self.always = always
    }

    var isUsable: Bool { !targetHost.trimmingCharacters(in: .whitespaces).isEmpty && (1...65535).contains(targetPort) }

    /// What the app calls the tunnel: the hub's name for it, else 「内网隧道」.
    var title: String {
        let n = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return n.isEmpty ? String(localized: "Tunnel [Hub Transport e248d4]") : n
    }

    /// `host:port` as the core's forward takes it (IPv6 in brackets).
    var target: String {
        let host = targetHost.trimmingCharacters(in: .whitespaces)
        return (host.contains(":") ? "[\(host)]" : host) + ":\(targetPort)"
    }

    enum CodingKeys: String, CodingKey {
        case always, name
        case targetHost = "target_host", targetPort = "target_port"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        targetHost = try c.decode(String.self, forKey: .targetHost)
        targetPort = try c.decodeIfPresent(Int.self, forKey: .targetPort) ?? Self.defaultTargetPort
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        always = try c.decodeIfPresent(Bool.self, forKey: .always) ?? false
    }
}
