package persona.feature.settings

import androidx.activity.ComponentActivity
import androidx.compose.runtime.Composable
import androidx.compose.ui.test.assert
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextClearance
import androidx.compose.ui.test.performTextInput
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.PersonaTheme

/**
 * The pairing pages as a user steps through them (#2481): where Next, the keyboard's action keys, Try again and system Back
 * lead, on the real PairingScreen and SettingsScreen with fake services.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1600dp-xhdpi")
class PairingFlowTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    private class FakePairing(paired: Boolean = false, private val error: Exception? = null) : PairingService {
        override val isPaired = MutableStateFlow(paired)
        var attempts = 0
        override suspend fun pair(request: PairingRequest) { attempts++; error?.let { throw it } }
        override suspend fun unpair() = Unit
    }
    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(emptyList<Machine>())
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() = Unit
    }

    private fun text(id: Int) = rule.activity.getString(id)
    private fun show(content: @Composable () -> Unit) = rule.setContent { PersonaTheme(reduceMotion = true) { content() } }
    private fun tap(id: Int) { rule.onNode(hasText(text(id)) and hasClickAction()).performClick(); rule.waitForIdle() }
    private fun fields() = rule.onAllNodes(hasSetTextAction())
    private fun back() { rule.runOnUiThread { rule.activity.onBackPressedDispatcher.onBackPressed() }; rule.waitForIdle() }
    private fun failed() = rule.waitUntil(5_000) { rule.onAllNodesWithText(text(R.string.settings_pairing_failed_title)).fetchSemanticsNodes().isNotEmpty() }

    /** From the connect choices into the manual form, filled in. */
    private fun fillManual(address: String) {
        tap(R.string.settings_pairing_manual)
        fields()[0].performTextInput(address)
        fields()[1].performTextInput("mock")
    }

    @Test fun aBareAddressGetsItsSchemeAndABadOneStaysInTheForm() {
        show { PairingScreen(FakePairing(), sessions, onReady = {}, onDiagnostics = {}, skipWelcome = true) }
        fillManual("127.0.0.1:18760")
        tap(R.string.settings_pairing_next)
        rule.onAllNodesWithText("http://127.0.0.1:18760").onFirst().assertExists()
        tap(R.string.settings_pairing_edit_input)
        fields()[0].performTextClearance()
        fields()[0].performTextInput("a b:99999")
        tap(R.string.settings_pairing_next)
        // Still the form, with what was typed and the reason under the field; no failure page, no internal code.
        fields()[0].assert(hasText("a b:99999"))
        rule.onNodeWithText(text(R.string.settings_pairing_manual_invalid)).assertExists()
        rule.onNodeWithText(text(R.string.settings_pairing_failed_title)).assertDoesNotExist()
        rule.onAllNodesWithText("InvalidCode", substring = true).assertCountEquals(0)
    }
}
