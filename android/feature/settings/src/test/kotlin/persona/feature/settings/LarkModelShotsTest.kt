package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/** Real Compose editor captures with made-up data, every style in light and dark, for each use of Agents the user can
 *  set apart (Lark /agent, the AI twin). */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1400dp-xhdpi")
class LarkModelShotsTest {
    @get:Rule val rule = createComposeRule()

    @Test fun overrideAndFollowingInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var separate by mutableStateOf(false)
        var run by mutableIntStateOf(0)
        var shown by mutableStateOf("lark.agent" to "Lark /agent")
        val providers = listOf(ModelProvider("launcher.demo", "Demo provider", model = "claude-opus-5-5"))
        fun purpose() = ModelPurpose(shown.first, shown.second, parent = "agent.new",
            override = if (separate) ModelBinding("launcher.demo", "claude-opus-5-5", "high") else ModelBinding(),
            effective = ModelBinding("launcher.demo", "claude-opus-5-5", "high"), revision = if (separate) 1 else 2,
            inheritedFrom = if (separate) null else "Agents", fields = listOf("provider", "model", "effort"))
        val api = object : ModelConfiguration {
            override suspend fun catalog() = ModelCatalog(listOf(purpose()), providers)
            override suspend fun purpose(id: String) = ModelPurposeDetail(purpose(), providers)
            override suspend fun providerModels(provider: String, refresh: Boolean, machine: String?): ProviderModels? = null
            override suspend fun save(purpose: ModelPurpose, draft: ModelConfigurationDraft): ModelPurposeSave = ModelPurposeSave.Conflict
        }
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                key(run) {
                    Box(Modifier.testTag("shot").width(400.dp).height(1200.dp).background(PersonaTheme.palette.background)) {
                        ModelPurposePage(api, shown.first, onBack = {})
                    }
                }
            }
        }
        val out = File(System.getProperty("persona.shots") ?: "build/lark-model-shots").also { it.mkdirs() }
        val uses = listOf("lark.agent" to "Lark /agent", "inbox.agent" to "AI twin runs")
        for (child in uses) for (look in AppStyle.entries) for (night in listOf(false, true)) for (override in listOf(false, true)) {
            shown = child; style = look; dark = night; separate = override; run++
            rule.waitForIdle()
            assertTrue(rule.onAllNodesWithText(if (override) "Separately configured" else "Follow Agents").fetchSemanticsNodes().isNotEmpty())
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out, "${child.first.substringBefore('.')}-${look.name.lowercase()}-${if (night) "dark" else "light"}-${if (override) "separate" else "follow"}.png")
                .outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
