@file:OptIn(androidx.compose.material3.adaptive.ExperimentalMaterial3AdaptiveApi::class)

package persona.android.ui

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
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import persona.android.navigation.canCollapseList
import persona.core.designsystem.PaneDividerState
import persona.core.navigation.*

@RunWith(AndroidJUnit4::class)
@Config(application = Application::class, sdk = [35])
class SecondaryPanePolicyTest {
    @get:Rule val rule = createEmptyComposeRule()

    @Test fun theVisibleListDeterminesCollapseNotTheOriginatingTab() {
        assertTrue(canCollapseList(listOf(AgentsRoute, ChatRoute("example", "session"))))
        assertFalse(canCollapseList(listOf(AgentsRoute, SettingsRoute, SettingsPageRoute(SettingsPage.About))))
        assertFalse(canCollapseList(listOf(AgentsRoute, InboxRoute, InboxItemRoute("example"))))
        assertFalse(canCollapseList(listOf(AssistantRoute, SettingsRoute)))
    }

    @Test fun secondaryListsStayVisibleWithoutLosingTheAgentsCollapseChoice() {
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        lateinit var state: PaneExpansionState
        val splitter = PaneSplitter(AppTab.Agents, null, true).apply { width = 1000f; detailOpen = true }
        ActivityScenario.launch(ComponentActivity::class.java).use { scenario ->
            scenario.onActivity { activity -> activity.setContent { state = rememberPaneExpansionState() } }
            rule.runOnIdle {
                splitter.collapse(state)
                assertEquals(PaneDividerState.Collapsed, splitter.look)
                splitter.collapseEnabled = false
                splitter.rest(state)
                assertFalse(splitter.canCollapse)
                assertEquals(PaneDividerState.Idle, splitter.look)
                assertTrue("The user's Agents choice is preserved, only hidden for a secondary list", splitter.collapsed)
                splitter.collapseEnabled = true
                splitter.rest(state)
                assertEquals(PaneDividerState.Collapsed, splitter.look)
            }
        }
    }
}
