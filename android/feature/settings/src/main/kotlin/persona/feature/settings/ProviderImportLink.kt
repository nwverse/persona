package persona.feature.settings

import androidx.compose.runtime.mutableStateOf
import androidx.navigation3.runtime.NavKey
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute

/** The provider import link opened from outside the app (#5428), held until its providers page takes it. */
object ProviderImportRequests {
    /** Kept across a cold start and pairing, never logged or shown. */
    val pending = mutableStateOf<String?>(null)

    /** Takes the link exactly once. */
    fun take(): String? = pending.value.also { pending.value = null }

    /** A provider service that is not ready yet must not consume the only pending link. */
    fun takeIfReady(ready: Boolean): String? = if (ready) take() else null

    /** Pairing keeps the external providers destination so the paired shell can consume it. */
    fun retainProviderNavigationDuringPairing(route: NavKey): Boolean =
        route == SettingsPageRoute(SettingsPage.Providers) && pending.value != null

    /** Explicit cancellation and destination ownership end the link's lifetime. */
    fun cancel() { pending.value = null }
}
