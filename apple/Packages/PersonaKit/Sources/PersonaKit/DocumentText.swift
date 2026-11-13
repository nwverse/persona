import Foundation
import PDFKit

/// The text of a document, cut into the units a person thinks of it in (docs/apple/ios.md「Compare」): PDF pages, a Word
/// document's pages (split at its page breaks), slides with their notes, a workbook's sheets. Two versions are
/// compared line by line on this (`TextCompare`), as the web's comparison does.
public nonisolated enum DocumentText {
    public struct Unit: Sendable, Equatable {
        public var label: String
        public var lines: [String]
    }

    public enum Kind: Sendable, Equatable { case pdf, docx, pptx, xlsx, image }

    public static func kind(_ name: String) -> Kind? {
        switch (name as NSString).pathExtension.lowercased() {
        case "pdf": .pdf
        case "docx", "docm": .docx
        case "pptx", "pptm": .pptx
        case "xlsx", "xlsm": .xlsx
        case "png", "jpg", "jpeg", "gif", "webp", "heic", "heif", "bmp", "tiff", "tif": .image
        default: nil
        }
    }

    public static func units(_ data: Data, kind: Kind) throws -> [Unit] {
        switch kind {
        case .pdf:
            guard let doc = PDFDocument(data: data) else { throw ZipArchive.Failure.corrupt("PDF") }
            return (0..<doc.pageCount).map { i in Unit(label: String(localized: "Page \(i + 1)"), lines: lines(doc.page(at: i)?.string ?? "")) }
        case .docx: return try docx(data)
        case .pptx: return try pptx(data)
        case .xlsx: return try XLSXReader.read(data).map { s in Unit(label: String(localized: "Sheet “\(s.name)”"), lines: s.rows.map { $0.joined(separator: "\t") }) }
        case .image: return []
        }
    }

    public static func lines(_ text: String) -> [String] {
        text.split(omittingEmptySubsequences: false, whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    // MARK: Word

    private static func docx(_ data: Data) throws -> [Unit] {
        let zip = try ZipArchive(data)
        guard let xml = try zip.file("word/document.xml") else { throw ZipArchive.Failure.corrupt(String(localized: "No document.xml")) }
        let p = WordText()
        let parser = XMLParser(data: xml)
        parser.delegate = p
        parser.parse()
        p.endPage()
        return p.pages.enumerated().map { i, lines in Unit(label: String(localized: "Page \(i + 1)"), lines: lines) }
    }

    /// Paragraphs (a table's row as its cells joined by tabs), deleted runs (tracked changes) left out, a new page at
    /// each page break.
    private final class WordText: NSObject, XMLParserDelegate {
        var pages: [[String]] = []
        private var page: [String] = []
        private var paragraph = ""
        private var cells: [String]?
        private var cell = ""
        private var inText = false
        private var deleted = 0

        func endPage() {
            if !paragraph.isEmpty { page.append(paragraph); paragraph = "" }
            if !page.isEmpty || pages.isEmpty { pages.append(page) }
            page = []
        }

        func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
            switch name {
            case "w:del": deleted += 1
            case "w:t": inText = deleted == 0
            case "w:tab": if deleted == 0 { append("\t") }
            case "w:tr": cells = []
            case "w:tc": cell = ""
            case "w:br" where attributes["w:type"] == "page": endPage()
            default: break
            }
        }

        func parser(_ parser: XMLParser, didEndElement name: String, namespaceURI: String?, qualifiedName: String?) {
            switch name {
            case "w:del": deleted -= 1
            case "w:t": inText = false
            case "w:p":
                if cells != nil { cell += (cell.isEmpty ? "" : " ") + paragraph } else if !paragraph.trimmingCharacters(in: .whitespaces).isEmpty {
                    page.append(paragraph)
                }
                paragraph = ""
            case "w:tc": cells?.append(cell.trimmingCharacters(in: .whitespaces))
            case "w:tr":
                if let row = cells, row.contains(where: { !$0.isEmpty }) { page.append(row.joined(separator: "\t")) }
                cells = nil
            default: break
            }
        }

        func parser(_ parser: XMLParser, foundCharacters string: String) { if inText { append(string) } }

        private func append(_ s: String) { paragraph += s }
    }

    // MARK: PowerPoint

    private static func pptx(_ data: Data) throws -> [Unit] {
        let zip = try ZipArchive(data)
        // Slides in the deck's order: presentation.xml's list of ids, through its relationships.
        guard let pres = try zip.file("ppt/presentation.xml") else { throw ZipArchive.Failure.corrupt(String(localized: "No presentation.xml")) }
        let order = Elements.collect(pres, ["p:sldId"]).compactMap { $0["r:id"] }
        var targets: [String: String] = [:]
        if let rels = try zip.file("ppt/_rels/presentation.xml.rels") {
            for r in Elements.collect(rels, ["Relationship"]) { if let id = r["Id"], let t = r["Target"] { targets[id] = t } }
        }
        var slides = order.compactMap { targets[$0] }.map { $0.hasPrefix("/") ? String($0.dropFirst()) : "ppt/" + $0 }
        if slides.isEmpty {
            slides = zip.entries.keys.filter { $0.hasPrefix("ppt/slides/slide") && $0.hasSuffix(".xml") }
                .sorted { $0.localizedStandardCompare($1) == .orderedAscending }
        }
        return try slides.enumerated().map { i, path in
            var lines = try zip.file(path).map(Paragraphs.text) ?? []
            // The notes: the slide's relationship to its notes page.
            let rels = (path as NSString).deletingLastPathComponent + "/_rels/" + (path as NSString).lastPathComponent + ".rels"
            if let r = try zip.file(rels),
               let notes = Elements.collect(r, ["Relationship"]).first(where: { ($0["Type"] ?? "").hasSuffix("/notesSlide") })?["Target"] {
                let notesPath = ((path as NSString).deletingLastPathComponent as NSString).appendingPathComponent(notes)
                let normalized = (notesPath as NSString).standardizingPath.replacingOccurrences(of: "ppt/slides/../", with: "ppt/")
                if let n = try zip.file(normalized).map(Paragraphs.text), !n.isEmpty { lines += n.map { String(localized: "Notes: \($0)") } }
            }
            return Unit(label: String(localized: "Slide \(i + 1)"), lines: lines)
        }
    }

    /// DrawingML paragraphs (`a:p`), their runs (`a:t`) joined.
    private final class Paragraphs: NSObject, XMLParserDelegate {
        var lines: [String] = []
        private var current = ""
        private var inText = false

        static func text(_ data: Data) -> [String] {
            let p = Paragraphs()
            let parser = XMLParser(data: data)
            parser.delegate = p
            parser.parse()
            return p.lines.filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        }

        func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
            if name == "a:t" { inText = true }
        }

        func parser(_ parser: XMLParser, didEndElement name: String, namespaceURI: String?, qualifiedName: String?) {
            if name == "a:t" { inText = false }
            if name == "a:p" { lines.append(current); current = "" }
        }

        func parser(_ parser: XMLParser, foundCharacters string: String) { if inText { current += string } }
    }

    private final class Elements: NSObject, XMLParserDelegate {
        let names: Set<String>
        var found: [[String: String]] = []
        init(_ names: Set<String>) { self.names = names }

        static func collect(_ data: Data, _ names: Set<String>) -> [[String: String]] {
            let e = Elements(names)
            let p = XMLParser(data: data)
            p.delegate = e
            p.parse()
            return e.found
        }

        func parser(_ parser: XMLParser, didStartElement name: String, namespaceURI: String?, qualifiedName: String?, attributes: [String: String]) {
            if names.contains(name) { found.append(attributes) }
        }
    }
}

/// Two versions' units compared line by line (`CollectionDifference`, a Myers diff), shown as the changes with a few
/// lines around each and the rest folded, each change under its unit's label.
public nonisolated enum TextCompare {
    public enum Row: Sendable, Equatable {
        case unit(String)
        case same(String)
        case added(String)
        case removed(String)
        /// Lines that did not change, folded.
        case folded(Int)
    }

    /// Lines shown around a change.
    public static let context = 2
    /// Lines per version compared at most (beyond that, a note).
    public static let maxLines = 20_000

    public struct Result: Sendable, Equatable {
        public var rows: [Row]
        public var added = 0
        public var removed = 0
        public var capped = false
    }

    public static func compare(_ old: [DocumentText.Unit], _ new: [DocumentText.Unit]) -> Result {
        // Each line is tagged with its unit, so a change says where it is; the unit lines themselves never differ.
        func flatten(_ units: [DocumentText.Unit]) -> [(unit: String, text: String)] {
            units.flatMap { u in u.lines.map { (u.label, $0) } }
        }
        var a = flatten(old), b = flatten(new)
        let capped = a.count > maxLines || b.count > maxLines
        a = Array(a.prefix(maxLines)); b = Array(b.prefix(maxLines))
        let diff = b.map(\.text).difference(from: a.map(\.text))
        var removedAt = Set<Int>(), addedAt = Set<Int>()
        for change in diff {
            switch change {
            case .remove(let offset, _, _): removedAt.insert(offset)
            case .insert(let offset, _, _): addedAt.insert(offset)
            }
        }
        // Walk both: removed lines of the old, added of the new, the rest the same.
        var merged: [(unit: String, kind: Int, text: String)] = [] // kind: 0 same, 1 added, -1 removed
        var i = 0, j = 0
        while i < a.count || j < b.count {
            if i < a.count, removedAt.contains(i) {
                merged.append((a[i].unit, -1, a[i].text)); i += 1
            } else if j < b.count, addedAt.contains(j) {
                merged.append((b[j].unit, 1, b[j].text)); j += 1
            } else if i < a.count, j < b.count {
                merged.append((b[j].unit, 0, b[j].text)); i += 1; j += 1
            } else if i < a.count {
                merged.append((a[i].unit, -1, a[i].text)); i += 1
            } else {
                merged.append((b[j].unit, 1, b[j].text)); j += 1
            }
        }
        let changed = merged.indices.filter { merged[$0].kind != 0 }
        var keep = Set<Int>()
        for c in changed { for k in max(0, c - context)...min(merged.count - 1, c + context) { keep.insert(k) } }
        var rows: [Row] = []
        var unit: String?
        var folded = 0
        for (k, line) in merged.enumerated() {
            guard keep.contains(k) else { folded += 1; continue }
            if folded > 0 { rows.append(.folded(folded)); folded = 0 }
            if line.unit != unit { rows.append(.unit(line.unit)); unit = line.unit }
            switch line.kind {
            case 1: rows.append(.added(line.text))
            case -1: rows.append(.removed(line.text))
            default: rows.append(.same(line.text))
            }
        }
        if folded > 0, !rows.isEmpty { rows.append(.folded(folded)) }
        return Result(rows: rows, added: addedAt.count, removed: removedAt.count, capped: capped)
    }
}
