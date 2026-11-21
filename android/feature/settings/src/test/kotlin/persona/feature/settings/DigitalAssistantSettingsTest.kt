package persona.feature.settings

import android.content.ActivityNotFoundException
import android.content.Intent
import android.provider.Settings
import androidx.activity.ComponentActivity
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import persona.core.contracts.api.ConnectionState
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h2200dp-xhdpi")
class DigitalAssistantSettingsTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test
    fun candidateIntentsPrioritizeVoiceInputSettingsAndFallbackCleanly() {
        val candidates = DigitalAssistantSettings.candidateIntents("com.example.persona")
        assertEquals(Settings.ACTION_VOICE_INPUT_SETTINGS, candidates[0].action)
        assertEquals(Settings.ACTION_MANAGE_DEFAULT_APPS_SETTINGS, candidates[1].action)
        assertEquals(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, candidates[2].action)

        val attempted = mutableListOf<String?>()
        val opened = DigitalAssistantSettings.openSystemSettings("com.example.persona") { intent ->
            attempted += intent.action
            if (intent.action == Settings.ACTION_VOICE_INPUT_SETTINGS) {
                throw ActivityNotFoundException("missing voice settings")
            }
        }
        assertTrue(opened)
        assertEquals(
            listOf(Settings.ACTION_VOICE_INPUT_SETTINGS, Settings.ACTION_MANAGE_DEFAULT_APPS_SETTINGS),
            attempted,
        )
    }

    @Test
    fun settingsHomePageShowsDefaultDigitalAssistantRowAndOpensSystemSettings() {
        val services = demoSettingsServices(rule.activity.resources).copy(demo = false)
        var query by mutableStateOf("")
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                SettingsHomePage(
                    services = services,
                    query = query,
                    onQuery = { query = it },
                    extensionsAvailable = false,
                    inboxSources = false,
                    extensionSummary = null,
                    channelsAvailable = false,
                    channelList = null,
                    knowledgeSelectionAvailable = false,
                    connection = ConnectionState.Online,
                    translationValue = null,
                    hubUpdateStatus = null,
                    updateValue = null,
                    selected = null,
                    onOpen = {},
                    onNavigate = null,
                    onClose = null,
                    onDemo = null,
                    onExitDemo = null,
                    footer = {},
                )
            }
        }

        rule.waitUntil(5_000) {
            rule.onAllNodesWithText("Default digital assistant").fetchSemanticsNodes().isNotEmpty()
        }
        rule.onNodeWithText("Not set").assertExists()

        query = "screenshot"
        rule.waitUntil(5_000) {
            rule.onAllNodesWithText("Models").fetchSemanticsNodes().isEmpty()
        }
        rule.onNodeWithText("Default digital assistant").assertExists()

        rule.onNodeWithText("Default digital assistant").performClick()
        val started: Intent? = shadowOf(rule.activity).nextStartedActivity
        assertEquals(Settings.ACTION_VOICE_INPUT_SETTINGS, started?.action)
    }
}
