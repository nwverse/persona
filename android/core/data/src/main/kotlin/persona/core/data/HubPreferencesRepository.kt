package persona.core.data

import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import kotlinx.coroutines.CancellationException
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
        val read = readSwitch()
        current.value = current.value.copy(pins = pins, mutes = mutes, read = read)
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
    /** Manual mark as read (#7356) as the hub says; what this phone knew when the hub cannot be read now. */
    private suspend fun readSwitch(): ReadSettingsState = try {
        transport.readSettings()?.let { ReadSettingsState(it.manual, it.canWrite, supported = true, known = true) }
            ?: ReadSettingsState(supported = false, known = true)
    } catch (cancelled: CancellationException) { throw cancelled }
    catch (_: Exception) { current.value.read }

    // the Agents list's Mark as read and Mark all as read, the capsule's buttons: the user's own, so it counts in manual mode
    override suspend fun markSeen(session: SessionKey, version: String) = change(
        { it.copy(seen = it.seen + (session.path to version)) }, { transport.markRead(session) })

    override suspend fun markLooked(session: SessionKey, version: String): Boolean {
        restored.await()
        // The switch as the hub says now (another device may have flipped it; this phone takes no read_settings push).
        // A look is rare (the capsule's shown reply), so the read is cheap; when it fails, the last answer decides.
        val read = readSwitch()
        lock.withLock { current.value = current.value.copy(read = read) }
        // Manual mark as read: a look is neither sent nor shown as read (the hub would ignore it anyway)
        if (read.manual) return false
        change({ it.copy(seen = it.seen + (session.path to version)) }, { transport.seen(session) })
        return true
    }

    override suspend fun setManualRead(on: Boolean) = lock.withLock {
        restored.await()
        val old = current.value
        current.value = old.copy(read = old.read.copy(manual = on))
        val answer = try { transport.putReadSettings(on) }
        catch (failure: Exception) {
            current.value = old
            throw failure
        }
        current.value = current.value.copy(read = ReadSettingsState(answer.manual, answer.canWrite, supported = true, known = true))
        snapshots.save("preferences", current.value)
    }

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
