package persona.core.navigation

import androidx.navigation3.runtime.NavKey
import kotlinx.serialization.json.Json
import org.junit.Assert.*
import org.junit.Test

class SentNavigationTest {
    private val parent = SettingsPageRoute(SettingsPage.Connectors)
    private val sent = SettingsPageRoute(SettingsPage.Sent)
    private fun stacks() = AppTab.entries.associateWith { mutableListOf<NavKey>(tabRoot(it)) }

    @Test fun oldAndNewAppLinksRestoreTheConnectorParentWithoutDuplicates() {
        val stacks = stacks()
        for (page in listOf("sent", "Sent", "connectors/sent")) {
            val route = Router().parse(RouteInput.DeepLink("persona://open?target=settings&page=$page"))!!
            assertEquals(sent, route.key)
            assertEquals(AppTab.Persona, route.restore(stacks))
            assertEquals(listOf(AssistantRoute, SettingsRoute, parent, sent), stacks.getValue(AppTab.Persona))
        }
        stacks.getValue(AppTab.Persona).removeAt(3)
        assertEquals(parent, stacks.getValue(AppTab.Persona).last())
        Router().parse(RouteInput.Internal(sent))!!.restore(stacks)
        assertEquals(listOf(AssistantRoute, SettingsRoute, parent, sent), stacks.getValue(AppTab.Persona))
    }

    @Test fun legacySerializedSentKeyKeepsItsIdentityAndGetsBothParents() {
        val restored = Json.decodeFromString(SettingsPageRoute.serializer(), """{"page":"Sent"}""")
        val stack = mutableListOf<NavKey>(AgentsRoute, restored)
        restoreSecondaryParents(stack)
        restoreSecondaryParents(stack)
        assertEquals(listOf(AgentsRoute, SettingsRoute, parent, sent), stack)
        assertSame(restored, stack.last())
    }

    @Test fun unrelatedDestinationsKeepTheirExistingBackStacks() {
        for (key in listOf(SettingsPageRoute(SettingsPage.Mail), SettingsPageRoute(SettingsPage.Feishu),
            SettingsPageRoute(SettingsPage.PhoneData), SettingsPageRoute(SettingsPage.FeishuBot), ConnectorRoute("github", "work"),
            SettingsPageRoute(SettingsPage.Notifications))) {
            val stack = mutableListOf<NavKey>(AssistantRoute, SettingsRoute, key)
            val before = stack.toList()
            restoreSecondaryParents(stack)
            assertEquals(before, stack)
        }
    }
}
