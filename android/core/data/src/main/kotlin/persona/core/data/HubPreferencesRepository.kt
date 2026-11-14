package persona.core.data

import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

/** Hub is authoritative. Serialize whole-document writes; persist only acknowledged state. */
class HubPreferencesRepository(
    private val transport: HubPreferencesTransport,
    scope: CoroutineScope,
    private val snapshots: ListSnapshotStore? = null,
    private val sessionSpace: (String) -> String? = { null },
) : HubPreferences {
    private val current = MutableStateFlow(HubPreferencesSnapshot())
    override val preferences = current.asStateFlow()
    private val lock = Mutex()
    private var verified = false
    private val restored = scope.async {
        snapshots.restore<HubPreferencesSnapshot>("preferences")?.let { current.value = it }
    }
    init { scope.launch { quietly { refresh() } } }

    override suspend fun refresh() = lock.withLock {
        restored.await()
        reconcile()
    }
    /** Publish an acknowledged external write without another network round trip. */
    suspend fun acceptAgentPreferences(pins: HubPreferenceSet? = null, mutes: HubPreferenceSet? = null) = lock.withLock {
        restored.await()
        current.value = current.value.copy(pins = pins ?: current.value.pins, mutes = mutes ?: current.value.mutes)
        snapshots.save("preferences", current.value)
    }
    private suspend fun reconcile() {
        val pins = transport.pins()
        val mutes = transport.mutes()
        current.value = current.value.copy(pins = pins, mutes = mutes)
        verified = true
        snapshots.save("preferences", current.value)
    }
    override suspend fun togglePin(path: String, space: Boolean) = change(
        { it.copy(pins = it.pins.toggle(path, space)) }, { transport.putPins(it.pins) })
    override suspend fun toggleMute(path: String, space: Boolean) = change(
        { state ->
            val old = state.mutes
            val inherited = sessionSpace(path) in old.spaces
            val muted = path in old.sessions || path !in old.allowed && inherited
            val next = when {
                space -> old.toggle(path, true)
                muted -> old.copy(sessions = old.sessions - path,
                    allowed = if (inherited) old.allowed + path else old.allowed - path)
                else -> old.copy(sessions = (old.sessions + path).distinct(), allowed = old.allowed - path)
            }
            state.copy(mutes = next)
        }, { transport.putMutes(it.mutes) })
    override suspend fun markSeen(session: SessionKey, version: String) = change(
        // the Agents list's Mark as read and Mark all as read: the user's own, so it counts in manual mode (#7356)
        { it.copy(seen = it.seen + (session.path to version)) }, { transport.markRead(session) })

    private suspend fun change(update: (HubPreferencesSnapshot) -> HubPreferencesSnapshot,
        send: suspend (HubPreferencesSnapshot) -> Unit) = lock.withLock {
        restored.await()
        // Never overwrite remote preferences with an empty or stale cold-start document.
        if (!verified) reconcile()
        val old = current.value
        val optimistic = update(old)
        current.value = optimistic
        try { send(optimistic) }
        catch (failure: Exception) {
            current.value = old
            verified = false
            throw failure
        }
        snapshots.save("preferences", optimistic)
    }
    private fun HubPreferenceSet.toggle(path: String, space: Boolean): HubPreferenceSet {
        require(path.isNotBlank())
        fun List<String>.toggle() = if (path in this) filterNot { it == path } else this + path
        return if (space) copy(spaces = spaces.toggle()) else copy(sessions = sessions.toggle())
    }
}
