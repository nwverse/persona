package persona.platform.notify

import android.content.Context
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/** The capsule adds a connection requirement; ordinary paired notification delivery remains independent. */
@Singleton
class ResidentConnectionRequests @Inject constructor(@ApplicationContext context: Context) {
    private val preferences = context.getSharedPreferences("resident-connection", Context.MODE_PRIVATE)
    private val requested = MutableStateFlow(preferences.getBoolean("capsule", false))
    val capsule = requested.asStateFlow()
    private val visible = MutableStateFlow(false)
    val overlayVisible = visible.asStateFlow()
    fun capsule(required: Boolean) {
        requested.value = required
        preferences.edit().putBoolean("capsule", required).apply()
    }
    fun overlayVisible(value: Boolean) { visible.value = value }
}

internal fun mayStartResidentConnection(paired: Boolean, userStopped: Boolean, foreground: Boolean,
    batteryUnrestricted: Boolean, capsule: Boolean, overlayVisible: Boolean): Boolean =
    paired && !userStopped && (foreground || batteryUnrestricted || capsule && overlayVisible)
