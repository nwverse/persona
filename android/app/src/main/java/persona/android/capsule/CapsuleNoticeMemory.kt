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

    fun observe(event: SessionsEvent): List<Session> {
        val known = !machines.add(event.machineId)
        val changed = event.sessions.filter { current ->
            val fingerprint = hash("${current.updatedAt.orEmpty()}|${current.lastText.orEmpty()}")
            val previous = fingerprints.put(capsuleId(current.key), fingerprint)
            known && previous != fingerprint && !current.lastText.isNullOrBlank()
        }
        preferences?.edit()?.apply {
            putStringSet("machines", machines.toSet())
            event.sessions.forEach { putString("version:${capsuleId(it.key)}", fingerprints.getValue(capsuleId(it.key))) }
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
        private fun hash(value: String) = MessageDigest.getInstance("SHA-256").digest(value.toByteArray())
            .joinToString("") { "%02x".format(it) }
    }
}
