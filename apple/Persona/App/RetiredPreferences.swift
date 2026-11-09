import Foundation

/// Discard retired presentation preferences without changing any supported appearance choices.
nonisolated enum RetiredPreferences {
    static func clear(from defaults: UserDefaults) {
        // Upgrade cleanup only: this key is never read to choose navigation or exposed as a setting.
        defaults.removeObject(forKey: ["ui.", "old", "Layout"].joined())
    }
}
