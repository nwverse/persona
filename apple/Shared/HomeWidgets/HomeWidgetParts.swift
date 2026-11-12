import SwiftUI
import WidgetKit

/// The pieces every Home Screen widget is built from (docs/apple/home-widgets.md「Pieces」): the stage behind it, the
/// tiles of the Persona widget, a module's label, numerals, dots, rings and bars. Each draws itself for the mode the
/// system renders in: its own colors in full color; in tinted, clear and Lock Screen modes, where the system paints
/// every opaque pixel in one color (two with the accent group), only shapes and the primary / secondary levels, the
/// states in the accent group.

/// The levels of ink words and marks use.
enum HomeInk { case primary, soft, faint }

extension View {
    /// Words and marks in one of the stage's inks, or the system's levels where it paints the widget itself.
    func homeInk(_ level: HomeInk) -> some View { modifier(InkModifier(level: level)) }

    /// A state's color on a mark (`dot`) or on words (`text`); in the accent color where the system has one.
    func homeTone(_ tone: StatusTone, text: Bool = false) -> some View { modifier(ToneModifier(tone: tone, text: text, plain: nil)) }

    /// A color of the widget's own (a recording's red, a usage window's fill), with the same rule as a state's.
    func homeTone(_ color: Color) -> some View { modifier(ToneModifier(tone: nil, text: false, plain: color)) }

    /// In the accent group: the color the system tints with in the tinted and clear modes and on the Lock Screen.
    func homeAccent() -> some View { modifier(AccentModifier()) }
}

#if DEBUG
/// The gallery's stand-in for the system's tint (`-showHomeWidgets`, docs/apple/home-widgets.md「Screenshots」): a
/// view outside a widget is never tinted, so the gallery paints the accent group in this color itself.
private struct HomeSimTintKey: EnvironmentKey { static let defaultValue: Color? = nil }
extension EnvironmentValues {
    var homeSimTint: Color? {
        get { self[HomeSimTintKey.self] }
        set { self[HomeSimTintKey.self] = newValue }
    }
}
#endif

private struct AccentModifier: ViewModifier {
    #if DEBUG
    @Environment(\.homeSimTint) private var simTint
    #endif

    func body(content: Content) -> some View {
        #if DEBUG
        if let simTint { content.widgetAccentable().colorMultiply(simTint) } else { content.widgetAccentable() }
        #else
        content.widgetAccentable()
        #endif
    }
}

private struct InkModifier: ViewModifier {
    var level: HomeInk
    @Environment(\.widgetRenderingMode) private var mode
    #if DEBUG
    @Environment(\.homeSimTint) private var simTint
    #endif

    func body(content: Content) -> some View { content.foregroundStyle(style) }

    private var style: AnyShapeStyle {
        #if DEBUG
        // The gallery: the system's white levels, which a view outside a widget does not get.
        if simTint != nil { return AnyShapeStyle(Color.white.opacity(level == .primary ? 1 : level == .soft ? 0.7 : 0.45)) }
        #endif
        if mode == .fullColor {
            return AnyShapeStyle(level == .primary ? HomeWidgetTokens.ink : level == .soft ? HomeWidgetTokens.inkSoft : HomeWidgetTokens.inkFaint)
        }
        switch level {
        case .primary: return AnyShapeStyle(.primary)
        case .soft: return AnyShapeStyle(.secondary)
        case .faint: return AnyShapeStyle(.tertiary)
        }
    }
}

private struct ToneModifier: ViewModifier {
    var tone: StatusTone?
    var text: Bool
    var plain: Color?
    @Environment(\.widgetRenderingMode) private var mode

    func body(content: Content) -> some View {
        if mode == .fullColor {
            content.foregroundStyle(plain ?? (text ? tone?.text : tone?.dot) ?? HomeWidgetTokens.ink)
        } else {
            content.foregroundStyle(Color.white).homeAccent()
        }
    }
}

/// The stage: the icon's paper (or its dark) with the persona's glow, behind every widget. Where the system draws its
/// own background (tinted, clear, StandBy, the Lock Screen) this is not drawn at all.
struct HomeStage: View {
    /// The color glowing from where the face stands; nil: none.
    var glow: Color?
    /// Where the glow is, in the widget's unit square.
    var at: UnitPoint = .center

    var body: some View {
        ZStack {
            LinearGradient(colors: [HomeWidgetTokens.stageTop, HomeWidgetTokens.stageBottom], startPoint: .top, endPoint: .bottom)
            if let glow {
                GeometryReader { geo in
                    let reach = min(geo.size.width, geo.size.height) * HomeWidgetTokens.glowReach
                    RadialGradient(colors: [glow.opacity(HomeWidgetTokens.glowOpacity), glow.opacity(0)],
                                   center: at, startRadius: 0, endRadius: reach)
                }
            }
        }
    }
}

/// A module's tile in the Persona widget: a soft card on the stage, its corners following the widget's.
struct HomeTile<Content: View>: View {
    @ViewBuilder var content: Content
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        content
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background {
                let shape = ContainerRelativeShape()
                if mode == .fullColor {
                    shape.fill(HomeWidgetTokens.tile).overlay(shape.strokeBorder(HomeWidgetTokens.tileEdge, lineWidth: 1))
                } else {
                    shape.fill(HomeWidgetTokens.tileMono)
                }
            }
    }
}

/// A module's name, small and bold, with its mark.
struct HomeLabel: View {
    var title: LocalizedStringKey
    var symbol: String

    var body: some View {
        Label { Text(title).lineLimit(1) } icon: { Image(systemName: symbol) }
            .labelStyle(TightLabel())
            .font(HomeWidgetTokens.label)
            .textCase(.uppercase)
            .homeInk(.soft)
    }
}

private struct TightLabel: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: HomeWidgetTokens.tight) { configuration.icon; configuration.title }
    }
}

/// A state's dot.
struct HomeDot: View {
    var tone: StatusTone
    var body: some View {
        Circle().frame(width: HomeWidgetTokens.dot, height: HomeWidgetTokens.dot).homeTone(tone)
    }
}

/// A ring filled to `fraction`, on its track.
struct HomeRing: View {
    var fraction: Double
    var color: Color
    var width: CGFloat = HomeWidgetTokens.ring
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        ZStack {
            Circle().stroke(mode == .fullColor ? HomeWidgetTokens.track : HomeWidgetTokens.trackMono, lineWidth: width)
            Circle().trim(from: 0, to: max(0.001, min(1, fraction)))
                .stroke(style: StrokeStyle(lineWidth: width, lineCap: .round))
                .rotationEffect(.degrees(-90))
                .homeTone(color)
        }
        .padding(width / 2)
    }
}

/// A bar filled to `fraction`, on its track, with an optional mark (the 7-day average under today's steps).
struct HomeBar: View {
    var fraction: Double
    var color: Color
    var height: CGFloat = HomeWidgetTokens.bar
    var mark: Double?
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            ZStack(alignment: .leading) {
                Capsule().fill(mode == .fullColor ? HomeWidgetTokens.track : HomeWidgetTokens.trackMono)
                Capsule().frame(width: max(height, w * max(0, min(1, fraction)))).homeTone(color)
                if let mark {
                    Capsule().frame(width: HomeWidgetTokens.hair, height: height + HomeWidgetTokens.tight)
                        .homeInk(.primary)
                        .offset(x: w * max(0, min(1, mark)) - HomeWidgetTokens.hair / 2)
                }
            }
        }
        .frame(height: height)
    }
}

/// The big number a widget is about.
struct HomeNumeral: View {
    var text: String
    var font: Font = HomeWidgetTokens.numeralHero
    var tone: StatusTone?
    var color: Color?

    var body: some View {
        let t = Text(verbatim: text).font(font).lineLimit(1).minimumScaleFactor(HomeWidgetTokens.numeralMinScale).monospacedDigit()
        if let tone { t.homeTone(tone, text: true) } else if let color { t.homeTone(color) } else { t.homeInk(.primary) }
    }
}

/// A count in words beside its state's dot ("3 working").
struct HomeCount: View {
    var tone: StatusTone
    var text: String

    var body: some View {
        HStack(spacing: HomeWidgetTokens.tight) {
            HomeDot(tone: tone)
            Text(verbatim: text).font(HomeWidgetTokens.metaStrong).lineLimit(1).homeInk(.soft)
        }
    }
}

/// The persona's words in a bubble (the Persona widget's large and extra large sizes): what it would tell you first.
struct HomeSpeech: View {
    var text: String
    @Environment(\.widgetRenderingMode) private var mode

    var body: some View {
        Text(verbatim: text)
            .font(HomeWidgetTokens.speech)
            .lineLimit(2)
            .multilineTextAlignment(.center)
            .homeInk(.primary)
            .padding(.horizontal, HomeWidgetTokens.tilePadding)
            .padding(.vertical, HomeWidgetTokens.tight + HomeWidgetTokens.hair)
            .background(Capsule().fill(mode == .fullColor ? HomeWidgetTokens.tile : HomeWidgetTokens.tileMono))
    }
}

/// "Updated 40 min ago" under content that stopped coming, so old numbers never pass for current ones.
struct HomeStaleNote: View {
    var since: Date
    /// The entry's date: a timeline's later entries say longer ago.
    var now: Date
    @Environment(\.locale) private var locale

    var body: some View {
        Text("Updated \(ago) [Home Widgets]").font(HomeWidgetTokens.meta).lineLimit(1).homeInk(.faint)
    }

    private var ago: String {
        let f = RelativeDateTimeFormatter()
        f.locale = locale
        f.unitsStyle = .full
        return f.localizedString(for: since, relativeTo: now)
    }
}
