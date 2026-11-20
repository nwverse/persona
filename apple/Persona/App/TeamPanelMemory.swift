import Foundation

nonisolated extension TeamWorkers {
    /// Only iPhone and iPad present a Team page. Other Apple clients keep their existing child pages.
    static var hasPanel: Bool {
        #if os(iOS)
        true
        #else
        false
        #endif
    }

    static func presentedLead(of row: Session) -> SessionKey? { hasPanel ? lead(of: row) : nil }
    static func presentedRows(_ rows: [Session]) -> [Session] { hasPanel ? listed(rows) : rows }
}

/// A deep link can arrive before the paged roster. Keep the full key until the requested worker is available.
struct TeamLanding: Equatable {
    let lead: SessionKey
    let worker: SessionKey
}

/// Closing a Team panel is a preference for this lead on this paired hub, as in the web app.
enum TeamPanelMemory {
    private static let storage = "team.closed-leads"

    static func closed(_ lead: SessionKey, defaults: UserDefaults = .standard) -> Bool {
        entries(defaults).contains(lead)
    }

    static func setClosed(_ closed: Bool, for lead: SessionKey, defaults: UserDefaults = .standard) {
        var saved = entries(defaults)
        if saved.contains(lead) == closed { return }
        saved.removeAll { $0 == lead }
        if closed { saved.append(lead) }
        save(Array(saved.suffix(50)), defaults)
    }

    static func forget(group: String, defaults: UserDefaults = .standard) {
        let saved = entries(defaults)
        let keep = saved.filter { $0.group != group }
        if keep.count != saved.count { save(keep, defaults) }
    }

    private static func entries(_ defaults: UserDefaults) -> [SessionKey] {
        guard let data = defaults.data(forKey: storage) else { return [] }
        return (try? JSONDecoder().decode([SessionKey].self, from: data)) ?? []
    }

    private static func save(_ entries: [SessionKey], _ defaults: UserDefaults) {
        if entries.isEmpty { defaults.removeObject(forKey: storage) }
        else if let data = try? JSONEncoder().encode(entries) { defaults.set(data, forKey: storage) }
    }
}
