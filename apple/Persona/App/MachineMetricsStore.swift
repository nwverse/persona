import Foundation
import Observation

@MainActor
@Observable
final class MachineMetricsStore {
    private(set) var metrics: [MachineRef: MachineMetrics] = [:]
    private(set) var unmonitoredGroups = Set<String>()
    @ObservationIgnored private var snapshots: [Scope: [MachineMetrics]] = [:]
    @ObservationIgnored private var configs: [String: ServerConfig] = [:]
    @ObservationIgnored private var read = 0

    nonisolated private struct Scope: Hashable, Sendable {
        var group: String
        var manager: String?
    }

    func load(groups: [String], apis: [String: APIClient], rosters: [String: ManagerRoster]) async {
        read += 1
        let current = read
        for (group, api) in apis where configs[group] != api.config {
            snapshots = snapshots.filter { $0.key.group != group }
            metrics = metrics.filter { $0.key.group != group }
            unmonitoredGroups.remove(group)
            configs[group] = api.config
        }
        await HubFanOut.run(groups, fetch: { group in
            await self.loadGroup(group, api: apis[group]!, roster: rosters[group], read: current)
        }) { _, _ in }
    }

    private func loadGroup(_ group: String, api: APIClient, roster cached: ManagerRoster?, read current: Int) async {
        var roster = cached
        if roster == nil {
            do { roster = ManagerRoster(try await api.manager(nil).managers()) }
            catch let error as APIError where error.status == 404 && error.code == nil { }
            catch { return }
        }
        guard self.read == current, !Task.isCancelled else { return }
        let scopes = roster.map { roster in
            roster.installed.filter(\.online).map { Scope(group: group, manager: $0.id) }
        } ?? [Scope(group: group, manager: nil)]
        if scopes.isEmpty {
            metrics = metrics.filter { $0.key.group != group }
            return
        }
        var unavailable = Set<Scope>()
        await HubFanOut.run(scopes, fetch: { scope in
            try await api.manager(scope.manager).metricsOverview()
        }) { scope, result in
            guard self.read == current, !Task.isCancelled else { return }
            switch result {
            case .success(let fresh):
                self.snapshots.updateValue(fresh, forKey: scope)
                var merged = self.metrics.filter { $0.key.group != scope.group }
                for target in scopes where target.group == scope.group {
                    for machine in self.snapshots[target] ?? [] {
                        let key = MachineRef(group: scope.group, id: machine.machineID)
                        if merged[key] == nil { merged[key] = machine }
                    }
                }
                self.metrics = merged
                self.unmonitoredGroups.remove(scope.group)
            case .failure(let error):
                if let error = error as? APIError, error.status == 404 {
                    unavailable.insert(scope)
                    if scopes.filter({ $0.group == scope.group }).allSatisfy(unavailable.contains) {
                        self.unmonitoredGroups.insert(scope.group)
                    }
                }
            }
        }
    }
}
