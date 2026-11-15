package persona.core.contracts.api

import persona.core.contracts.SessionKey
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.map
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

@Serializable
data class HubPreferenceSet(
    val spaces: List<String> = emptyList(),
    val sessions: List<String> = emptyList(),
    val allowed: Set<String> = emptySet(),
)

/** The hub's answer to `GET/PUT /api/v1/read-settings` (Manual mark as read, #7356). */
@Serializable
data class ReadSettings(val manual: Boolean = false, @SerialName("can_write") val canWrite: Boolean = false)

/**
 * Manual mark as read as this phone knows it (#7356): off, opening an Agent marks it seen as always; on, only the user's
 * Mark as read does. Kept with the snapshot, so a relaunch starts from the last answer. [supported] is false on a hub
 * from before the switch (404): off there, and nothing to turn on.
 */
@Serializable
data class ReadSettingsState(
    val manual: Boolean = false,
    val canWrite: Boolean = false,
    val supported: Boolean = true,
    /** The hub answered at least once. */
    val known: Boolean = false,
)

@Serializable
data class HubPreferencesSnapshot(
    val pins: HubPreferenceSet = HubPreferenceSet(),
    val mutes: HubPreferenceSet = HubPreferenceSet(),
    val seen: Map<String, String> = emptyMap(),
    val read: ReadSettingsState = ReadSettingsState(),
)

interface HubPreferences {
    val preferences: StateFlow<HubPreferencesSnapshot>
    suspend fun refresh()
    suspend fun togglePin(path: String, space: Boolean = false)
    suspend fun toggleMute(path: String, space: Boolean = false)
    /** The user's own Mark as read (a row's menu, Mark all as read, the capsule's buttons): counts in manual mode (#7356). */
    suspend fun markSeen(session: SessionKey, version: String)
    /**
     * The phone saw it without the user saying so (the capsule acknowledging the reply it showed). Dropped while Manual mark
     * as read is on (#7356): looking is not reading. Off, the same as [markSeen]. False when it was dropped.
     */
    suspend fun markLooked(session: SessionKey, version: String): Boolean {
        markSeen(session, version)
        return true
    }
    /** Manual mark as read on the hub Settings shows (the selected one where several are paired). */
    val readSettings: Flow<ReadSettingsState> get() = preferences.map { it.read }
    /** Turns Manual mark as read on or off on that hub; put back (and thrown) when the hub refuses. */
    suspend fun setManualRead(on: Boolean) = Unit
}

interface HubPreferencesTransport {
    suspend fun pins(): HubPreferenceSet
    suspend fun mutes(): HubPreferenceSet
    suspend fun putPins(value: HubPreferenceSet)
    suspend fun putMutes(value: HubPreferenceSet)
    suspend fun seen(session: SessionKey)
    /** The user's own Mark as read (#7356): the only seen the hub records while Manual mark as read is on. */
    suspend fun markRead(session: SessionKey) = seen(session)
    /** The switch; null from a hub from before it (404). */
    suspend fun readSettings(): ReadSettings? = null
    suspend fun putReadSettings(manual: Boolean): ReadSettings = throw UnsupportedOperationException("read settings")
}
