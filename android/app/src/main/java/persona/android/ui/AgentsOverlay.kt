package persona.android.ui

import androidx.activity.compose.BackHandler
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.remember
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavEntry
import androidx.navigation3.runtime.NavKey
import androidx.navigation3.scene.Scene
import androidx.navigation3.scene.SceneStrategy
import androidx.navigation3.scene.SceneStrategyScope
import persona.android.navigation.PaneSplits
import persona.core.designsystem.JSidebarColumn
import persona.core.designsystem.LocalPaneListToggle
import persona.core.navigation.AgentsRoute
import persona.core.navigation.Navigator

/** A pick in the overlaid list replaces its root detail, as iOS's split path does; drill-down navigation stays intact. */
internal fun pickAgentsList(navigator: Navigator, stack: MutableList<NavKey>, shown: MutableState<Boolean>, key: NavKey) {
    val replacing = shown.value
    shown.value = false
    if (replacing && stack.lastOrNull() != key) while (stack.size > 1) stack.removeAt(stack.lastIndex)
    navigator.navigate(key)
}

/** A narrow regular window uses the same decorated list and detail entries, with the list over the full-width page. */
@Composable
internal fun rememberAgentsOverlayStrategy(enabled: Boolean, shown: MutableState<Boolean>): SceneStrategy<NavKey> =
    remember(enabled, shown) { AgentsOverlayStrategy(enabled, shown) }

private class AgentsOverlayStrategy(private val enabled: Boolean, private val shown: MutableState<Boolean>) : SceneStrategy<NavKey> {
    private val listKey = NavEntry<NavKey>(AgentsRoute) {}.contentKey
    override fun SceneStrategyScope<NavKey>.calculateScene(entries: List<NavEntry<NavKey>>): Scene<NavKey>? {
        if (!enabled || entries.size < 2 || entries.first().contentKey != listKey) return null
        return AgentsOverlayScene(entries, shown)
    }
}

private class AgentsOverlayScene(
    override val entries: List<NavEntry<NavKey>>,
    private val shown: MutableState<Boolean>,
) : Scene<NavKey> {
    override val key: Any = AgentsRoute
    override val previousEntries: List<NavEntry<NavKey>> = entries.dropLast(1)
    override val content: @Composable () -> Unit = {
        val detail = entries.last()
        // A row pick, including a new detail on the same list, closes only the overlaid column.
        LaunchedEffect(detail.contentKey) { shown.value = false }
        JSidebarColumn(sidebar = { entries.first().Content() }, width = PaneSplits.DefaultLeft.dp,
            pageStart = PaneSplits.DefaultLeft.dp, shown = shown.value, overlay = true,
            onDismiss = { shown.value = false }, insetSidebar = false) {
            CompositionLocalProvider(LocalPaneListToggle provides if (entries.size == 2) ({ shown.value = true }) else null) {
                detail.Content()
            }
        }
        BackHandler(shown.value) { shown.value = false }
    }
}
