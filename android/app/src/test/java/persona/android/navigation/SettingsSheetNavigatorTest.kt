package persona.android.navigation

import androidx.navigation3.runtime.NavKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.navigation.*

class SettingsSheetNavigatorTest {
    @Test fun settingsBackPopsInsideTheSheetThenClosesWithoutChangingEitherHome() {
        val home = mutableListOf<NavKey>(AssistantRoute, InboxRoute)
        val agents = mutableListOf<NavKey>(AgentsRoute)
        var selected = AppTab.Agents
        val outside = AppNavigator(mapOf(AppTab.Persona to home, AppTab.Agents to agents), { selected }, { selected = it })
        val pages = mutableListOf<NavKey>(SettingsRoute)
        var closed = false
        val sheet = SettingsSheetNavigator(pages, { closed = true }, outside)
        sheet.navigate(SettingsPageRoute(SettingsPage.Diagnostics))
        sheet.back()
        assertEquals(listOf(SettingsRoute), pages)
        assertFalse(closed)
        sheet.back()
        assertTrue(closed)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AssistantRoute, InboxRoute), home)
        assertEquals(listOf(AgentsRoute), agents)
    }

    @Test fun sentAndDiagnosticsRetainTheirSettingsParents() {
        val pages = mutableListOf<NavKey>(SettingsRoute)
        val outside = object : Navigator {
            override fun navigate(key: NavKey) = error("Unexpected external route")
            override fun selectTab(tab: AppTab) = error("Unexpected primary tab")
            override fun back() = Unit
        }
        val sheet = SettingsSheetNavigator(pages, { error("Closed before returning to the root") }, outside)
        sheet.navigate(SettingsPageRoute(SettingsPage.Sent))
        assertEquals(listOf(SettingsRoute, SettingsPageRoute(SettingsPage.Connectors), SettingsPageRoute(SettingsPage.Sent)), pages)
        sheet.back()
        sheet.back()
        sheet.navigate(ConnectionSettingsRoute)
        assertEquals(listOf(SettingsRoute, SettingsPageRoute(SettingsPage.Diagnostics)), pages)
    }

    @Test fun assistantConfigurationReturnsToSettingsInsideTheSheet() {
        val pages = mutableListOf<NavKey>(SettingsRoute)
        val outside = object : Navigator {
            override fun navigate(key: NavKey) = error("Assistant settings must stay inside the sheet")
            override fun selectTab(tab: AppTab) = error("Unexpected primary tab")
            override fun back() = Unit
        }
        val sheet = SettingsSheetNavigator(pages, { error("Closed before returning to Settings") }, outside)
        sheet.navigate(AssistantSettingsRoute)
        assertEquals(listOf(SettingsRoute, AssistantSettingsRoute), pages)
        sheet.navigate(ModelPurposeRoute("chat"))
        sheet.back()
        assertEquals(listOf(SettingsRoute, AssistantSettingsRoute), pages)
        sheet.back()
        assertEquals(listOf(SettingsRoute), pages)
    }

    @Test fun openingAConversationLeavesTheSheetAndUsesTheMainNavigator() {
        var selected = AppTab.Persona
        val home = mutableListOf<NavKey>(AssistantRoute)
        val agents = mutableListOf<NavKey>(AgentsRoute)
        val outside = AppNavigator(mapOf(AppTab.Persona to home, AppTab.Agents to agents), { selected }, { selected = it })
        var closed = false
        val sheet = SettingsSheetNavigator(mutableListOf(SettingsRoute), { closed = true }, outside)
        val chat = ChatRoute("example", "session", ConversationKind.Agent)
        sheet.navigate(chat)
        assertTrue(closed)
        assertEquals(AppTab.Agents, selected)
        assertEquals(listOf(AgentsRoute, chat), agents)
        assertEquals(listOf(AssistantRoute), home)
    }
}
