import Foundation
import Observation

@MainActor @Observable final class PersonalAgentListModel {
    private(set) var page: SessionListModel<PersonalAgentSession>?
    /// Removed here and not re-pulled yet: their rows leave at once, and come back if the hub refuses.
    private(set) var going: Set<String> = []
    /// What the folded Removed (N) section lists (GET /agents/hidden); empty on a hub without it.
    private(set) var removed: [PersonalAgentSession] = []
    private var configuration: ServerConfig?
    private var group: String?

    func connect(_ api: APIClient) {
        guard configuration != api.config || group != api.group else { return }
        page?.cancel()
        configuration = api.config
        group = api.group
        removed = []
        page = SessionListModel(id: { $0.id }, removalID: { $0 },
            fetch: { try await api.personalAgentListPage($0, cursor: $1, since: $2) },
            counts: { try await api.sessionListCounts($0, personal: true) })
        page?.changed = { PersonalAgentIndex.update($0) }
    }

    func loadRemoved(_ api: APIClient) async {
        removed = (try? await api.hiddenPersonalAgents()) ?? []
    }

    /// Remove from List: the row goes at once and stays gone while the list is pulled again (the returned task); a
    /// refusal puts it back and is thrown for the caller to say. Returns once the hub took it, so Undo shows at once.
    @discardableResult
    func remove(_ s: PersonalAgentSession, api: APIClient) async throws -> Task<Void, Never> {
        going.insert(s.id)
        do {
            try await api.hidePersonalAgent(s.id)
        } catch {
            going.remove(s.id)
            throw error
        }
        return Task {
            await pull(api)
            going.remove(s.id)
        }
    }

    /// Restore (and the toast's Undo): back in the list.
    func restore(_ s: PersonalAgentSession, api: APIClient) async throws {
        removed.removeAll { $0.id == s.id }
        do {
            try await api.unhidePersonalAgent(s.id)
        } catch {
            await loadRemoved(api)
            throw error
        }
        await pull(api)
    }

    private func pull(_ api: APIClient) async {
        if let page { if page.paged { await page.synchronize() } else { await page.reload() } }
        await loadRemoved(api)
    }
}
