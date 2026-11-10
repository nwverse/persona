import SwiftUI

// UI version 6's pictures (docs/extensions/extensions.md "UI version 6", "Pictures through the hub", #827): an `image`
// component, and a picture in a tile or a row's thumbnail. An https `url` loads without any of the app's credentials; a
// `path` the hub signed for this page's extension loads through the page with the device's (`picture(_:pixels:)`), and
// when it does not load (its token may have run out) the page is drawn again, signed anew, at most once a minute.
// While it loads the plain fill shows; when it cannot, its alt text over a broken-picture mark.

/// A picture from an extension's `ImageSource`.
struct ExtensionPicture: View {
    var src: ExtensionImageSource
    var model: (any ExtensionViewControlling)?
    /// How wide it is drawn, in points: the hub sends it at about that many pixels.
    var points: CGFloat = Theme.Size.tileRegular
    /// Fills its frame (cropped) or fits in it.
    var fill = true
    var alt: String? = nil
    /// The picture's bytes once loaded (the full-screen viewer shows them).
    var loaded: ((Data) -> Void)? = nil
    @Environment(\.displayScale) private var scale
    @State private var image: Image?
    @State private var failed = false

    var body: some View {
        ZStack {
            if let image {
                if fill { image.resizable().scaledToFill() } else { image.resizable().scaledToFit() }
            } else if failed {
                VStack(spacing: Theme.Space.xs) {
                    ThemedIcon(Theme.Icon.brokenImage).font(Theme.Font.heading).foregroundStyle(.tertiary)
                    if let alt, !alt.isEmpty {
                        Text(alt).font(Theme.Font.label).foregroundStyle(.secondary).multilineTextAlignment(.center).lineLimit(3)
                    }
                }
                .padding(Theme.Space.s)
            } else {
                Rectangle().fill(Theme.Fill.well)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.Fill.well)
        .task(id: src) { await load() }
    }

    private func load() async {
        image = nil
        failed = false
        do {
            let data: Data
            if let s = src.url, let url = ExtensionComponentRowLabel<EmptyView>.image(s) {
                data = try await ExtensionRemoteImage.session.data(from: url).0
            } else if let path = src.path, let model {
                data = try await model.picture(path, pixels: Int((points * scale).rounded(.up)))
            } else {
                failed = true
                return
            }
            guard let img = UIImage(data: data) else { failed = true; return }
            image = Image(uiImage: img)
            loaded?(data)
        } catch {
            guard !Task.isCancelled else { return }
            failed = true
            if src.path != nil { model?.pictureFailed() }
        }
    }
}

/// An extension's `image`: its picture in its shape (`aspect`, from `width` / `height` when `auto`), cropped or fitted,
/// its caption under it; a tap shows it full screen unless `no_zoom`. VoiceOver reads its alt text.
struct ExtensionImageItem: View {
    var item: ExtensionImageComponent
    var model: (any ExtensionViewControlling)?
    @State private var data: Data?
    @State private var viewing = false

    private var ratio: CGFloat {
        switch item.aspect {
        case "square": 1
        case "wide": 16 / 9
        case "portrait": 3 / 4
        case "auto":
            if let w = item.src.width, let h = item.src.height, w > 0, h > 0 { CGFloat(w) / CGFloat(h) } else { 4 / 3 }
        default: 4 / 3
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ExtensionPicture(src: item.src, model: model, points: Theme.Size.box150 * 3, fill: item.fit != "fit", alt: item.alt) { data = $0 }
                .aspectRatio(ratio, contentMode: .fit)
                .frame(maxWidth: .infinity)
                .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                .contentShape(.rect)
                .onTapGesture { if item.noZoom != true, data != nil { viewing = true } }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(item.alt)
                .accessibilityAddTraits(.isImage)
                .accessibilityAddTraits(item.noZoom != true ? .isButton : [])
                .accessibilityIdentifier("plugin-ui-image-\(item.key ?? item.alt)")
            if let c = item.caption, !c.isEmpty {
                Text(c).font(Theme.Font.label).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        #if os(iOS)
        .fullScreenCover(isPresented: $viewing) {
            if let payload = data.flatMap(Self.payload) {
                ImageViewer(items: [ViewerImage(payload: payload, title: item.alt, detail: item.caption)], index: 0)
            }
        }
        #endif
    }

    /// The picture as the viewer takes it: its bytes as they came, their type from their first bytes.
    nonisolated static func payload(_ data: Data) -> ImagePayload? {
        let mime: String
        switch [UInt8](data.prefix(4)) {
        case [0x89, 0x50, 0x4E, 0x47]: mime = "image/png"
        case let b where b.starts(with: [0xFF, 0xD8]): mime = "image/jpeg"
        case [0x47, 0x49, 0x46, 0x38]: mime = "image/gif"
        case [0x52, 0x49, 0x46, 0x46]: mime = "image/webp"
        default: return nil
        }
        return ImagePayload(mime: mime, data: data.base64EncodedString())
    }
}
