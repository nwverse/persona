import Foundation

/// Keep package choices across catalog outages; only retired local drawings resolve to Mochi.
nonisolated enum PersonaSelection {
    static let builtinID = "persona"
    static let packageID = "nwverse"

    static let retiredIDs: Set<String> = ["persona-visor", "persona-circuit", "persona-pixel", "persona-facet", "persona-blob", "spark", "halo"]

    static func resolve(_ id: String?) -> String {
        guard let id, !id.isEmpty else { return builtinID }
        let migrated = NativeStateMigration.lookID(id)
        return retiredIDs.contains(migrated) || LegacyNames.retiredLookIDs.contains(migrated) ? builtinID : migrated
    }
}
