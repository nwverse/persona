import Foundation
import CoreGraphics

extension Theme {
    /// Every sidebar row's metrics (the Persona sidebar on iPhone and iPad, its list on the Mac, the Mac's sections
    /// column): a minimum height and padding, not the text's own height, so a row is as tall in every language.
    /// iOS: the system sidebar's 44 pt row (`List(.sidebar)`, the smallest touch target). macOS: the system's
    /// Sidebar icon size (General settings: small / medium / large rows of 24 / 28 / 32 pt, `NSTableViewDefaultSizeMode`).
    /// Sizes go through `scaledFrame`, so they still follow the text size.
    nonisolated enum Sidebar {
        /// A one-line row's height.
        static var row: CGFloat { metrics.row }
        /// The least space above and below a row's text (a two-line row, a larger text size, a taller script).
        static var rowPadding: CGFloat { metrics.padding }
        /// The box a row's symbol is drawn in, and the column it sits in (wide symbols scale down into the box).
        static var glyph: CGFloat { metrics.glyph }
        static var glyphColumn: CGFloat { metrics.column }
        /// Between a row's symbol and its words.
        static var gap: CGFloat { metrics.gap }
        /// A conversation's round mark.
        static var mark: CGFloat { metrics.mark }
        /// The selected or hovered row's rounded shade.
        static var radius: CGFloat { metrics.radius }

        private struct Metrics {
            var row: CGFloat, padding: CGFloat, glyph: CGFloat, column: CGFloat, gap: CGFloat, mark: CGFloat, radius: CGFloat
        }

        #if os(macOS)
        /// The Mac's three sizes, small to large.
        private static let sizes: [Metrics] = [
            Metrics(row: 24, padding: 2, glyph: 14, column: 18, gap: 6, mark: 18, radius: 6),
            Metrics(row: 28, padding: 4, glyph: 16, column: 20, gap: 8, mark: 20, radius: 6),
            Metrics(row: 32, padding: 4, glyph: 20, column: 24, gap: 8, mark: 24, radius: 8),
        ]

        /// The system's Sidebar icon size: 1 small, 2 medium (also when unset), 3 large.
        private static var systemSize: Int {
            let mode = UserDefaults.standard.integer(forKey: "NSTableViewDefaultSizeMode")
            return (1...3).contains(mode) ? mode - 1 : 1
        }

        private static var metrics: Metrics { sizes[systemSize] }
        #else
        private static let metrics = Metrics(row: 44, padding: 4, glyph: 22, column: 24, gap: 16, mark: 32, radius: 10)
        #endif
    }
}
