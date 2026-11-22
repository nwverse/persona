package persona.feature.settings

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.ExtensionDetail
import persona.core.contracts.api.ExtensionUiContext
import persona.core.contracts.api.ExtensionView
import persona.core.contracts.api.HubExtension
import persona.core.contracts.api.ManagerPins
import persona.core.designsystem.PersonaTheme

/** An extension's pages name their manager on the requests they make, and only on those. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ExtensionPagePinsTest {
    @get:Rule val rule = createComposeRule()

    @Before fun choose() = AssistantManagerChoice.choose(null, null, "assistant")
    @After fun reset() { AssistantManagerChoice.reset(); AssistantManagerChoice.forgetItems() }

    private class Recording : ExtensionApiStub() {
        val seen = mutableListOf<ManagerPins?>()
        override suspend fun extension(id: String): ExtensionDetail {
            seen += currentCoroutineContext()[ManagerPins]
            return ExtensionDetail(HubExtension("voice", "Voice", "voice", "running"))
        }
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView {
            seen += currentCoroutineContext()[ManagerPins]
            return ExtensionView(title = "Voice")
        }
    }

    @Test fun theExtensionsPageNamesItsManagerOnItsOwnRequestsOnly() {
        val api = Recording()
        rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionPage(api, "voice", {}, manager = "m-3") } }
        rule.waitUntil { api.seen.isNotEmpty() }
        rule.runOnIdle {
            assertEquals("m-3", api.seen.first()?.manager(listOf("extensions", "voice", "-", "config")))
            assertEquals("m-3", api.seen.first()?.manager(listOf("plugins", "voice")))
            assertNull(api.seen.first()?.manager(listOf("extensions", "notes")))
            runBlocking { api.extension("voice") }
            assertNull(api.seen.last())
        }
    }

    @Test fun anExtensionsUiPageNamesItsManagerOnItsOwnRequestsOnly() {
        val api = Recording()
        rule.setContent { PersonaTheme(reduceMotion = true) { ExtensionUiPage(api, "voice", "panel", "Voice", {}, manager = "m-3") } }
        rule.waitUntil { api.seen.isNotEmpty() }
        rule.runOnIdle {
            assertEquals("m-3", api.seen.first()?.manager(listOf("extensions", "voice", "-", "ui", "panel")))
            runBlocking { api.renderUi("voice", "panel") }
            assertNull(api.seen.last())
        }
    }
}
