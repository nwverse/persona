package persona.android.navigation

import android.content.Intent
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.navigation3.runtime.EntryProviderScope
import androidx.navigation3.runtime.NavKey
import persona.core.contracts.api.PairingRequest
import persona.core.navigation.Navigator
import persona.core.navigation.ResolvedRoute
import persona.core.navigation.Router
import persona.android.ui.AppServices
import persona.android.ui.ShellSession

/** Optional launch handling is installed by a build-specific manifest; release builds install none. */
interface LaunchAdapter {
    fun read(intent: Intent): LaunchInput?
    /** Developer pages exist only in builds whose manifest installs an adapter. */
    val tools: DeveloperTools? get() = null
}

class LaunchInput(
    val resolve: (Router) -> ResolvedRoute?,
    val darkTheme: Boolean? = null,
    val automation: Boolean = false,
    val pairing: PairingRequest? = null,
    val prepare: (suspend (AppServices) -> Unit)? = null,
    /** An interface style to switch to before showing anything (captures of every style). */
    val style: persona.core.designsystem.AppStyle? = null,
    /** A pretend fold ("tabletop" or "book") for captures on emulators that report no FoldingFeature. */
    val posture: String? = null,
)

/** Debug-only pages; their routes and code stay out of release builds. */
interface DeveloperTools {
    @Composable fun session(session: ShellSession): ShellSession = session
    /** The developer page offered by Settings, such as the component catalog. */
    val home: NavKey
    val homeTitle: String
    fun EntryProviderScope<NavKey>.register(navigator: Navigator)
}

val LocalDeveloperTools = staticCompositionLocalOf<DeveloperTools?> { null }
