package persona.android.navigation

import android.net.Uri
import androidx.navigation3.runtime.NavKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import persona.core.navigation.*

/** A card outside any session (`personad ask` in a plain terminal): its notification opens Agents at that card (#4808). */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class NotificationCardLinkTest {
    private fun stacks() = mapOf<AppTab, MutableList<NavKey>>(
        AppTab.Persona to mutableListOf(AssistantRoute), AppTab.Agents to mutableListOf(AgentsRoute),
    )

    @Test fun theLinkNamesTheCardAndLandsOnTheAgentsList() {
        val link = NotificationRoute.card("machine", "script")
        val parsed = notificationRoute(Uri.parse(link.uri().toString()))!!
        assertEquals(RequestRef("machine", "script"), parsed.card)
        val stacks = stacks()
        stacks.getValue(AppTab.Agents).add(ChatRoute("machine", "other", ConversationKind.Agent))
        val route = Router().open(parsed)!!
        assertEquals(RequestRef("machine", "script"), route.card)
        assertEquals(AppTab.Agents, route.open(stacks))
        assertEquals(listOf<NavKey>(AgentsRoute), stacks.getValue(AppTab.Agents))
    }

    @Test fun withoutAnIdItIsTheTabOnlyLink() {
        assertEquals(NotificationRoute(AppTab.Agents), NotificationRoute.card("machine", null))
        assertEquals(NotificationRoute(AppTab.Agents), NotificationRoute.card("", "script"))
        assertNull(Router().open(notificationRoute(NotificationRoute(AppTab.Agents).uri())!!)!!.card)
        // A machine without a request, or a card under the Persona tab, is not a card link.
        assertNull(notificationRoute(Uri.parse("persona://open?tab=agents&machine=machine")))
        assertNull(NotificationRoute.parse(Uri.parse("persona://open?tab=persona&machine=machine&request=script"))?.card)
    }

    @Test fun aConversationLinkIsUnchanged() {
        val link = NotificationRoute.conversation("machine", "session", false, "second")
        assertEquals(link, notificationRoute(link.uri()))
        assertNull(link.card)
    }
}
