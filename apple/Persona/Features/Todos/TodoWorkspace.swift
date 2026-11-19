import Foundation
import Observation
import PersonaWire
#if canImport(UIKit)
import UIKit
#endif

/// Where the owner is in To-dos (#10465; docs/design/proposals/todos-redesign.md 3.3, "Places have identity"): the
/// place, the to-do selected, and where each place's list was scrolled to. One for the app, above `RootView`, so a fold
/// or an unfold that builds the pages again (`RootView`'s trees) finds it as it was.
///
/// - Written only while the scene is active: iOS snapshots the app on its way out in other widths, and the pages it
///   builds for them must not move the owner's place.
/// - A tree on its way out (a fold or an unfold built another) leaves it to the shown one: the page passes whether its
///   tree is the one shown (`RootView`'s `ShownTree`).
///
/// The places themselves arrive later in #10465 (All only until then); the selection with the to-do's own page.
@MainActor @Observable
final class TodoWorkspace {
    static let shared = TodoWorkspace()

    /// The place shown: All To-dos, Today, Upcoming or a project (`TodoPlace.id`).
    private(set) var place = TodoPlaceKind.allValue
    /// The to-do selected, by its list key; none.
    private(set) var selection: String?
    /// The row at the top of each place's list when it was last left, by place.
    @ObservationIgnored private var scroll: [String: String] = [:]
    @ObservationIgnored private let active: @MainActor () -> Bool

    init(active: @escaping @MainActor () -> Bool = TodoWorkspace.sceneActive) {
        self.active = active
    }

    /// The row the list of `place` was left at.
    func scrolled(in place: String) -> String? { scroll[place] }

    /// The list of `place` was left with `key` at its top (nil: at its start).
    func leave(_ place: String, at key: String?, shown: Bool) {
        guard shown, active() else { return }
        scroll[place] = key
    }

    func show(_ place: String, shown: Bool) {
        guard shown, active(), place != self.place else { return }
        self.place = place
    }

    func select(_ key: String?, shown: Bool) {
        guard shown, active(), key != selection else { return }
        selection = key
    }

    static func sceneActive() -> Bool {
        #if canImport(UIKit)
        UIApplication.shared.applicationState == .active
        #else
        true
        #endif
    }
}
