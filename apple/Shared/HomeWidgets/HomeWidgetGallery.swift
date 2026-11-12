#if DEBUG && canImport(UIKit)
import SwiftUI
import WidgetKit

/// Every widget at its size, in each way the system draws it, for screenshots and design review (`-showHomeWidgets`,
/// docs/apple/home-widgets.md「Screenshots」). A view outside a widget gets no container background, family, margins
/// or tint from the system, so the gallery supplies each: the stage, the size, the 16 pt margins a module widget has,
/// and a stand-in for the tint (`homeSimTint`). Tinted and clear are close to, not exactly, what iOS draws; the shots
/// on the Home Screen itself are the proof.
enum HomeGallery {
    /// How the system draws a widget: on the Home Screen in light, dark, tinted or clear; on the Lock Screen.
    enum Look: String, CaseIterable, Identifiable {
        case light, dark, tinted, clear, lock
        var id: String { rawValue }
        var title: String {
            switch self {
            case .light: "Light"
            case .dark: "Dark"
            case .tinted: "Tinted"
            case .clear: "Clear"
            case .lock: "Lock Screen"
            }
        }
        var scheme: ColorScheme { self == .light ? .light : .dark }
        var mode: WidgetRenderingMode {
            switch self {
            case .light, .dark: .fullColor
            case .tinted, .clear: .accented
            case .lock: .vibrant
            }
        }
    }

    /// The tint the gallery paints tinted widgets with (one of the system's choices: a warm amber).
    static let tint = Color(hex: 0xF2B45A)
    /// iPhone 17 Pro Max's widget sizes; iPad Pro 13-inch's extra large; the Lock Screen's.
    static func size(_ family: WidgetFamily) -> CGSize {
        switch family {
        case .systemSmall: CGSize(width: 170, height: 170)
        case .systemMedium: CGSize(width: 364, height: 170)
        case .systemLarge: CGSize(width: 364, height: 382)
        case .systemExtraLarge: CGSize(width: 795, height: 379)
        case .accessoryCircular: CGSize(width: 76, height: 76)
        case .accessoryRectangular: CGSize(width: 172, height: 76)
        default: CGSize(width: 260, height: 22)
        }
    }
    static let corner: CGFloat = 24
    static let margin: CGFloat = 16
    static let pad: CGFloat = 18
    static let tileCorner: CGFloat = 14
}

/// What sits behind a widget in a shot: a made-up wallpaper.
struct HomeGalleryWallpaper: View {
    var look: HomeGallery.Look
    var body: some View {
        switch look {
        case .light:
            LinearGradient(colors: [Color(hex: 0xDCE7F5), Color(hex: 0xF3E3EC)], startPoint: .topLeading, endPoint: .bottomTrailing)
        case .dark:
            LinearGradient(colors: [Color(hex: 0x0E1B33), Color(hex: 0x2B1630)], startPoint: .topLeading, endPoint: .bottomTrailing)
        case .tinted:
            LinearGradient(colors: [Color(hex: 0x1E140A), Color(hex: 0x3A2610)], startPoint: .topLeading, endPoint: .bottomTrailing)
        case .clear:
            LinearGradient(colors: [Color(hex: 0x3E6FB0), Color(hex: 0xB0566F), Color(hex: 0xE59A4B)], startPoint: .topLeading, endPoint: .bottomTrailing)
        case .lock:
            LinearGradient(colors: [Color(hex: 0x23305A), Color(hex: 0x5A3E6E)], startPoint: .top, endPoint: .bottom)
        }
    }
}

/// One widget as the system would frame it.
struct HomeGalleryCell: View {
    var kind: HomeKind
    var family: WidgetFamily
    var look: HomeGallery.Look
    var entry: HomeEntry
    /// Redacted as on a locked phone.
    var locked = false

    var body: some View {
        let size = HomeGallery.size(family)
        let accessory = family == .accessoryCircular || family == .accessoryRectangular || family == .accessoryInline
        HomeWidgetView(kind: kind, entry: entry, size: family)
            .padding(accessory ? 0 : HomeGallery.margin)
            .frame(width: size.width, height: size.height)
            .background { if !accessory { background } }
            .clipShape(RoundedRectangle(cornerRadius: accessory ? 0 : HomeGallery.corner, style: .continuous))
            .environment(\.colorScheme, look.scheme)
            .environment(\.widgetRenderingMode, look.mode)
            .environment(\.homeSimTint, simTint)
            .redacted(reason: locked ? .privacy : [])
    }

    private var simTint: Color? {
        switch look {
        case .light, .dark: nil
        case .tinted: HomeGallery.tint
        case .clear, .lock: .white
        }
    }

    @ViewBuilder private var background: some View {
        switch look {
        case .light, .dark: HomeWidgetTokens.surface
        case .tinted: Color(hex: 0x2A1E12).opacity(0.92)
        case .clear, .lock: Color.white.opacity(0.12)
        }
    }
}

/// A titled row of cells for one widget at one size, every look side by side.
struct HomeGalleryRow: View {
    var title: String
    var kind: HomeKind
    var family: WidgetFamily
    var looks: [HomeGallery.Look]
    var entry: HomeEntry

    var body: some View {
        VStack(alignment: .leading, spacing: HomeWidgetTokens.gap) {
            Text(verbatim: title).font(.headline).foregroundStyle(.black.opacity(0.75))
            HStack(alignment: .top, spacing: HomeGallery.pad) {
                ForEach(looks) { look in
                    VStack(spacing: HomeWidgetTokens.tight) {
                        HomeGalleryCell(kind: kind, family: family, look: look, entry: entry)
                            .padding(HomeGallery.pad)
                            .background(HomeGalleryWallpaper(look: look))
                            .clipShape(RoundedRectangle(cornerRadius: HomeGallery.tileCorner, style: .continuous))
                        Text(verbatim: look.title).font(.caption).foregroundStyle(.black.opacity(0.55))
                    }
                }
            }
        }
    }
}

/// A page of rows with a title, as one image.
struct HomeGallerySheet<Content: View>: View {
    var title: String
    var note: String?
    @ViewBuilder var content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: HomeGallery.pad * 1.2) {
            VStack(alignment: .leading, spacing: HomeWidgetTokens.tight) {
                Text(verbatim: title).font(.largeTitle.bold()).foregroundStyle(.black.opacity(0.85))
                if let note { Text(verbatim: note).font(.callout).foregroundStyle(.black.opacity(0.55)) }
            }
            content
        }
        .padding(HomeGallery.pad * 2)
        .background(Color(hex: 0xF4F4F6))
    }
}
#endif
