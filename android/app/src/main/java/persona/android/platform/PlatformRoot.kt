package persona.android.platform

import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.platform.LocalContext
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import persona.android.MainActivity
import persona.android.data.Hub
import persona.android.ui.AppServices
import persona.android.ui.PersonaRoot
import persona.core.navigation.ResolvedRoute
import persona.core.navigation.Router
import persona.feature.settings.OnboardingScreen
import persona.feature.settings.SettingsServices

@EntryPoint
@InstallIn(SingletonComponent::class)
interface PlatformEntryDependencies {
    fun permission(): LocalNetworkPermission
    fun platformPairing(): PlatformPairing
}

@Composable
fun PlatformRoot(
    hub: Hub?,
    openNavigation: MutableState<((Router) -> ResolvedRoute?)?>,
    services: SettingsServices,
    shell: AppServices,
) {
    val application = LocalContext.current.applicationContext
    val dependencies = remember(application) { EntryPointAccessors.fromApplication(application, PlatformEntryDependencies::class.java) }
    val pairing = dependencies.platformPairing()
    var checklist by rememberSaveable { mutableStateOf(pairing.readinessPending()) }
    val openTest = MainActivity.pendingLockTest.value
    LocalNetworkPermissionDialog(dependencies.permission(), services.connection)
    val session by shell.session.collectAsState()
    val updateState = persona.feature.settings.rememberAppUpdateState(services)
    persona.feature.settings.AppUpdateHost(updateState, services.updates,
        enabled = hub != null && session?.demo == null && !checklist && !openTest) {
        PlatformRootContent(hub, openNavigation, services, shell, pairing, checklist, openTest) { checklist = false }
    }
}

@Composable
private fun PlatformRootContent(
    hub: Hub?, openNavigation: MutableState<((Router) -> ResolvedRoute?)?>,
    services: SettingsServices, shell: AppServices, pairing: PlatformPairing,
    checklist: Boolean, openTest: Boolean, clearChecklist: () -> Unit,
) {
    // Automated launches capture pages directly; a pairing they made must not leave the checklist in front.
    if (hub != null && (checklist && !shell.automation || openTest)) {
        OnboardingScreen(services) {
            pairing.completeReadiness()
            clearChecklist()
            MainActivity.pendingLockTest.value = false
        }
    } else PersonaRoot(hub, openNavigation, services, shell, onPaired = {
        // The pairing flow ends with the same checklist; showing it again on arrival would ask twice.
        pairing.completeReadiness()
        clearChecklist()
    })
}
