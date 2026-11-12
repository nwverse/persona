import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// The Home Screen and Lock Screen widgets (docs/apple/home-widgets.md). The widget extension builds this file but not
/// `Theme`, as it does `IslandMetrics` and `WatchFaceTokens`: a table of its own. Widget sizes are the system's, so the
/// big numerals and the faces are fixed sizes (the documented exception in DESIGN_SYSTEM.md 2.2); every line of words
/// is a text style and follows the text size.
///
/// The look: the persona's stage. Every widget stands on the paper of the app icon (its dark in the dark) with the
/// icon's ink for words, so a row of them reads as one family; the only strong colors are the agent states'
/// (`StatusPalette`), a recording's red and the persona's own color behind its face.
nonisolated enum HomeWidgetTokens {
    // MARK: Stage

    /// The widget's background, top to bottom: the icon's paper, or its dark (`MochiInk`'s disc, lifted a little so
    /// the face's own disc still shows on it).
    static let stageTop = Color(light: 0xFBF5EE, dark: 0x2B2421)
    static let stageBottom = Color(light: 0xF1E5D6, dark: 0x181311)
    /// A module's tile on the stage.
    static let tile = dynamic(light: (0xFFFFFF, 0.72), dark: (0xFFFFFF, 0.07))
    /// A tile's hairline edge, so white on paper still has a shape.
    static let tileEdge = dynamic(light: (0x1D1816, 0.06), dark: (0xFFFFFF, 0.06))
    /// The same tile in the tinted, clear and Lock Screen modes, where the system paints every opaque pixel in one
    /// color: only a faint wash, or a tile would be a solid block.
    static let tileMono = Color.white.opacity(0.14)
    /// A track under a bar or a ring.
    static let track = dynamic(light: (0x1D1816, 0.08), dark: (0xFFFFFF, 0.12))
    static let trackMono = Color.white.opacity(0.25)

    // MARK: Ink

    /// Words: the icon's ink on paper, the paper on the dark.
    static let ink = Color(light: 0x1D1816, dark: 0xF6EEE6)
    /// Second lines and labels.
    static let inkSoft = Color(light: 0x6E6763, dark: 0xB3AAA4)
    /// Times, counts of the quiet kind, empty states.
    static let inkFaint = Color(light: 0x9C938D, dark: 0x7D746F)

    // MARK: States (the app's, StatusPalette; done is the app's unread)

    static let needsYou = StatusPalette.needsYou
    static let working = StatusPalette.working
    static let done = StatusPalette.unread
    /// A recording in progress and the Record button: the system's red (`Palette.recordingRed`, its darker button red
    /// under a white mark).
    static let recording = Palette.recordingRed
    static let recordButton = Palette.recordingButton
    /// The persona's glow behind its face when it has nothing to say: Mochi's cheek, a package's primary.
    static let mochiGlow = Mochi.cheekColor
    /// How strong a state's glow is behind the face, and how far it reaches past the face (× its side).
    static let glowOpacity = 0.2
    static let glowReach: CGFloat = 0.62

    /// A subscription window's fill by how full it is: calm, getting there (≥ 80 %, `UsageWindows.isWarn`), full.
    static let usageCalm = Color(light: 0x248A3D, dark: 0x30D158)
    static let usageWarn = Color(light: 0xF08A00, dark: 0xFF9F0A)
    static let usageFull = Color(light: 0xD93A3A, dark: 0xFF5F57)
    static let warnAt = 80.0
    /// Content from a snapshot older than `HomeSnapshot.staleAfter`.
    static let staleOpacity = 0.55
    static let fullAt = 95.0

    /// An event on Today: the app icon's coral.
    static let event = Palette.coral

    /// Health's three measures.
    static let steps = Color(light: 0xE0457B, dark: 0xFF5C8F)
    static let sleep = Color(light: 0x5B4FD6, dark: 0x9C94FF)
    static let heart = Color(light: 0xD93A3A, dark: 0xFF5F57)

    // MARK: Type

    /// The one number a widget is about ("2" need you): rounded, bold, as large as the size allows.
    static let numeralHero = Font.system(size: 48, weight: .bold, design: .rounded)
    static let numeralLarge = Font.system(size: 34, weight: .bold, design: .rounded)
    static let numeral = Font.system(size: 24, weight: .bold, design: .rounded)
    static let numeralSmall = Font.system(size: 17, weight: .bold, design: .rounded)
    /// How far a numeral may shrink to fit ("1.2M").
    static let numeralMinScale: CGFloat = 0.5
    /// The day of the month on Today.
    static let day = Font.system(size: 34, weight: .heavy, design: .rounded)
    /// A module's name above its content.
    static let label = Font.system(.caption2, design: .rounded, weight: .bold)
    /// A line of content and its emphasized form.
    static let line = Font.system(.footnote)
    static let lineStrong = Font.system(.footnote, weight: .semibold)
    /// The persona's words in its bubble.
    static let speech = Font.system(.subheadline, design: .rounded, weight: .semibold)
    /// Times and second lines.
    static let meta = Font.system(.caption2)
    static let metaStrong = Font.system(.caption2, weight: .semibold)
    /// Lock Screen: the rectangular widget's first line and the rest.
    static let lockTitle = Font.system(.headline)
    static let lockLine = Font.system(.caption)

    // MARK: Spacing and shape

    /// Between a widget's parts, and inside a tile.
    static let gap: CGFloat = 8
    static let tight: CGFloat = 4
    static let hair: CGFloat = 2
    static let tilePadding: CGFloat = 10
    /// A tile's corners; the tiles in a widget's corners follow the widget's own (`ContainerRelativeShape`).
    static let tileRadius: CGFloat = 14
    static let chipRadius: CGFloat = 6
    static let bar: CGFloat = 6
    static let barThin: CGFloat = 4
    static let dot: CGFloat = 7
    static let ring: CGFloat = 5
    static let ringThin: CGFloat = 3.5

    // MARK: Faces

    /// The persona at the middle of the Persona widget, by size.
    static let faceSmall: CGFloat = 60
    static let faceMedium: CGFloat = 76
    static let faceLarge: CGFloat = 100
    static let faceExtraLarge: CGFloat = 136
    /// The stage's color around the middle face, so the face stands clear of the four tiles it sits between.
    static let faceGap: CGFloat = 5
    /// The persona in a module widget's corner, saying how that module is.
    static let faceBadge: CGFloat = 30
    /// The Lock Screen's rectangular widget.
    static let faceLock: CGFloat = 44
    /// Done's check, × the face's side.
    static let checkShare: CGFloat = 0.34

    /// The Record button's side in a small widget, and its mark.
    static let recordSide: CGFloat = 64
    static let recordMark = Font.system(size: 26, weight: .semibold)

    // MARK: Symbols (the extension doesn't build Theme.Icon; each mirrors its token)

    static let agentsSymbol = "chevron.left.forwardslash.chevron.right" // icon: mirrors Theme.Icon.agents
    static let todaySymbol = "sun.max.fill" // icon: mirrors Theme.Icon.today
    static let todoSymbol = "checklist" // icon: mirrors Theme.Icon.todo
    static let calendarSymbol = "calendar" // icon: mirrors Theme.Icon.calendarView
    static let usageSymbol = "gauge.with.dots.needle.50percent" // icon: mirrors Theme.Icon.effort (how much is used)
    static let machinesSymbol = "desktopcomputer" // icon: mirrors Theme.Icon.machine
    static let recordSymbol = "waveform" // icon: mirrors RecordingSymbol.recording (the bare waveform on the red disc)
    static let stopSymbol = "stop.fill" // icon: mirrors Theme.Icon.interrupt
    static let healthSymbol = "heart" // icon: mirrors Theme.Icon.health
    static let stepsSymbol = "figure.walk" // icon: steps, as the Health app marks them
    static let sleepSymbol = "bed.double" // icon: sleep, as the Health app marks it
    static let energySymbol = "flame" // icon: active energy, as the Health app marks it
    static let inboxSymbol = "tray.full" // icon: mirrors Theme.Icon.inbox
    static let checkSymbol = "checkmark" // icon: mirrors Theme.Icon.done
    static let personaSymbol = "sparkle" // icon: mirrors Theme.Icon.persona

    #if canImport(UIKit)
    private static func dynamic(light: (UInt32, CGFloat), dark: (UInt32, CGFloat)) -> Color {
        Color(uiColor: UIColor { trait in
            let (hex, alpha) = trait.userInterfaceStyle == .dark ? dark : light
            return UIColor(hex: hex).withAlphaComponent(alpha)
        })
    }
    #endif
}
