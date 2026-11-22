import SwiftUI

/// One Theme Color (`ThemeColor`): what can be tapped, the user's own bubble and its words, Lark's links and my bubble, and the
/// pale wash that the styles with a colored canvas (Clay, Island) paint with it. The same in every style, and held to
/// the floors on every style's page (StyleSpecTests).
nonisolated struct ColorSpec: Sendable {
    typealias Pair = PersonaPalette.Pair

    /// The canvas a hue gives Clay and Island (the other styles keep a neutral page): the page, cards a step lighter,
    /// and the puffy surfaces' colored shadow (light; black in dark) and lit edge (dark; white in light).
    struct Wash: Sendable {
        var page: Pair
        var card: Pair
        var shadow: UInt32
        var highlight: UInt32

        init(page: Pair, card: Pair, shadow: UInt32, highlight: UInt32) {
            self.page = page
            self.card = card
            self.shadow = shadow
            self.highlight = highlight
        }

        /// Worked out from an accent, close to what Clay and Island chose by hand for theirs: a page nine parts white,
        /// a card a step lighter (in dark, the accent faint on a neutral dark, lighter than the system's cells), a
        /// shadow a little deeper than the accent. Light enough that every accent meeting its floors reads on it as a link.
        init(accent a: Pair) {
            let mix = PersonaPalette.mix
            page = Pair(light: mix(a.light, 0xFFFFFF, 0.9), dark: mix(a.dark, 0x000000, 0.93))
            card = Pair(light: mix(a.light, 0xFFFFFF, 0.95), dark: mix(a.dark, 0x1E1E22, 0.85))
            shadow = mix(a.light, 0x000000, 0.12)
            highlight = mix(card.dark, 0xFFFFFF, 0.07)
        }
    }

    var title: String
    /// Where it comes from (the style that brought it).
    var subtitle: String
    var accent: Color
    var userBubble: Color
    var userBubbleText: Color
    /// Colored bubbles darken a little toward the bottom; light ones stay flat.
    var deepensBubble: Bool
    var wash: Wash
    var larkBlue: Color
    var larkMyBubble: Color
    var extensionRoles: ExtensionThemePalette? = nil

    /// `larksOwn`: Lark's chrome takes the bubble as it is (Lark Blue, the Lark gateway's own); otherwise a pale bubble of the accent.
    init(title: String, subtitle: String, accent: Pair, bubble: Color, bubbleText: Color, deepensBubble: Bool,
         wash: Wash? = nil, larksOwn: Bool = false) {
        self.title = title
        self.subtitle = subtitle
        self.accent = accent.color
        userBubble = bubble
        userBubbleText = bubbleText
        self.deepensBubble = deepensBubble
        self.wash = wash ?? Wash(accent: accent)
        larkBlue = accent.color
        larkMyBubble = larksOwn ? bubble : accent.color.opacity(0.16)
    }

    init(extensionTheme theme: ExtensionThemePalette, over base: ColorSpec) {
        self = base
        title = theme.name
        subtitle = String(localized: "Extension Theme")
        accent = theme.accent.color
        larkBlue = accent
        larkMyBubble = accent.opacity(0.16)
        wash = Wash(accent: theme.accent)
        extensionRoles = theme
    }

    /// A persona's colors (docs/apple/persona.md「Color scheme → design tokens」), the style's own where it brings none.
    init(persona p: PersonaPalette, over base: ColorSpec) {
        self = base
        title = ThemeColor.persona.title
        subtitle = String(localized: "Avatar's colors")
        if let a = p.accent {
            accent = a.color
            wash = Wash(accent: a)
            larkBlue = a.color
            larkMyBubble = a.color.opacity(0.16)
        }
        if let b = p.bubble {
            userBubble = b.color
            userBubbleText = p.bubbleText?.color ?? .white
            // A colored bubble under white words deepens like the styles' own; a pale one under dark words stays flat.
            deepensBubble = p.bubbleText.map { PersonaPalette.luminance($0.light) > 0.5 } ?? true
        }
    }
}

/// Settings › Appearance › Theme Color (UserDefaults `ui.color`): each style's own colors (the default), the persona's, or one style's colors
/// on any style. Changing it rebuilds the view tree like a style does (`AppModel.look`).
nonisolated enum ThemeColor: String, CaseIterable, Identifiable, Sendable {
    /// Match Interface Style: the style's own (`ComponentSpec.colors`), as each looked before the colors were a choice.
    case style
    /// Match Avatar: the persona's (only offered when it brings colors); the style's while it brings none.
    case persona
    case blue
    case coral
    case lavender
    case teal
    case lark
    case extensionTheme

    nonisolated(unsafe) static var current: ThemeColor = AppearanceChoice.saved.color {
        didSet { StyleSpec.recompose() }
    }

    /// The fixed ones, in the picker's order after the two that follow something.
    static let presets: [ThemeColor] = [.blue, .coral, .lavender, .teal, .lark]

    var id: String { rawValue }

    var title: String {
        switch self {
        case .style: String(localized: "Match Interface Style")
        case .persona: String(localized: "Match Avatar")
        case .extensionTheme: ExtensionThemePalette.current.map { String(localized: "Extension · \($0.name)") } ?? String(localized: "Extension Theme")
        default: preset?.title ?? ""
        }
    }

    /// A fixed color's spec; nil for the two that follow something.
    var preset: ColorSpec? {
        switch self {
        case .style, .persona, .extensionTheme: nil
        case .blue: .blue
        case .coral: .coral
        case .lavender: .lavender
        case .teal: .teal
        case .lark: .lark
        }
    }

    func colors(style: AppStyle, persona: PersonaPalette?) -> ColorSpec {
        switch self {
        case .style: style.component.colors
        case .persona: persona.map { ColorSpec(persona: $0, over: style.component.colors) } ?? style.component.colors
        case .extensionTheme:
            ExtensionThemePalette.current.map { ColorSpec(extensionTheme: $0, over: style.component.colors) } ?? style.component.colors
        default: preset ?? style.component.colors
        }
    }
}

/// The saved Interface Style and Theme Color (UserDefaults `ui.style`, `ui.color`; `-ui.style clay -ui.color coral` as launch
/// arguments). Until 2026-10-31 a style brought its colors and a persona laid its own over them; read the first time,
/// the old settings become the choices that look the same: the style with Match Interface Style, Claude (Liquid Glass in coral,
/// now only a color) as Liquid Glass with Coral, and with a persona chosen, Match Avatar.
nonisolated struct AppearanceChoice: Equatable, Sendable {
    var style: AppStyle
    var color: ThemeColor

    static let styleKey = "ui.style"
    static let colorKey = "ui.color"
    /// `PersonaStore.chosenKey`, shared with the watch and extension targets.
    static let personaKey = "persona.id"

    static func resolve(style: String?, color: String?, persona: String?) -> AppearanceChoice {
        let wasClaude = style == "claude"
        let s = AppStyle(rawValue: style ?? "") ?? .glass
        if let c = color.flatMap(ThemeColor.init(rawValue:)) {
            return AppearanceChoice(style: s, color: wasClaude && c == .style ? .coral : c)
        }
        let personaChosen = PersonaSelection.resolve(persona) != PersonaSelection.builtinID
        return AppearanceChoice(style: s, color: personaChosen ? .persona : wasClaude ? .coral : .style)
    }

    /// Read once at launch; settings from before the split are written back as what they became.
    static let saved: AppearanceChoice = {
        let d = UserDefaults.standard
        _ = NativeStateMigration.defaults(d, domainName: Bundle.main.bundleIdentifier)
        let style = d.string(forKey: styleKey), color = d.string(forKey: colorKey)
        let choice = resolve(style: style, color: color, persona: NativeStateMigration.readPersonaID(from: d))
        if color == nil || style == "claude" {
            d.set(choice.style.rawValue, forKey: styleKey)
            d.set(choice.color.rawValue, forKey: colorKey)
        }
        return choice
    }()
}
