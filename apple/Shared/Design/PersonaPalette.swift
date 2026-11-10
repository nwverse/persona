import SwiftUI

/// The colors a persona brings (docs/apple/persona.md「Color scheme → design tokens」): the accent (what can be tapped; BeautifulUI's accent and
/// Lark's links follow it), the user's bubble and its words, and the persona's own primary for decoration
/// (`Theme.persona`). They color the app only when Theme Color is Match Avatar (`ColorSpec(persona:over:)`); what a persona leaves
/// out stays the style's, and the built-in Persona brings nothing. Held to the same floors as every color
/// (StyleSpecTests): a color that misses one is darkened until it passes, so a package can't make a button unreadable.
nonisolated struct PersonaPalette: Sendable, Hashable {
    /// A light / dark pair of sRGB values (0xRRGGBB).
    struct Pair: Sendable, Hashable {
        var light: UInt32
        var dark: UInt32
        var color: Color { Color(light: light, dark: dark) }
    }

    var primary: Pair?
    var accent: Pair?
    var bubble: Pair?
    var bubbleText: Pair?

    /// The persona on screen's (AppModel keeps it with the persona; RootView rebuilds on a change, as for a style).
    nonisolated(unsafe) static var current: PersonaPalette? {
        didSet { StyleSpec.recompose() }
    }

    /// The floors (docs/apple/ios.md「Design language」): white words on the accent, the accent as a link on the page, words on the bubble.
    static let onAccent = 3.5, link = 3.0, bubbleWords = 4.5

    /// From persona.json's `colors` ("#RRGGBB" strings per appearance), made to meet the floors. Nil when it has none.
    init?(light: [String: String]?, dark: [String: String]?) {
        func pair(_ key: String) -> Pair? {
            let l = light?[key].flatMap(Self.hex), d = dark?[key].flatMap(Self.hex)
            guard let any = l ?? d else { return nil }
            return Pair(light: l ?? any, dark: d ?? any)
        }
        primary = pair("primary")
        if let a = pair("accent") {
            accent = Pair(light: Self.meeting(a.light, page: 0xFFFFFF), dark: Self.meeting(a.dark, page: 0x000000))
        }
        if let b = pair("bubble") {
            let words = pair("bubble_text") ?? Pair(light: 0xFFFFFF, dark: 0xFFFFFF)
            bubble = Pair(light: Self.darkened(b.light, until: { Self.contrast($0, words.light) >= Self.bubbleWords }),
                          dark: Self.darkened(b.dark, until: { Self.contrast($0, words.dark) >= Self.bubbleWords }))
            bubbleText = words
        }
        if primary == nil && accent == nil && bubble == nil { return nil }
    }

    init(primary: Pair? = nil, accent: Pair? = nil, bubble: Pair? = nil, bubbleText: Pair? = nil) {
        self.primary = primary
        self.accent = accent
        self.bubble = bubble
        self.bubbleText = bubbleText
    }

    static func hex(_ s: String) -> UInt32? {
        let t = s.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: "#", with: "")
        guard t.count == 6, let v = UInt32(t, radix: 16) else { return nil }
        return v
    }

    /// An accent that white words read on and that reads as a link on `page`: too light for white words, it is
    /// darkened; too dark to read on a black page, lightened. As given when it already holds both.
    static func meeting(_ c: UInt32, page: UInt32) -> UInt32 {
        let ok: (UInt32) -> Bool = { contrast($0, 0xFFFFFF) >= onAccent && contrast($0, page) >= link }
        if ok(c) { return c }
        let toward: UInt32 = contrast(c, 0xFFFFFF) < onAccent ? 0x000000 : 0xFFFFFF
        for i in 1...50 {
            let v = mix(c, toward, Double(i) / 50)
            if ok(v) { return v }
        }
        return c
    }

    /// Mixed toward black a step at a time until `ok` (black itself at worst).
    static func darkened(_ c: UInt32, until ok: (UInt32) -> Bool) -> UInt32 {
        for i in 0...50 {
            let v = mix(c, 0x000000, Double(i) / 50)
            if ok(v) { return v }
        }
        return 0
    }

    static func mix(_ a: UInt32, _ b: UInt32, _ t: Double) -> UInt32 {
        func ch(_ v: UInt32, _ s: UInt32) -> Double { Double((v >> s) & 0xFF) }
        func m(_ s: UInt32) -> UInt32 { UInt32((ch(a, s) * (1 - t) + ch(b, s) * t).rounded()) << s }
        return m(16) | m(8) | m(0)
    }

    static func luminance(_ c: UInt32) -> Double {
        func lin(_ s: UInt32) -> Double {
            let v = Double((c >> s) & 0xFF) / 255
            return v <= 0.03928 ? v / 12.92 : pow((v + 0.055) / 1.055, 2.4)
        }
        return 0.2126 * lin(16) + 0.7152 * lin(8) + 0.0722 * lin(0)
    }

    static func contrast(_ a: UInt32, _ b: UInt32) -> Double {
        let la = luminance(a), lb = luminance(b)
        return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
    }
}
