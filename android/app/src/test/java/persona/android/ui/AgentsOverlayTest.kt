package persona.android.ui

import androidx.compose.runtime.mutableStateOf
import androidx.navigation3.runtime.NavKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Test
import persona.core.navigation.*

class AgentsOverlayTest {
    @Test fun nonDetailListActionsPushWithoutDiscardingTheCurrentChat() {
        val chat = ChatRoute("computer", "original", ConversationKind.Agent)
        for (target in listOf<NavKey>(NewAgentRoute(), TodosRoute, TodoDetailRoute("trip"), ConnectionSettingsRoute)) {
            val stack = mutableListOf<NavKey>(AgentsRoute, chat)
            val shown = mutableStateOf(true)
            val navigator = object : Navigator {
                override fun navigate(key: NavKey) { stack.add(key) }
                override fun back() { stack.removeAt(stack.lastIndex) }
                override fun selectTab(tab: AppTab) = Unit
            }
            pickAgentsList(navigator, stack, shown, target)
            assertFalse(shown.value)
            assertEquals(listOf(AgentsRoute, chat, target), stack)
            navigator.back()
            assertEquals(listOf(AgentsRoute, chat), stack)
        }
    }
}
