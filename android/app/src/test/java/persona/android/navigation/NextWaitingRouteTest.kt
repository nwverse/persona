package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import persona.core.contracts.SessionKey
import persona.core.navigation.*
import persona.feature.assistant.NeedsYouLine
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * A card outside any session (`personad ask` in a plain terminal) offered by a chat's "Next: …" line or listed in the
 * Persona home's "Needs you now" opens on Agents with its sheet, not as a blank conversation (#5701).
 */
class NextWaitingRouteTest {
    private val stacks = mapOf<AppTab, MutableList<NavKey>>(
        AppTab.Agents to mutableListOf(AgentsRoute, ChatRoute("m", "other", ConversationKind.Agent)),
        AppTab.Persona to mutableListOf(AssistantRoute),
    )

    @Test fun theNextSessionlessCardOpensOnAgentsWithItsSheet() {
        val route = waitingRoute(SessionKey("m", ""), "deploy")
        assertEquals(RequestRef("m", "deploy"), route.card)
        assertEquals(AppTab.Agents, route.open(stacks))
        assertEquals(listOf<NavKey>(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun aSessionlessLineOfTheHomeOpensOnAgentsWithItsSheet() {
        val line = NeedsYouLine.Agent(SessionKey("m", ""), "deploy", "question", null, "Agent", "Deploy?")
        val route = needsYouRoute(line)
        assertEquals(RequestRef("m", "deploy"), route.card)
        assertEquals(AppTab.Agents, route.open(stacks))
        assertEquals(listOf<NavKey>(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun theNextCardOfAConversationStillOpensItThere() {
        val route = waitingRoute(SessionKey("m", "s"), "r1")
        assertNull(route.card)
        assertEquals(AppTab.Agents, route.tab)
        assertEquals(InteractionRoute(ChatRoute("m", "s", ConversationKind.Agent), "r1"), route.key)
    }
}
