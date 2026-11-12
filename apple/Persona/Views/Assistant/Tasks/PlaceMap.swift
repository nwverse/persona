import MapKit
import SwiftUI

// Places a location tool found, drawn on a map under the turn's work line (#5543), the way QuickResults draws a
// reminder it added: the call itself stays folded in "Tools N" (docs/SUPER_RULES.md, rule 2). The chat shows a still
// snapshot, so scrolling never drives a live map; tapping it opens one.

/// Where the phone is, or the places a search found, read from the tool call's result.
nonisolated struct PlaceMapResult: Hashable, Sendable {
    enum Kind: Sendable { case here, places }

    struct Place: Hashable, Sendable {
        var name: String?
        var address: String?
        var latitude: Double
        var longitude: Double
        /// From the search center, when the tool measured it (`phone_maps_search_nearby`).
        var distance: Double?

        var coordinate: CLLocationCoordinate2D { CLLocationCoordinate2D(latitude: latitude, longitude: longitude) }
    }

    var kind: Kind
    var callID: String
    /// In the order the tool returned them, so pin 2 is the place the answer calls the second.
    var places: [Place]

    /// The calls drawn this way, by the tool's own name (an MCP name ends in it: mcp__<server>__<name>).
    static func kind(of name: String) -> Kind? {
        switch name.components(separatedBy: "__").last ?? name {
        case "phone_location_current": .here
        case "phone_location_search", "phone_maps_search_nearby": .places
        default: nil
        }
    }

    /// The most pins a card draws: `phone_maps_search_nearby` returns up to 20, more than a small map can tell apart.
    static let most = 10

    init?(_ call: ToolCall) {
        guard let kind = Self.kind(of: call.name), let result = call.result, result.isError != true,
              let data = result.text?.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return nil }
        let found: [Place]
        switch kind {
        case .here:
            let address = obj["address"] as? [String: Any]
            found = Self.place(obj, name: nil, address: (address?["formatted"] as? String) ?? (address?["name"] as? String)).map { [$0] } ?? []
        case .places:
            found = ((obj["places"] as? [[String: Any]]) ?? []).compactMap {
                Self.place($0, name: $0["name"] as? String, address: $0["address"] as? String)
            }
        }
        guard !found.isEmpty else { return nil }
        self.kind = kind
        callID = call.id
        places = Array(found.prefix(Self.most))
    }

    private static func place(_ obj: [String: Any], name: String?, address: String?) -> Place? {
        guard let lat = (obj["latitude"] as? NSNumber)?.doubleValue, (-90...90).contains(lat),
              let lon = (obj["longitude"] as? NSNumber)?.doubleValue, (-180...180).contains(lon) else { return nil }
        func text(_ s: String?) -> String? { s.flatMap { $0.isEmpty ? nil : $0 } }
        return Place(name: text(name), address: text(address), latitude: lat, longitude: lon,
                     distance: (obj["distance_meters"] as? NSNumber)?.doubleValue)
    }

    /// The region that shows every place with room around it; at least `span` meters across, so one place is not a
    /// street-level close-up.
    func region(span: Double = 600) -> MKCoordinateRegion { PlaceMapSnapshot.region(places.map(\.coordinate), span: span) }
}

/// What a place is called in a row: its name, else its address, else "Place N".
nonisolated enum PlaceMapText {
    static func title(_ place: PlaceMapResult.Place, number: Int) -> String {
        place.name ?? place.address ?? String(localized: "Place \(number) [Place Map]")
    }

    /// The line under a place's name: distance and address, whichever it has.
    static func detail(_ place: PlaceMapResult.Place) -> String? {
        var parts: [String] = []
        if let d = place.distance { parts.append(distance(d)) }
        if place.name != nil, let a = place.address { parts.append(a) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    static func distance(_ meters: Double) -> String {
        Measurement(value: meters, unit: UnitLength.meters)
            .formatted(.measurement(width: .abbreviated, usage: .road, numberFormatStyle: .number.precision(.fractionLength(0...1))))
    }

    /// Hands a place to Apple Maps, with directions there from wherever the phone is.
    @MainActor static func directions(to place: PlaceMapResult.Place, number: Int) {
        let item = MKMapItem(placemark: MKPlacemark(coordinate: place.coordinate))
        item.name = title(place, number: number)
        item.openInMaps(launchOptions: [MKLaunchOptionsDirectionsModeKey: MKLaunchOptionsDirectionsModeDefault])
    }
}

/// A still picture of the map and where each place falls on it (0…1 across and down), made off the main thread and
/// kept for the chat's next pass over the same card.
nonisolated final class PlaceMapSnapshot: @unchecked Sendable {
    let image: UIImage
    let points: [CGPoint]

    init(image: UIImage, points: [CGPoint]) {
        self.image = image
        self.points = points
    }

    nonisolated(unsafe) private static let cache: NSCache<NSString, PlaceMapSnapshot> = { // NSCache is thread-safe
        let c = NSCache<NSString, PlaceMapSnapshot>()
        c.countLimit = 24
        return c
    }()

    static func key(_ id: String, size: CGSize, dark: Bool) -> NSString {
        "\(id)|\(Int(size.width))x\(Int(size.height))|\(dark ? "d" : "l")" as NSString
    }

    /// The region that shows every coordinate with room around it; at least `span` meters across, so one place is not
    /// a street-level close-up.
    static func region(_ coordinates: [CLLocationCoordinate2D], span: Double = 600) -> MKCoordinateRegion {
        let lats = coordinates.map(\.latitude), lons = coordinates.map(\.longitude)
        let (minLat, maxLat) = (lats.min() ?? 0, lats.max() ?? 0), (minLon, maxLon) = (lons.min() ?? 0, lons.max() ?? 0)
        let center = CLLocationCoordinate2D(latitude: (minLat + maxLat) / 2, longitude: (minLon + maxLon) / 2)
        let metersPerDegree = 111_000.0
        let least = span / metersPerDegree
        return MKCoordinateRegion(center: center, span: MKCoordinateSpan(latitudeDelta: max((maxLat - minLat) * 1.5, least),
                                                                      longitudeDelta: max((maxLon - minLon) * 1.5, least)))
    }

    static func cached(_ key: NSString) -> PlaceMapSnapshot? { cache.object(forKey: key) }

    /// The map around `coordinates` and where each of them falls on it.
    static func make(_ id: String, coordinates: [CLLocationCoordinate2D], size: CGSize, dark: Bool, scale: CGFloat) async -> PlaceMapSnapshot? {
        let key = key(id, size: size, dark: dark)
        if let hit = cache.object(forKey: key) { return hit }
        let options = MKMapSnapshotter.Options()
        options.region = region(coordinates)
        options.size = size
        options.traitCollection = UITraitCollection { traits in
            traits.userInterfaceStyle = dark ? .dark : .light
            traits.displayScale = scale
        }
        options.pointOfInterestFilter = .excludingAll
        guard let shot = try? await MKMapSnapshotter(options: options).start() else { return nil }
        let points = coordinates.map { coordinate -> CGPoint in
            let p = shot.point(for: coordinate)
            return CGPoint(x: p.x / size.width, y: p.y / size.height)
        }
        let made = PlaceMapSnapshot(image: shot.image, points: points)
        cache.setObject(made, forKey: key)
        return made
    }
}

/// The card in the chat: the map with a numbered pin for each place (a dot for where the phone is), then the first few
/// places by number. Tapping the map opens `PlaceMapSheet`.
struct PlaceMapCard: View {
    var result: PlaceMapResult
    @Environment(\.colorScheme) private var scheme
    @Environment(\.displayScale) private var scale
    @State private var size: CGSize = .zero
    @State private var snapshot: PlaceMapSnapshot?
    @State private var open = false

    /// Rows under the map; the rest are on the map and in the sheet.
    static let rows = 3

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { open = true } label: { map }
                .buttonStyle(.plain)
                .accessibilityLabel(Self.spoken(result))
                .accessibilityHint(String(localized: "Opens a larger map [Place Map]"))
                .accessibilityIdentifier("chat.place-map." + result.callID)
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                ForEach(result.places.indices.prefix(Self.rows), id: \.self) { i in
                    PlaceMapRow(result: result, place: result.places[i], number: i + 1)
                }
                if result.places.count > Self.rows {
                    Text(String(localized: "\(result.places.count - Self.rows) more on the map [Place Map]"))
                        .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            .padding(Theme.Space.m)
        }
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .sheet(isPresented: $open) { PlaceMapSheet(result: result) }
    }

    private var map: some View {
        Color.clear
            .scaledFrame(height: Theme.Size.chatMap)
            .frame(maxWidth: .infinity)
            .onGeometryChange(for: CGSize.self) { $0.size } action: { size = $0 }
            .background {
                if let snapshot {
                    Image(uiImage: snapshot.image).resizable().transition(.opacity)
                } else {
                    Theme.Fill.well
                }
            }
            .overlay {
                if let snapshot {
                    GeometryReader { box in
                        ForEach(snapshot.points.indices, id: \.self) { i in
                            PlaceMapPin(kind: result.kind, number: i + 1)
                                .position(x: snapshot.points[i].x * box.size.width, y: snapshot.points[i].y * box.size.height)
                        }
                    }
                }
            }
            .clipped()
            .animation(Theme.Motion.state, value: snapshot != nil)
            .task(id: PlaceMapSnapshot.key(result.callID, size: size, dark: scheme == .dark)) {
                guard size.width > 0, size.height > 0 else { return }
                let key = PlaceMapSnapshot.key(result.callID, size: size, dark: scheme == .dark)
                if let hit = PlaceMapSnapshot.cached(key) { snapshot = hit; return }
                snapshot = await PlaceMapSnapshot.make(result.callID, coordinates: result.places.map(\.coordinate), size: size,
                                                       dark: scheme == .dark, scale: scale)
            }
    }

    /// "Map: 1 Blue Bottle, 2 % Arabica, 3 Manner" or "Map: your location, <address>".
    static func spoken(_ result: PlaceMapResult) -> String {
        switch result.kind {
        case .here:
            let address = result.places.first?.address ?? ""
            return String(localized: "Map: your location \(address) [Place Map]")
        case .places:
            let names = result.places.enumerated().map { "\($0.offset + 1) \(PlaceMapText.title($0.element, number: $0.offset + 1))" }
            return String(localized: "Map: \(names.joined(separator: ", ")) [Place Map]")
        }
    }
}

/// A place's number (or the phone's dot), name and distance, and Directions, which hands it to Maps.
private struct PlaceMapRow: View {
    var result: PlaceMapResult
    var place: PlaceMapResult.Place
    var number: Int

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            Group {
                if result.kind == .here {
                    ThemedIcon(Theme.Icon.currentLocation).font(Theme.Font.secondaryStrong).foregroundStyle(Theme.accent)
                } else {
                    CountBadge(count: number, look: .accent)
                }
            }
            .scaledFrame(width: Theme.Size.iconS)
            .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(result.kind == .here ? String(localized: "Your Location [Place Map]") : PlaceMapText.title(place, number: number))
                    .font(Theme.Font.secondaryStrong).lineLimit(2)
                if let detail = result.kind == .here ? place.address : PlaceMapText.detail(place) {
                    Text(detail).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            if result.kind == .places {
                Button { PlaceMapText.directions(to: place, number: number) } label: {
                    Label(String(localized: "Directions [Place Map]"), systemImage: Theme.Icon.directions)
                        .labelStyle(.iconOnly)
                }
                .font(Theme.Font.captionStrong)
                .themedButton()
                .tapTargetFrame()
                .accessibilityLabel(String(localized: "Directions to \(PlaceMapText.title(place, number: number)) [Place Map]"))
            }
        }
    }
}

/// A place's mark on the map: its number on the accent, or the phone's dot.
private struct PlaceMapPin: View {
    var kind: PlaceMapResult.Kind
    var number: Int

    var body: some View {
        switch kind {
        case .here:
            Circle().fill(Theme.accent)
                .overlay(Circle().stroke(.white, lineWidth: Theme.Size.lineMedium))
                .scaledFrame(width: Theme.Size.markS, height: Theme.Size.markS)
        case .places:
            CountBadge(count: number, look: .accent)
                .overlay(Capsule().stroke(.white, lineWidth: Theme.Size.lineMedium))
        }
    }
}

/// The places on a live map that pans and zooms, the same numbers as in the chat, and Directions for each.
struct PlaceMapSheet: View {
    var result: PlaceMapResult
    @Environment(\.dismiss) private var dismiss
    @State private var camera: MapCameraPosition

    init(result: PlaceMapResult) {
        self.result = result
        _camera = State(initialValue: .region(result.region()))
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                Map(position: $camera) {
                    UserAnnotation()
                    ForEach(result.places.indices, id: \.self) { i in
                        let place = result.places[i]
                        if result.kind == .here {
                            Marker(String(localized: "Your Location [Place Map]"), systemImage: Theme.Icon.currentLocation, coordinate: place.coordinate)
                                .tint(Theme.accent)
                        } else {
                            Marker(PlaceMapText.title(place, number: i + 1), monogram: Text(verbatim: "\(i + 1)"), coordinate: place.coordinate)
                                .tint(Theme.accent)
                        }
                    }
                }
                .accessibilityIdentifier("place-map-sheet.map")
                List {
                    ForEach(result.places.indices, id: \.self) { i in
                        PlaceMapRow(result: result, place: result.places[i], number: i + 1)
                    }
                }
                .listStyle(.plain)
                .scaledFrame(maxHeight: Theme.Size.box240)
            }
            .navigationTitle(result.kind == .here ? String(localized: "Your Location [Place Map]") : String(localized: "Places [Place Map]"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Done [Place Map]")) { dismiss() }.lineLimit(1)
                }
            }
        }
    }
}
