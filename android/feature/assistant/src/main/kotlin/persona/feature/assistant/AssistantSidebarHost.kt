package persona.feature.assistant

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf

/** The shell owns one history sidebar, shared by Assistant and Agents. */
class AssistantSidebarHost {
    val opened = mutableStateOf(false)
    var content by mutableStateOf<(@Composable () -> Unit)?>(null)
}

val LocalAssistantSidebarHost = staticCompositionLocalOf<AssistantSidebarHost?> { null }
