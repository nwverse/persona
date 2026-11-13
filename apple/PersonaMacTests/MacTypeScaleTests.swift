import Foundation
import Testing
@testable import Persona

/// The Mac's type scale (#779, docs/apple/ios.md「Design language」): the user chose body 13, nothing under 11, and replies,
/// profiles and Feishu text read the body level rather than the iPhone's 17.
struct MacTypeScaleTests {
    @Test func theChosenSizes() {
        let sizes: [Theme.Level: CGFloat] = [.display: 24, .title: 18, .heading: 13, .body: 13, .secondary: 12,
                                             .caption: 11, .label: 11, .code: 11]
        for (level, size) in sizes { #expect(level.size == size, "\(level)") }
    }

    @Test func nothingUnderEleven() {
        for level in [Theme.Level.display, .title, .heading, .body, .secondary, .caption, .label, .code] {
            #expect(level.size >= 11, "\(level)")
        }
    }

    @Test func feishuTextStartsAtTheBodyLevel() {
        #expect(LarkTextBase().size == Theme.Level.body.size)
    }
}

/// Small / Standard / Large on the Mac (#833): text styles there do not follow Dynamic Type, so the fonts, Markdown and the sizes
/// that go with text (frames, line spacing, Feishu text) move by the Mac's own step, a point at the body (12 / 13 / 14).
@MainActor @Suite(.serialized)
struct MacTextSizeTests {
    /// The scale goes through iOS's 17 (`Theme.textScale`), so it is not bit for bit 12 / 13.
    private func near(_ a: CGFloat, _ b: CGFloat) -> Bool { abs(a - b) < 1e-9 }

    @Test func aPointAStepAtTheBody() {
        #expect(near(Theme.textScale(TextSize.small.applied(to: .large)) * Theme.Level.body.size, 12))
        #expect(near(Theme.textScale(TextSize.standard.applied(to: .large)), 1))
        #expect(near(Theme.textScale(TextSize.large.applied(to: .large)) * Theme.Level.body.size, 14))
        // Frames and icon boxes keep the same ratio, not iOS's 16 / 17 / 19.
        #expect(near(Theme.sizeScale(TextSize.large.applied(to: .large)), 14.0 / 13))
    }

    @Test func theFontsFollowTheSetting() {
        defer { TextSize.standard.applyToMacFonts() }
        TextSize.large.applyToMacFonts()
        #expect(near(Theme.Font.scale, 14.0 / 13))
        TextSize.small.applyToMacFonts()
        #expect(near(Theme.Font.scale, 12.0 / 13))
        TextSize.standard.applyToMacFonts()
        #expect(near(Theme.Font.scale, 1))
    }

    @Test func smallKeepsElevenPointsAtLeast() {
        defer { TextSize.standard.applyToMacFonts() }
        TextSize.small.applyToMacFonts()
        for level in [Theme.Level.caption, .label, .code] { #expect(Theme.Font.pointSize(level) == 11) }
        #expect(near(Theme.Font.pointSize(.body), 12))
        #expect(near(Theme.Font.pointSize(.secondary), 12.0 * 12 / 13)) // 11.08: above the floor, keeps the ratio
        TextSize.large.applyToMacFonts()
        #expect(near(Theme.Font.pointSize(.caption), 11.0 * 14 / 13))
        TextSize.standard.applyToMacFonts()
        #expect(Theme.Font.pointSize(.label) == 11)
    }

    @Test func markdownThemesAreMadeAgainForANewSize() {
        defer { TextSize.standard.applyToMacFonts() }
        var made = 0
        let theme = PerTextScale { made += 1; return Theme.Font.scale }
        TextSize.standard.applyToMacFonts()
        #expect(theme.value == 1)
        #expect(theme.value == 1)
        #expect(made == 1) // kept while the size stays
        TextSize.large.applyToMacFonts()
        #expect(near(theme.value, 14.0 / 13))
        #expect(made == 2)
    }
}
