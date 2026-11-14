package persona.core.contracts.api

import persona.core.contracts.SessionKey
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.Serializable

@Serializable
data class HubPreferenceSet(
    val spaces: List<String> = emptyList(),
    val sessions: List<String> = emptyList(),
    val allowed: Set<String> = emptySet(),
)

@Serializable
data class HubPreferencesSnapshot(
    val pins: HubPreferenceSet = HubPreferenceSet(),
    val mutes: HubPreferenceSet = HubPreferenceSet(),
    val seen: Map<String, String> = emptyMap(),
)

interface HubPreferences {
    val preferences: StateFlow<HubPreferencesSnapshot>
    suspend fun refresh()
    suspend fun togglePin(path: String, space: Boolean = false)
    suspend fun toggleMute(path: String, space: Boolean = false)
    suspend fun markSeen(session: SessionKey, version: String)
}

interface HubPreferencesTransport {
    suspend fun pins(): HubPreferenceSet
    suspend fun mutes(): HubPreferenceSet
    suspend fun putPins(value: HubPreferenceSet)
    suspend fun putMutes(value: HubPreferenceSet)
    suspend fun seen(session: SessionKey)
    /** The user's own Mark as read (#7356): the only seen the hub records while Manual mark as read is on. */
    suspend fun markRead(session: SessionKey) = seen(session)
}
