package persona.core.contracts.api

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow

/** Device-local display settings; the Android host owns the system overlay permission. */
interface FloatingCapsuleSettings {
    /** One of off, active, always. */
    val mode: StateFlow<String>
    val permissionGranted: StateFlow<Boolean>
    fun setMode(mode: String)
    fun refreshPermission()
    fun requestPermission()
    /** The accessibility keeper that lets the system hold and restart the capsule's process (Android host). */
    val keeperEnabled: StateFlow<Boolean> get() = NoKeeper
    fun openKeeperSettings() = Unit
}

private val NoKeeper = MutableStateFlow(false)
