import Foundation
import Observation

/// What the hub-level group's hub can do for the To-dos redesign (#10465, docs/apple/ios.md "To-dos (V1, #10465)"):
/// the hello's features, each listed only while every manager behind the hub names it (docs/protocol/protocol.md
/// "Health check and feature flags"). Before this launch's hello, what the group's last hello said (`remembered`), so
/// a list restored at launch is drawn the way it will be.
nonisolated struct TodoFeatures: Equatable, Sendable {
    static let viewName = "todo_view"
    static let placesName = "todo_places"
    static let timelineName = "todo_timeline"
    static let asksName = "todo_asks"

    /// Rows carry `view`, the list `counts`, and `GET /api/v1/todos/{id}` answers; Someday and restore are the hub's.
    var view = false
    /// `GET /api/v1/todos/places`.
    var places = false
    /// `GET /api/v1/todos/{id}/timeline`.
    var timeline = false
    /// `POST /api/v1/todos/{id}/asks/{ask}/answer`.
    var asks = false
    /// The hub's `todo_send_state` hello marker: it says Processing for a to-do's message (docs/protocol/protocol.md
    /// "Message feedback (#5258)"). Trusted only from the current hello, so never remembered; false until the app reads
    /// that marker.
    var sendState = false

    init(view: Bool = false, places: Bool = false, timeline: Bool = false, asks: Bool = false, sendState: Bool = false) {
        (self.view, self.places, self.timeline, self.asks, self.sendState) = (view, places, timeline, asks, sendState)
    }

    init(_ features: Set<String>, sendState: Bool = false) {
        self.init(view: features.contains(Self.viewName), places: features.contains(Self.placesName),
                  timeline: features.contains(Self.timelineName), asks: features.contains(Self.asksName), sendState: sendState)
    }

    // MARK: Remembered per group

    private var bits: Int { (view ? 1 : 0) | (places ? 2 : 0) | (timeline ? 4 : 0) | (asks ? 8 : 0) }

    /// What `group`'s last hello listed (nothing for a group never heard from).
    static func remembered(_ group: String, in defaults: UserDefaults = .standard) -> TodoFeatures {
        let bits = defaults.integer(forKey: key(group))
        return TodoFeatures(view: bits & 1 != 0, places: bits & 2 != 0, timeline: bits & 4 != 0, asks: bits & 8 != 0)
    }

    /// Notes what `group`'s hello listed; written only when it changes, so rarely (P2).
    static func remember(_ features: TodoFeatures, group: String, in defaults: UserDefaults = .standard) {
        let bits = features.bits
        if defaults.integer(forKey: key(group)) != bits { defaults.set(bits, forKey: key(group)) }
    }

    private static func key(_ group: String) -> String { "hubTodoFeatures." + group }
}

extension HubLink {
    /// What this group's hub can do for To-dos: by its hello, or before this launch's hello by what the last one said.
    var todoFeatures: TodoFeatures { hello.features.isEmpty ? .remembered(group) : TodoFeatures(features) }
}

extension AppModel {
    /// What the hub-level group's hub can do for To-dos (`TodoFeatures`); nothing while no hub is paired.
    var todoFeatures: TodoFeatures { hub?.todoFeatures ?? TodoFeatures() }
}

/// Whether To-dos is the V1 design (#10465, decision S2-1): on for the hub-level group once its hello lists `todo_view`,
/// or at launch when its last hello did, and then on until the app quits. A hello without `todo_view` while a manager
/// reconnects never takes the page back to today's (rows without a view are filed by `TodoFiling`, and the item and
/// places routes answering 404 or 405 are "not there"); a hub that never listed it keeps today's screens.
@MainActor @Observable
final class TodosV1 {
    static let shared = TodosV1()

    /// Whether this build shows V1 at all: off until #10465's switch-on, so hubs that send the view keep today's
    /// screens in every build that reaches people before then (S2-2).
    nonisolated static let released = false

    private(set) var isOn: Bool
    @ObservationIgnored private let allowed: Bool
    @ObservationIgnored private let hubGroup: @MainActor () -> String

    /// `launchGroup`: the hub-level group at launch, read from where the app keeps it rather than from `AppModel`, which
    /// may still be restoring itself when this is first made. `hubGroup`: the hub-level group when a hello arrives.
    init(allowed: Bool = TodosV1.released, defaults: UserDefaults = .standard, launchGroup: String? = nil,
         hubGroup: @escaping @MainActor () -> String = { AppModel.shared.hub?.group ?? AppModel.shared.hubGroup }) {
        self.allowed = allowed
        self.hubGroup = hubGroup
        let group = launchGroup ?? defaults.string(forKey: AppModel.hubGroupKey) ?? ""
        isOn = allowed && TodoFeatures.remembered(group, in: defaults).view
    }

    /// A hello from `group` listed `features`: V1 comes on when it is the hub-level group's and lists `todo_view`; it
    /// never goes off.
    func saw(_ features: TodoFeatures, group: String) {
        guard allowed, !isOn, features.view, group == hubGroup() else { return }
        isOn = true
    }
}
