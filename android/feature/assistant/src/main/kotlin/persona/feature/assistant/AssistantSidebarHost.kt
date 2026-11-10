package persona.feature.assistant

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf

/** Navigation requests consumed when the Assistant home is shown. */
enum class AssistantSidebarAction { Home, NewConversation, MainConversation, ScheduledTasks, SessionInfo, Launcher }

/** The shell owns one history sidebar, independent of the Assistant page's navigation lifetime. */
class AssistantSidebarHost {
    val opened = mutableStateOf(false)
    var personaState by mutableStateOf(persona.core.designsystem.PersonaState.Idle)
    var active by mutableStateOf<persona.core.contracts.SessionKey?>(null)
    var action by mutableStateOf<AssistantSidebarAction?>(null)
    var retired by mutableStateOf(emptySet<persona.core.contracts.SessionKey>())
}

val LocalAssistantSidebarHost = staticCompositionLocalOf<AssistantSidebarHost?> { null }
