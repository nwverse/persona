package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import kotlinx.serialization.json.Json
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import persona.core.navigation.*

class TaskHandoffNavigationTest {
    private val stacks = AppTab.entries.associateWith { mutableListOf<NavKey>(tabRoot(it)) }
    private var selected = AppTab.Persona
    private fun navigator() = AppNavigator(stacks, { selected }, { selected = it }, chatTab = {
        if (it.kind == ConversationKind.Assistant) AppTab.Persona else AppTab.Agents
    })
    private fun agent(machine: String, from: RouteSource.Handoff) =
        ChatRoute(machine, "build", ConversationKind.Agent, from)

    @Test fun theEmbeddedConversationRemainsBelowItsAgentAfterMetadataAndActivityRestore() {
        val destination = agent("mac~g00000001", RouteSource.Handoff("mac~g00000001", "main", "Release notes", "editor", embedded = true))
        navigator().navigate(destination)
        assertEquals(listOf(AssistantRoute, destination), stacks.getValue(AppTab.Persona))
        assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
        // The cold position stores only the last chat, while Navigation3 saves the whole stack for Activity recreation.
        val saved = ShellPosition(chats = mapOf(AppTab.Persona to destination))
        val restoredPosition = Json.decodeFromString<ShellPosition>(Json.encodeToString(saved))
        stacks.getValue(AppTab.Persona).apply {
            clear(); add(AssistantRoute); addAll(restoredPosition.chatStack(AppTab.Persona, embeddedAssistant = true))
        }
        val restored = navigator()
        restored.restoreChatTabs()
        assertEquals(destination, stacks.getValue(selected).last())
        restored.back()
        assertEquals(AppTab.Persona, selected)
        assertEquals(listOf(AssistantRoute), stacks.getValue(selected))
    }

    @Test fun theExactSourcedPageStaysAboveTheOrdinaryChatEvenWhenItsAgentWasAlreadyOpen() {
        selected = AppTab.Agents
        val existing = ChatRoute("laptop~g00000001", "build", ConversationKind.Agent)
        val conversation = ChatRoute("mac~g00000001", "review", ConversationKind.Agent)
        val destination = agent(existing.machineId, RouteSource.Handoff(conversation.machineId, conversation.sessionId, "Review", "research", ConversationKind.Agent))
        val stack = stacks.getValue(selected)
        stack.addAll(listOf(existing, conversation))
        val nav = navigator()
        nav.navigate(destination)
        assertEquals(listOf(AgentsRoute, existing, conversation, destination), stack)
        nav.back()
        assertEquals(conversation, stack.last())
        assertEquals(listOf(conversation, destination),
            ShellPosition(chats = mapOf(selected to destination)).chatStack(selected))
    }

    @Test fun theEmbeddedOriginOnlySelectsItsInstalledManagerInItsOwnGroup() {
        val source = RouteSource.Handoff("mac~g00000001", "main", "Review", "design", embedded = true)
        val roster = persona.core.contracts.api.ManagerRoster(listOf(
            persona.core.contracts.api.HubManager("design"), persona.core.contracts.api.HubManager("release")), "release")
        val current = persona.core.contracts.api.CurrentAssistant(roster,
            persona.core.contracts.api.AssistantManagerChoice.Choice("work", "release"), "g00000001")
        assertEquals("design", source.managerToReturn(current))
        assertNull(source.managerToReturn(current.copy(group = "g00000002")))
        assertNull(source.copy(manager = "gone").managerToReturn(current))
        assertNull(source.copy(embedded = false).managerToReturn(current))
        assertNull(source.copy(manager = "release").managerToReturn(current))

        var restored: RouteSource.Handoff? = null
        val nav = AppNavigator(stacks, { selected }, { selected = it }, onReturnToEmbeddedHandoff = { restored = it })
        nav.navigate(agent("mac~g00000001", source))
        nav.back()
        assertEquals(source, restored)
    }

    @Test fun aSecondManagerCanOpenTheSameAgentWithItsOwnConversationTitle() {
        val first = RouteSource.Handoff("mac~g00000001", "review", "Design review", "design", embedded = true)
        val second = first.copy(title = "Release review", manager = "release")
        val nav = navigator()
        nav.navigate(agent(first.machineId, first))
        nav.back()
        val destination = agent(second.machineId, second)
        nav.navigate(destination)
        nav.navigate(destination)
        assertEquals(listOf(AssistantRoute, destination), stacks.getValue(selected))
        nav.back()
        assertEquals(listOf(AssistantRoute), stacks.getValue(selected))
    }

    @Test fun aDirectAgentOpenAfterAHandoffHasNoOtherHubsOrManagersReturnStrip() {
        val first = RouteSource.Handoff("mac~g00000001", "review", "Review", "design")
        val second = RouteSource.Handoff("mac~g00000002", "review", "Review", "release")
        val firstPage = agent("mac~g00000001", first)
        val secondPage = agent("mac~g00000002", second)
        val nav = navigator()
        nav.navigate(firstPage)
        nav.back()
        nav.navigate(secondPage)
        assertEquals(second, (stacks.getValue(selected).last() as ChatRoute).source)
        val direct = secondPage.copy(source = null)
        nav.navigate(direct.copy(kind = null))
        assertEquals(AppTab.Agents, selected)
        assertNull((stacks.getValue(selected).last() as ChatRoute).source)
        assertEquals(listOf(AssistantRoute), stacks.getValue(AppTab.Persona))
    }
}
