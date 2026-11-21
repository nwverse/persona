package dev.persona.watch

import dev.persona.watch.data.HubConfig
import java.io.File

/** Disk-backed deletion intent is visible even to a provider with stale SharedPreferences. */
internal class HubDeletionFence(private val directory: File) {
    fun contains(id: String): Boolean = file(id).exists()
    fun mark(id: String) {
        val target = file(id)
        target.outputStream().use { it.write(byteArrayOf(1)); it.fd.sync() }
    }
    fun ids(): Set<String> = directory.listFiles().orEmpty().mapNotNull { entry ->
        entry.name.removePrefix("group_").removeSuffix(".removed").takeIf {
            entry.name == "group_$it.removed" && valid(it)
        }
    }.toSet()

    /** Check again after loading so a concurrently written fence cannot authorize that result. */
    fun credential(id: String, load: () -> HubConfig?): HubConfig? {
        if (!valid(id) || contains(id)) return null
        val result = load()
        return result.takeUnless { contains(id) }
    }
    private fun file(id: String): File {
        require(valid(id))
        return File(directory, "group_$id.removed")
    }
    private fun valid(id: String) = id.matches(Regex("[a-f0-9-]{36}"))
}

/** Abort cleanup never needs to publish an issued credential or contact its Hub. */
internal interface HubRegistrationAbortStore {
    fun groups(): List<SavedHub>
    fun pending(): List<PendingHubRegistration>
    fun markRemoved(id: String)
    fun writeGroups(groups: List<SavedHub>)
    fun writePending(pending: List<PendingHubRegistration>)
    fun clearCredential(id: String)
}

internal class HubRegistrationAbort(private val store: HubRegistrationAbortStore) {
    fun abort(owned: PendingHubRegistration) {
        val pending = store.pending().firstOrNull { it.id == owned.id }
        val group = store.groups().firstOrNull { it.id == owned.id }
        // A recorded random ID is required; an origin or display name alone never authorizes cleanup.
        require(owned.id.matches(Regex("[a-f0-9-]{36}")) && owned.inputKey?.matches(Regex("[a-f0-9]{64}")) == true)
        require(pending == null || pending.inputKey == owned.inputKey && HubConfig.sameOrigin(pending.baseUrl, owned.baseUrl))
        require(group?.config == null || HubConfig.sameOrigin(group.config.baseUrl, owned.baseUrl))
        store.markRemoved(owned.id)
        store.writeGroups(store.groups().filterNot { it.id == owned.id })
        store.clearCredential(owned.id)
        store.writePending(store.pending().filterNot { it.id == owned.id })
    }
}
