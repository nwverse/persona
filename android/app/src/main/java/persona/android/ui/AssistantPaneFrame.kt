package persona.android.ui

import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import persona.android.navigation.PaneHinge
import persona.android.navigation.sidebarSplit
import persona.core.designsystem.JSidebarColumn

/** Keeps every Assistant entry in the same composition slot; only the sidebar parameter changes with the window. */
@Composable
internal fun AssistantPaneFrame(
    regular: Boolean,
    showSidebar: Boolean,
    shown: Boolean,
    sidebar: (@Composable () -> Unit)?,
    hinges: List<PaneHinge>,
    content: @Composable () -> Unit,
) {
    BoxWithConstraints(Modifier.fillMaxSize()) {
        val split = sidebarSplit(maxWidth.value, hinges)
        JSidebarColumn(if (regular && showSidebar) sidebar else null,
            width = split.sidebar.dp, pageStart = split.page.dp, shown = shown, content = content)
    }
}
