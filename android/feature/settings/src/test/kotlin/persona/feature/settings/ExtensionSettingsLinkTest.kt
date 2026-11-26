package persona.feature.settings

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.ExtensionUiContext
import persona.core.contracts.api.ExtensionUiResult
import persona.core.contracts.api.ExtensionView
import persona.core.contracts.api.ExtensionViewAction
import persona.core.contracts.api.ExtensionViewItem
import persona.core.contracts.api.ExtensionViewSection
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-xhdpi")
class ExtensionSettingsLinkTest {
    @get:Rule val rule = createComposeRule()

    private class SettingsUi(private val url: String) : ExtensionApiStub() {
        override suspend fun renderUi(extension: String, ui: String, context: ExtensionUiContext?, state: String?): ExtensionView =
            ExtensionView(sections = listOf(ExtensionViewSection(items = listOf(
                ExtensionViewItem("button", title = "Open settings", action = ExtensionViewAction("open")),
            ))))

        override suspend fun uiAction(
            extension: String,
            ui: String,
            action: String,
            context: ExtensionUiContext?,
            state: String?,
            values: Map<String, String>?,
        ): ExtensionUiResult = ExtensionUiResult(openUrl = url)
    }

    @Test fun aPageOpensItsOwnSettingsWithThePagesManager() {
        val opened = mutableListOf<Pair<String, String?>>()
        val api = SettingsUi("persona://settings/extensions/example")
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                ExtensionUiPage(api, "example", "page", "Example", {}, manager = "work",
                    onSettings = { id, manager -> opened += id to manager })
            }
        }
        rule.waitUntil(5_000) { runCatching { rule.onNodeWithText("Open settings").assertExists() }.isSuccess }
        rule.onNodeWithText("Open settings").performClick()
        rule.runOnIdle { assertEquals(listOf("example" to "work"), opened) }
    }
}
