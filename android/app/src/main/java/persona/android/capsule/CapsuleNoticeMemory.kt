package persona.android.capsule

import android.content.Context
import persona.core.contracts.Session
import persona.core.contracts.SessionsEvent
import persona.platform.capsule.CapsuleNoticeKind
import java.security.MessageDigest

internal data class CapsulePendingNotice(val id: String, val targetId: String, val kind: CapsuleNoticeKind, val machineId: String)

/** Stores hashes of observed message versions, never message text or credentials. */
internal class CapsuleNoticeMemory(context: Context? = null, identity: String = "") {
    private val name = "capsule-notices-${hash(identity)}"
    private val preferences = context?.getSharedPreferences(name, Context.MODE_PRIVATE)
    init {
        context?.getSharedPreferences("capsule-notice-scopes", Context.MODE_PRIVATE)?.let { scopes ->
            scopes.edit().putStringSet("names", scopes.getStringSet("names", emptySet()).orEmpty() + name).apply()
        }
    }
    private val machines = preferences?.getStringSet("machines", emptySet()).orEmpty().toMutableSet()
    private val fingerprints = preferences?.all.orEmpty().filterKeys { it.startsWith("version:") }
        .mapKeys { it.key.removePrefix("version:") }.mapValues { it.value as String }.toMutableMap()
    var pending: CapsulePendingNotice? = preferences?.let { stored -> stored.getString("pending.id", null)?.let { id ->
        CapsulePendingNotice(id, requireNotNull(stored.getString("pending.target", null)),
            CapsuleNoticeKind.valueOf(requireNotNull(stored.getString("pending.kind", null))),
            requireNotNull(stored.getString("pending.machine", null)))
    } }
        set(value) {
            field = value
            preferences?.edit()?.apply {
                if (value == null) remove("pending.id").remove("pending.target").remove("pending.kind").remove("pending.machine")
                else putString("pending.id", value.id).putString("pending.target", value.targetId)
                    .putString("pending.kind", value.kind.name).putString("pending.machine", value.machineId)
            }?.apply()
        }

    /**
     * In-memory cheap keys of what was last hashed: a machine's whole list arrives with every push, and hashing each
     * session's last message (and writing every fingerprint) per push kept the resident process busy.
     */
    private val seen = HashMap<String, String>()

    fun observe(event: SessionsEvent): List<Session> {
        val newMachine = machines.add(event.machineId)
        val known = !newMachine
        val written = mutableMapOf<String, String>()
        val changed = event.sessions.filter { current ->
            val id = capsuleId(current.key)
            val text = current.lastText.orEmpty()
            val cheap = "${current.updatedAt.orEmpty()}|${text.length}|${text.hashCode()}"
            if (seen[id] == cheap && fingerprints.containsKey(id)) return@filter false
            seen[id] = cheap
            val fingerprint = hash("${current.updatedAt.orEmpty()}|$text")
            val previous = fingerprints.put(id, fingerprint)
            if (previous != fingerprint) written[id] = fingerprint
            known && previous != fingerprint && text.isNotBlank()
        }
        if (newMachine || written.isNotEmpty()) preferences?.edit()?.apply {
            if (newMachine) putStringSet("machines", machines.toSet())
            written.forEach { (id, fingerprint) -> putString("version:$id", fingerprint) }
        }?.apply()
        return changed
    }

    fun clearPending() { pending = null }

    companion object {
        fun clearPending(context: Context) {
            val scopes = context.getSharedPreferences("capsule-notice-scopes", Context.MODE_PRIVATE)
            scopes.getStringSet("names", emptySet()).orEmpty().forEach { name ->
                context.getSharedPreferences(name, Context.MODE_PRIVATE).edit()
                    .remove("pending.id").remove("pending.target").remove("pending.kind").remove("pending.machine").apply()
            }
        }
        private val Hex = "0123456789abcdef".toCharArray()
        private fun hash(value: String): String {
            val bytes = MessageDigest.getInstance("SHA-256").digest(value.toByteArray())
            val out = CharArray(bytes.size * 2)
            bytes.forEachIndexed { i, b -> out[2 * i] = Hex[(b.toInt() shr 4) and 0xF]; out[2 * i + 1] = Hex[b.toInt() and 0xF] }
            return String(out)
        }
    }
}
