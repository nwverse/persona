import AppKit
import Foundation
import Testing
@testable import Persona

/// The Mac's sidebar rows follow the system's Sidebar icon size (docs/apple/sidebar.md, #1647): small / medium / large
/// rows of 24 / 28 / 32 pt, medium when the setting was never touched.
@MainActor
struct MacSidebarMetricsTests {
    private static let key = "NSTableViewDefaultSizeMode"

    @Test func rowFollowsTheSystemSidebarIconSize() {
        let saved = UserDefaults.standard.object(forKey: Self.key)
        defer { UserDefaults.standard.set(saved, forKey: Self.key) }
        let rows: [(mode: Int, row: CGFloat)] = [(1, 24), (2, 28), (3, 32)]
        for (mode, row) in rows {
            UserDefaults.standard.set(mode, forKey: Self.key)
            #expect(Theme.Sidebar.row == row, "size mode \(mode)")
            #expect(Theme.Sidebar.glyph < Theme.Sidebar.row)
        }
        UserDefaults.standard.set(0, forKey: Self.key)
        #expect(Theme.Sidebar.row == CGFloat(28), "unset is medium")
    }

    /// One line of 13 pt body text (the Mac's, `Theme.Font.body`) and its padding fit the smallest row.
    @Test func bodyTextFitsTheSmallestRow() {
        let saved = UserDefaults.standard.object(forKey: Self.key)
        defer { UserDefaults.standard.set(saved, forKey: Self.key) }
        UserDefaults.standard.set(1, forKey: Self.key)
        let size = Theme.Font.pointSize(.body)
        for name in ["SFProText-Regular", "PingFangSC-Regular"] {
            let font = NSFont(name: name, size: size) ?? NSFont.systemFont(ofSize: size)
            let line = font.ascender - font.descender + font.leading
            #expect(line + 2 * Theme.Sidebar.rowPadding <= Theme.Sidebar.row, "\(name): \(line) + padding > \(Theme.Sidebar.row)")
        }
    }
}
