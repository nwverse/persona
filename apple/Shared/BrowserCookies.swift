import Foundation

/// Cookies the user picked in a file, for importing into ego lite on a Mac (#1304,
/// docs/plans/plan-ego-browser.md「Importing cookies」): parsed here on the device, grouped by site for the user to tick, and sent
/// straight to the hub, which passes them to the Mac and keeps nothing. Nothing here stores, logs or shows a value. The web's lib/cookies.ts does
/// the same; BrowserCookiesTests holds them to the same cases.
nonisolated struct BrowserCookie: Codable, Hashable, Sendable {
    var name: String
    var value: String
    var domain: String
    var path: String? = nil
    /// Unix seconds; nil for a session cookie.
    var expires: Double? = nil
    var secure: Bool? = nil
    var httpOnly: Bool? = nil
    var sameSite: String? = nil // Strict | Lax | None
    var hostOnly: Bool? = nil

    enum CodingKeys: String, CodingKey {
        case name, value, domain, path, expires, secure
        case httpOnly = "http_only", sameSite = "same_site", hostOnly = "host_only"
    }
}

/// A site with cookies in ego lite now (GET …/browser/cookies): never a name or a value.
nonisolated struct BrowserCookieSite: Codable, Hashable, Sendable, Identifiable {
    var domain: String
    var count: Int
    var expires: Date? = nil
    var id: String { domain }
}

/// What the hub answers about the sign-ins: the sites now, and what an import or a clear did.
nonisolated struct BrowserCookies: Codable, Sendable {
    var sites: [BrowserCookieSite]
    var imported: Int? = nil
    var skipped: Int? = nil
    var removed: Int? = nil
}

/// A site in a picked file: its cookies, how many had already expired (not imported), when the last one expires.
nonisolated struct CookieFileSite: Hashable, Sendable, Identifiable {
    var site: String
    var cookies: [BrowserCookie] = []
    var expired = 0
    var expires: Double? = nil
    var id: String { site }
}

nonisolated enum CookieFile {
    enum Failure: LocalizedError {
        case unreadable, empty(json: Bool)
        var errorDescription: String? {
            switch self {
            case .unreadable: String(localized: "Can't read this file: it's neither cookies.txt nor JSON")
            case .empty(let json): json ? String(localized: "No cookies in the JSON") : String(localized: "No cookies in the file (expected cookies.txt: 7 tab-separated columns per line)")
            }
        }
    }

    /// A cookie file: JSON (Cookie-Editor / EditThisCookie, or `{cookies: [...]}`) when it looks like JSON, else
    /// Netscape cookies.txt. `bad`: lines or entries that are no cookie.
    static func parse(_ text: String) throws -> (cookies: [BrowserCookie], bad: Int) {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines.union(CharacterSet(charactersIn: "\u{FEFF}")))
        let isJSON = t.hasPrefix("[") || t.hasPrefix("{")
        let out = isJSON ? try json(t) : netscape(t)
        if out.cookies.isEmpty { throw Failure.empty(json: isJSON) }
        return out
    }

    private static func netscape(_ text: String) -> (cookies: [BrowserCookie], bad: Int) {
        var cookies: [BrowserCookie] = []
        var bad = 0
        for raw in text.components(separatedBy: .newlines) {
            var line = raw
            var httpOnly = false
            if line.hasPrefix("#HttpOnly_") {
                httpOnly = true
                line = String(line.dropFirst("#HttpOnly_".count))
            } else if line.hasPrefix("#") || line.trimmingCharacters(in: .whitespaces).isEmpty {
                continue
            }
            let f = line.components(separatedBy: "\t")
            guard f.count >= 7, !f[0].isEmpty, !f[5].isEmpty else {
                bad += 1
                continue
            }
            let exp = Double(f[4]) ?? 0
            cookies.append(BrowserCookie(name: f[5], value: f[6...].joined(separator: "\t"), domain: f[0], path: f[2].isEmpty ? "/" : f[2],
                                         expires: exp > 0 ? exp : nil, secure: f[3].uppercased() == "TRUE", httpOnly: httpOnly,
                                         hostOnly: f[1].uppercased() != "TRUE" && !f[0].hasPrefix(".")))
        }
        return (cookies, bad)
    }

    private static func json(_ text: String) throws -> (cookies: [BrowserCookie], bad: Int) {
        guard let data = text.data(using: .utf8), let any = try? JSONSerialization.jsonObject(with: data) else { throw Failure.unreadable }
        guard let list = (any as? [Any]) ?? ((any as? [String: Any])?["cookies"] as? [Any]) else { throw Failure.empty(json: true) }
        var cookies: [BrowserCookie] = []
        var bad = 0
        for item in list {
            guard let c = item as? [String: Any], let name = c["name"] as? String, !name.isEmpty, let value = c["value"] as? String,
                  let domain = (c["domain"] as? String) ?? (c["host"] as? String), !domain.isEmpty else {
                bad += 1
                continue
            }
            let raw = (c["expirationDate"] ?? c["expires"] ?? c["expiry"]) as? NSNumber
            var exp = raw?.doubleValue ?? 0
            if exp > 1e11 { exp /= 1000 } // milliseconds in some exports
            let session = (c["session"] as? Bool) == true
            let same = ((c["sameSite"] ?? c["same_site"]) as? String).flatMap { s -> String? in
                switch s.lowercased() {
                case "strict": "Strict"
                case "lax": "Lax"
                case "none", "no_restriction": "None"
                default: nil
                }
            }
            cookies.append(BrowserCookie(name: name, value: value, domain: domain, path: (c["path"] as? String) ?? "/",
                                         expires: session || exp <= 0 ? nil : exp.rounded(.down), secure: (c["secure"] as? Bool) == true,
                                         httpOnly: (c["httpOnly"] as? Bool) == true || (c["http_only"] as? Bool) == true, sameSite: same,
                                         hostOnly: (c["hostOnly"] as? Bool) == true || (c["host_only"] as? Bool) == true))
        }
        return (cookies, bad)
    }

    /// The site a cookie domain belongs to, as people know it: example.com for .www.example.com; two-part suffixes
    /// (example.co.uk, example.com.cn) keep three labels. The node groups the same way with the public suffix list.
    static func site(of domain: String) -> String {
        let host = (domain.hasPrefix(".") ? String(domain.dropFirst()) : domain).lowercased()
        let parts = host.split(separator: ".").map(String.init)
        guard parts.count > 2, Int(parts.last ?? "") == nil else { return host }
        let two = parts.suffix(2).joined(separator: ".")
        let second = two.range(of: #"^(co|com|net|org|gov|edu|ac|ne|or|go)\.[a-z]{2}$"#, options: .regularExpression) != nil
        return parts.suffix(second ? 3 : 2).joined(separator: ".")
    }

    /// The file's cookies by site, the sites by name; expired ones counted, not kept.
    static func sites(_ cookies: [BrowserCookie], now: Date = .now) -> [CookieFileSite] {
        var by: [String: CookieFileSite] = [:]
        let t = now.timeIntervalSince1970
        for c in cookies {
            let s = site(of: c.domain)
            guard !s.isEmpty else { continue }
            var e = by[s] ?? CookieFileSite(site: s)
            if let exp = c.expires, exp < t {
                e.expired += 1
            } else {
                e.cookies.append(c)
                if let exp = c.expires, exp > (e.expires ?? 0) { e.expires = exp }
            }
            by[s] = e
        }
        return by.values.sorted { $0.site < $1.site }
    }
}
