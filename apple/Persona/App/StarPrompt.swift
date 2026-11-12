import Foundation
import Observation
import SwiftUI

// Asking for a GitHub star in three quiet places on the iPhone and iPad (#5761, docs/design/DESIGN_SYSTEM.md "GitHub star
// prompts"): a pill under the empty Agents list's Start Your First Session (`StarPromptPill`), a notice in the Agents
// list's notice slot once this device has started enough Agents (`StarPromptNoticeRow`), and a toast when the checklist
// after pairing ends with Get started (`welcomeToast`). The rules are spec/fixtures/star-prompt's, which the web
// (web/src/lib/star-prompt.ts) and Android replay too (`StarPromptFixtureTests`). The state is this device's and never
// goes to the hub. Whether the user really starred can't be known: a tap on Star counts.

/// What happens to the prompts (spec/fixtures/star-prompt): an Agent this device started, Later (or ✕) on the card,
/// Star on GitHub anywhere, ✕ on the pill, and Get started at the end of the checklist after pairing.
nonisolated enum StarPromptEvent: String, Decodable, Sendable {
    case launch, later, star, hidePill, onboarded
}

/// One device's star prompts (the web's `StarPromptState`).
nonisolated struct StarPromptState: Codable, Equatable, Sendable {
    /// Agents this device started.
    var launches = 0
    /// The launches the card waits for; Later doubles it.
    var threshold = 20
    /// Unix milliseconds before which the card stays away (Later's three days), or nil.
    var snoozedUntil: Int64?
    /// Star on GitHub was tapped: every prompt stops.
    var done = false
    var pillHidden = false
    /// The onboarding toast was shown.
    var welcomed = false

    static let start = StarPromptState()
    /// How long Later holds the card: three days, in milliseconds.
    static let wait: Int64 = 3 * 24 * 60 * 60 * 1000

    /// The state after `event` at `now` (Unix milliseconds), and whether it showed the onboarding toast (`starPromptAfter`).
    func after(_ event: StarPromptEvent, now: Int64) -> (state: StarPromptState, toast: Bool) {
        var next = self
        switch event {
        case .launch:
            next.launches = launches < Int.max ? launches + 1 : launches
        case .later:
            // Doubled at least once, then until it is past the launches so far.
            var t = max(1, threshold)
            repeat { t = t <= Int.max / 2 ? t * 2 : Int.max } while t <= launches && t < Int.max
            next.threshold = t
            next.snoozedUntil = now + Self.wait
        case .star:
            next.done = true
        case .hidePill:
            next.pillHidden = true
        case .onboarded:
            next.welcomed = true
            return (next, !done && !welcomed)
        }
        return (next, false)
    }

    /// The empty Agents list's pill (`showsStarPill`): until it is hidden or the user starred.
    var showsPill: Bool { !done && !pillHidden }

    /// The Agents list's card (`showsStarCard`): from the threshold, outside Later's wait, until the user starred.
    func showsCard(now: Int64) -> Bool {
        !done && launches >= threshold && (snoozedUntil.map { now >= $0 } ?? true)
    }

    fileprivate nonisolated enum CodingKeys: String, CodingKey {
        case launches, threshold, snoozedUntil, done, pillHidden, welcomed
    }
}

nonisolated extension StarPromptState {
    /// A damaged or older value falls back field by field, as the web's does: a count that is not one starts over, a
    /// threshold of 0 is the first one.
    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        func count(_ key: CodingKeys, _ start: Int) -> Int {
            guard let n = try? c.decodeIfPresent(Int.self, forKey: key), n >= 0 else { return start }
            return n
        }
        func flag(_ key: CodingKeys) -> Bool { (try? c.decodeIfPresent(Bool.self, forKey: key)) == true }
        launches = count(.launches, 0)
        let threshold = count(.threshold, Self.start.threshold)
        self.threshold = threshold == 0 ? Self.start.threshold : threshold
        snoozedUntil = try? c.decodeIfPresent(Int64.self, forKey: .snoozedUntil)
        done = flag(.done)
        pillHidden = flag(.pillHidden)
        welcomed = flag(.welcomed)
    }
}

/// This device's star prompts: read once, by the first view that asks (`shared` is made then, not at launch), and
/// written only on a launch or a tap (docs/apple/performance.md P2), one small JSON value in UserDefaults, as the
/// clipboard's once-only paste hint keeps its flag (`ClipboardHints`).
@MainActor @Observable final class StarPromptModel {
    static let shared = StarPromptModel()
    /// The project's page. A `Link` / `openURL` opens it in the GitHub app where installed, else in Safari, where the
    /// user's GitHub session is (not the app's link card).
    static let repoURL = URL(string: "https://github.com/nwverse/persona")!
    static let defaultsKey = "persona.starPrompt"

    private(set) var state: StarPromptState
    @ObservationIgnored private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        state = Self.load(defaults)
        #if DEBUG
        // `-showStarPromptCard YES`: the Agents list's card, as at its threshold (screenshots); written only after a tap.
        if defaults.bool(forKey: "showStarPromptCard") {
            state.done = false
            state.snoozedUntil = nil
            state.launches = max(state.launches, state.threshold)
        }
        #endif
    }

    /// The prompts are the iPhone's and iPad's: the Mac app is frozen (docs/apple/mac.md), Apple Vision Pro asks nothing.
    static var applies: Bool {
        #if os(iOS)
        return true
        #else
        return false
        #endif
    }

    /// The empty Agents list's pill. Never in Demo Mode.
    func showsPill(demo: Bool) -> Bool { Self.applies && !demo && state.showsPill }

    /// The Agents list's card at `now`. Never in Demo Mode; the list also holds it while a session needs the user or a
    /// search is on.
    func showsCard(demo: Bool, now: Date = .now) -> Bool { Self.applies && !demo && state.showsCard(now: Self.milliseconds(now)) }

    /// The hub took an Agent this device started (New Agent's Start). Not counted in Demo Mode.
    func launched(demo: Bool) {
        guard Self.applies, !demo else { return }
        apply(.launch)
    }

    /// ✕ on the card: it comes back after twice the launches, and not before three days.
    func later() { apply(.later) }

    /// ✕ on the pill: gone for good on this device.
    func hidePill() { apply(.hidePill) }

    /// Star on GitHub, from any prompt: every prompt stops on this device, and the project's page opens.
    func star(open: OpenURLAction) {
        apply(.star)
        open(Self.repoURL)
    }

    /// Get started at the end of the checklist after pairing: whether to show the toast, once per device and never after
    /// a star. Nothing in Demo Mode.
    func onboarded(demo: Bool) -> Bool {
        guard Self.applies, !demo else { return false }
        return apply(.onboarded)
    }

    /// Applies `event` now and keeps the result when it changed anything; whether it showed the onboarding toast.
    @discardableResult private func apply(_ event: StarPromptEvent, now: Date = .now) -> Bool {
        let (next, toast) = state.after(event, now: Self.milliseconds(now))
        if next != state {
            state = next
            if let data = try? JSONEncoder().encode(next) { defaults.set(data, forKey: Self.defaultsKey) }
        }
        return toast
    }

    static func milliseconds(_ date: Date) -> Int64 { Int64((date.timeIntervalSince1970 * 1000).rounded()) }

    private static func load(_ defaults: UserDefaults) -> StarPromptState {
        guard let data = defaults.data(forKey: defaultsKey),
              let saved = try? JSONDecoder().decode(StarPromptState.self, from: data) else { return .start }
        return saved
    }
}
