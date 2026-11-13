import Foundation
import Testing
import UIKit
@testable import Persona

/// Settings › Appearance › Light & Dark (ColorSchemeChoice, docs/apple/ios.md「Design language」): what it reads, and what it does to the windows.
@MainActor struct ColorSchemeChoiceTests {
    @Test func followsTheSystemUnlessPicked() {
        let d = UserDefaults.standard
        let saved = d.string(forKey: ColorSchemeChoice.key)
        defer { if let saved { d.set(saved, forKey: ColorSchemeChoice.key) } else { d.removeObject(forKey: ColorSchemeChoice.key) } }
        d.removeObject(forKey: ColorSchemeChoice.key)
        #expect(ColorSchemeChoice.current == .system)
        d.set("dark", forKey: ColorSchemeChoice.key)
        #expect(ColorSchemeChoice.current == .dark)
        d.set("sepia", forKey: ColorSchemeChoice.key) // not a choice (a newer build's, say): the system's
        #expect(ColorSchemeChoice.current == .system)
    }

    @Test func eachChoiceIsItsOwnStyle() {
        #expect(ColorSchemeChoice.system.interfaceStyle == .unspecified)
        #expect(ColorSchemeChoice.light.interfaceStyle == .light)
        #expect(ColorSchemeChoice.dark.interfaceStyle == .dark)
        let all = ColorSchemeChoice.allCases
        #expect(Set(all.map(\.title)).count == all.count && Set(all.map(\.symbol)).count == all.count)
    }

    /// Every window of the scene takes it (a sheet or an alert inherits its window's), and Match System hands them back.
    @Test func setsTheWindowsAndHandsThemBack() throws {
        let scene = try #require(UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first)
        let window = UIWindow(windowScene: scene)
        defer { ColorSchemeChoice.system.apply(); window.isHidden = true }
        ColorSchemeChoice.dark.apply()
        #expect(window.overrideUserInterfaceStyle == .dark)
        #expect(window.traitCollection.userInterfaceStyle == .dark)
        ColorSchemeChoice.light.apply()
        #expect(window.overrideUserInterfaceStyle == .light)
        #expect(window.traitCollection.userInterfaceStyle == .light)
        ColorSchemeChoice.system.apply()
        #expect(window.overrideUserInterfaceStyle == .unspecified)
    }
}
