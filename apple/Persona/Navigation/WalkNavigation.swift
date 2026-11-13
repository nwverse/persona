#if os(iOS)
import AVFoundation
import CoreLocation
import MapKit
import NaturalLanguage
import Observation
import UIKit

/// Walking navigation inside Persona (#6672): one walk at a time, from a walking route card, `phone_maps_navigate`
/// with `mode: walking`, or DEBUG's `-showWalkNavigation`. It asks Apple Maps for the walking route, follows the phone's
/// fixes with `WalkGuide`, works the route out again when the walker leaves it, and says each maneuver with the system
/// voice. While it runs, location updates go on with the screen locked (`CLBackgroundActivitySession`, the blue
/// indicator); End stops them at once. `WalkNavigationView` draws it, presented from `RootView`.
@MainActor @Observable
final class WalkNavigation {
    static let shared = WalkNavigation()

    /// Where the walk stands.
    enum Phase: Equatable {
        /// Asking Apple Maps for the route.
        case planning
        case guiding
        /// Off the route: asking for a new one from where the walker is, still showing the old.
        case rerouting
        case arrived
        /// No route, or no location; the words say why.
        case failed(String)
    }

    /// Where the walk goes.
    struct Destination: Equatable {
        var coordinate: CLLocationCoordinate2D
        var name: String

        static func == (a: Destination, b: Destination) -> Bool {
            a.name == b.name && WalkGeo.meters(a.coordinate, b.coordinate) < 1
        }
    }

    /// The navigation screen is up. False while a walk goes on behind the chat (Back to Chat).
    var isPresented = false
    private(set) var destination: Destination?
    private(set) var phase: Phase = .planning
    private(set) var route: WalkRoute?
    private(set) var progress: WalkProgress?
    /// The latest fix (the simulated walker's in DEBUG).
    private(set) var fix: CLLocationCoordinate2D?
    /// Which way the walker faces, 0…360 from north: the phone's course while it moves, else the route's way there.
    private(set) var heading: Double?
    /// A DEBUG walk: no location updates or voice, the walker placed on the route.
    private(set) var simulated = false
    var muted: Bool {
        didSet {
            UserDefaults.standard.set(muted, forKey: Self.mutedKey)
            if muted { voice.stop() }
        }
    }

    /// A walk is on (not arrived, failed or ended).
    var isActive: Bool {
        guard destination != nil else { return false }
        switch phase {
        case .planning, .guiding, .rerouting: return true
        case .arrived, .failed: return false
        }
    }

    private static let mutedKey = "walkNavigation.muted"
    @ObservationIgnored private var updates: Task<Void, Never>?
    @ObservationIgnored private var background: CLBackgroundActivitySession?
    @ObservationIgnored private var announcer = WalkAnnouncer()
    @ObservationIgnored private var reroute = WalkReroute()
    @ObservationIgnored private let voice = WalkVoice()

    private init() {
        muted = UserDefaults.standard.bool(forKey: Self.mutedKey)
    }

    /// Starts walking to `coordinate`, or brings back the screen of the walk already going there.
    func start(to coordinate: CLLocationCoordinate2D, name: String) {
        let next = Destination(coordinate: coordinate, name: name)
        if destination == next, isActive {
            isPresented = true
            return
        }
        end()
        destination = next
        phase = .planning
        isPresented = true
        updates = Task { [weak self] in await self?.run(to: next) }
    }

    /// Stops the walk: location updates, the background session and the voice, and closes the screen.
    func end() {
        updates?.cancel()
        updates = nil
        stopLocation()
        voice.stop()
        destination = nil
        route = nil
        progress = nil
        fix = nil
        heading = nil
        simulated = false
        phase = .planning
        announcer = WalkAnnouncer()
        reroute = WalkReroute()
        isPresented = false
    }

    /// Opens the same walk in Apple Maps, for when Persona can't guide it (or the walker would rather).
    func openInMaps() {
        guard let destination else { return }
        let item = MKMapItem(location: CLLocation(latitude: destination.coordinate.latitude, longitude: destination.coordinate.longitude), address: nil)
        item.name = destination.name
        item.openInMaps(launchOptions: [MKLaunchOptionsDirectionsModeKey: MKLaunchOptionsDirectionsModeWalking])
        end()
    }

    // MARK: - The walk

    private func run(to destination: Destination) async {
        switch LocationAccess.status() {
        case .granted, .limited, .notNeeded: break
        case .notDetermined:
            guard await LocationAccess.request() == .granted else { return fail(Self.noLocation) }
        case .denied:
            return fail(Self.noLocation)
        }
        guard let first = await plan(from: nil, to: destination), !Task.isCancelled else { return }
        take(first)
        phase = .guiding
        background = CLBackgroundActivitySession()
        do {
            for try await update in CLLocationUpdate.liveUpdates(.otherNavigation) {
                if Task.isCancelled { break }
                if update.authorizationDenied || update.authorizationDeniedGlobally { return fail(Self.noLocation) }
                guard let location = update.location, location.horizontalAccuracy >= 0 else { continue }
                await follow(location)
                if phase == .arrived { break }
            }
        } catch {
            if !Task.isCancelled { fail(Self.noLocation) }
        }
        stopLocation()
    }

    /// One fix: where it falls, what to say, and whether to ask for a new route.
    private func follow(_ location: CLLocation) async {
        guard let route, let destination else { return }
        let coordinate = location.coordinate
        let now = WalkGuide.progress(of: coordinate, on: route, hint: progress?.segment ?? 0)
        fix = coordinate
        progress = now
        heading = location.course >= 0 && location.speed >= 0.5 ? location.course : route.bearing(at: now.segment)
        let arrived = WalkGuide.arrived(coordinate, now, on: route)
        if let line = announcer.due(now, on: route, arrived: arrived) { say(line) }
        if arrived {
            phase = .arrived
            return
        }
        guard phase == .guiding,
              reroute.take(off: WalkGuide.isOff(now, accuracy: location.horizontalAccuracy), at: .now) else { return }
        phase = .rerouting
        if let again = await plan(from: coordinate, to: destination), !Task.isCancelled, self.destination == destination {
            take(again)
        }
        if phase == .rerouting { phase = .guiding }
    }

    /// The walking route from `start` (nil: where Maps thinks the phone is) to `destination`; nil after saying why.
    private func plan(from start: CLLocationCoordinate2D?, to destination: Destination) async -> WalkRoute? {
        let request = MKDirections.Request()
        request.source = start.map { MKMapItem(location: CLLocation(latitude: $0.latitude, longitude: $0.longitude), address: nil) }
            ?? MKMapItem.forCurrentLocation()
        let target = MKMapItem(location: CLLocation(latitude: destination.coordinate.latitude, longitude: destination.coordinate.longitude), address: nil)
        target.name = destination.name
        request.destination = target
        request.transportType = .walking
        request.requestsAlternateRoutes = false
        do {
            guard let found = try await MKDirections(request: request).calculate().routes.first,
                  let route = WalkRoute(name: destination.name, destination: destination.coordinate,
                                        steps: found.steps.map { WalkStep(instruction: $0.instructions, line: $0.polyline.coordinates) },
                                        expectedSeconds: found.expectedTravelTime) else {
                if start == nil { fail(Self.noRoute) }
                return nil
            }
            return route
        } catch {
            // A failed new route keeps the old one on screen; only the first one leaves nothing to guide.
            if start == nil, !Task.isCancelled { fail(Self.noRoute) }
            return nil
        }
    }

    private func take(_ next: WalkRoute) {
        route = next
        progress = nil
        announcer.reset()
    }

    private func fail(_ why: String) {
        stopLocation()
        voice.stop()
        phase = .failed(why)
    }

    private func stopLocation() {
        background?.invalidate()
        background = nil
    }

    private func say(_ line: WalkAnnouncer.Line) {
        guard !muted, !simulated, let route else { return }
        if let text = Self.words(line, on: route) { voice.say(text) }
    }

    /// The words for a line of guidance: Apple's instruction, with how far ahead when it isn't right here.
    nonisolated static func words(_ line: WalkAnnouncer.Line, on route: WalkRoute) -> String? {
        switch line {
        case .start:
            return String(localized: "Walking to \(route.name) [Walk Navigation]")
        case .maneuver(let step, let meters):
            let instruction = route.steps[step].instruction
            guard !instruction.isEmpty else { return nil }
            guard meters >= 15 else { return instruction }
            return String(localized: "In \(distance(meters)), \(instruction) [Walk Navigation]")
        case .arrived:
            return String(localized: "You have arrived at \(route.name) [Walk Navigation]")
        }
    }

    /// Meters as the walker hears and reads them: to 10 m under a kilometer, then in kilometers.
    nonisolated static func distance(_ meters: Double) -> String {
        let rounded = meters < 1000 ? (meters / 10).rounded() * 10 : (meters / 100).rounded() * 100
        let measure = Measurement(value: max(rounded, 0), unit: UnitLength.meters)
        return measure.formatted(.measurement(width: .abbreviated, usage: .road))
    }

    private static var noLocation: String {
        String(localized: "Persona needs your location to guide the walk. Allow it in Settings, or open the walk in Maps. [Walk Navigation]")
    }

    private static var noRoute: String {
        String(localized: "Apple Maps found no walking route there. [Walk Navigation]")
    }

    // MARK: - DEBUG

    #if DEBUG
    /// DEBUG `-showWalkNavigation <meters along | end>`: a walk on a sample route, the walker placed on it, for
    /// screenshots and UI tests. No location updates, no voice.
    func simulate(at value: String) {
        end()
        guard let sample = WalkRoute.sample else { return }
        destination = Destination(coordinate: sample.destination, name: sample.name)
        simulated = true
        route = sample
        let meters = value == "end" ? sample.length : min(Double(value) ?? 0, sample.length)
        let at = sample.point(at: meters)
        fix = at.coordinate
        heading = at.bearing
        progress = WalkGuide.progress(of: at.coordinate, on: sample)
        phase = value == "end" ? .arrived : .guiding
        isPresented = true
    }

    #endif
}

#if DEBUG
extension WalkRoute {
    /// The mock hub's walk to the second cafe (scripts/mock/mock_hub.py, MOCK_PLACE_MAP), as Apple Maps worked it out
    /// for those two points (walking, 979 m, Chinese instructions), so screenshots follow real streets.
    static let sample = WalkRoute(
        name: "安静书房咖啡", // i18n-ignore: the mock hub's place
        destination: CLLocationCoordinate2D(latitude: 31.229074, longitude: 121.476983),
        steps: [
            WalkStep(instruction: "右转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.231799, longitude: 121.473693), .init(latitude: 31.231729, longitude: 121.473719), .init(latitude: 31.231605, longitude: 121.473809)]),
            WalkStep(instruction: "右转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.231605, longitude: 121.473809), .init(latitude: 31.231588, longitude: 121.473733), .init(latitude: 31.231620, longitude: 121.473679), .init(latitude: 31.231699, longitude: 121.473567), .init(latitude: 31.231756, longitude: 121.473517), .init(latitude: 31.232081, longitude: 121.473239), .init(latitude: 31.232212, longitude: 121.473067), .init(latitude: 31.232780, longitude: 121.472620)]),
            WalkStep(instruction: "左转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.232780, longitude: 121.472620), .init(latitude: 31.232898, longitude: 121.473251), .init(latitude: 31.232911, longitude: 121.473771), .init(latitude: 31.232892, longitude: 121.474028), .init(latitude: 31.232792, longitude: 121.474432), .init(latitude: 31.232673, longitude: 121.474727), .init(latitude: 31.232550, longitude: 121.474968), .init(latitude: 31.232404, longitude: 121.475204), .init(latitude: 31.232329, longitude: 121.475289)]),
            WalkStep(instruction: "右转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.232329, longitude: 121.475289), .init(latitude: 31.232386, longitude: 121.475370), .init(latitude: 31.232442, longitude: 121.475450)]),
            WalkStep(instruction: "左转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.232442, longitude: 121.475450), .init(latitude: 31.232394, longitude: 121.475463), .init(latitude: 31.231912, longitude: 121.475619), .init(latitude: 31.231725, longitude: 121.475719)]),
            WalkStep(instruction: "继续前行", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.231725, longitude: 121.475719), .init(latitude: 31.231756, longitude: 121.475764), .init(latitude: 31.231844, longitude: 121.475884)]),
            WalkStep(instruction: "左转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.231844, longitude: 121.475884), .init(latitude: 31.231725, longitude: 121.475971), .init(latitude: 31.231717, longitude: 121.475976), .init(latitude: 31.231653, longitude: 121.475842), .init(latitude: 31.231570, longitude: 121.475899), .init(latitude: 31.231296, longitude: 121.476087), .init(latitude: 31.231320, longitude: 121.476129), .init(latitude: 31.230797, longitude: 121.476502)]),
            WalkStep(instruction: "右转进入西藏中路", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.230797, longitude: 121.476502), .init(latitude: 31.230818, longitude: 121.476548), .init(latitude: 31.230904, longitude: 121.476725)]),
            WalkStep(instruction: "稍向右转，进入武胜路", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.230904, longitude: 121.476725), .init(latitude: 31.230780, longitude: 121.476822), .init(latitude: 31.230268, longitude: 121.477209)]),
            WalkStep(instruction: "稍向右转", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.230268, longitude: 121.477209), .init(latitude: 31.230107, longitude: 121.477198), .init(latitude: 31.229329, longitude: 121.477144), .init(latitude: 31.229276, longitude: 121.477133)]),
            WalkStep(instruction: "到达目的地", // i18n-ignore: Apple's instruction
                     line: [.init(latitude: 31.229276, longitude: 121.477133), .init(latitude: 31.229247, longitude: 121.477084), .init(latitude: 31.229195, longitude: 121.477052), .init(latitude: 31.229074, longitude: 121.476983)]),
        ],
        expectedSeconds: 1028
    )
}
#endif

private extension MKPolyline {
    var coordinates: [CLLocationCoordinate2D] {
        var out = [CLLocationCoordinate2D](repeating: kCLLocationCoordinate2DInvalid, count: pointCount)
        getCoordinates(&out, range: NSRange(location: 0, length: pointCount))
        return out
    }
}

/// The system voice for guidance, in the language of what it says (Apple's instructions follow the phone's language,
/// which can differ from the app's). Its own audio session, managed by the system, so a recording's or a voice chat's
/// session is left as it is.
@MainActor
private final class WalkVoice {
    private let synthesizer: AVSpeechSynthesizer = {
        let s = AVSpeechSynthesizer()
        s.usesApplicationAudioSession = false
        return s
    }()

    func say(_ text: String) {
        // A newer instruction replaces one still being said: the old one is out of date.
        if synthesizer.isSpeaking { synthesizer.stopSpeaking(at: .word) }
        let utterance = AVSpeechUtterance(string: text)
        let recognizer = NLLanguageRecognizer()
        recognizer.processString(text)
        if let language = recognizer.dominantLanguage?.rawValue {
            utterance.voice = AVSpeechSynthesisVoice(language: language)
        }
        synthesizer.speak(utterance)
    }

    func stop() {
        synthesizer.stopSpeaking(at: .immediate)
    }
}
#endif
