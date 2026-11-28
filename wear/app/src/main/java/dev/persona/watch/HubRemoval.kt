package dev.persona.watch

internal interface HubRemovalStore {
    fun groups(): List<SavedHub>
    fun pending(): Set<String>
    fun writePending(ids: Set<String>)
    fun writeGroups(groups: List<SavedHub>)
    fun clearCredential(id: String)
}

/** Record deletion before hiding a row; a failed credential deletion remains discoverable and retryable. */
internal class HubRemoval(private val store: HubRemovalStore) {
    fun remove(id: String) {
        require(store.groups().any { it.id == id } || id in store.pending())
        store.writePending(store.pending() + id)
        finish(id)
    }

    fun recover(): Boolean {
        val pending = store.pending()
        var failure: Exception? = null
        for (id in pending) {
            try { finish(id) }
            catch (problem: Exception) {
                if (failure == null) failure = problem else failure.addSuppressed(problem)
            }
        }
        failure?.let { throw it }
        return pending.isNotEmpty()
    }

    private fun finish(id: String) {
        // Recommit even when a failed preferences commit changed its in-memory view.
        store.writeGroups(store.groups().filterNot { it.id == id })
        store.clearCredential(id)
        store.writePending(store.pending() - id)
    }
}
