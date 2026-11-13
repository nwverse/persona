import Foundation

/// ⌘K's Files (docs/apple/mac.md「Quick Open and New」): the files under the open conversation's directory, listed by the node
/// (`GET /machines/{mid}/fs/find`, one request when the panel opens) and matched here as the user types. The same
/// matcher as the web's (`web/src/lib/file-find.ts`): every word has to match in order; word starts, path separators,
/// camelCase humps, consecutive characters and the file's own name score higher; of two equal matches the shorter
/// path first.
nonisolated enum MacFileFind {
    /// The most files one search lists.
    static let maxHits = 50

    struct Hit: Hashable, Sendable {
        /// The path as the node listed it: relative to the directory, with `/`.
        var path: String
        var score: Double
        /// UTF-16 offsets into `path` of the characters that matched (ascending), to draw them.
        var matched: [Int]
    }

    // What a character matched is worth (the web's weights, so both rank alike).
    private static let wordStart = 10.0
    private static let afterSlash = 12.0
    private static let camel = 9.0
    private static let firstChar = 12.0
    private static let consecutive = 6.0
    private static let gap = 1.2 // per character skipped between two matches
    private static let lead = 0.15 // per character of folders before the first match
    private static let inName = 3.0
    private static let nameExact = 30.0
    private static let namePrefix = 12.0
    private static let short = 0.02 // per character of the whole path
    private static let neg = -1e9
    private static let slash = UInt16(UInt8(ascii: "/"))
    private static let separators: Set<UInt16> = Set("/\\.-_ ".utf16)

    /// The words of what was typed, lower case.
    static func terms(_ query: String) -> [String] {
        query.split(whereSeparator: \.isWhitespace).map { String($0).lowercased() }
    }

    /// One listed file, ready to be matched many times: its characters, lower case, and what each is worth.
    struct Entry: Sendable {
        let path: String
        let lower: [UInt16]
        let nameStart: Int
        /// Per character: what matching it is worth by where it stands, the file name's bonus included.
        let gain: [Double]

        init(_ path: String) {
            self.path = path
            let units = Array(path.utf16)
            let lower = units.map(MacFileFind.lower)
            self.lower = lower
            let nameStart = (units.lastIndex(of: MacFileFind.slash) ?? -1) + 1
            self.nameStart = nameStart
            gain = units.indices.map { i in
                MacFileFind.boundary(units, lower, i) + (i >= nameStart ? MacFileFind.inName : 0)
            }
        }
    }

    /// A UTF-16 unit in lower case when that is one unit too (ASCII and most letters); else as it is.
    static func lower(_ u: UInt16) -> UInt16 {
        if u >= 65 && u <= 90 { return u + 32 }
        guard u >= 128, let s = Unicode.Scalar(u) else { return u }
        let l = s.properties.lowercaseMapping.utf16
        return l.count == 1 ? l.first! : u
    }

    private static func boundary(_ units: [UInt16], _ lower: [UInt16], _ i: Int) -> Double {
        if i == 0 { return firstChar }
        let prev = units[i - 1]
        if prev == slash { return afterSlash }
        if separators.contains(prev) { return wordStart }
        // A hump: an upper-case letter after a lower-case one.
        let up = units[i] != lower[i]
        let prevLower = prev == lower[i - 1] && Unicode.Scalar(prev)?.properties.isLowercase == true
        return up && prevLower ? camel : 0
    }

    /// The best way `term` can be laid over `e`: its score and, when asked, where its characters land. A dynamic
    /// program over (character of the term, position in the path), so "App" in "src/AppView/app.ts" finds the hump of
    /// AppView rather than the first scattering of a, p, p. nil when it does not fit.
    private static func align(_ e: Entry, _ term: [UInt16], positions: Bool) -> (score: Double, at: [Int])? {
        let lower = e.lower, n = lower.count, m = term.count
        guard m > 0, n >= m else { return nil }
        var k = 0
        for c in lower where k < m && c == term[k] { k += 1 }
        guard k == m else { return nil }
        var prev = [Double](repeating: neg, count: n), cur = [Double](repeating: neg, count: n)
        var from = positions ? [Int32](repeating: -1, count: n * m) : []
        for i in 0..<m {
            let ch = term[i]
            var far = neg, farAt = -1 // the previous character's best at j-2 or earlier, less a gap per skip
            for j in 0..<n {
                if i > 0 && j >= 2 {
                    let decayed = far > neg / 2 ? far - gap : neg
                    let fresh = prev[j - 2] > neg / 2 ? prev[j - 2] - gap : neg
                    if fresh > decayed { far = fresh; farAt = j - 2 } else { far = decayed }
                }
                guard lower[j] == ch else { cur[j] = neg; continue }
                let gain = e.gain[j]
                if i == 0 {
                    cur[j] = gain - (j < e.nameStart ? lead * Double(j) : 0)
                    continue
                }
                let adj = j >= 1 && prev[j - 1] > neg / 2 ? prev[j - 1] + consecutive : neg
                if adj <= neg / 2 && far <= neg / 2 { cur[j] = neg; continue }
                if adj >= far {
                    cur[j] = gain + adj
                    if positions { from[i * n + j] = Int32(j - 1) }
                } else {
                    cur[j] = gain + far
                    if positions { from[i * n + j] = Int32(farAt) }
                }
            }
            swap(&prev, &cur)
        }
        var top = neg, end = -1
        for j in 0..<n where prev[j] > top { top = prev[j]; end = j }
        guard end >= 0 else { return nil }
        var at: [Int] = []
        if positions {
            var j = end
            for i in stride(from: m - 1, through: 0, by: -1) {
                at.append(j)
                j = Int(from[i * n + j])
            }
            at.reverse()
        }
        return (top, at)
    }

    /// How well `e` matches all of `terms`, or nil when one of them does not.
    static func score(_ e: Entry, _ terms: [[UInt16]], positions: Bool) -> Hit? {
        let name = e.lower[e.nameStart...]
        var score = 0.0
        var marks = Set<Int>()
        for term in terms {
            guard let a = align(e, term, positions: positions) else { return nil }
            score += a.score
            if name.elementsEqual(term) { score += nameExact } else if name.starts(with: term) { score += namePrefix }
            marks.formUnion(a.at)
        }
        return Hit(path: e.path, score: score - short * Double(e.lower.count), matched: marks.sorted())
    }

    static func ranked(_ a: Hit, _ b: Hit) -> Bool {
        if a.score != b.score { return a.score > b.score }
        let (la, lb) = (a.path.utf16.count, b.path.utf16.count)
        return la != lb ? la < lb : a.path < b.path
    }

    /// A search over one list of files that remembers the last query's matches: a query that continues the last one
    /// (more letters, another word) can only match some of those, so it looks at them and not the whole list again.
    final class Search: @unchecked Sendable {
        let entries: [Entry]
        private var lastQuery = ""
        private var lastMatches: [Int]

        init(_ files: [String]) {
            entries = files.map(Entry.init)
            lastMatches = Array(entries.indices)
        }

        /// The best `limit` matches, best first. An empty query matches nothing.
        func search(_ query: String, limit: Int = MacFileFind.maxHits) -> [Hit] {
            let words = MacFileFind.terms(query)
            let key = words.joined(separator: " ")
            guard !key.isEmpty else {
                lastQuery = ""
                lastMatches = Array(entries.indices)
                return []
            }
            let terms = words.map { $0.utf16.map(MacFileFind.lower) }
            let pool = !lastQuery.isEmpty && key.hasPrefix(lastQuery) ? lastMatches : Array(entries.indices)
            var hits: [(Int, Hit)] = []
            for i in pool {
                if let h = MacFileFind.score(entries[i], terms, positions: false) { hits.append((i, h)) }
            }
            lastQuery = key
            lastMatches = hits.map(\.0)
            hits.sort { MacFileFind.ranked($0.1, $1.1) }
            // Positions only for the ones listed.
            return hits.prefix(limit).map { MacFileFind.score(entries[$0.0], terms, positions: true) ?? $0.1 }
        }
    }

    /// One-shot: the best `limit` files for a query.
    static func match(_ files: [String], _ query: String, limit: Int = maxHits) -> [Hit] {
        Search(files).search(query, limit: limit)
    }

    /// The path the file viewer opens for a listed file.
    static func join(_ cwd: String, _ rel: String) -> String {
        cwd.hasSuffix("/") ? cwd + rel : cwd + "/" + rel
    }

    /// `text` (the part of a path starting at UTF-16 `offset`) cut into runs, the matched characters apart. Whole
    /// characters: a run never splits one.
    static func runs(_ text: String, offset: Int, matched: [Int]) -> [(text: String, hit: Bool)] {
        let at = Set(matched)
        var runs: [(text: String, hit: Bool)] = []
        var i = offset
        for c in text {
            let width = c.utf16.count
            let hit = (i..<i + width).contains { at.contains($0) }
            i += width
            if let last = runs.last, last.hit == hit { runs[runs.count - 1].text.append(c) } else { runs.append((String(c), hit)) }
        }
        return runs
    }
}

/// The listing behind Files: one per directory, kept a while so ⌘K opened again answers at once.
@MainActor final class MacFileIndex {
    static let shared = MacFileIndex()
    /// How long a list is reused when the panel opens again (the node keeps its own for 10 s).
    static let ttl: TimeInterval = 20

    enum State { case loading, ready(MacFileFind.Search, truncated: Bool), failed }

    private struct Cached { var search: MacFileFind.Search; var truncated: Bool; var at: Date }
    private var cache: [String: Cached] = [:]

    private nonisolated struct Listing: Decodable, Sendable { var files: [String]?; var truncated: Bool? }

    /// The list for `cwd` on a machine, from the cache when it is fresh.
    func load(_ api: APIClient, machine: String, cwd: String) async -> State {
        let id = machine + "\u{0}" + cwd
        if let c = cache[id], Date.now.timeIntervalSince(c.at) < Self.ttl { return .ready(c.search, truncated: c.truncated) }
        do {
            let l = try await api.getDecodedOffMain("/api/v1/machines/\(machine)/fs/find", query: ["cwd": cwd], as: Listing.self)
            let files = l.files ?? []
            let search = await Task.detached(priority: .userInitiated) { MacFileFind.Search(files) }.value
            cache[id] = Cached(search: search, truncated: l.truncated ?? false, at: .now)
            return .ready(search, truncated: l.truncated ?? false)
        } catch {
            return .failed
        }
    }
}
