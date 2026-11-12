import Foundation
import Observation

/// The lists behind a running Claude Code session's model menus (#3754): what New Agent offers for the session's
/// launcher on its computer (`GET /api/v1/machines/{mid}/launchers/{name}/models`: Default, the agent's models with
/// their versions, the IDs typed before). The fixed aliases (`PromptItems.claudeModels`) had no Fable and no versions.
/// Read once a chat that can switch shows, kept per computer and launcher for `fresh` (the hub caches the CLI's answer
/// as long), so a menu draws at once; until a list is read, or when it can't be, the menus keep the fixed aliases.
@Observable final class RunningModelLists {
    struct Deps {
        /// The hub of a group of hubs (`MachineRef.group`); nil: not connected.
        var api: (_ group: String) -> APIClient?
        static var live: Deps { .init(api: { AppModel.shared.api($0) }) }
    }

    static let shared = RunningModelLists()
    /// How long a list read is used before it is read again.
    static let fresh: TimeInterval = 600

    /// By `LauncherModelsModel.key`: the rows of that launcher's list.
    private(set) var lists: [String: [AgentModel]] = [:]
    @ObservationIgnored private var asked: [String: Date] = [:]
    @ObservationIgnored private let deps: Deps

    init(deps: Deps = .live) { self.deps = deps }

    /// The list a session's menus offer; nil: not read (yet), or the session can't switch.
    func models(_ key: SessionKey, _ session: Session?) -> [AgentModel]? {
        guard SessionModes.switchesModel(session) else { return nil }
        return lists[LauncherModelsModel.key(key.machineRef, SessionModes.launcher(session))]
    }

    /// Reads the session's list unless it was read within `fresh`. A failure is asked again next time.
    func load(_ key: SessionKey, _ session: Session?) async {
        guard SessionModes.switchesModel(session) else { return }
        let machine = key.machineRef, launcher = SessionModes.launcher(session)
        let k = LauncherModelsModel.key(machine, launcher)
        if let at = asked[k], Date.now.timeIntervalSince(at) < Self.fresh { return }
        guard let api = deps.api(machine.group) else { return }
        asked[k] = .now
        do {
            let answer = try await api.launcherModels(machine: machine.id, launcher: launcher, refresh: false)
            // An empty list (the agent offers none) or a launcher that passes its own --model: the fixed aliases stay.
            lists[k] = answer.models.isEmpty || answer.locked != nil ? nil : answer.models
        } catch {
            asked[k] = nil
        }
    }
}
