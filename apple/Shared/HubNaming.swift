import Foundation

/// Wire readers accept both generations. Writers without a hello keep the pre-rename contract;
/// capability selection belongs to each hub link, never to a process-wide or persisted switch.
nonisolated enum HubNaming {
    static let feature = "persona_contracts_v1"
    static var productPattern: String { "(?:persona|" + NSRegularExpression.escapedPattern(for: LegacyNames.product) + ")" }
    static var contextPattern: String { productPattern + "-context" }
    static var filePattern: String { productPattern + "-file" }
    private static let agyMCPTool = try! NSRegularExpression(
        pattern: "^mcp_" + productPattern + "(?:_[0-9a-fA-F]{8,16})?_([a-z][a-z0-9_]*)$"
    )
    private static let agyToolResultHeader = try! NSRegularExpression(
        pattern: #"^(?:Created At: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\r?\nCompleted At: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})(?:\r?\n|$))+"#
    )

    /// Normalizes Antigravity's single-underscore Persona MCP names (`mcp_persona_<tool>`, `mcp_persona_<hex>_<tool>`)
    /// to `mcp__persona__<tool>`.
    static func canonicalMCPTool(_ name: String) -> String {
        guard name.hasPrefix("mcp_"), !name.hasPrefix("mcp__") else { return name }
        let range = NSRange(name.startIndex..., in: name)
        guard let match = agyMCPTool.firstMatch(in: name, range: range),
              let toolRange = Range(match.range(at: 1), in: name) else { return name }
        return "mcp__persona__" + name[toolRange]
    }

    /// Strips leading Antigravity `Created At:` / `Completed At:` RFC3339 wrapper header pairs (and an immediately
    /// following `Output:` line when preceded by those headers) so persisted JSON tool results still parse cleanly.
    static func stripToolResultTimestampHeaders(_ text: String) -> String {
        guard text.hasPrefix("Created At: ") else { return text }
        let range = NSRange(text.startIndex..., in: text)
        guard let match = agyToolResultHeader.firstMatch(in: text, range: range),
              let headerRange = Range(match.range, in: text) else { return text }
        let rest = text[headerRange.upperBound...]
        if rest.hasPrefix("Output:\r\n") { return String(rest.dropFirst("Output:\r\n".count)) }
        if rest.hasPrefix("Output:\n") { return String(rest.dropFirst("Output:\n".count)) }
        return String(rest)
    }

    static func accepts(scheme: String?) -> Bool {
        guard let scheme = scheme?.lowercased() else { return false }
        return scheme == "persona" || scheme == LegacyNames.urlScheme
    }

    #if !WIDGET_EXTENSION
    static func operationHeaders(_ id: UUID) -> [String: String] {
        let value = id.uuidString.lowercased()
        return [OpRetry.header: value, LegacyNames.operationIDHeader: value]
    }
    #endif

    struct ContextBlockSpan: Sendable {
        let start: Int
        let openEnd: Int
        let closeAt: Int
        let end: Int
        let attributes: String
        let body: String
    }

    private struct ContextTagToken {
        let start: Int
        let end: Int
        let isClose: Bool
        let attributes: String
    }

    private static let contextToken = try! NSRegularExpression(
        pattern: "<" + contextPattern + #"(?:\s+[^<>]*)?>|</"# + contextPattern + #"\s*>"#
    )
    private static let legacyOpenTag = try! NSRegularExpression(pattern: #"^<persona-context(?=[\s>])"#)
    private static let legacyCloseTag = try! NSRegularExpression(pattern: #"^</persona-context(?=[\s>])"#)
    private static let legacyFileTag = try! NSRegularExpression(pattern: #"(<\s*/?\s*)persona-file(?=[\s/>])"#)

    /// Depth-aware linear O(n) scanner for top-level context blocks; recovers 2-opens-1-close historical blocks too.
    static func findContextBlocks(_ text: String) -> [ContextBlockSpan] {
        guard text.contains("-context") else { return [] }
        let ns = text as NSString
        let matches = contextToken.matches(in: text, range: NSRange(location: 0, length: ns.length))
        guard matches.count >= 2 else { return [] }
        var tokens: [ContextTagToken] = []
        tokens.reserveCapacity(matches.count)
        var hasClose = false
        for m in matches {
            let start = m.range.location
            let end = NSMaxRange(m.range)
            let tok = ns.substring(with: m.range)
            if tok.hasPrefix("</") {
                tokens.append(ContextTagToken(start: start, end: end, isClose: true, attributes: ""))
                hasClose = true
                continue
            }
            if tok.hasSuffix("/>") { continue }
            let tagNS = tok as NSString
            var nameEnd = 1
            while nameEnd < tagNS.length {
                let c = tagNS.character(at: nameEnd)
                if c == 0x20 || c == 0x09 || c == 0x0A || c == 0x0D || c == 0x3E { break }
                nameEnd += 1
            }
            let attrs = nameEnd < tagNS.length - 1
                ? tagNS.substring(with: NSRange(location: nameEnd, length: tagNS.length - 1 - nameEnd))
                : ""
            tokens.append(ContextTagToken(start: start, end: end, isClose: false, attributes: attrs))
        }
        guard hasClose, tokens.count >= 2 else { return [] }
        let count = tokens.count
        var bal = [Int](repeating: 0, count: count + 1)
        for i in 0..<count {
            bal[i + 1] = bal[i] + (tokens[i].isClose ? -1 : 1)
        }
        var matchIdx = [Int](repeating: -1, count: count)
        var nextAtBal: [Int: Int] = [:]
        nextAtBal.reserveCapacity(count)
        var minVal = count + 1
        var minIdx = -1
        for i in stride(from: count - 1, through: 0, by: -1) {
            if tokens[i].isClose {
                let v = bal[i + 1]
                nextAtBal[v] = i
                if v <= minVal {
                    minVal = v
                    minIdx = i
                }
                continue
            }
            matchIdx[i] = nextAtBal[bal[i]] ?? minIdx
        }
        var blocks: [ContextBlockSpan] = []
        var i = 0
        while i < count {
            if tokens[i].isClose || matchIdx[i] < 0 {
                i += 1
                continue
            }
            let j = matchIdx[i]
            let body = ns.substring(with: NSRange(location: tokens[i].end, length: tokens[j].start - tokens[i].end))
            blocks.append(ContextBlockSpan(
                start: tokens[i].start,
                openEnd: tokens[i].end,
                closeAt: tokens[j].start,
                end: tokens[j].end,
                attributes: tokens[i].attributes,
                body: body
            ))
            i = j + 1
        }
        return blocks
    }

    static func findLeadingContextBlocks(_ text: String) -> (blocks: [ContextBlockSpan], endUTF16: Int) {
        let blocks = findContextBlocks(text)
        guard !blocks.isEmpty else { return ([], 0) }
        let ns = text as NSString
        var pos = 0
        var count = 0
        for b in blocks {
            let gap = ns.substring(with: NSRange(location: pos, length: b.start - pos))
            if !gap.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { break }
            count += 1
            pos = b.end
        }
        return (Array(blocks.prefix(count)), pos)
    }

    static func stripLeadingContextBlocks(_ text: String) -> String {
        let (_, endUTF16) = findLeadingContextBlocks(text)
        return endUTF16 > 0 ? (text as NSString).substring(from: endUTF16) : text
    }

    static func stripContextBlocks(_ text: String, replacement: String = "", trimTrailingWhitespace: Bool = true) -> String {
        let blocks = findContextBlocks(text)
        guard !blocks.isEmpty else { return text }
        let ns = text as NSString
        var out = ""
        var prev = 0
        for b in blocks {
            if b.start < prev { continue }
            out += ns.substring(with: NSRange(location: prev, length: b.start - prev))
            out += replacement
            var end = b.end
            if trimTrailingWhitespace {
                while end < ns.length && isASCIIOrUnicodeSpace(ns.character(at: end)) {
                    end += 1
                }
            }
            prev = end
        }
        out += ns.substring(from: prev)
        return out
    }

    private static func isASCIIOrUnicodeSpace(_ unit: unichar) -> Bool {
        unit == 0x20 || unit == 0x09 || unit == 0x0A || unit == 0x0D || unit == 0x0C || unit == 0x0B || unit == 0xA0 || unit == 0x3000
    }

    private static func rewriteFileTags(_ segment: String) -> String {
        let ns = segment as NSString
        return legacyFileTag.stringByReplacingMatches(
            in: segment,
            range: NSRange(location: 0, length: ns.length),
            withTemplate: "$1" + LegacyNames.product + "-file"
        )
    }

    /// Rewrite only outer matched protocol context block tags and standalone file tags, never literal tags inside
    /// encapsulated bodies or ordinary user prose.
    static func outbound(_ text: String, canonical: Bool) -> String {
        guard !canonical, text.contains("persona-") else { return text }
        let blocks = findContextBlocks(text)
        guard !blocks.isEmpty else { return rewriteFileTags(text) }
        let ns = text as NSString
        var out = ""
        var prev = 0
        for b in blocks {
            if b.start > prev {
                out += rewriteFileTags(ns.substring(with: NSRange(location: prev, length: b.start - prev)))
            }
            let rawOpen = ns.substring(with: NSRange(location: b.start, length: b.openEnd - b.start))
            let openTag = legacyOpenTag.stringByReplacingMatches(
                in: rawOpen,
                range: NSRange(location: 0, length: (rawOpen as NSString).length),
                withTemplate: "<" + LegacyNames.product + "-context"
            )
            let rawClose = ns.substring(with: NSRange(location: b.closeAt, length: b.end - b.closeAt))
            let closeTag = legacyCloseTag.stringByReplacingMatches(
                in: rawClose,
                range: NSRange(location: 0, length: (rawClose as NSString).length),
                withTemplate: "</" + LegacyNames.product + "-context"
            )
            out += openTag + b.body + closeTag
            prev = b.end
        }
        if prev < ns.length {
            out += rewriteFileTags(ns.substring(from: prev))
        }
        return out
    }

    /// Dynamic compatibility keys stay outside the canonical Codable keys, so local snapshots encode canonical names.
    struct Field: CodingKey {
        let stringValue: String
        var intValue: Int? { nil }
        init(_ name: String) { stringValue = name }
        init?(stringValue: String) { self.init(stringValue) }
        init?(intValue: Int) { return nil }
    }
}
