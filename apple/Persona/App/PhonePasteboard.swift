import Foundation
import UniformTypeIdentifiers
#if canImport(UIKit)
import UIKit
#endif

/// The phone's own clipboard, for sending it to a computer and putting a computer's on it (#3064). Closures, so the
/// clipboard model's tests hand in a pasteboard of their own. On the Mac `UIPasteboard` is the general pasteboard
/// (Mac/Compat).
struct PhonePasteboard {
    /// What is on it now, or nil when it is empty. On iOS reading what another app copied asks "Allow Paste?" first
    /// (unless Settings › Persona › Paste from Other Apps is Allow); refused, it reads as nil.
    var read: @MainActor () -> ClipboardContent?
    /// Replaces it (no prompt).
    var write: @MainActor (ClipboardContent) -> Void
    /// Whether something is on it, without reading it (never prompts).
    var hasContent: @MainActor () -> Bool

    static let live = PhonePasteboard(
        read: {
            let board = UIPasteboard.general
            if board.hasImages, let image = board.image, let encoded = PhonePasteboard.encode(image) { return .image(encoded) }
            if let text = board.string, !text.isEmpty { return .text(text) }
            if let url = board.url { return .text(url.absoluteString) }
            return nil
        },
        write: { content in
            let board = UIPasteboard.general
            switch content.kind {
            case .text: board.string = content.text ?? ""
            case .image:
                if let bytes = content.image?.bytes, let type = PhonePasteboard.encodedImageType(bytes) {
                    // Keep the original encoded bytes instead of initializing ImageIO or decoding a UIImage on the
                    // main actor. The bytes themselves determine the pasteboard type.
                    board.setData(bytes, forPasteboardType: type)
                }
            case .empty, .concealed: break
            }
        },
        hasContent: {
            let board = UIPasteboard.general
            return board.hasStrings || board.hasImages || board.hasURLs
        }
    )

    /// The clipboard contract carries PNG / JPEG. Validate their bounded container structure directly: even ImageIO's
    /// metadata initialization can block on plug-in discovery. These checks reject truncation and marker-only data;
    /// they do not decode pixels, and the pasteboard still receives the exact source bytes.
    private nonisolated static func encodedImageType(_ bytes: Data) -> String? {
        if isPNG(bytes) { return UTType.png.identifier }
        if isJPEG(bytes) { return UTType.jpeg.identifier }
        return nil
    }

    private nonisolated static func isPNG(_ bytes: Data) -> Bool {
        let signature: [UInt8] = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]
        // Signature + IHDR + at least one byte of IDAT + IEND.
        guard bytes.count >= 58, bytes.prefix(8).elementsEqual(signature) else { return false }
        return bytes.withUnsafeBytes { raw -> Bool in
            let b = raw.bindMemory(to: UInt8.self)
            func word(_ offset: Int) -> Int {
                Int(b[offset]) << 24 | Int(b[offset + 1]) << 16 | Int(b[offset + 2]) << 8 | Int(b[offset + 3])
            }
            // The first chunk must be a 13-byte IHDR with a non-empty pixel area.
            guard word(8) == 13, word(12) == 0x49484452, word(16) > 0, word(20) > 0 else { return false }
            var offset = 33
            var idatHeader: [UInt8] = []
            while offset + 12 <= raw.count {
                let length = word(offset)
                guard length <= raw.count - offset - 12 else { return false }
                let type = word(offset + 4)
                if type == 0x49484452 { return false } // a second IHDR
                if type == 0x49444154 { // IDAT: collect the zlib header even when it spans chunks
                    for index in (offset + 8)..<(offset + 8 + length) where idatHeader.count < 2 {
                        idatHeader.append(b[index])
                    }
                }
                if type == 0x49454e44 { // IEND, including its fixed CRC; nothing may follow it
                    guard length == 0, idatHeader.count == 2, offset + 12 == raw.count,
                          word(offset + 8) == 0xae426082 else { return false }
                    let cmf = idatHeader[0], flg = idatHeader[1]
                    // PNG uses zlib deflate, at most a 32K window, without a preset dictionary.
                    return cmf & 0x0f == 8 && cmf >> 4 <= 7 && flg & 0x20 == 0
                        && (Int(cmf) << 8 | Int(flg)) % 31 == 0
                }
                offset += length + 12
            }
            return false
        }
    }

    private nonisolated static func isJPEG(_ bytes: Data) -> Bool {
        guard bytes.count >= 4, bytes.prefix(3).elementsEqual([0xff, 0xd8, 0xff]),
              bytes.suffix(2).elementsEqual([0xff, 0xd9]) else { return false }
        return bytes.withUnsafeBytes { raw -> Bool in
            let b = raw.bindMemory(to: UInt8.self)
            func word(_ offset: Int) -> Int { Int(b[offset]) << 8 | Int(b[offset + 1]) }
            let eoi = raw.count - 2
            var offset = 2
            var frameComponents = 0
            while offset < eoi {
                while offset < eoi, b[offset] == 0xff { offset += 1 }
                guard offset < eoi else { return false }
                let marker = b[offset]
                offset += 1
                if marker == 0x00 || marker == 0xd8 || marker == 0xd9 { return false }
                if marker == 0x01 || (0xd0...0xd7).contains(marker) { continue }
                guard offset + 2 <= eoi else { return false }
                let length = word(offset)
                guard length >= 2, offset + length <= eoi else { return false }
                // Start Of Frame (except DHT, JPG and DAC): require non-zero height and width.
                if [0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].contains(marker) {
                    let components = Int(b[offset + 7])
                    guard components > 0, length == 8 + 3 * components,
                          word(offset + 3) > 0, word(offset + 5) > 0 else { return false }
                    frameComponents = components
                }
                if marker == 0xda { // Start Of Scan: a frame and at least one entropy-coded byte are required
                    guard length >= 3, frameComponents > 0 else { return false }
                    let components = Int(b[offset + 2])
                    guard components > 0, components <= frameComponents, length == 6 + 2 * components else { return false }
                    var scan = offset + length
                    while scan < eoi {
                        if b[scan] != 0xff || (scan + 1 < eoi && b[scan + 1] == 0x00) { return true }
                        scan += 1
                    }
                    return false
                }
                offset += length
            }
            return false
        }
    }

    /// An image as the hub takes it: PNG, or JPEG at falling quality when the PNG is too big (`ClipboardLimits`). Still
    /// too big, the smallest JPEG comes back anyway: sending it is refused as too large, said in words.
    static func encode(_ image: UIImage) -> ClipboardImage? {
        if let png = image.pngData(), png.count <= ClipboardLimits.imageBytes { return ClipboardImage(mime: "image/png", bytes: png) }
        var smallest: Data?
        for quality in ClipboardLimits.jpegQualities {
            guard let jpeg = image.jpegData(compressionQuality: quality) else { continue }
            if jpeg.count <= ClipboardLimits.imageBytes { return ClipboardImage(mime: "image/jpeg", bytes: jpeg) }
            smallest = jpeg
        }
        return smallest.map { ClipboardImage(mime: "image/jpeg", bytes: $0) }
    }

    /// What the system Paste button (`PasteButton`) handed over: text first, then an image, then a link; nil when none
    /// of them could be read.
    static func content(of providers: [NSItemProvider]) async -> ClipboardContent? {
        for p in providers where p.canLoadObject(ofClass: String.self) {
            if let text = try? await p.loadString(), !text.isEmpty { return .text(text) }
        }
        for p in providers where p.hasItemConformingToTypeIdentifier(UTType.image.identifier) {
            if let data = try? await p.loadData(UTType.image),
               let encoded = UIImage(data: data).flatMap(encode) {
                return .image(encoded)
            }
        }
        for p in providers where p.hasItemConformingToTypeIdentifier(UTType.url.identifier) {
            if let data = try? await p.loadData(UTType.url), let url = URL(dataRepresentation: data, relativeTo: nil) {
                return .text(url.absoluteString)
            }
        }
        return nil
    }
}

private extension NSItemProvider {
    nonisolated func loadString() async throws -> String {
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<String, Error>) in
            _ = loadObject(ofClass: String.self) { text, error in
                if let text { done.resume(returning: text) } else { done.resume(throwing: error ?? CocoaError(.fileReadUnknown)) }
            }
        }
    }

    nonisolated func loadData(_ type: UTType) async throws -> Data {
        try await withCheckedThrowingContinuation { (done: CheckedContinuation<Data, Error>) in
            _ = loadDataRepresentation(for: type) { data, error in
                if let data { done.resume(returning: data) } else { done.resume(throwing: error ?? CocoaError(.fileReadUnknown)) }
            }
        }
    }
}
