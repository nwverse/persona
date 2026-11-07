import PersonaKit
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// Images in a conversation. Inline ones decode from their base64; lazy ones (history leaves the bytes out) come from
/// the hub the first time they are shown, then from memory and from disk: an item's image never changes.
nonisolated enum ChatImageLoader {
    nonisolated(unsafe) private static let images: NSCache<NSString, UIImage> = { // NSCache is thread-safe
        let c = NSCache<NSString, UIImage>()
        c.totalCostLimit = 96 << 20
        return c
    }()
    private static let fetches = Fetches()

    private static func key(_ p: ImagePayload, _ side: CGFloat?) -> NSString {
        "\(p.identity)@\(side.map { Int($0) } ?? 0)" as NSString
    }

    /// Settings › Storage cleared the files: drop the decoded copies too.
    static func clearMemory() { images.removeAllObjects() }

    /// Already decoded: lets a row that scrolls back into view show its image without a placeholder frame.
    static func cached(_ p: ImagePayload, side: CGFloat?) -> UIImage? { images.object(forKey: key(p, side)) }

    /// The image, scaled down to fit `side` × `side` (nil: full size). Decoding runs off the main thread.
    static func image(_ p: ImagePayload, side: CGFloat?) async -> UIImage? {
        let k = key(p, side)
        if let img = images.object(forKey: k) { return img }
        guard let data = await data(p) else { return nil }
        let img: UIImage? = await Task.detached(priority: .userInitiated) {
            guard let full = UIImage(data: data) else { return nil }
            guard let side else { return full.preparingForDisplay() ?? full }
            return full.preparingThumbnail(of: ZoomMath.fittedSize(image: full.size, in: CGSize(width: side, height: side))) ?? full
        }.value
        if let img { images.setObject(img, forKey: k, cost: Int(img.size.width * img.size.height * img.scale * img.scale * 4)) }
        // Downloaded bytes that are not a picture (a proxy's page, a cut body) must not stay on disk: a retry fetches again.
        else if p.data.isEmpty, let ref = p.ref { await fetches.forget(ref) }
        return img
    }

    /// The encoded bytes (for saving / sharing the original).
    static func data(_ p: ImagePayload) async -> Data? {
        if !p.data.isEmpty {
            let s = p.data
            return await Task.detached { Data(base64Encoded: s) }.value
        }
        guard let ref = p.ref else { return nil }
        return await fetches.data(ref)
    }

    /// One download per image however many views ask for it at once; finished ones are kept on disk.
    private actor Fetches {
        private var running: [String: Task<Data?, Never>] = [:]
        private let dir: URL = {
            let d = URL.cachesDirectory.appending(path: "chat-images")
            try? FileManager.default.createDirectory(at: d, withIntermediateDirectories: true)
            return d
        }()

        private func file(_ ref: String) -> URL {
            dir.appending(path: String(ref.map { $0.isLetter || $0.isNumber || $0 == "-" ? $0 : "_" }))
        }

        /// Drops the kept bytes of an image that did not decode.
        func forget(_ ref: String) {
            try? FileManager.default.removeItem(at: file(ref))
        }

        func data(_ ref: String) async -> Data? {
            let file = file(ref)
            if let d = try? Data(contentsOf: file) { return d }
            if let t = running[ref] { return await t.value }
            let t = Task<Data?, Never> {
                guard let api = await MainActor.run(body: { AppModel.shared.api }),
                      let d = try? await api.image(ref: ref) else { return nil }
                try? d.write(to: file, options: .atomic)
                return d
            }
            running[ref] = t
            let d = await t.value
            running[ref] = nil
            return d
        }
    }
}

/// A square thumbnail filling its frame (the caller sizes and clips it).
struct ChatThumbnail: View {
    var payload: ImagePayload
    var side: CGFloat = 360
    @State private var image: UIImage?

    var body: some View {
        Group {
            if let img = image ?? ChatImageLoader.cached(payload, side: side) {
                Image(uiImage: img).resizable().scaledToFill()
            } else {
                Color.clear
            }
        }
        .task(id: payload.identity) {
            if image == nil { image = await ChatImageLoader.image(payload, side: side) }
        }
    }
}
