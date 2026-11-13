package persona.android.debug

import android.content.Intent
import androidx.navigation3.runtime.NavKey
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import persona.android.navigation.LaunchAdapter
import persona.core.navigation.*

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35])
class DebugLaunchAdapterClassTest {
    @Test fun manifestAdapterCanBeLoadedAndConstructedReflectively() {
        val type = Class.forName("persona.android.debug.DebugLaunchAdapter")
        assertTrue(LaunchAdapter::class.java.isAssignableFrom(type))
        val adapter = type.getDeclaredConstructor().newInstance() as LaunchAdapter
        assertNotNull(adapter.tools)
    }

    @Test fun inboxAndSettingsDebugEntrancesAreSecondaryPages() {
        for (argument in listOf("debug.openTab", "debug.openPage")) {
            for ((name, page) in listOf("inbox" to InboxRoute, "settings" to SettingsRoute)) {
                val route = DebugLaunchAdapter().read(Intent().putExtra(argument, name))!!.resolve(Router())!!
                val stacks = AppTab.entries.associateWith { mutableListOf<NavKey>(tabRoot(it)) }
                assertEquals(AppTab.Persona, route.restore(stacks))
                assertEquals(listOf(AssistantRoute, page), stacks.getValue(AppTab.Persona))
                assertEquals(listOf(AgentsRoute), stacks.getValue(AppTab.Agents))
            }
        }
    }

    @Test fun settingsDebugPagesRestoreTheirSecondaryParents() {
        for ((name, expected) in listOf(
            "mail" to listOf(SettingsRoute, SettingsPageRoute(SettingsPage.Mail)),
            "phone" to listOf(SettingsRoute, SettingsPageRoute(SettingsPage.PhoneData)),
            "connectors/sent" to listOf(SettingsRoute, SettingsPageRoute(SettingsPage.Connectors), SettingsPageRoute(SettingsPage.Sent)),
            "plugin-ui:example:details" to listOf(SettingsRoute, ExtensionUiRoute("example", "details", "details")),
        )) {
            val route = DebugLaunchAdapter().read(Intent().putExtra("debug.openPage", name))!!.resolve(Router())!!
            val stacks = AppTab.entries.associateWith { mutableListOf<NavKey>(tabRoot(it)) }
            route.restore(stacks)
            assertEquals(listOf(AssistantRoute) + expected, stacks.getValue(AppTab.Persona))
        }
    }
}
