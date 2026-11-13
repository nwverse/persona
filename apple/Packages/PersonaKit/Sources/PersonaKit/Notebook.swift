import Foundation

/// A Jupyter notebook (.ipynb, nbformat 4) as the file viewer shows it (docs/apple/ios.md「File viewer」): its cells in
/// order, each code cell with what it printed. Fields an older or hand-written file leaves out read as empty.
public nonisolated struct Notebook: Sendable, Equatable {
    public var language: String
    public var cells: [Cell]

    public struct Cell: Sendable, Equatable, Identifiable {
        public enum Kind: String, Sendable { case markdown, code, raw }
        public var id: Int
        public var kind: Kind
        public var source: String
        public var count: Int?
        public var outputs: [Output] = []
    }

    public enum Output: Sendable, Equatable {
        /// stdout / stderr.
        case stream(name: String, text: String)
        /// The richest of what a result or display carried that the app shows: an image, else HTML, else text.
        case image(mime: String, base64: String)
        case html(String, plain: String?)
        case text(String)
        /// An exception: its name, message and traceback (colors taken out).
        case error(name: String, value: String, traceback: String)
    }

    public enum Failure: Error, LocalizedError {
        case notJSON, unsupported(Int)
        public var errorDescription: String? {
            switch self {
            case .notJSON: String(localized: "Not a valid notebook file (its JSON can't be read)")
            case .unsupported(let v): String(localized: "Notebooks in nbformat \(v) aren't supported, so this one is shown as source")
            }
        }
    }

    public init(language: String, cells: [Cell]) {
        self.language = language
        self.cells = cells
    }

    public init(data: Data) throws {
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw Failure.notJSON }
        let major = root["nbformat"] as? Int ?? 4
        guard major >= 4, let raw = root["cells"] as? [[String: Any]] else { throw Failure.unsupported(major) }
        let meta = root["metadata"] as? [String: Any]
        language = ((meta?["language_info"] as? [String: Any])?["name"] as? String)
            ?? ((meta?["kernelspec"] as? [String: Any])?["language"] as? String) ?? "python"
        cells = raw.enumerated().map { i, c in
            let kind = Cell.Kind(rawValue: c["cell_type"] as? String ?? "") ?? .raw
            var cell = Cell(id: i, kind: kind, source: Self.text(c["source"]), count: c["execution_count"] as? Int)
            if kind == .code { cell.outputs = (c["outputs"] as? [[String: Any]] ?? []).compactMap(Self.output) }
            return cell
        }
    }

    /// A multiline string as nbformat writes it: one string, or a list of lines.
    public static func text(_ v: Any?) -> String {
        if let s = v as? String { return s }
        if let lines = v as? [String] { return lines.joined() }
        return ""
    }

    private static func output(_ o: [String: Any]) -> Output? {
        switch o["output_type"] as? String {
        case "stream":
            return .stream(name: o["name"] as? String ?? "stdout", text: text(o["text"]))
        case "error":
            let trace = (o["traceback"] as? [String] ?? []).map(stripANSI).joined(separator: "\n")
            return .error(name: o["ename"] as? String ?? "Error", value: o["evalue"] as? String ?? "", traceback: trace)
        case "execute_result", "display_data":
            let data = o["data"] as? [String: Any] ?? [:]
            for mime in ["image/png", "image/jpeg", "image/gif"] {
                if let b = data[mime] { return .image(mime: mime, base64: text(b).replacingOccurrences(of: "\n", with: "")) }
            }
            let plain = data["text/plain"].map(text)
            if let html = data["text/html"] { return .html(text(html), plain: plain) }
            if let md = data["text/markdown"] { return .text(text(md)) }
            return plain.map(Output.text)
        default:
            return nil
        }
    }

    /// Terminal colors (ESC [ … m) and other CSI sequences out of a traceback.
    public static func stripANSI(_ s: String) -> String {
        guard s.contains("\u{1B}") else { return s }
        var out = ""
        var it = s.unicodeScalars.makeIterator()
        while let u = it.next() {
            guard u == "\u{1B}" else { out.unicodeScalars.append(u); continue }
            guard let next = it.next() else { break }
            guard next == "[" else { continue }
            while let c = it.next(), !(0x40...0x7E).contains(c.value) {}
        }
        return out
    }

    /// The code cells' source as a fenced block in the notebook's language, for the chat's Markdown look.
    public func fenced(_ cell: Cell) -> String {
        let fence = cell.source.contains("```") ? "~~~~" : "```"
        return fence + language + "\n" + cell.source + (cell.source.hasSuffix("\n") ? "" : "\n") + fence
    }
}
