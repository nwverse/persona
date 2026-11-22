import MapKit
import SwiftUI

/// Saved Places' state and what the page does with the hub (#1598): the places the user names (home, the office) that
/// reminders by place go off at. The view draws what is here and calls its methods, so tests run it against a `FakeHub`.
@Observable final class SavedPlacesModel {
    typealias Deps = AssistantDeps
    typealias Item = APIClient.SavedPlace

    private(set) var places: [Item] = []
    private(set) var loaded = false
    /// Why the list could not be read; the page shows it only when it has none to show.
    private(set) var error: String?
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    func load() async {
        guard let api = deps.api() else { return }
        do {
            places = try await api.savedPlaces()
            error = nil
        } catch {
            self.error = error.shown ?? self.error
        }
        loaded = true
    }

    /// Adds `p` (no id yet) or saves the changes to it; returns it as saved, nil when the hub refused (a name another
    /// place has, a device that may only look) after reporting why.
    func save(_ p: Item) async -> Item? {
        guard let api = deps.api() else { return nil }
        var p = p
        p.name = p.name.trimmingCharacters(in: .whitespacesAndNewlines)
        p.address = p.address.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            let saved = try await api.savePlace(p)
            withAnimation(Theme.Motion.standard) {
                if let i = places.firstIndex(where: { $0.id == saved.id }) { places[i] = saved } else { places.append(saved) }
            }
            return saved
        } catch {
            deps.report(error)
            return nil
        }
    }

    /// Optimistic: the row goes at once; a failure reports and reloads what the hub has.
    func delete(_ p: Item) async {
        guard let api = deps.api(), !p.id.isEmpty else { return }
        withAnimation(Theme.Motion.standard) { places.removeAll { $0.id == p.id } }
        do {
            try await api.deletePlace(p.id)
            deps.toast(Toast(String(localized: "Deleted “\(p.name)”"), style: .success))
        } catch {
            deps.report(error)
            if let fresh = try? await api.savedPlaces() { places = fresh }
        }
    }
}

/// What the place editor decides without the hub or a MapKit request: when it may save, the radius choices, how a place
/// is described under its name, where the map opens and what a search's outcome says.
enum SavedPlaceText {
    /// The radius choices in the editor, in meters: the reminders' default first among the small ones.
    static let radii = [100, 150, 300, 500, 1000, 2000]

    /// Where the editor's map opens (#4248): on the place; on the user, when Persona may already see where they are (the
    /// editor never asks for that by itself); else wherever MapKit opens.
    enum Opening: Equatable { case place, user, anywhere }

    static func opening(located: Bool, location: PhoneAuthStatus) -> Opening {
        if located { return .place }
        return location == .granted ? .user : .anywhere
    }

    /// The meters across the map shows around a place: its circle and a street or two around it.
    static func mapSpan(radius: Int) -> Double { Double(radius) * 4 }

    /// A tap on the map moves the place there; the map zooms in on it only when it shows far more than the place's own
    /// span (the world, a whole city), so a small correction keeps the user's zoom. `shownLatitudeDelta` is the map's
    /// height in degrees, nil before it has reported one.
    static func zoomsIn(shownLatitudeDelta: Double?, radius: Int) -> Bool {
        guard let shownLatitudeDelta else { return true }
        return shownLatitudeDelta * metersPerDegree > mapSpan(radius: radius) * 10
    }

    /// About how many meters a degree of latitude is.
    private static let metersPerDegree = 111_000.0

    /// How a search ended: MapKit's "nothing there" is not a failure, everything else (offline, too many requests) is.
    enum SearchOutcome: Equatable {
        case found, nothing, failed

        init(_ error: any Error) {
            self = (error as? MKError)?.code == .placemarkNotFound ? .nothing : .failed
        }
    }

    /// What the location section's footer says after a search for `query`; nil when it found something.
    static func searchNote(_ outcome: SearchOutcome, query: String) -> String? {
        switch outcome {
        case .found: nil
        case .nothing: String(localized: "No places found for “\(query)”.")
        case .failed: String(localized: "Couldn't search for places. Check the connection and try again.")
        }
    }

    /// A name and a location: what the hub needs.
    static func canSave(_ p: APIClient.SavedPlace, located: Bool) -> Bool {
        located && !p.name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// The choices with `current` among them (a radius the assistant saved that is not one of the presets).
    static func radii(with current: Int) -> [Int] {
        radii.contains(current) ? radii : (radii + [current]).sorted()
    }

    /// "150 m", "1 km": the radius as the user's locale writes a short distance.
    static func radius(_ meters: Int) -> String {
        Measurement(value: Double(meters), unit: UnitLength.meters)
            .formatted(.measurement(width: .abbreviated, usage: .road))
    }

    /// The line under a place's name: its address, else its coordinates.
    static func detail(_ p: APIClient.SavedPlace) -> String {
        p.address.isEmpty ? coordinates(p) : p.address
    }

    /// "31.23040, 121.47370".
    static func coordinates(_ p: APIClient.SavedPlace) -> String {
        "\(p.latitude.formatted(.number.precision(.fractionLength(5)))), \(p.longitude.formatted(.number.precision(.fractionLength(5))))"
    }
}
