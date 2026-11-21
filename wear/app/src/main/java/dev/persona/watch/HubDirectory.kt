package dev.persona.watch

import android.content.Context
import dev.persona.watch.data.HubConfig
import dev.persona.watch.data.PrimaryPairing
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.concurrent.atomic.AtomicReference

/** Names and IDs are metadata; every credential stays in its own Keystore-encrypted envelope. */
data class SavedHub(val id: String, val name: String, val config: HubConfig?)
class HubDirectory(private val context: Context) {
    private val preferences = context.getSharedPreferences("hub_directory", Context.MODE_PRIVATE)
    private val deletionFence = HubDeletionFence(context.noBackupFilesDir)
    private val registration = HubRegistration(object : HubRegistrationStore {
        override fun groups() = readGroups()
        override fun primaryUrl() = CredentialStore(context).load()?.baseUrl
        override fun pending(): List<PendingHubRegistration> {
            // Read the original single-entry journal as well as the independently recoverable queue.
            val deleting = deletionIds()
            return rawPending().filterNot { it.id in deleting }
        }
        override fun writePending(pending: List<PendingHubRegistration>) {
            writeRegistrations(pending)
        }
        override fun credential(id: String): HubConfig? {
            val store = CredentialStore(context, "group_$id")
            val config = store.load()
            // A temporarily unavailable Keystore must not make us abandon an issued credential.
            check(config != null || !store.exists()) { "Saved connection is unavailable" }
            return config
        }
        override fun saveCredential(id: String, config: HubConfig) = CredentialStore(context, "group_$id").save(config)
        override fun writeGroups(groups: List<SavedHub>) = write(groups)
        override fun aborted(id: String) = deletionFence.contains(id)
    })
    private val removal = HubRemoval(object : HubRemovalStore {
        override fun groups() = readGroups()
        override fun pending() = deletionIds()
        override fun writePending(ids: Set<String>) {
            check(preferences.edit().putString("pending_deletions", JSONArray(ids.toList()).toString()).commit())
        }
        override fun writeGroups(groups: List<SavedHub>) = write(groups)
        override fun clearCredential(id: String) {
            CredentialStore(context, "group_$id").clear()
            writeRegistrations(rawPending().filterNot { it.id == id })
        }
    })
    fun additional(): List<SavedHub> = synchronized(lock) {
        // Read callers still get the surviving rows if cleanup storage is temporarily unavailable.
        runCatching { removal.recover() }
        readGroups().filterNot { deletionFence.contains(it.id) || it.id in deletionIds() }
    }
    // Primary publication must not interleave with an additional registration or its recovery.
    internal fun primaryPairing() = PrimaryPairing(publicationLock = lock) { additional().map { it.config?.baseUrl } }
    private fun readGroups(): List<SavedHub> {
        val rows = runCatching { JSONArray(preferences.getString("groups", "[]")) }.getOrElse { JSONArray() }
        return (0 until rows.length()).mapNotNull { index ->
            val row = rows.optJSONObject(index) ?: return@mapNotNull null
            val id = row.optString("id")
            if (!id.matches(Regex("[a-f0-9-]{36}"))) return@mapNotNull null
            SavedHub(id, row.optString("name").take(80), CredentialStore(context, "group_$id").load())
        }.take(3)
    }
    fun add(name: String, config: HubConfig, pair: (HubConfig) -> HubConfig) = synchronized(lock) {
        registration.add(name, config, record = { observer.get()?.invoke(it) }, pair = pair)
        WidgetUpdates.request(context)
    }
    fun recoverPending(): Boolean = synchronized(lock) {
        val removed = runCatching { removal.recover() }
        val registered = runCatching { registration.recover() }
        removed.exceptionOrNull()?.let { throw it }
        registered.exceptionOrNull()?.let { throw it }
        if (removed.getOrDefault(false) || registered.getOrDefault(false)) WidgetUpdates.request(context)
        registered.getOrDefault(false)
    }
    fun remove(id: String) = synchronized(lock) {
        require(id.matches(Regex("[a-f0-9-]{36}")))
        require(readGroups().any { it.id == id } || id in deletionIds())
        // A disk fence precedes both the preference write and any fallible credential deletion.
        deletionFence.mark(id)
        removal.remove(id)
        WidgetUpdates.request(context)
    }
    private fun write(groups: List<SavedHub>) {
        val rows = JSONArray()
        groups.forEach { rows.put(JSONObject().put("id", it.id).put("name", it.name)) }
        check(preferences.edit().putString("groups", rows.toString()).commit())
    }
    /** Providers reread this disk fence rather than trusting a process-local directory cache. */
    internal fun activeCredential(id: String): HubConfig? {
        if (id in journalDeletionIds()) { deletionFence.mark(id); return null }
        return deletionFence.credential(id) { CredentialStore(context, "group_$id").load() }
    }

    internal fun registrationIds(): Set<String> = synchronized(lock) {
        readGroups().map { it.id }.toSet() + rawPending().map { it.id } + deletionFence.ids() +
            context.noBackupFilesDir.listFiles().orEmpty().mapNotNull {
                Regex("group_([a-f0-9-]{36})\\.enc").matchEntire(it.name)?.groupValues?.get(1)
            }
    }
    internal fun hasPendingRegistrations(): Boolean = synchronized(lock) { rawPending().isNotEmpty() }

    /** Debug instrumentation records the assigned ID before the journal/network operation starts. */
    internal fun observeRegistrations(record: (PendingHubRegistration) -> Unit): AutoCloseable {
        check(BuildConfig.DEBUG && observer.compareAndSet(null, record))
        return AutoCloseable { observer.compareAndSet(record, null) }
    }
    internal fun fenceRegistration(owned: PendingHubRegistration) {
        check(BuildConfig.DEBUG)
        deletionFence.mark(owned.id)
    }
    internal fun abortRegistration(owned: PendingHubRegistration) = synchronized(lock) {
        check(BuildConfig.DEBUG)
        HubRegistrationAbort(object : HubRegistrationAbortStore {
            override fun groups() = readGroups()
            override fun pending() = rawPending()
            override fun markRemoved(id: String) = deletionFence.mark(id)
            override fun writeGroups(groups: List<SavedHub>) = write(groups)
            override fun writePending(pending: List<PendingHubRegistration>) = writeRegistrations(pending)
            override fun clearCredential(id: String) = CredentialStore(context, "group_$id").clear()
        }).abort(owned)
        check(readGroups().none { it.id == owned.id } && rawPending().none { it.id == owned.id } &&
            !File(context.noBackupFilesDir, "group_${owned.id}.enc").exists()) { "Registration cleanup incomplete" }
        WidgetUpdates.request(context)
    }
    private fun rawPending() = PendingHubJournal.decode(preferences.getString("pending", null))
    private fun writeRegistrations(pending: List<PendingHubRegistration>) {
        val editor = preferences.edit()
        if (pending.isEmpty()) editor.remove("pending") else editor.putString("pending", PendingHubJournal.encode(pending))
        check(editor.commit()) { "Could not save pending connection" }
    }
    private fun journalDeletionIds(): Set<String> {
        val rows = JSONArray(preferences.getString("pending_deletions", "[]"))
        return (0 until rows.length()).map { rows.getString(it).also { id ->
            require(id.matches(Regex("[a-f0-9-]{36}")))
        } }.toSet()
    }
    private fun deletionIds(): Set<String> {
        val known = readGroups().map { it.id }.toSet() + rawPending().map { it.id }
        return journalDeletionIds() + deletionFence.ids().filter { it in known || CredentialStore(context, "group_$it").exists() }
    }
    companion object {
        private val lock = Any()
        private val observer = AtomicReference<((PendingHubRegistration) -> Unit)?>(null)
    }
}
