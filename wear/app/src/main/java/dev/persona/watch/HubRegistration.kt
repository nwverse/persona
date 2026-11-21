package dev.persona.watch

import dev.persona.watch.data.HubConfig
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID
import java.security.MessageDigest

/** The journal contains metadata only; issued credentials stay in encrypted CredentialStore files. */
internal data class PendingHubRegistration(val id: String, val name: String, val baseUrl: String, val inputKey: String? = null)

/** Only the digest is journaled; an entered pairing token must never be stored as plaintext metadata. */
internal fun registrationInputKey(input: HubConfig): String {
    val config = input.canonicalized()
    val bytes = JSONArray(listOf(config.baseUrl, config.token, config.fingerprint, config.deviceId)).toString().toByteArray(Charsets.UTF_8)
    return MessageDigest.getInstance("SHA-256").digest(bytes).joinToString("") { "%02x".format(it.toInt() and 255) }
}

internal object PendingHubJournal {
    fun decode(value: String?): List<PendingHubRegistration> {
        if (value == null) return emptyList()
        val rows = if (value.trimStart().startsWith("[")) JSONArray(value) else JSONArray().put(JSONObject(value))
        return (0 until rows.length()).map { index ->
            val row = rows.getJSONObject(index)
            val id = row.getString("id")
            require(id.matches(Regex("[a-f0-9-]{36}")))
            val key = row.optString("inputKey").takeIf { it.matches(Regex("[a-f0-9]{64}")) }
            PendingHubRegistration(id, row.getString("name"), row.getString("baseUrl"), key)
        }
    }
    fun encode(pending: List<PendingHubRegistration>): String = JSONArray().also { rows ->
        pending.forEach { entry -> rows.put(JSONObject().put("id", entry.id).put("name", entry.name).put("baseUrl", entry.baseUrl)
            .also { entry.inputKey?.let { key -> it.put("inputKey", key) } }) }
    }.toString()
}

internal interface HubRegistrationStore {
    fun groups(): List<SavedHub>
    fun primaryUrl(): String?
    fun pending(): List<PendingHubRegistration>
    fun writePending(pending: List<PendingHubRegistration>)
    fun credential(id: String): HubConfig?
    fun saveCredential(id: String, config: HubConfig)
    fun writeGroups(groups: List<SavedHub>)
    fun aborted(id: String): Boolean = false
}

/** Caller serializes operations. Publication may be retried without another token exchange. */
internal class HubRegistration(private val store: HubRegistrationStore,
                               private val newId: () -> String = { UUID.randomUUID().toString() }) {
    fun add(name: String, input: HubConfig, record: (PendingHubRegistration) -> Unit = {}, pair: (HubConfig) -> HubConfig) {
        val config = input.canonicalized()
        val inputKey = registrationInputKey(config)
        require(name.trim().isNotEmpty() && name.length <= 80)
        // Unrelated journals recover independently; a failed old Hub must not block a new Hub.
        // Keep unreadable issued credentials, but discard metadata whose exchange never became durable.
        val pending = store.pending()
        val retained = pending.filter { runCatching { store.credential(it.id) != null }.getOrDefault(true) }
        if (retained != pending) store.writePending(retained)
        val matching = retained.firstOrNull { it.inputKey == inputKey && HubConfig.sameOrigin(it.baseUrl, config.baseUrl) }
        if (matching != null) {
            record(matching)
            val issued = checkNotNull(store.credential(matching.id))
            publish(matching, issued)
            return
        }
        val current = store.groups()
        require(current.size < 3)
        require(current.none { HubConfig.sameOrigin(it.config?.baseUrl, config.baseUrl) } && !HubConfig.sameOrigin(store.primaryUrl(), config.baseUrl))
        val registration = PendingHubRegistration(newId(), name.trim(), config.baseUrl, inputKey)
        require(current.none { it.id == registration.id } && retained.none { it.id == registration.id })
        record(registration)
        check(!store.aborted(registration.id)) { "Registration was aborted" }
        // This must succeed before the network can consume a one-time pairing token.
        store.writePending(store.pending() + registration)
        check(!store.aborted(registration.id)) { "Registration was aborted" }
        val paired = pair(config).canonicalized()
        require(paired.baseUrl == config.baseUrl && paired.fingerprint == config.fingerprint)
        check(!store.aborted(registration.id)) { "Registration was aborted" }
        store.saveCredential(registration.id, paired)
        publish(registration, paired)
    }

    /** Never contacts the Hub. A process restart can finish a saved exchange using only this journal. */
    fun recover(): Boolean {
        var recovered = false
        var failure: Exception? = null
        for (pending in store.pending()) {
            try {
                val issued = store.credential(pending.id)
                if (issued == null) forget(pending.id)
                else { publish(pending, issued); recovered = true }
            } catch (problem: Exception) {
                // One unreadable/invalid Hub cannot prevent another saved registration from recovering.
                if (failure == null) failure = problem else failure.addSuppressed(problem)
            }
        }
        failure?.let { throw it }
        return recovered
    }

    private fun publish(pending: PendingHubRegistration, issued: HubConfig) {
        check(!store.aborted(pending.id)) { "Registration was aborted" }
        val config = issued.validated()
        require(HubConfig.sameOrigin(pending.baseUrl, config.baseUrl))
        val other = store.groups().filterNot { it.id == pending.id }
        require(other.size < 3)
        require(other.none { HubConfig.sameOrigin(it.config?.baseUrl, config.baseUrl) } && !HubConfig.sameOrigin(store.primaryUrl(), config.baseUrl))
        // Always commit, even if a failed SharedPreferences commit already changed its in-memory view.
        check(!store.aborted(pending.id)) { "Registration was aborted" }
        store.writeGroups(other + SavedHub(pending.id, pending.name, config))
        // A failed cleanup is harmless: replay rewrites the same ID, never another credential or row.
        forget(pending.id)
    }

    private fun forget(id: String) = store.writePending(store.pending().filterNot { it.id == id })
}
