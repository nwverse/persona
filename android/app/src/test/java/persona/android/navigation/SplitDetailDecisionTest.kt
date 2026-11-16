@file:OptIn(androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi::class)

package persona.android.navigation

import android.app.Application
import android.content.ComponentName
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.material3.adaptive.layout.PaneExpansionState
import androidx.compose.material3.adaptive.layout.rememberPaneExpansionState
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import persona.android.ui.PaneSplitter
import persona.core.designsystem.DetailLeading
import persona.core.designsystem.resolveDetailLeading
import persona.core.navigation.AgentsRoute
import persona.core.navigation.AppTab
import persona.core.navigation.AssistantRoute
import persona.core.navigation.ChatRoute
import persona.core.navigation.ConversationKind
import persona.core.navigation.MachineSpacesRoute
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute
import persona.core.navigation.SettingsRoute
import persona.core.navigation.SpaceRoute
import persona.core.navigation.TerminalRoute

@RunWith(AndroidJUnit4::class)
@Config(application = Application::class, sdk = [35])
class SplitDetailDecisionTest {
    @get:Rule val rule = createEmptyComposeRule()

    private val chat = ChatRoute("mac", "s1", ConversationKind.Agent)
    private val secondChat = ChatRoute("mac", "s2", ConversationKind.Agent)
    private val space = SpaceRoute("space-1", "Workspace")
    private val terminal = TerminalRoute("mac", "s1")
    private val onBack: () -> Unit = {}
    private val onShowList: () -> Unit = {}

    @Test fun firstPageWithListBesideHidesBack() {
        for (stack in listOf(listOf(AgentsRoute, chat), listOf(AgentsRoute, space), listOf(AgentsRoute, MachineSpacesRoute("mac")))) {
            val split = agentsSplitDetail(AppTab.Agents, stack, twoPanes = true, listHidden = false, onShowList = onShowList)
            assertNotNull(stack.toString(), split)
            assertEquals(DetailLeading.Hidden, resolveDetailLeading(split, onBack))
        }
    }

    @Test fun firstPageWithCollapsedListShowsSidebarActionAndExpandingUpdatesKeptChoice() {
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        lateinit var state: PaneExpansionState
        val memory = ColumnMemory(initialSidebarShown = true, initialAgentsShown = true, persist = true)
        lateinit var splitter: PaneSplitter
        splitter = PaneSplitter(
            tab = AppTab.Agents,
            store = null,
            collapsible = true,
            initialCollapsed = false,
            onCollapsedChange = { collapsed ->
                memory.toggleAgentsBeside(shown = !collapsed, twoPanes = true, columnsOver = false, tabShown = true, foreground = true)
            },
        ).apply { width = 1000f; detailOpen = true }

        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent { state = rememberPaneExpansionState() } }
            rule.runOnIdle {
                splitter.collapse(state)
                assertTrue(splitter.listHidden)
                assertFalse(memory.agentsShown)

                val split = agentsSplitDetail(
                    tab = AppTab.Agents,
                    stack = listOf(AgentsRoute, chat),
                    twoPanes = true,
                    listHidden = splitter.listHidden,
                    onShowList = { splitter.expand(state) },
                )
                val leading = resolveDetailLeading(split, onBack)
                assertTrue(leading is DetailLeading.ShowList)

                (leading as DetailLeading.ShowList).onClick()
                assertFalse(splitter.listHidden)
                assertFalse(splitter.collapsed)
                assertTrue(memory.agentsShown)
            }
        }
    }

    @Test fun pushedPagesAndCompactAndNonAgentsStacksKeepBack() {
        for (stack in listOf(
            listOf(AgentsRoute, space, chat),
            listOf(AgentsRoute, chat, secondChat),
            listOf(AgentsRoute, chat, terminal),
        )) {
            val split = agentsSplitDetail(AppTab.Agents, stack, twoPanes = true, listHidden = true, onShowList = onShowList)
            assertEquals(DetailLeading.Back(onBack), resolveDetailLeading(split, onBack))
        }

        assertNull(agentsSplitDetail(AppTab.Agents, listOf(AgentsRoute, chat), twoPanes = false, listHidden = true, onShowList = onShowList))
        assertNull(agentsSplitDetail(AppTab.Agents, listOf(AgentsRoute, SettingsRoute, SettingsPageRoute(SettingsPage.About)), twoPanes = true, listHidden = false, onShowList = onShowList))
        assertNull(agentsSplitDetail(AppTab.Persona, listOf(AssistantRoute, chat), twoPanes = true, listHidden = false, onShowList = onShowList))
        assertNull(agentsSplitDetail(AppTab.Agents, listOf(AgentsRoute), twoPanes = true, listHidden = false, onShowList = onShowList))
    }

    @Test fun separatingFoldKeepsListVisibleEvenIfCollapsedStateWasSaved() {
        val splitter = PaneSplitter(
            tab = AppTab.Agents,
            store = null,
            collapsible = true,
            initialCollapsed = true,
        ).apply {
            width = 1000f
            detailOpen = true
            hinges = listOf(PaneHinge(left = 499f, right = 501f, separating = true))
        }
        assertTrue(splitter.split.fixed)
        assertFalse(splitter.listHidden)
        val split = agentsSplitDetail(AppTab.Agents, listOf(AgentsRoute, chat), twoPanes = true, listHidden = splitter.listHidden, onShowList = onShowList)
        assertEquals(DetailLeading.Hidden, resolveDetailLeading(split, onBack))
    }
}
