package persona.android.ui

import android.app.Application
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.SideEffect
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.LocalLargeWindowInput

@RunWith(AndroidJUnit4::class)
@Config(application = Application::class, sdk = [35])
class AssistantPaneFrameTest {
    @get:Rule val rule = createComposeRule()

    @Test
    fun windowMarkerChangesKeepEveryAssistantLayerMounted() {
        val regular = mutableStateOf(false)
        val showSidebar = mutableStateOf(false)
        val routes = mutableStateListOf("root")
        val identities = mutableMapOf<String, Any>()
        val disposals = mutableMapOf<String, Int>()
        val seenWindowValues = mutableListOf<Boolean>()
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides regular.value) {
                routes.forEach { route ->
                    AssistantPaneFrame(regular.value, showSidebar.value && routes.last() == route,
                        shown = true, sidebar = { Text("Sidebar") }, hinges = emptyList()) {
                        val marker = remember { Any() }
                        val large = LocalLargeWindowInput.current
                        SideEffect {
                            identities[route] = marker
                            if (seenWindowValues.lastOrNull() != large) seenWindowValues += large
                        }
                        DisposableEffect(marker) { onDispose { disposals[route] = disposals.getOrDefault(route, 0) + 1 } }
                    }
                }
            }
        }
        lateinit var root: Any
        rule.runOnIdle {
            root = identities.getValue("root")
            regular.value = true
            showSidebar.value = true
            routes += "inbox"
        }
        lateinit var inbox: Any
        rule.runOnIdle {
            assertSame(root, identities.getValue("root"))
            inbox = identities.getValue("inbox")
            regular.value = false
            showSidebar.value = false
            routes += "mail"
        }
        rule.runOnIdle {
            assertSame(root, identities.getValue("root"))
            assertSame(inbox, identities.getValue("inbox"))
            regular.value = true
            showSidebar.value = false
        }
        rule.runOnIdle {
            assertSame(root, identities.getValue("root"))
            assertSame(inbox, identities.getValue("inbox"))
            assertTrue(seenWindowValues.contains(false))
            assertTrue(seenWindowValues.contains(true))
            assertEquals(emptyMap<String, Int>(), disposals)
        }
    }
}
