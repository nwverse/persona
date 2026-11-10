import Compression
import Foundation

/// One sheet of a table file (docs/apple/ios.md「Tables」): its cells as text, row by row; rows can be shorter than `columns`.
public nonisolated struct TableSheet: Sendable, Equatable {
    public var name: String
    public var rows: [[String]] = []
    public var columns = 0
    /// Rows past the cap were left out.
    public var capped = false

    public init(name: String, rows: [[String]] = [], columns: Int = 0, capped: Bool = false) {
        self.name = name
        self.rows = rows
        self.columns = columns
        self.capped = capped
    }

    /// More rows than this are not kept (a sheet this long is not read on a phone).
    public static let maxRows = 200_000

    public mutating func append(_ more: [[String]]) {
        let room = Self.maxRows - rows.count
        if more.count > room { capped = true }
        for row in more.prefix(max(0, room)) {
            rows.append(row)
            columns = max(columns, row.count)
        }
    }

    public subscript(row: Int, column: Int) -> String {
        guard row < rows.count, column < rows[row].count else { return "" }
        return rows[row][column]
    }

    /// The spreadsheet name of a column: A … Z, AA …
    public static func columnName(_ index: Int) -> String {
        var n = index + 1
        var name = ""
        while n > 0 {
            let r = (n - 1) % 26
            name = String(UnicodeScalar(UInt8(65 + r))) + name
            n = (n - 1) / 26
        }
        return name
    }
}

/// CSV / TSV (RFC 4180: quoted fields with "" for a quote, separators and line breaks inside quotes), fed a page at a
/// time: a row cut by the end of a page is finished by the next one. Blank lines are skipped, a lone CR is ignored.
public nonisolated struct DelimitedParser: Sendable {
    public let separator: UInt8
    private var field: [UInt8] = []
    private var row: [String] = []
    private var inQuotes = false
    private var quotePending = false
    private var quoted = false // the current field started with a quote

    public init(separator: UInt8) { self.separator = separator }

    /// The separator for a file: tab for .tsv / .tab, else comma, or semicolon when the first line has more of them
    /// (what spreadsheets write where the comma is the decimal mark).
    public static func separator(name: String, firstLine: String) -> UInt8 {
        switch (name as NSString).pathExtension.lowercased() {
        case "tsv", "tab": return UInt8(ascii: "\t")
        default:
            let semicolons = firstLine.utf8.count { $0 == UInt8(ascii: ";") }
            let commas = firstLine.utf8.count { $0 == UInt8(ascii: ",") }
            return semicolons > commas ? UInt8(ascii: ";") : UInt8(ascii: ",")
        }
    }

    /// The rows finished by this page; `final`: the file ends here.
    public mutating func feed(_ text: String, final: Bool) -> [[String]] {
        var out: [[String]] = []
        let quote = UInt8(ascii: "\""), lf = UInt8(ascii: "\n"), cr = UInt8(ascii: "\r")
        for b in text.utf8 {
            if inQuotes {
                if quotePending {
                    quotePending = false
                    if b == quote {
                        field.append(quote)
                        continue
                    }
                    inQuotes = false // the closing quote: this byte is outside the quotes
                } else if b == quote {
                    quotePending = true
                    continue
                } else {
                    field.append(b)
                    continue
                }
            }
            switch b {
            case quote where field.isEmpty && !quoted:
                inQuotes = true
                quoted = true
            case separator:
                endField()
            case lf:
                endField()
                endRow(into: &out)
            case cr:
                break
            default:
                field.append(b)
            }
        }
        if final {
            quotePending = false
            inQuotes = false
            if !field.isEmpty || !row.isEmpty || quoted {
                endField()
                endRow(into: &out)
            }
        }
        return out
    }

    private mutating func endField() {
        row.append(String(decoding: field, as: UTF8.self))
        field.removeAll(keepingCapacity: true)
        quoted = false
    }

    private mutating func endRow(into out: inout [[String]]) {
        if !(row.count == 1 && row[0].isEmpty) { out.append(row) }
        row = []
    }
}

// MARK: - xlsx

/// The files in a zip archive (an xlsx is one), read whole from memory: stored and deflated entries, no zip64.
public nonisolated struct ZipArchive: Sendable {
    public struct Entry: Sendable {
        public var name: String
        public var method: UInt16
        public var compressed: Int
        public var size: Int
        public var offset: Int
    }

    public enum Failure: Error, LocalizedError {
        case notZip, unsupported(String), corrupt(String)
        public var errorDescription: String? {
            switch self {
            case .notZip: String(localized: "Not a valid xlsx file (not a zip)")
            case .unsupported(let what): String(localized: "Unsupported xlsx file: \(what)")
            case .corrupt(let what): String(localized: "The xlsx file is damaged: \(what)")
            }
        }
    }

    public let data: Data
    private(set) var entries: [String: Entry] = [:]

    public init(_ data: Data) throws {
        self.data = data
        let bytes = [UInt8](data)
        guard bytes.count >= 22 else { throw Failure.notZip }
        // The end of the central directory: its signature within the last 64 KB (it may be followed by a comment).
        var eocd = -1
        var i = bytes.count - 22
        while i >= max(0, bytes.count - 22 - 0xFFFF) {
            if bytes[i] == 0x50, bytes[i + 1] == 0x4B, bytes[i + 2] == 0x05, bytes[i + 3] == 0x06 { eocd = i; break }
            i -= 1
        }
        guard eocd >= 0 else { throw Failure.notZip }
        let count = Int(Self.u16(bytes, eocd + 10))
        var p = Int(Self.u32(bytes, eocd + 16))
        guard p < bytes.count, count != 0xFFFF else { throw Failure.unsupported("zip64") }
        for _ in 0..<count {
            guard p + 46 <= bytes.count, Self.u32(bytes, p) == 0x0201_4B50 else { throw Failure.corrupt(String(localized: "central directory")) }
            let method = Self.u16(bytes, p + 10)
            let compressed = Self.u32(bytes, p + 20), size = Self.u32(bytes, p + 24)
            let nameLength = Int(Self.u16(bytes, p + 28)), extra = Int(Self.u16(bytes, p + 30)), comment = Int(Self.u16(bytes, p + 32))
            let offset = Self.u32(bytes, p + 42)
            guard compressed != 0xFFFF_FFFF, size != 0xFFFF_FFFF, offset != 0xFFFF_FFFF else { throw Failure.unsupported("zip64") }
            guard p + 46 + nameLength <= bytes.count else { throw Failure.corrupt(String(localized: "central directory")) }
            let name = String(decoding: bytes[(p + 46)..<(p + 46 + nameLength)], as: UTF8.self)
            entries[name] = Entry(name: name, method: method, compressed: Int(compressed), size: Int(size), offset: Int(offset))
            p += 46 + nameLength + extra + comment
        }
    }

    /// An entry's bytes, or nil when there is no such entry.
    public func file(_ name: String) throws -> Data? {
        guard let e = entries[name] else { return nil }
        let bytes = data
        let h = e.offset
        guard h + 30 <= bytes.count, Self.u32(bytes, h) == 0x0403_4B50 else { throw Failure.corrupt(name) }
        let start = h + 30 + Int(Self.u16(bytes, h + 26)) + Int(Self.u16(bytes, h + 28))
        guard start + e.compressed <= bytes.count else { throw Failure.corrupt(name) }
        let raw = bytes.subdata(in: (bytes.startIndex + start)..<(bytes.startIndex + start + e.compressed))
        switch e.method {
        case 0: return raw
        case 8: return try Self.inflate(raw, size: e.size, name: name)
        default: throw Failure.unsupported(String(localized: "compression method \(e.method)"))
        }
    }

    private static func inflate(_ raw: Data, size: Int, name: String) throws -> Data {
        guard size > 0 else { return Data() }
        var out = Data(count: size)
        let written = out.withUnsafeMutableBytes { dst in
            raw.withUnsafeBytes { src in
                // COMPRESSION_ZLIB is raw DEFLATE (no zlib header), which is what zip stores.
                compression_decode_buffer(dst.bindMemory(to: UInt8.self).baseAddress!, size,
                                          src.bindMemory(to: UInt8.self).baseAddress!, raw.count, nil, COMPRESSION_ZLIB)
            }
        }
        guard written == size else { throw Failure.corrupt(name) }
        return out
    }

    private static func u16(_ b: [UInt8], _ i: Int) -> UInt16 { UInt16(b[i]) | UInt16(b[i + 1]) << 8 }
    private static func u32(_ b: [UInt8], _ i: Int) -> UInt32 {
        UInt32(b[i]) | UInt32(b[i + 1]) << 8 | UInt32(b[i + 2]) << 16 | UInt32(b[i + 3]) << 24
    }
    private static func u16(_ d: Data, _ i: Int) -> UInt16 { UInt16(d[d.startIndex + i]) | UInt16(d[d.startIndex + i + 1]) << 8 }
    private static func u32(_ d: Data, _ i: Int) -> UInt32 {
        let s = d.startIndex + i
        return UInt32(d[s]) | UInt32(d[s + 1]) << 8 | UInt32(d[s + 2]) << 16 | UInt32(d[s + 3]) << 24
    }
}

/// An Excel workbook (.xlsx, Office Open XML): each sheet's cells as the text Excel would show for the plain cases:
/// shared and inline strings, numbers without float noise, TRUE / FALSE, errors, and dates for the date formats.
public nonisolated enum XLSXReader {
    public static func read(_ data: Data) throws -> [TableSheet] {
        let zip = try ZipArchive(data)
        guard let workbook = try zip.file("xl/workbook.xml") else { throw ZipArchive.Failure.corrupt(String(localized: "No workbook.xml")) }
        let sheets = XMLCollector.elements(workbook, named: ["sheet"])
        let rels = try zip.file("xl/_rels/workbook.xml.rels").map { XMLCollector.elements($0, named: ["Relationship"]) } ?? []
        var targets: [String: String] = [:]
        for r in rels {
            guard let id = r["Id"], let target = r["Target"] else { continue }
            targets[id] = target.hasPrefix("/") ? String(target.dropFirst()) : "xl/" + target
        }
        let strings = try zip.file("xl/sharedStrings.xml").map(SharedStrings.parse) ?? []
        let dates = try zip.file("xl/styles.xml").map(DateStyles.parse) ?? []
        var out: [TableSheet] = []
        for (i, s) in sheets.enumerated() {
            let name = s["name"] ?? "Sheet\(i + 1)"
            let path = s["r:id"].flatMap { targets[$0] } ?? "xl/worksheets/sheet\(i + 1).xml"
            guard let xml = try zip.file(path) else { continue }
            out.append(SheetCells.parse(xml, name: name, strings: strings, dates: dates))
        }
        guard !out.isEmpty else { throw ZipArchive.Failure.corrupt(String(localized: "No sheets")) }
        return out
    }

    /// A number as Excel shows it in a General cell: integers without ".0", others to 15 significant digits.
    public static func number(_ raw: String) -> String {
        guard let v = Double(raw), v.isFinite else { return raw }
        if v == v.rounded(), abs(v) < 1e15 { return String(Int64(v)) }
        var s = String(format: "%.15g", v)
        if s.contains("e") { return s }
        if s.contains(".") { while s.hasSuffix("0") { s.removeLast() }; if s.hasSuffix(".") { s.removeLast() } }
        return s
    }

    /// An Excel date serial (days since 1899-12-30, the 1900 system) as yyyy-MM-dd, with the time when it has one.
    public static func date(_ raw: String) -> String {
        guard let v = Double(raw), v.isFinite, v > -1, v < 2_958_466 else { return raw }
        let seconds = (v * 86_400).rounded()
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        let base = DateComponents(calendar: calendar, year: 1899, month: 12, day: 30).date!
        let date = base.addingTimeInterval(seconds)
        let c = calendar.dateComponents([.year, .month, .day, .hour, .minute, .second], from: date)
        let day = String(format: "%04d-%02d-%02d", c.year!, c.month!, c.day!)
        if seconds.truncatingRemainder(dividingBy: 86_400) == 0 { return day }
        let time = c.second == 0 ? String(format: "%02d:%02d", c.hour!, c.minute!) : String(format: "%02d:%02d:%02d", c.hour!, c.minute!, c.second!)
        return v < 1 ? time : day + " " + time
    }
}

/// Every element with one of the names, as its attributes (qualified names, so `r:id`).
nonisolated private final class XMLCollector: NSObject, XMLParserDelegate {
    let names: Set<String>
    var found: [[String: String]] = []
    init(_ names: Set<String>) { self.names = names }

    static func elements(_ data: Data, named names: Set<String>) -> [[String: String]] {
        let c = XMLCollector(names)
        let p = XMLParser(data: data)
        p.delegate = c
        p.parse()
        return c.found
    }

    func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        if names.contains(name) { found.append(attributes) }
    }
}

/// xl/sharedStrings.xml: each `<si>`'s text, its runs joined; phonetic guides (`<rPh>`) left out.
nonisolated private final class SharedStrings: NSObject, XMLParserDelegate {
    var strings: [String] = []
    private var current = ""
    private var inText = false
    private var phonetic = 0

    static func parse(_ data: Data) -> [String] {
        let s = SharedStrings()
        let p = XMLParser(data: data)
        p.delegate = s
        p.parse()
        return s.strings
    }

    func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        switch name {
        case "si": current = ""
        case "rPh": phonetic += 1
        case "t": inText = phonetic == 0
        default: break
        }
    }

    func parser(_ parser: XMLParser, didEndElement name: String, namespaceURI: String?, qualifiedName: String?) {
        switch name {
        case "si": strings.append(current)
        case "rPh": phonetic -= 1
        case "t": inText = false
        default: break
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) { if inText { current += string } }
}

/// xl/styles.xml: which cell styles (`cellXfs` index) show a number as a date or time.
nonisolated private final class DateStyles: NSObject, XMLParserDelegate {
    private var custom: [Int: String] = [:]
    private var formats: [Int] = []
    private var inCellXfs = false

    static func parse(_ data: Data) -> [Bool] {
        let s = DateStyles()
        let p = XMLParser(data: data)
        p.delegate = s
        p.parse()
        return s.formats.map { id in
            if (14...22).contains(id) || (45...47).contains(id) { return true }
            guard let code = s.custom[id] else { return false }
            // A custom code is a date when it has date letters outside quotes and brackets ([Red], "text").
            var plain = ""
            var skip: Character?
            for ch in code.lowercased() {
                if let end = skip { if ch == end { skip = nil }; continue }
                if ch == "\"" { skip = "\"" } else if ch == "[" { skip = "]" } else { plain.append(ch) }
            }
            return plain.contains { "ymdhs".contains($0) }
        }
    }

    func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        switch name {
        case "numFmt":
            if let id = attributes["numFmtId"].flatMap(Int.init), let code = attributes["formatCode"] { custom[id] = code }
        case "cellXfs": inCellXfs = true
        case "xf" where inCellXfs: formats.append(attributes["numFmtId"].flatMap(Int.init) ?? 0)
        default: break
        }
    }

    func parser(_ parser: XMLParser, didEndElement name: String, namespaceURI: String?, qualifiedName: String?) {
        if name == "cellXfs" { inCellXfs = false }
    }
}

/// One worksheet's `<sheetData>`: cells placed by their reference (B3), gaps left empty.
nonisolated private final class SheetCells: NSObject, XMLParserDelegate {
    let strings: [String]
    let dates: [Bool]
    var sheet: TableSheet
    private var row: [String] = []
    private var rowIndex = -1
    private var cellColumn = 0
    private var cellType = ""
    private var cellStyle = 0
    private var value = ""
    private var inValue = false
    private var inInline = false

    init(name: String, strings: [String], dates: [Bool]) {
        self.strings = strings
        self.dates = dates
        sheet = TableSheet(name: name)
    }

    static func parse(_ data: Data, name: String, strings: [String], dates: [Bool]) -> TableSheet {
        let s = SheetCells(name: name, strings: strings, dates: dates)
        let p = XMLParser(data: data)
        p.delegate = s
        p.parse()
        s.flushRow()
        return s.sheet
    }

    /// The column of a reference like "AB12" (0-based), or nil.
    static func column(_ ref: String) -> Int? {
        var n = 0
        var any = false
        for u in ref.utf8 {
            guard u >= 65, u <= 90 else { break }
            n = n * 26 + Int(u - 64)
            any = true
        }
        return any ? n - 1 : nil
    }

    func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
        switch name {
        case "row":
            flushRow()
            let r = attributes["r"].flatMap(Int.init).map { $0 - 1 } ?? rowIndex + 1
            if r > rowIndex + 1, rowIndex >= 0 || r > 0 { sheet.append(Array(repeating: [], count: min(r - rowIndex - 1, TableSheet.maxRows))) }
            rowIndex = r
        case "c":
            cellColumn = attributes["r"].flatMap(Self.column) ?? row.count
            cellType = attributes["t"] ?? ""
            cellStyle = attributes["s"].flatMap(Int.init) ?? 0
            value = ""
        case "v": inValue = true
        case "is": inInline = true
        case "t" where inInline: inValue = true
        default: break
        }
    }

    func parser(_ parser: XMLParser, didEndElement name: String, namespaceURI: String?, qualifiedName: String?) {
        switch name {
        case "v", "t": inValue = false
        case "is": inInline = false
        case "c":
            let shown: String
            switch cellType {
            case "s": shown = Int(value).flatMap { $0 < strings.count ? strings[$0] : nil } ?? ""
            case "b": shown = value == "1" ? "TRUE" : "FALSE"
            case "str", "inlineStr", "e": shown = value
            default:
                shown = value.isEmpty ? "" : cellStyle < dates.count && dates[cellStyle] ? XLSXReader.date(value) : XLSXReader.number(value)
            }
            guard cellColumn < 16_384 else { return }
            if cellColumn >= row.count { row += Array(repeating: "", count: cellColumn - row.count + 1) }
            row[cellColumn] = shown
        default: break
        }
    }

    func parser(_ parser: XMLParser, foundCharacters string: String) { if inValue { value += string } }

    private func flushRow() {
        guard rowIndex >= 0 else { return }
        while row.last == "" { row.removeLast() }
        sheet.append([row])
        row = []
    }
}
