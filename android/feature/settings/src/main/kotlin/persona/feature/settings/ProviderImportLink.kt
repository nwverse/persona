package persona.feature.settings

import androidx.compose.runtime.mutableStateOf

/** The provider import link opened from outside the app (#5428), held until its providers page takes it. */
object ProviderImportRequests {
    /** Kept across a cold start and pairing; cleared when the wizard is closed, never logged or shown. */
    val pending = mutableStateOf<String?>(null)

    fun clear() { pending.value = null }
}
