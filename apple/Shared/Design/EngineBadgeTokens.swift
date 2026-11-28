import SwiftUI

/// Color and proportional layout tokens for `CodingAgentAvatar` and `HostingEngineBadge`
/// (docs/design/DESIGN_SYSTEM.md "Chips, badges, avatars, marks").
/// Every badge fill is paired with `Theme.onSolid` white glyphs at >= 4.5:1 contrast in light and dark schemes,
/// matching the web `--engine-*` palette.
nonisolated enum EngineBadgeTokens {
    static let orca = Color(light: 0x0B7285, dark: 0x0C7C91)
    static let herdr = Color(light: 0x4338CA, dark: 0x5B52E5)
    static let herdrCore = Color(light: 0x3730A3, dark: 0x4F46E5)
    static let paseo = Color(light: 0x0F766E, dark: 0x0E7C71)
    static let t3code = Color(light: 0x6D28D9, dark: 0x7C3AED)
    static let cindy = Color(light: 0xBE185D, dark: 0xDB2777)
    static let tern = Color(light: 0x0369A1, dark: 0x0373B0)
    static let rex = Color(light: 0xB45309, dark: 0xB8560A)
    static let botmux = Color(light: 0x0E7490, dark: 0x0D83A1)
    static let custom = Color(light: 0x52525B, dark: 0x63636E)

    /// Share of the circular avatar occupied by the centered model/vendor brand mark.
    static let markRatio: CGFloat = 0.56
    /// Share of the circular avatar occupied by the bottom-trailing hosting engine badge.
    static let badgeRatio: CGFloat = 0.42
    /// Minimum point diameter for the hosting engine badge before Dynamic Type scaling.
    static let badgeMinSide: CGFloat = 12
    /// Maximum point diameter for the hosting engine badge before Dynamic Type scaling.
    static let badgeMaxSide: CGFloat = 20
    /// Share of the hosting engine badge occupied by its inner vector glyph.
    static let glyphRatio: CGFloat = 0.62
    /// Outward corner overhang for the bottom-trailing engine badge (kept clear of the top-trailing status dot).
    static let cornerOffset: CGFloat = 2
    /// Stroke scale for geometric vector paths inside the badge (fraction of glyph box).
    static let strokeFactor: CGFloat = 0.125
}
