package persona.feature.settings

import androidx.compose.runtime.mutableStateOf

/** The provider import link opened from outside the app (#5428), held until its providers page takes it. */
object ProviderImportRequests {
    /** Kept across a cold start and pairing, never logged or shown. */
    val pending = mutableStateOf<String?>(null)

    /** Takes the link exactly once. Also called when no provider service can open it, so it cannot replay later. */
    fun take(): String? = pending.value.also { pending.value = null }
}
