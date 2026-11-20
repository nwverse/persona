package persona.feature.machines

import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalResources
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.navigation3.runtime.EntryProviderScope
import androidx.navigation3.runtime.NavKey
import persona.core.contracts.api.ComputerPairing
import persona.core.contracts.api.MachineManagement
import persona.core.contracts.api.MachineMetricsApi
import persona.core.contracts.api.SessionsRepository
import persona.core.designsystem.LocalAppFeedback
import persona.core.navigation.*
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf

/**
 * Supply the same hub-scoped repositories used by Agents; replacing a pairing replaces this identity. Without
 * [management] (demo mode) the pages only watch: no launchers, "Reconnect", "Redeploy" or engine choice.
 */
fun EntryProviderScope<NavKey>.machinesEntries(
    navigator: Navigator,
    metrics: MachineMetricsApi,
    sessions: SessionsRepository,
    management: MachineManagement? = null,
    /** Whether a launcher's row opens its page (the hub manages extensions, which may draw a section there). */
    launcherPages: Flow<Boolean> = flowOf(false),
    /**
     * The sections extensions draw on a machine's page (`machine_section`, docs/extensions/extensions.md "UI slots"), as the page's
     * list items after the machine's own; built by the settings module, which draws extensions' views.
     */
    extensionSections: (@Composable (String) -> (LazyListScope.() -> Unit))? = null,
    /** "Add a computer" and "Confirm computer" (docs/assistant/onboarding.md 4.2); null in demo mode. */
    computers: ComputerPairing? = null,
) {
    entry<MachinesRoute> { MachinesRouteContent(null, navigator, metrics, sessions, management, launcherPages, null, computers) }
    entry<MachineDetailRoute> { MachinesRouteContent(it.machineId, navigator, metrics, sessions, management, launcherPages, extensionSections, computers) }
    entry<AddComputerRoute> { AddComputerPage(computers, sessions.machines, it.confirm, navigator::back) }
}

@Composable
private fun MachinesRouteContent(
    id: String?, navigator: Navigator, api: MachineMetricsApi, sessions: SessionsRepository, management: MachineManagement?,
    launcherPages: Flow<Boolean>, extensionSections: (@Composable (String) -> (LazyListScope.() -> Unit))?, computers: ComputerPairing?,
) {
    val launchers by launcherPages.collectAsState(false)
    // "Add a computer" opens its page only on a hub with machines_add; an older one keeps the help dialog.
    val canAdd by remember(computers) { computers?.canAdd ?: flowOf(false) }.collectAsState(false)
    key(api, sessions, management) {
        val model = viewModel(key = "machines:${System.identityHashCode(api)}:${id.orEmpty()}") {
            MachinesViewModel(api, sessions, id)
        }
        val actions = management?.let {
            viewModel(key = "machine-actions:${System.identityHashCode(it)}:${id.orEmpty()}") { MachineActionsViewModel(it, sessions) }
        }
        val health = management?.let {
            viewModel(key = "machine-health:${System.identityHashCode(it)}:${id.orEmpty()}") { MachineHealthViewModel(it) }
        }
        val lifecycle = LocalLifecycleOwner.current.lifecycle
        LaunchedEffect(model, lifecycle) { lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) { model.poll() } }
        val feedback = LocalAppFeedback.current
        val resources = LocalResources.current
        LaunchedEffect(actions, feedback, resources) { actions?.notices?.collect { feedback?.notice(it.text(resources)) } }
        LaunchedEffect(health, feedback, resources) { health?.notices?.collect { feedback?.notice(it.text(resources)) } }
        // "Subscription limits" on the overview, once a minute while it is in front.
        if (id == null && health != null && LocalSidebarUsage.current == null) LaunchedEffect(health, lifecycle) { lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) { health.pollUsage() } }
        val extensions = if (id != null && extensionSections != null) extensionSections(id) else null
        MachinesScreen(id, model, navigator, actions, launcherPages = launchers, extensionSections = extensions, health = health, adds = computers != null, canAdd = canAdd)
    }
}
