import Foundation
import PersonaModuleAPI

/// Whether this hub lets the app make or change recordings (#15214): the hub's `module.recording` flag, read by the one
/// rule every module follows (`HubModules.allows`). Off only when the hub publishes module flags (`modules`) and not this
/// one; an older hub and a hub never heard from are on.
///
/// Off takes away what is new: starting, the all-day switch, and everything that writes through the recording module's
/// routes (rename, minutes, to-dos, the private mark, photos, questions, hand to an Agent). What the user already has
/// stays listed, readable, searchable and theirs to delete (the owner's rule, #13430).
nonisolated enum RecordingAllowance: Hashable, Sendable {
    case on
    case off

    /// The hub's answer as the allowance.
    init(hub: HubModules) {
        self = hub.allows(RecordingModule.descriptor) ? .on : .off
    }

    /// New recordings and changes to stored ones are offered.
    var offersWrites: Bool { self == .on }
}

/// The recording feature as the hub's module flags see it. Recording is not a packaged module on Apple yet (its code is
/// in `apple/Persona/Recording`), so it is not in `Modules.builtIn`; it has the module's id, and so its flag.
enum RecordingModule {
    /// `recording`, gated by `module.recording`. It registers nothing, so it names no slot.
    nonisolated static let descriptor = ModuleDescriptor(id: "recording", hubGated: true, slots: [])

    /// The allowance of the hub the app follows. Observed through the module registry, as the other modules' entries
    /// are: a view that reads it draws again when a hello changes the hub's answer (at most once per hello; a set lookup,
    /// cheap in `body`: docs/apple/performance.md P4, P5).
    @MainActor static var allowance: RecordingAllowance { RecordingAllowance(hub: ModuleState.shared.registry.hub) }
}

extension RecordingAllowance {
    /// The allowance as the Apple Watch is told (`PhoneRecordingAllowance`): the phone only ever says allowed or off.
    nonisolated var forWatch: PhoneRecordingAllowance { self == .on ? .allowed : .off }
}

#if canImport(WatchConnectivity)
extension RecordingModule {
    /// Tells the Apple Watch what the hub the app follows says now (#15214): after each hello of that hub, when
    /// another hub is picked, and once the watch session is up. Before any hello it is the remembered answer, else
    /// allowed. Skipped by `WatchSync` when the watch already has the same word.
    @MainActor static func tellWatch() {
        WatchSync.shared.pushRecording(allowance.forWatch)
        #if os(iOS)
        MinutesRelay.shared.recordingChanged()
        #endif
    }
}
#endif
