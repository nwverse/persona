import CoreLocation
import Foundation
import MapKit

/// Nearby search, a route's distance and time (#5544), and handing a destination to Maps (#2281). Navigation happens in
/// the Maps app: the tool says it opened Maps, never that Persona navigates.
enum MapsTool {
    static var tools: [PhoneTool] {
        #if os(iOS)
        [
            PhoneTool(
                spec: ToolSpec(
                    name: "phone_maps_search_nearby",
                    description: """
                    Search places near the user (or near the latitude / longitude given), such as "quiet cafe" or \
                    "pharmacy", with Apple Maps. Returns each place's name, address, latitude, longitude, distance in \
                    meters from the search center, category, phone and website, nearest first, and the source. \
                    Apple Maps does not give opening hours to apps: say so instead of guessing. Use \
                    phone_maps_navigate to hand one to Maps.
                    """,
                    inputSchema: Schema.object([
                        "query": Schema.string("What to look for, e.g. quiet cafe."),
                        "latitude": Schema.number("Search center latitude. Default: the phone's current location."),
                        "longitude": Schema.number("Search center longitude. Default: the phone's current location."),
                        "radius_meters": Schema.integer("How far around the center to look, default 1500, 100–50000.", min: 100, max: 50000),
                        "limit": Schema.integer("Maximum number of places, default 8, max 20.", min: 1, max: 20),
                    ], required: ["query"])
                ),
                source: .location,
                run: { args in try await searchNearby(args) }
            ),
            PhoneTool(
                spec: ToolSpec(
                    name: "phone_maps_route",
                    description: """
                    How far a place is and how long it takes to get there, with Apple Maps: from the phone's \
                    location (or the from_latitude / from_longitude given) to a destination, by car (default), on \
                    foot or by public transport. Returns the distance in meters, the expected travel time in \
                    seconds, the route's name and notices, and its shape. It opens nothing: the user sees the \
                    route on a map in the chat with Start in Maps. Use phone_maps_navigate only when the user \
                    asks to start navigating now.
                    """,
                    inputSchema: Schema.object([
                        "latitude": Schema.number("Destination latitude."),
                        "longitude": Schema.number("Destination longitude."),
                        "name": Schema.string("Destination name, shown with the route."),
                        "mode": Schema.string("driving (default), walking or transit.", values: ["driving", "walking", "transit"]),
                        "from_latitude": Schema.number("Start latitude. Default: the phone's current location."),
                        "from_longitude": Schema.number("Start longitude. Default: the phone's current location."),
                    ], required: ["latitude", "longitude"])
                ),
                source: .location,
                run: { args in try await route(args) }
            ),
            PhoneTool(
                spec: ToolSpec(
                    name: "phone_maps_navigate",
                    description: """
                    Open directions to a place in the Apple Maps app, with a transport mode. Navigation then runs in \
                    Maps, not in Persona: tell the user Maps has opened, do not say you are navigating. Persona must \
                    be open on the iPhone.
                    """,
                    inputSchema: Schema.object([
                        "latitude": Schema.number("Destination latitude."),
                        "longitude": Schema.number("Destination longitude."),
                        "name": Schema.string("Destination name shown in Maps."),
                        "mode": Schema.string("driving (default), walking or transit.", values: ["driving", "walking", "transit"]),
                    ], required: ["latitude", "longitude"])
                ),
                source: .location,
                run: { args in try await navigate(args) }
            ),
        ]
        #else
        []
        #endif
    }

    /// A destination handed to Maps.
    nonisolated struct Destination: Equatable, Sendable {
        nonisolated enum Mode: String, Sendable { case driving, walking, transit }

        var latitude: Double
        var longitude: Double
        var name: String?
        var mode: Mode

        static func parse(_ args: JSONValue) throws -> Destination {
            guard let latitude = args["latitude"]?.double, (-90...90).contains(latitude),
                  let longitude = args["longitude"]?.double, (-180...180).contains(longitude) else {
                throw ToolFailure(.invalidArgument, "latitude (-90…90) and longitude (-180…180) are required")
            }
            let raw = ToolArgs.string(args, "mode") ?? Mode.driving.rawValue
            guard let mode = Mode(rawValue: raw) else {
                throw ToolFailure(.invalidArgument, "mode must be driving, walking or transit")
            }
            return Destination(latitude: latitude, longitude: longitude, name: ToolArgs.string(args, "name"), mode: mode)
        }

        var transportType: MKDirectionsTransportType {
            switch mode {
            case .driving: .automobile
            case .walking: .walking
            case .transit: .transit
            }
        }

        var directionsMode: String {
            switch mode {
            case .driving: MKLaunchOptionsDirectionsModeDriving
            case .walking: MKLaunchOptionsDirectionsModeWalking
            case .transit: MKLaunchOptionsDirectionsModeTransit
            }
        }
    }

    #if os(iOS)
    static func searchNearby(_ args: JSONValue) async throws -> [ToolContent] {
        guard let query = ToolArgs.string(args, "query") else {
            throw ToolFailure(.invalidArgument, "query is required")
        }
        let radius = Double(ToolArgs.int(args, "radius_meters", fallback: 1500, min: 100, max: 50000))
        let limit = ToolArgs.int(args, "limit", fallback: 8, min: 1, max: 20)
        let center: CLLocation
        var centerSource = "given"
        if let latitude = args["latitude"]?.double, let longitude = args["longitude"]?.double {
            center = CLLocation(latitude: latitude, longitude: longitude)
        } else {
            switch LocationAccess.status() {
            case .granted, .limited, .notNeeded: break
            case .denied: throw ToolFailure(.denied, "Location")
            case .notDetermined: throw ToolFailure(.denied, "Location access has not been requested yet")
            }
            center = try await OneShotLocation.fetch(timeout: 15)
            centerSource = "current_location"
        }

        let request = MKLocalSearch.Request()
        request.naturalLanguageQuery = query
        request.region = MKCoordinateRegion(center: center.coordinate, latitudinalMeters: radius * 2, longitudinalMeters: radius * 2)
        request.resultTypes = .pointOfInterest
        let items: [MKMapItem]
        do {
            items = try await MKLocalSearch(request: request).start().mapItems
        } catch {
            throw ToolFailure(.unavailable, "Apple Maps found nothing for \"\(query)\": \(error.localizedDescription)")
        }
        let rows = items.map { item -> (Double, JSONValue) in
            let place = item.placemark
            let distance = center.distance(from: CLLocation(latitude: place.coordinate.latitude, longitude: place.coordinate.longitude))
            return (distance, Output.object([
                ("name", Output.opt(item.name)),
                ("address", Output.opt(place.title)),
                ("latitude", .number(place.coordinate.latitude)),
                ("longitude", .number(place.coordinate.longitude)),
                ("distance_meters", .number(distance.rounded())),
                ("category", Output.opt(item.pointOfInterestCategory?.rawValue)),
                ("phone", Output.opt(item.phoneNumber)),
                ("url", Output.opt(item.url?.absoluteString)),
            ]))
        }
        let nearest = rows.filter { $0.0 <= radius * 1.5 }.sorted { $0.0 < $1.0 }.prefix(limit).map(\.1)
        return Output.json(Output.object([
            ("query", .string(query)),
            ("center", Output.object([("latitude", .number(center.coordinate.latitude)),
                                      ("longitude", .number(center.coordinate.longitude)),
                                      ("from", .string(centerSource))])),
            ("radius_meters", .number(radius)),
            ("count", .number(Double(nearest.count))),
            ("places", .array(Array(nearest))),
            ("source", .string("Apple Maps (MapKit local search)")),
            ("note", .string("Opening hours are not available from Apple Maps to apps.")),
        ]))
    }

    /// The most points of a route's shape a result carries: enough to draw it on a small map, little for the model.
    nonisolated static let shapePoints = 120

    /// Every n-th point of `points`, at most `most`, always with the last.
    nonisolated static func thin(_ points: [CLLocationCoordinate2D], most: Int = shapePoints) -> [CLLocationCoordinate2D] {
        guard points.count > most, most > 1 else { return points }
        let step = Double(points.count - 1) / Double(most - 1)
        return (0..<most).map { points[Int((Double($0) * step).rounded())] }
    }

    static func route(_ args: JSONValue) async throws -> [ToolContent] {
        let destination = try Destination.parse(args)
        let origin: CLLocation
        var originSource = "given"
        if let latitude = args["from_latitude"]?.double, (-90...90).contains(latitude),
           let longitude = args["from_longitude"]?.double, (-180...180).contains(longitude) {
            origin = CLLocation(latitude: latitude, longitude: longitude)
        } else {
            switch LocationAccess.status() {
            case .granted, .limited, .notNeeded: break
            case .denied: throw ToolFailure(.denied, "Location")
            case .notDetermined: throw ToolFailure(.denied, "Location access has not been requested yet")
            }
            origin = try await OneShotLocation.fetch(timeout: 15)
            originSource = "current_location"
        }
        let to = CLLocationCoordinate2D(latitude: destination.latitude, longitude: destination.longitude)
        let request = MKDirections.Request()
        request.source = MKMapItem(placemark: MKPlacemark(coordinate: origin.coordinate))
        request.destination = MKMapItem(placemark: MKPlacemark(coordinate: to))
        request.transportType = destination.transportType
        var distance: Double
        var seconds: Double
        var name: String?
        var notices: [String] = []
        var shape: [CLLocationCoordinate2D] = []
        do {
            guard let route = try await MKDirections(request: request).calculate().routes.first else {
                throw ToolFailure(.unavailable, "Apple Maps found no route")
            }
            distance = route.distance
            seconds = route.expectedTravelTime
            name = route.name.isEmpty ? nil : route.name
            notices = route.advisoryNotices
            var points = [CLLocationCoordinate2D](repeating: CLLocationCoordinate2D(), count: route.polyline.pointCount)
            route.polyline.getCoordinates(&points, range: NSRange(location: 0, length: points.count))
            shape = thin(points)
        } catch {
            // Public transport often has a time but no drawable route (MapKit gives apps transit routes in few places).
            guard destination.mode == .transit, let eta = try? await MKDirections(request: request).calculateETA() else {
                throw ToolFailure(.unavailable, "Apple Maps found no \(destination.mode.rawValue) route: \(error.localizedDescription)")
            }
            distance = eta.distance
            seconds = eta.expectedTravelTime
        }
        let path: [JSONValue] = shape.map { .array([.number(($0.latitude * 1e5).rounded() / 1e5), .number(($0.longitude * 1e5).rounded() / 1e5)]) }
        return Output.json(Output.object([
            ("destination", Output.object([("name", Output.opt(destination.name)), ("latitude", .number(destination.latitude)),
                                           ("longitude", .number(destination.longitude))])),
            ("origin", Output.object([("latitude", .number(origin.coordinate.latitude)), ("longitude", .number(origin.coordinate.longitude)),
                                      ("from", .string(originSource))])),
            ("mode", .string(destination.mode.rawValue)),
            ("distance_meters", .number(distance.rounded())),
            ("expected_travel_seconds", .number(seconds.rounded())),
            ("route_name", Output.opt(name)),
            ("notices", notices.isEmpty ? nil : .array(notices.map { .string($0) })),
            ("path", path.isEmpty ? nil : .array(path)),
            ("source", .string("Apple Maps (MapKit directions)")),
            ("note", .string("Nothing was opened. The user sees this route in the chat with Start in Maps; phone_maps_navigate opens Maps.")),
        ]))
    }

    static func navigate(_ args: JSONValue) async throws -> [ToolContent] {
        let destination = try Destination.parse(args)
        try Foreground.require("open Maps")
        let coordinate = CLLocationCoordinate2D(latitude: destination.latitude, longitude: destination.longitude)
        let item = MKMapItem(placemark: MKPlacemark(coordinate: coordinate))
        item.name = destination.name
        let opened = item.openInMaps(launchOptions: [MKLaunchOptionsDirectionsModeKey: destination.directionsMode])
        guard opened else {
            throw ToolFailure(.unavailable, "iOS did not open Maps. Ask the user to open Persona and try again.")
        }
        return Output.json(Output.object([
            ("opened", .string("maps")),
            ("destination", Output.opt(destination.name)),
            ("latitude", .number(destination.latitude)),
            ("longitude", .number(destination.longitude)),
            ("mode", .string(destination.mode.rawValue)),
            ("note", .string("Directions are open in the Maps app; navigation runs there, not in Persona.")),
        ]))
    }
    #endif
}
