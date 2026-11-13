#if os(iOS)
import MapKit
import SwiftUI

/// Walking navigation's screen (#6672): the map following the walker with the route ahead, the next maneuver on top,
/// time and distance left with Mute, Back to Chat and End at the bottom. Presented over everything from `RootView`
/// while `WalkNavigation.shared.isPresented`.
struct WalkNavigationView: View {
    @Bindable var session: WalkNavigation
    @State private var camera: MapCameraPosition = .userLocation(followsHeading: true, fallback: .automatic)
    @State private var keptAwake: Bool?

    var body: some View {
        ZStack {
            map.ignoresSafeArea()
            VStack(spacing: Theme.Space.m) {
                WalkBanner(session: session)
                Spacer(minLength: 0)
                if camera.positionedByUser, !session.simulated, session.isActive { recenter }
                WalkBottomBar(session: session)
            }
            .padding(.horizontal, Theme.Space.page)
            .padding(.vertical, Theme.Space.s)
        }
        .onChange(of: session.fix?.latitude, initial: true) { follow() }
        // The screen stays on while the walk is on screen, as in Maps; the lock button still locks it.
        .onAppear {
            keptAwake = UIApplication.shared.isIdleTimerDisabled
            UIApplication.shared.isIdleTimerDisabled = true
        }
        .onDisappear { if let keptAwake { UIApplication.shared.isIdleTimerDisabled = keptAwake } }
    }

    private var map: some View {
        Map(position: $camera) {
            if session.simulated, let fix = session.fix {
                Annotation(String(localized: "Your Location [Place Map]"), coordinate: fix) { WalkerDot() }
                    .annotationTitles(.hidden)
            } else {
                UserAnnotation()
            }
            if let ahead = ahead, ahead.count > 1 {
                MapPolyline(coordinates: ahead)
                    .stroke(Theme.accent, style: StrokeStyle(lineWidth: Theme.Size.barWide, lineCap: .round, lineJoin: .round))
            }
            if let destination = session.destination {
                Marker(destination.name, systemImage: Theme.Icon.arrive, coordinate: destination.coordinate)
                    .tint(Theme.accent)
            }
        }
        .mapStyle(.standard(emphasis: .muted, pointsOfInterest: .excludingAll))
        .mapControls { MapCompass() }
        .accessibilityIdentifier("walk-nav.map")
    }

    /// The route from the walker on: what is behind them is not drawn.
    private var ahead: [CLLocationCoordinate2D]? {
        guard let route = session.route else { return nil }
        guard let progress = session.progress, let fix = session.fix else { return route.line }
        return [fix] + route.line[(progress.segment + 1)...]
    }

    private var recenter: some View {
        HStack {
            Spacer()
            Button { camera = .userLocation(followsHeading: true, fallback: .automatic) } label: {
                Label(String(localized: "Follow Me [Walk Navigation]"), systemImage: Theme.Icon.currentLocation)
                    .labelStyle(.iconOnly)
            }
            .font(Theme.Font.bodyStrong)
            .themedButton()
            .tapTargetFrame()
            .accessibilityIdentifier("walk-nav.recenter")
        }
    }

    /// A simulated walker is followed by moving the camera over the walk ahead; the real one by the map itself.
    private func follow() {
        guard session.simulated, let ahead, let first = ahead.first else { return }
        let lats = ahead.map(\.latitude), lons = ahead.map(\.longitude)
        let center = CLLocationCoordinate2D(latitude: (lats.min()! + lats.max()!) / 2, longitude: (lons.min()! + lons.max()!) / 2)
        let span = MKCoordinateSpan(latitudeDelta: max((lats.max()! - lats.min()!) * 2.2, 0.003),
                                    longitudeDelta: max((lons.max()! - lons.min()!) * 1.6, 0.003))
        camera = .region(MKCoordinateRegion(center: ahead.count > 1 ? center : first, span: span))
    }
}

/// The next maneuver: its arrow, how far, and Apple's instruction; or what the walk is waiting on.
private struct WalkBanner: View {
    var session: WalkNavigation

    var body: some View {
        HStack(alignment: .center, spacing: Theme.Space.m) {
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(Theme.Space.l)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("walk-nav.banner")
    }

    @ViewBuilder private var content: some View {
        switch session.phase {
        case .planning:
            ProgressView()
            Text(String(localized: "Planning the walk… [Walk Navigation]"))
                .font(Theme.Font.heading)
        case .failed(let why):
            ThemedIcon(Theme.Icon.directions).font(Theme.Font.display).foregroundStyle(.secondary)
                .accessibilityHidden(true)
            Text(why).font(Theme.Font.body)
        case .arrived:
            WalkManeuverIcon(maneuver: .arrive)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(String(localized: "You have arrived [Walk Navigation]")).font(Theme.Font.display).lineLimit(2)
                if let name = session.destination?.name { Text(name).font(Theme.Font.heading).foregroundStyle(.secondary) }
            }
        case .guiding, .rerouting:
            if let next = next {
                WalkManeuverIcon(maneuver: next.maneuver)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(WalkNavigation.distance(next.meters))
                        .font(Theme.Font.display).monospacedDigit()
                        .contentTransition(.numericText())
                    Text(next.instruction).font(Theme.Font.heading).lineLimit(3)
                    if session.phase == .rerouting {
                        Text(String(localized: "Finding a new route… [Walk Navigation]"))
                            .font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                }
            }
        }
    }

    /// What comes next: the next step's maneuver, or arriving.
    private var next: (maneuver: WalkManeuver, meters: Double, instruction: String)? {
        guard let route = session.route else { return nil }
        let progress = session.progress ?? WalkGuide.progress(of: route.line[0], on: route)
        if progress.step == 0, session.progress == nil {
            // Before the first fix: the walk's start.
            return (route.maneuver(at: 0) == .start ? .straight : route.maneuver(at: 0), progress.toNext, route.steps[0].instruction)
        }
        if let n = progress.nextStep(in: route) {
            return (route.maneuver(at: n), progress.toNext, route.steps[n].instruction)
        }
        return (.arrive, progress.remaining, String(localized: "Arrive at \(route.name) [Walk Navigation]"))
    }
}

/// Time and distance left with when the walker gets there; Mute, Back to Chat and End. Arrived: Done. Failed: Open in
/// Maps and Close.
private struct WalkBottomBar: View {
    @Bindable var session: WalkNavigation

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            switch session.phase {
            case .arrived:
                wide(String(localized: "Done [Walk Navigation]"), id: "walk-nav.done", prominent: true) { session.end() }
            case .failed:
                HStack(spacing: Theme.Space.s) {
                    wide(String(localized: "Open in Maps [Place Map]"), id: "walk-nav.maps", prominent: true) { session.openInMaps() }
                    wide(String(localized: "Close [Walk Navigation]"), id: "walk-nav.close", prominent: false) { session.end() }
                }
            case .planning, .guiding, .rerouting:
                if let left = left {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(left.time).font(Theme.Font.title).monospacedDigit().lineLimit(1)
                        Text(left.detail).font(Theme.Font.secondary).foregroundStyle(.secondary).monospacedDigit().lineLimit(2)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("walk-nav.left")
                }
                HStack(spacing: Theme.Space.s) {
                    icon(session.muted ? String(localized: "Unmute Guidance [Walk Navigation]") : String(localized: "Mute Guidance [Walk Navigation]"),
                         symbol: session.muted ? Theme.Icon.guidanceMuted : Theme.Icon.voiceSpeaking, id: "walk-nav.mute") {
                        session.muted.toggle()
                    }
                    icon(String(localized: "Back to Chat [Walk Navigation]"), symbol: Theme.Icon.backToChat, id: "walk-nav.back") {
                        session.isPresented = false
                    }
                    wide(String(localized: "End [Walk Navigation]"), id: "walk-nav.end", prominent: true) { session.end() }
                        .tint(Theme.danger)
                }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(Theme.Space.l)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
    }

    /// "6 min" and "420 m · Arrive 17:47".
    private var left: (time: String, detail: String)? {
        guard let route = session.route else { return nil }
        let progress = session.progress ?? WalkGuide.progress(of: route.line[0], on: route)
        let seconds = max(60, progress.remainingSeconds)
        let time = Duration.seconds(seconds).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated))
        let at = Date.now.addingTimeInterval(seconds).formatted(date: .omitted, time: .shortened)
        return (time, String(localized: "\(WalkNavigation.distance(progress.remaining)) · Arrive \(at) [Walk Navigation]"))
    }

    private func wide(_ title: String, id: String, prominent: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).lineLimit(1).minimumScaleFactor(0.7).frame(maxWidth: .infinity)
        }
        .font(Theme.Font.bodyStrong)
        .themedButton(prominent: prominent)
        .controlSize(.large)
        .accessibilityIdentifier(id)
    }

    private func icon(_ title: String, symbol: String, id: String, action: @escaping () -> Void) -> some View {
        // A large round button is a finger's size already; a 44 pt frame around it let its glass spill onto the next.
        Button(action: action) { Label(title, systemImage: symbol).labelStyle(.iconOnly) }
            .font(Theme.Font.bodyStrong)
            .themedButton()
            .buttonBorderShape(.circle)
            .controlSize(.large)
            .accessibilityLabel(title)
            .accessibilityIdentifier(id)
    }
}

/// A maneuver's arrow: right-hand turns draw the left-hand symbol mirrored, so each kind of turn has one symbol.
struct WalkManeuverIcon: View {
    var maneuver: WalkManeuver

    var body: some View {
        ThemedIcon(symbol)
            .font(Theme.Font.display)
            .foregroundStyle(Theme.accent)
            .scaleEffect(x: mirrored ? -1 : 1, y: 1)
            .scaledFrame(width: Theme.Size.avatar, height: Theme.Size.avatar)
            .accessibilityHidden(true)
    }

    private var symbol: String {
        switch maneuver {
        case .start, .straight: Theme.Icon.goStraight
        case .slightLeft, .slightRight: Theme.Icon.slightTurn
        case .left, .right, .sharpLeft, .sharpRight: Theme.Icon.turn
        case .uTurn: Theme.Icon.uTurn
        case .arrive: Theme.Icon.arrive
        }
    }

    private var mirrored: Bool {
        switch maneuver {
        case .slightRight, .right, .sharpRight: true
        default: false
        }
    }
}

/// The simulated walker (DEBUG), drawn as the system draws the phone: an accent dot in a white ring.
private struct WalkerDot: View {
    var body: some View {
        Circle().fill(Theme.accent)
            .overlay(Circle().stroke(.white, lineWidth: Theme.Size.ruleWide))
            .scaledFrame(width: Theme.Size.markM, height: Theme.Size.markM)
    }
}
#endif
