import MapKit
import SwiftUI

// A route the assistant looked up with phone_maps_route (#5544), drawn under the turn's work line like the place maps
// (PlaceMap.swift): the route on a still map, how long and how far, and Start in Maps. Nothing opens until it is tapped.

/// A route read from `phone_maps_route`'s result.
nonisolated struct RouteMapResult: Hashable, Sendable {
    enum Mode: String, Sendable { case driving, walking, transit }

    var callID: String
    var destination: PlaceMapResult.Place
    var origin: PlaceMapResult.Place
    var mode: Mode
    var distance: Double
    var seconds: Double
    /// The route's shape, empty when Maps gave only a time (public transport in most places).
    var path: [PlaceMapResult.Place]

    static func isRoute(_ name: String) -> Bool { (name.components(separatedBy: "__").last ?? name) == "phone_maps_route" }

    init?(_ call: ToolCall) {
        guard Self.isRoute(call.name), let result = call.result, result.isError != true,
              let data = result.text?.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              var to = Self.point(obj["destination"] as? [String: Any]), let from = Self.point(obj["origin"] as? [String: Any]),
              let distance = (obj["distance_meters"] as? NSNumber)?.doubleValue,
              let seconds = (obj["expected_travel_seconds"] as? NSNumber)?.doubleValue else { return nil }
        to.name = ((obj["destination"] as? [String: Any])?["name"] as? String).flatMap { $0.isEmpty ? nil : $0 }
        callID = call.id
        destination = to
        origin = from
        mode = (obj["mode"] as? String).flatMap(Mode.init(rawValue:)) ?? .driving
        self.distance = distance
        self.seconds = seconds
        path = ((obj["path"] as? [[NSNumber]]) ?? []).compactMap { pair in
            guard pair.count == 2 else { return nil }
            return Self.point(["latitude": pair[0], "longitude": pair[1]])
        }
    }

    private static func point(_ obj: [String: Any]?) -> PlaceMapResult.Place? {
        guard let lat = (obj?["latitude"] as? NSNumber)?.doubleValue, (-90...90).contains(lat),
              let lon = (obj?["longitude"] as? NSNumber)?.doubleValue, (-180...180).contains(lon) else { return nil }
        return PlaceMapResult.Place(name: nil, address: nil, latitude: lat, longitude: lon, distance: nil)
    }

    /// What the map shows: the start, the route, the end.
    var coordinates: [CLLocationCoordinate2D] { [origin.coordinate] + path.map(\.coordinate) + [destination.coordinate] }
}

nonisolated enum RouteMapText {
    /// "25 min · 3.2 km".
    static func summary(_ route: RouteMapResult) -> String {
        let time = Duration.seconds(max(route.seconds, 60))
            .formatted(.units(allowed: [.hours, .minutes], width: .abbreviated, maximumUnitCount: 2))
        return time + " · " + PlaceMapText.distance(route.distance)
    }

    /// "To Corner Café · by car".
    static func detail(_ route: RouteMapResult) -> String {
        let how = switch route.mode {
        case .driving: String(localized: "By Car [Route Map]")
        case .walking: String(localized: "On Foot [Route Map]")
        case .transit: String(localized: "By Public Transport [Route Map]")
        }
        guard let name = route.destination.name else { return how }
        return String(localized: "To \(name) [Route Map]") + " · " + how
    }

    /// Opens Apple Maps with the same destination and transport, where navigation runs.
    @MainActor static func start(_ route: RouteMapResult) {
        let item = MKMapItem(placemark: MKPlacemark(coordinate: route.destination.coordinate))
        item.name = route.destination.name
        let mode = switch route.mode {
        case .driving: MKLaunchOptionsDirectionsModeDriving
        case .walking: MKLaunchOptionsDirectionsModeWalking
        case .transit: MKLaunchOptionsDirectionsModeTransit
        }
        item.openInMaps(launchOptions: [MKLaunchOptionsDirectionsModeKey: mode])
    }
}

/// The card in the chat: the route on a still map (a dot at the start, a pin at the end), then time, distance, where to
/// and Start in Maps. Tapping the map opens `RouteMapSheet`.
struct RouteMapCard: View {
    var route: RouteMapResult
    @Environment(\.colorScheme) private var scheme
    @Environment(\.displayScale) private var scale
    @State private var size: CGSize = .zero
    @State private var snapshot: PlaceMapSnapshot?
    @State private var open = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button { open = true } label: { map }
                .buttonStyle(.plain)
                .accessibilityLabel(String(localized: "Route map: \(RouteMapText.summary(route)), \(RouteMapText.detail(route)) [Route Map]"))
                .accessibilityHint(String(localized: "Opens a larger map [Place Map]"))
                .accessibilityIdentifier("chat.route-map." + route.callID)
            RouteMapRow(route: route).padding(Theme.Space.m)
        }
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .sheet(isPresented: $open) { RouteMapSheet(route: route) }
    }

    private var map: some View {
        Color.clear
            .scaledFrame(height: Theme.Size.chatMap)
            .frame(maxWidth: .infinity)
            .onGeometryChange(for: CGSize.self) { $0.size } action: { size = $0 }
            .background {
                if let snapshot { Image(uiImage: snapshot.image).resizable().transition(.opacity) } else { Theme.Fill.well }
            }
            .overlay {
                if let snapshot, let first = snapshot.points.first, let last = snapshot.points.last {
                    GeometryReader { box in
                        let at = { (p: CGPoint) in CGPoint(x: p.x * box.size.width, y: p.y * box.size.height) }
                        if snapshot.points.count > 2 {
                            Path { line in line.addLines(snapshot.points.map(at)) }
                                .stroke(Theme.accent, style: StrokeStyle(lineWidth: Theme.Size.routeLine, lineCap: .round, lineJoin: .round))
                        }
                        Circle().fill(Theme.accent)
                            .overlay(Circle().stroke(.white, lineWidth: Theme.Size.lineMedium))
                            .scaledFrame(width: Theme.Size.markS, height: Theme.Size.markS)
                            .position(at(first))
                        ThemedIcon(Theme.Icon.place).font(Theme.Font.captionStrong).foregroundStyle(.white)
                            .scaledFrame(width: Theme.Size.iconS, height: Theme.Size.iconS)
                            .background(Theme.accent, in: Circle())
                            .overlay(Circle().stroke(.white, lineWidth: Theme.Size.lineMedium))
                            .position(at(last))
                    }
                }
            }
            .clipped()
            .animation(Theme.Motion.state, value: snapshot != nil)
            .task(id: PlaceMapSnapshot.key(route.callID, size: size, dark: scheme == .dark)) {
                guard size.width > 0, size.height > 0 else { return }
                let key = PlaceMapSnapshot.key(route.callID, size: size, dark: scheme == .dark)
                if let hit = PlaceMapSnapshot.cached(key) { snapshot = hit; return }
                snapshot = await PlaceMapSnapshot.make(route.callID, coordinates: route.coordinates, size: size,
                                                       dark: scheme == .dark, scale: scale)
            }
    }
}

/// How (car, foot, transit), time and distance, where to, and Start in Maps.
private struct RouteMapRow: View {
    var route: RouteMapResult

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            ThemedIcon(icon).font(Theme.Font.secondaryStrong).foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.iconS)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(RouteMapText.summary(route)).font(Theme.Font.secondaryStrong).lineLimit(1)
                Text(RouteMapText.detail(route)).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .accessibilityElement(children: .combine)
            Button { RouteMapText.start(route) } label: {
                Text(String(localized: "Start in Maps [Route Map]")).lineLimit(1)
            }
            .font(Theme.Font.captionStrong)
            .themedButton()
            .accessibilityIdentifier("route-map.start")
        }
    }

    private var icon: String {
        switch route.mode {
        case .driving: Theme.Icon.byCar
        case .walking: Theme.Icon.onFoot
        case .transit: Theme.Icon.byTransit
        }
    }
}

/// The route on a live map, with the same row and Done.
struct RouteMapSheet: View {
    var route: RouteMapResult
    @Environment(\.dismiss) private var dismiss
    @State private var camera: MapCameraPosition

    init(route: RouteMapResult) {
        self.route = route
        _camera = State(initialValue: .region(PlaceMapSnapshot.region(route.coordinates)))
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                Map(position: $camera) {
                    UserAnnotation()
                    if !route.path.isEmpty {
                        MapPolyline(coordinates: route.coordinates)
                            .stroke(Theme.accent, style: StrokeStyle(lineWidth: Theme.Size.routeLine, lineCap: .round, lineJoin: .round))
                    }
                    Annotation(String(localized: "Start [Route Map]"), coordinate: route.origin.coordinate) {
                        Circle().fill(Theme.accent)
                            .overlay(Circle().stroke(.white, lineWidth: Theme.Size.lineMedium))
                            .scaledFrame(width: Theme.Size.markS, height: Theme.Size.markS)
                    }
                    Marker(route.destination.name ?? String(localized: "Destination [Route Map]"), systemImage: Theme.Icon.place,
                           coordinate: route.destination.coordinate)
                        .tint(Theme.accent)
                }
                .accessibilityIdentifier("route-map-sheet.map")
                RouteMapRow(route: route).padding(Theme.Space.page)
            }
            .navigationTitle(String(localized: "Route [Route Map]"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button(String(localized: "Done [Place Map]")) { dismiss() }.lineLimit(1)
                }
            }
        }
    }
}
