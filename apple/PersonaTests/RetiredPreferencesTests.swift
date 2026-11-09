import Foundation
import Testing
@testable import Persona

struct RetiredPreferencesTests {
    @Test func clearsPersistedPresentationChoiceWithoutChangingAppearance() throws {
        let suite = "RetiredPreferencesTests.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: suite))
        defer { defaults.removePersistentDomain(forName: suite) }
        let retiredKey = ["ui.", "old", "Layout"].joined()
        defaults.set(true, forKey: retiredKey)
        defaults.set("glass", forKey: "ui.style")
        defaults.set("dark", forKey: "ui.scheme")

        RetiredPreferences.clear(from: defaults)

        #expect(defaults.persistentDomain(forName: suite)?[retiredKey] == nil)
        #expect(defaults.string(forKey: "ui.style") == "glass")
        #expect(defaults.string(forKey: "ui.scheme") == "dark")
        RetiredPreferences.clear(from: defaults)
        #expect(defaults.persistentDomain(forName: suite)?[retiredKey] == nil)
    }
}
