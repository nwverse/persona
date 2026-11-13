#if os(iOS)
import MapKit
import SwiftUI

/// Walking navigation's screen (#6672), laid out the way people know from Amap's and Maps' walking navigation: the next
/// turn in a dark panel on top; the map turned to the way the walker faces and tilted 30°, the route a thick accent line
/// with a close row of chevrons on it, the way walked grayed, the next turn a 3D arrow standing on the road, and the
/// walker an arrow in a halo; Mute and Back to Chat floating on the right; a light card at the bottom with End, what is
/// left and when they get there, and Overview. Presented over everything from `RootView` while
/// `WalkNavigation.shared.isPresented`.
struct WalkNavigationView: View {
    @Bindable var session: WalkNavigation
    @State private var camera: MapCameraPosition = .userLocation(followsHeading: true, fallback: .automatic)
    /// The map's own heading, so the walker, drawn flat on the screen, points the right way, and the turn arrow's side
    /// falls toward the camera.
    @State private var mapHeading: Double = 0
    /// The camera follows the walker until they move the map or ask for the overview.
    @State private var following = true
    @State private var overview = false
    @State private var keptAwake: Bool?

    /// How far the camera stands, how far ahead of the walker it looks and how much it tilts: close and gentle, so the
    /// next corner is in view without the far blocks crowding it (Amap's walking view tilts 30°).
    private static let cameraDistance = 300.0
    private static let lookAhead = 45.0
    private static let pitch = 30.0
    /// How tall the turn arrow stands: how far its side shows below its top.
    private static let arrowSide = 1.6

    var body: some View {
        ZStack {
            map.ignoresSafeArea()
            VStack(spacing: Theme.Space.m) {
                WalkBanner(session: session)
                if session.isActive { floating }
                Spacer(minLength: 0)
                WalkBottomBar(session: session, overview: overview) {
                    overview.toggle()
                    following = !overview
                    overview ? showOverview() : follow()
                }
            }
            .padding(.horizontal, Theme.Space.page)
            .padding(.vertical, Theme.Space.s)
        }
        .onChange(of: session.fix?.latitude) { follow() }
        .onChange(of: session.fix?.longitude) { follow() }
        .onChange(of: session.heading) { follow() }
        .onChange(of: camera.positionedByUser) { _, byUser in if byUser { following = false } }
        .onAppear {
            follow()
            // The screen stays on while the walk is on screen, as in Maps; the lock button still locks it.
            keptAwake = UIApplication.shared.isIdleTimerDisabled
            UIApplication.shared.isIdleTimerDisabled = true
        }
        .onDisappear { if let keptAwake { UIApplication.shared.isIdleTimerDisabled = keptAwake } }
    }

    // MARK: - The map

    private var map: some View {
        Map(position: $camera) {
            // In map coordinates, so it turns and tilts with the map; above the labels, so nothing hides it. MapKit
            // stacks overlays in the order they are added and adds a changed one on top, so the painted route is one
            // group, added again in its order whenever any of it changes (the walker moved, the camera turned).
            ForEach([paintKey], id: \.self) { _ in paint }
            if let destination = session.destination {
                Marker(destination.name, systemImage: Theme.Icon.arrive, coordinate: destination.coordinate)
                    .tint(Theme.accent)
            }
            if let fix = session.fix {
                Annotation(String(localized: "Your Location [Place Map]"), coordinate: fix, anchor: .center) {
                    WalkerMark(rotation: (session.heading ?? mapHeading) - mapHeading)
                }
                .annotationTitles(.hidden)
            } else {
                UserAnnotation()
            }
        }
        .mapStyle(.standard(elevation: .flat, emphasis: .muted, pointsOfInterest: .excludingAll))
        .mapControls { }
        .onMapCameraChange(frequency: .continuous) { context in mapHeading = context.camera.heading }
        .accessibilityIdentifier("walk-nav.map")
    }

    /// The camera's heading to 5°: the turn arrow's side falls toward it.
    private var cameraHeading: Double { (mapHeading / 5).rounded() * 5 }

    /// What the painted route depends on: where the walker is on it, and which way the camera faces.
    private var paintKey: String {
        let fix = session.fix.map { "\($0.latitude),\($0.longitude)" } ?? "-"
        return "\(session.route?.length ?? 0)|\(fix)|\(session.isActive)|\(cameraHeading)"
    }

    /// The route painted on the road, bottom to top: the way walked, the way ahead over its casing, the chevrons, and
    /// the next turn's arrow standing on the road (its side moved toward the camera, then its white top).
    @MapContentBuilder private var paint: some MapContent {
        if let behind, behind.count > 1 {
            MapPolyline(coordinates: behind)
                .stroke(Theme.Navigation.walked, style: Self.line(Theme.Size.routeLine))
                .mapOverlayLevel(level: .aboveLabels)
        }
        if let ahead, ahead.count > 1 {
            MapPolyline(coordinates: ahead)
                .stroke(Self.casing, style: Self.line(Theme.Size.routeCasing))
                .mapOverlayLevel(level: .aboveLabels)
            MapPolyline(coordinates: ahead)
                .stroke(Theme.accent, style: Self.line(Theme.Size.routeLine))
                .mapOverlayLevel(level: .aboveLabels)
        }
        ForEach(Array(chevrons.enumerated()), id: \.offset) { _, chevron in
            MapPolyline(coordinates: chevron)
                .stroke(.white, style: Self.line(Theme.Size.ruleWide))
                .mapOverlayLevel(level: .aboveLabels)
        }
        if let arrow = turnArrow {
            MapPolyline(coordinates: WalkGeo.shifted(arrow.shaft, meters: Self.arrowSide, bearing: cameraHeading + 180))
                .stroke(Theme.Navigation.arrowSide, style: Self.line(Theme.Size.routeCasing))
                .mapOverlayLevel(level: .aboveLabels)
            MapPolygon(coordinates: WalkGeo.shifted(arrow.head, meters: Self.arrowSide, bearing: cameraHeading + 180))
                .foregroundStyle(Theme.Navigation.arrowSide)
                .mapOverlayLevel(level: .aboveLabels)
            MapPolyline(coordinates: arrow.shaft)
                .stroke(.white, style: Self.line(Theme.Size.routeCasing))
                .mapOverlayLevel(level: .aboveLabels)
            MapPolygon(coordinates: arrow.head)
                .foregroundStyle(.white)
                .mapOverlayLevel(level: .aboveLabels)
        }
    }

    /// The route's darker edge: the accent's own hue, so the line stands off white streets and dark ones alike.
    private static var casing: Color { Theme.accent.mix(with: .black, by: 0.25) }

    private static func line(_ width: CGFloat) -> StrokeStyle {
        StrokeStyle(lineWidth: width, lineCap: .round, lineJoin: .round)
    }

    private var progress: WalkProgress? {
        guard let route = session.route else { return nil }
        return session.progress ?? WalkGuide.progress(of: route.line[0], on: route)
    }

    /// The route from the walker on.
    private var ahead: [CLLocationCoordinate2D]? {
        guard let route = session.route else { return nil }
        guard let progress = session.progress, let fix = session.fix else { return route.line }
        return [fix] + route.line[(progress.segment + 1)...]
    }

    /// The route already walked, up to the walker.
    private var behind: [CLLocationCoordinate2D]? {
        guard let route = session.route, let progress = session.progress, let fix = session.fix else { return nil }
        return Array(route.line[...progress.segment]) + [fix]
    }

    /// The row of chevrons along the way ahead, leaving the next turn's arrow alone.
    private var chevrons: [[CLLocationCoordinate2D]] {
        guard let route = session.route, session.isActive, let progress else { return [] }
        return route.chevrons(from: progress.along, clear: route.arrowStretch(progress.step))
    }

    /// The arrow standing on the road at the next turn; none when the next thing is arriving.
    private var turnArrow: (shaft: [CLLocationCoordinate2D], head: [CLLocationCoordinate2D])? {
        guard let route = session.route, session.isActive, let progress else { return nil }
        return route.maneuverArrow(progress.step)
    }

    // MARK: - Floating buttons

    /// Mute and Back to Chat on the map's right, and Follow Me once the walker moved the map.
    private var floating: some View {
        HStack {
            Spacer()
            VStack(spacing: Theme.Space.s) {
                round(session.muted ? String(localized: "Unmute Guidance [Walk Navigation]") : String(localized: "Mute Guidance [Walk Navigation]"),
                      symbol: session.muted ? Theme.Icon.guidanceMuted : Theme.Icon.voiceSpeaking, id: "walk-nav.mute") {
                    session.muted.toggle()
                }
                round(String(localized: "Back to Chat [Walk Navigation]"), symbol: Theme.Icon.backToChat, id: "walk-nav.back") {
                    session.isPresented = false
                }
                if !following, !overview {
                    round(String(localized: "Follow Me [Walk Navigation]"), symbol: Theme.Icon.currentLocation, id: "walk-nav.recenter") {
                        following = true
                        follow()
                    }
                }
            }
        }
    }

    private func round(_ title: String, symbol: String, id: String, action: @escaping () -> Void) -> some View {
        // A large round button is a finger's size already.
        Button(action: action) { Label(title, systemImage: symbol).labelStyle(.iconOnly) }
            .font(Theme.Font.bodyStrong)
            .themedButton()
            .buttonBorderShape(.circle)
            .controlSize(.large)
            .accessibilityLabel(title)
            .accessibilityIdentifier(id)
    }

    // MARK: - The camera

    /// Turns and tilts the camera to the way the walker faces, looking a little ahead of them.
    private func follow() {
        guard following, !overview, let fix = session.fix else { return }
        let heading = session.heading ?? 0
        let look = WalkGeo.offset(fix, meters: Self.lookAhead, bearing: heading)
        withAnimation(Theme.Motion.state) {
            camera = .camera(MapCamera(centerCoordinate: look, distance: Self.cameraDistance, heading: heading, pitch: Self.pitch))
        }
    }

    /// The whole way left, north up and flat, with room for the panels.
    private func showOverview() {
        guard let route = session.route else { return }
        let points = (ahead ?? route.line) + [route.destination]
        let lats = points.map(\.latitude), lons = points.map(\.longitude)
        let center = CLLocationCoordinate2D(latitude: (lats.min()! + lats.max()!) / 2, longitude: (lons.min()! + lons.max()!) / 2)
        let span = MKCoordinateSpan(latitudeDelta: max((lats.max()! - lats.min()!) * 2.2, 0.003),
                                    longitudeDelta: max((lons.max()! - lons.min()!) * 1.5, 0.003))
        withAnimation(Theme.Motion.state) { camera = .region(MKCoordinateRegion(center: center, span: span)) }
    }
}

// MARK: - The walker

/// The walker: an accent disc in a white ring with an arrow the way they face, in a soft halo of the accent.
private struct WalkerMark: View {
    var rotation: Double

    var body: some View {
        ThemedIcon(Theme.Icon.walker)
            .font(Theme.Font.bodyStrong)
            .foregroundStyle(.white)
            .rotationEffect(.degrees(rotation))
            .scaledFrame(width: Theme.Size.wellL, height: Theme.Size.wellL)
            .background(Theme.accent, in: .circle)
            .overlay(Circle().stroke(.white, lineWidth: Theme.Size.bar))
            .padding(Theme.Space.s)
            .background(Theme.accent.opacity(0.18), in: .circle)
            .accessibilityHidden(true)
    }
}

// MARK: - The banner

/// The next turn in a dark panel: a large arrow, how far, and Apple's instruction; then the one after it when it comes
/// within a short walk. Or what the walk is waiting on.
private struct WalkBanner: View {
    var session: WalkNavigation

    /// A maneuver this close after the next one is shown under it ("Then …").
    private static let thenWithin = 200.0

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .center, spacing: Theme.Space.l) {
                content
            }
            .padding(Theme.Space.l)
            .frame(maxWidth: .infinity, alignment: .leading)
            if let then {
                HStack(spacing: Theme.Space.s) {
                    Text(String(localized: "Then [Walk Navigation]")).font(Theme.Font.secondaryStrong)
                    WalkManeuverGlyph(maneuver: then.maneuver).font(Theme.Font.secondaryStrong)
                    Text(then.instruction).font(Theme.Font.secondary).lineLimit(1)
                }
                .foregroundStyle(.white.secondary)
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.s)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.black.opacity(0.22))
            }
        }
        .foregroundStyle(.white)
        .background(Theme.Navigation.panel)
        .clipShape(.rect(cornerRadius: Theme.Radius.panel, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("walk-nav.banner")
    }

    @ViewBuilder private var content: some View {
        switch session.phase {
        case .planning:
            ProgressView().tint(.white)
            Text(String(localized: "Planning the walk… [Walk Navigation]")).font(Theme.Font.heading)
        case .failed(let why):
            WalkManeuverGlyph(maneuver: .arrive).font(Theme.Font.display).accessibilityHidden(true)
            Text(why).font(Theme.Font.body)
        case .arrived:
            turnIcon(.arrive)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(String(localized: "You have arrived [Walk Navigation]")).font(Theme.Font.display).lineLimit(2)
                if let name = session.destination?.name { Text(name).font(Theme.Font.heading).foregroundStyle(.white.secondary) }
            }
        case .guiding, .rerouting:
            if let next {
                turnIcon(next.maneuver)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(WalkNavigation.distance(next.meters))
                        .font(Theme.Font.display).monospacedDigit()
                        .contentTransition(.numericText())
                    Text(next.instruction).font(Theme.Font.title).lineLimit(3)
                    if session.phase == .rerouting {
                        Text(String(localized: "Finding a new route… [Walk Navigation]"))
                            .font(Theme.Font.caption).foregroundStyle(.white.secondary)
                    }
                }
            }
        }
    }

    /// The turn's arrow, large, as navigation apps lead their panel with it.
    private func turnIcon(_ maneuver: WalkManeuver) -> some View {
        WalkManeuverGlyph(maneuver: maneuver)
            .font(Theme.Font.display)
            .imageScale(.large)
            .scaledFrame(width: Theme.Size.avatarM, height: Theme.Size.avatarM)
            .accessibilityHidden(true)
    }

    private var progress: WalkProgress? {
        guard let route = session.route else { return nil }
        return session.progress ?? WalkGuide.progress(of: route.line[0], on: route)
    }

    /// What comes next: the maneuver at the end of the step being walked.
    private var next: (maneuver: WalkManeuver, meters: Double, instruction: String)? {
        guard let route = session.route, let progress else { return nil }
        return (route.maneuver(at: progress.step), progress.toNext, instruction(progress.step, on: route))
    }

    /// The maneuver after the next, when it comes within a short walk of it; arriving counts.
    private var then: (maneuver: WalkManeuver, instruction: String)? {
        guard session.phase == .guiding || session.phase == .rerouting,
              let route = session.route, let progress, let n = progress.thenStep(in: route),
              route.stepEnds[n] - route.stepStarts[n] <= Self.thenWithin else { return nil }
        return (route.maneuver(at: n), instruction(n, on: route))
    }

    /// Apple's instruction for step `i`, or, for an arrival it left blank, "Arrive at <place>".
    private func instruction(_ i: Int, on route: WalkRoute) -> String {
        let said = route.steps[i].instruction
        guard said.isEmpty, route.maneuver(at: i) == .arrive else { return said }
        return String(localized: "Arrive at \(route.name) [Walk Navigation]")
    }
}

// MARK: - The bottom card

/// End on the left, what is left and when they get there in the middle, Overview on the right. Arrived: Done. Failed:
/// Open in Maps and Close.
private struct WalkBottomBar: View {
    @Bindable var session: WalkNavigation
    var overview: Bool
    var toggleOverview: () -> Void

    var body: some View {
        Group {
            switch session.phase {
            case .arrived:
                wide(String(localized: "Done [Walk Navigation]"), id: "walk-nav.done", prominent: true) { session.end() }
            case .failed:
                HStack(spacing: Theme.Space.s) {
                    wide(String(localized: "Open in Maps [Place Map]"), id: "walk-nav.maps", prominent: true) { session.openInMaps() }
                    wide(String(localized: "Close [Walk Navigation]"), id: "walk-nav.close", prominent: false) { session.end() }
                }
            case .planning, .guiding, .rerouting:
                // In a row; at the largest text sizes what is left on top, the buttons under it.
                ViewThatFits(in: .horizontal) {
                    HStack(spacing: Theme.Space.m) {
                        end
                        summary.frame(maxWidth: .infinity)
                        overviewButton
                    }
                    VStack(spacing: Theme.Space.m) {
                        summary
                        HStack(spacing: Theme.Space.s) { end; overviewButton }
                    }
                }
            }
        }
        .frame(maxWidth: .infinity)
        .padding(Theme.Space.l)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
    }

    /// "Remaining 300 m · 5 min" over "Arrive 23:05".
    @ViewBuilder private var summary: some View {
        if let left {
            VStack(spacing: Theme.Space.xxs) {
                Text(String(localized: "Remaining \(left.distance) · \(left.time) [Walk Navigation]"))
                    .font(Theme.Font.heading).monospacedDigit().lineLimit(1).minimumScaleFactor(0.7)
                Text(String(localized: "Arrive \(left.arrival) [Walk Navigation]"))
                    .font(Theme.Font.secondary).foregroundStyle(.secondary).monospacedDigit().lineLimit(1)
            }
            .multilineTextAlignment(.center)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("walk-nav.left")
        }
    }

    private var end: some View {
        compact(String(localized: "End [Walk Navigation]"), id: "walk-nav.end") { session.end() }
            .tint(Theme.danger)
    }

    private var overviewButton: some View {
        compact(overview ? String(localized: "Resume [Walk Navigation]") : String(localized: "Overview [Walk Navigation]"),
                id: "walk-nav.overview", action: toggleOverview)
    }

    /// "21:04", "5 min", "450 m".
    private var left: (arrival: String, time: String, distance: String)? {
        guard let route = session.route else { return nil }
        let progress = session.progress ?? WalkGuide.progress(of: route.line[0], on: route)
        let seconds = max(60, progress.remainingSeconds)
        let time = Duration.seconds(seconds).formatted(.units(allowed: [.hours, .minutes], width: .abbreviated))
        let at = Date.now.addingTimeInterval(seconds).formatted(date: .omitted, time: .shortened)
        return (at, time, WalkNavigation.distance(progress.remaining))
    }

    private func compact(_ title: String, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) { Text(title).lineLimit(1).minimumScaleFactor(0.7) }
            .font(Theme.Font.bodyStrong)
            .themedButton()
            .controlSize(.large)
            .fixedSize()
            .accessibilityIdentifier(id)
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
}

// MARK: - Maneuver arrows

/// A maneuver's arrow in the surrounding font and color: right-hand turns draw the left-hand symbol mirrored, so each
/// kind of turn has one symbol.
struct WalkManeuverGlyph: View {
    var maneuver: WalkManeuver

    var body: some View {
        ThemedIcon(symbol)
            .scaleEffect(x: mirrored ? -1 : 1, y: 1)
            .accessibilityHidden(true)
    }

    private var symbol: String {
        switch maneuver {
        case .straight: Theme.Icon.goStraight
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
#endif
