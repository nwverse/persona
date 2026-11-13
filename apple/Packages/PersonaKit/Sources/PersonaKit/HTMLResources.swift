import Foundation

/// The files an .html page refers to beside it (docs/apple/ios.md「File viewer」): read from the machine through the hub and
/// put into the page as data: URLs before it is shown, so pictures, style sheets (with their url() and @import),
/// fonts and scripts load in the isolated preview while the network stays off. URLs, anchors and absolute paths are
/// left as they are. At most `maxFiles` files, each up to `maxFileBytes`, `maxTotalBytes` in all; what is past that,
/// missing or unreadable stays a reference that does not load.
public nonisolated enum HTMLResources {
    public static let maxFiles = 64
    public static let maxFileBytes = 8 << 20
    public static let maxTotalBytes = 24 << 20

    /// Reads a file on the machine by its absolute path; nil when it is not there.
    public typealias Fetch = @Sendable (String) async -> Data?

    public struct Result: Sendable, Equatable {
        public var html: String
        /// Relative references read in, and those that could not be.
        public var loaded: Int
        public var missing: Int
    }

    /// A reference to a file beside the page: not a URL (scheme:), an anchor, or an absolute / protocol-relative path.
    public static func isRelative(_ ref: String) -> Bool {
        let r = ref.trimmingCharacters(in: .whitespaces)
        guard !r.isEmpty, !r.hasPrefix("#"), !r.hasPrefix("/") else { return false }
        if let colon = r.firstIndex(of: ":"), r[..<colon].allSatisfy({ $0.isLetter || $0.isNumber || "+.-".contains($0) }) { return false }
        return true
    }

    /// Where a relative reference points from the folder `dir` ("a/../b.png?v=1" → "dir/b.png").
    public static func resolve(_ dir: String, _ ref: String) -> String {
        var clean = ref.trimmingCharacters(in: .whitespaces)
        if let cut = clean.firstIndex(where: { $0 == "?" || $0 == "#" }) { clean = String(clean[..<cut]) }
        clean = clean.removingPercentEncoding ?? clean
        var parts = dir.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        for seg in clean.split(separator: "/", omittingEmptySubsequences: true) {
            if seg == ".." { if !parts.isEmpty { parts.removeLast() } } else if seg != "." { parts.append(String(seg)) }
        }
        return "/" + parts.joined(separator: "/")
    }

    public static func mime(_ path: String) -> String {
        switch (path as NSString).pathExtension.lowercased() {
        case "png": "image/png"
        case "jpg", "jpeg": "image/jpeg"
        case "gif": "image/gif"
        case "webp": "image/webp"
        case "svg": "image/svg+xml"
        case "ico": "image/x-icon"
        case "css": "text/css"
        case "js", "mjs": "text/javascript"
        case "woff2": "font/woff2"
        case "woff": "font/woff"
        case "ttf": "font/ttf"
        case "otf": "font/otf"
        case "mp3": "audio/mpeg"
        case "mp4": "video/mp4"
        case "json": "application/json"
        default: "application/octet-stream"
        }
    }

    /// The page with its files beside it read in; `dir` is the page's folder on the machine.
    public static func inline(_ html: String, dir: String, fetch: @escaping Fetch) async -> Result {
        let reader = Reader(fetch: fetch)
        var out = html
        // Style sheets first (a <link> becomes a <style> with its own references read in), then the attributes.
        for (tag, href) in matches(#"<link\b[^>]*>"#, in: out).compactMap({ t in attribute("href", in: t).map { (t, $0) } })
        where isRelative(href) && (attribute("rel", in: tag)?.lowercased().contains("stylesheet") ?? false) {
            let path = resolve(dir, href)
            guard let css = await reader.text(path) else { continue }
            let sheet = await inlineCSS(css, dir: (path as NSString).deletingLastPathComponent, reader: reader, depth: 0)
            out = out.replacingOccurrences(of: tag, with: "<style>\n" + sheet.replacingOccurrences(of: "</style", with: "<\\/style") + "\n</style>")
        }
        for (tag, src) in matches(#"<(?:img|script|source|audio|video|input)\b[^>]*>"#, in: out).compactMap({ t in attribute("src", in: t).map { (t, $0) } })
        where isRelative(src) {
            guard let url = await reader.dataURL(resolve(dir, src)) else { continue }
            out = out.replacingOccurrences(of: tag, with: replaceAttribute("src", in: tag, with: url))
        }
        for block in matches(#"<style\b[^>]*>[\s\S]*?</style>"#, in: out) {
            let sheet = await inlineCSS(block, dir: dir, reader: reader, depth: 0)
            if sheet != block { out = out.replacingOccurrences(of: block, with: sheet) }
        }
        return Result(html: out, loaded: await reader.loaded, missing: await reader.missing)
    }

    /// A style sheet's url(…) read in, its @import "x.css" put in its place (three levels deep at most).
    private static func inlineCSS(_ css: String, dir: String, reader: Reader, depth: Int) async -> String {
        var out = css
        if depth < 3 {
            for (rule, ref) in groups(#"@import\s+(?:url\(\s*)?["']?([^"')\s;]+)["']?\s*\)?[^;]*;"#, in: out) where isRelative(ref) {
                let path = resolve(dir, ref)
                guard let sub = await reader.text(path) else { continue }
                let inner = await inlineCSS(sub, dir: (path as NSString).deletingLastPathComponent, reader: reader, depth: depth + 1)
                out = out.replacingOccurrences(of: rule, with: inner)
            }
        }
        for (whole, ref) in groups(#"url\(\s*["']?([^"')]+)["']?\s*\)"#, in: out) where isRelative(ref) {
            guard let url = await reader.dataURL(resolve(dir, ref)) else { continue }
            out = out.replacingOccurrences(of: whole, with: "url(\"\(url)\")")
        }
        return out
    }

    /// Reads each file once, within the limits.
    private actor Reader {
        let fetch: Fetch
        var cache: [String: Data?] = [:]
        var total = 0
        var loaded = 0
        var missing = 0
        init(fetch: @escaping Fetch) { self.fetch = fetch }

        func data(_ path: String) async -> Data? {
            if let hit = cache[path] { return hit }
            guard cache.count < HTMLResources.maxFiles, total < HTMLResources.maxTotalBytes else {
                missing += 1
                return nil
            }
            let d = await fetch(path)
            let ok = d.map { $0.count <= HTMLResources.maxFileBytes && total + $0.count <= HTMLResources.maxTotalBytes } ?? false
            cache[path] = ok ? d : nil
            if ok, let d { total += d.count; loaded += 1 } else { missing += 1 }
            return ok ? d : nil
        }

        func text(_ path: String) async -> String? {
            await data(path).map { String(decoding: $0, as: UTF8.self) }
        }

        func dataURL(_ path: String) async -> String? {
            await data(path).map { "data:\(HTMLResources.mime(path));base64,\($0.base64EncodedString())" }
        }
    }

    // MARK: Markup

    public static func matches(_ pattern: String, in s: String) -> [String] {
        guard let re = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]) else { return [] }
        let ns = s as NSString
        return Array(Set(re.matches(in: s, range: NSRange(location: 0, length: ns.length)).map { ns.substring(with: $0.range) }))
    }

    /// Each match and its first group.
    public static func groups(_ pattern: String, in s: String) -> [(String, String)] {
        guard let re = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]) else { return [] }
        let ns = s as NSString
        var seen = Set<String>()
        return re.matches(in: s, range: NSRange(location: 0, length: ns.length)).compactMap { m in
            let whole = ns.substring(with: m.range)
            guard m.numberOfRanges > 1, m.range(at: 1).location != NSNotFound, seen.insert(whole).inserted else { return nil }
            return (whole, ns.substring(with: m.range(at: 1)))
        }
    }

    /// An attribute's value, double-quoted, single-quoted or bare.
    public static func attribute(_ name: String, in tag: String) -> String? {
        guard let re = try? NSRegularExpression(pattern: #"\s"# + name + #"\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))"#,
                                                options: [.caseInsensitive]) else { return nil }
        let ns = tag as NSString
        guard let m = re.firstMatch(in: tag, range: NSRange(location: 0, length: ns.length)) else { return nil }
        for i in 1..<m.numberOfRanges where m.range(at: i).location != NSNotFound { return ns.substring(with: m.range(at: i)) }
        return nil
    }

    public static func replaceAttribute(_ name: String, in tag: String, with value: String) -> String {
        guard let re = try? NSRegularExpression(pattern: #"(\s"# + name + #"\s*=\s*)(?:"[^"]*"|'[^']*'|[^\s>]+)"#, options: [.caseInsensitive]) else { return tag }
        let ns = tag as NSString
        guard let m = re.firstMatch(in: tag, range: NSRange(location: 0, length: ns.length)) else { return tag }
        return ns.replacingCharacters(in: m.range, with: ns.substring(with: m.range(at: 1)) + "\"" + value + "\"")
    }
}
