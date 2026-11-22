package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onLast
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.navigation.ModelPurposeRoute
import java.io.File

/**
 * "Checklist" with the main conversation's four steps (docs/assistant/onboarding.md 4.5): a new hub with no computer, and one whose
 * Claude Code is not signed in, in every style, light and dark. Written to `build/onboarding-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1500dp-xhdpi")
class ReadinessMainShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/onboarding-shots").also { it.mkdirs() }

    private val sessions = object : SessionsRepository {
        override val machines = MutableStateFlow(listOf(Machine("mbp", name = "工作电脑", online = true)))
        override val sessions = MutableStateFlow(emptyList<Session>())
        override suspend fun refresh() {}
    }

    private val noComputer = AssistantReadiness(mode = "setup", ready = false, next = "computer", steps = listOf(
        ReadinessStep("computer", "todo", code = "no_machine", title = "还没接上电脑",
            detail = "Persona 要在你的一台电脑上干活，现在还没接上电脑。", action = ReadinessAction("add_computer")),
        ReadinessStep("claude", "unknown"), ReadinessStep("model", "unknown"), ReadinessStep("first_message", "unknown")))

    private val notLoggedIn = AssistantReadiness(mode = "setup", ready = false, next = "model",
        machine = ReadinessMachine("mbp", "工作电脑"), launcher = "claude", steps = listOf(
            ReadinessStep("computer", "ok", machine = "mbp"), ReadinessStep("claude", "ok", version = "2.1.308"),
            ReadinessStep("model", "problem", code = "not_logged_in", title = "Claude Code 还没登录",
                detail = "在 工作电脑 上登录一次，或者改用 API Key。", action = ReadinessAction("connect_model"),
                altActions = listOf(ReadinessAction("login_claude"))),
            ReadinessStep("first_message", "todo", code = "never_tried", title = "试一句", detail = "发一句「你好」，看看能不能答上来。",
                action = ReadinessAction("test_assistant"))))

    private class Source(val answer: AssistantReadiness) : AssistantReadinessSource {
        override val available = flowOf(true)
        var probes = 0
        override suspend fun readiness(probe: Boolean): AssistantReadiness {
            if (!probe) return answer
            probes++
            return answer.copy(probe = ReadinessProbe(ok = true, answer = "你好！"))
        }
        override suspend fun dismiss(dismissed: Boolean) {}
    }

    @Test fun light() = shots("light", interact = true)

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1500dp-night-xhdpi")
    @Test fun dark() = shots("dark")

    private fun shots(mode: String, interact: Boolean = false) {
        var style by mutableStateOf(AppStyle.Glass)
        var source by mutableStateOf(Source(noComputer))
        val opened = mutableListOf<NavKey>()
        rule.setContent {
            PersonaTheme(reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(1400.dp).background(PersonaTheme.palette.background)) {
                    key(source, style) {
                        ReadinessScreen(sessions, onDiagnostics = {}, onDone = {}, readiness = source, onNavigate = { opened += it })
                    }
                }
            }
        }
        fun capture(name: String) {
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "readiness-$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        for (s in AppStyle.entries) {
            style = s
            source = Source(noComputer); rule.waitForIdle(); rule.onNodeWithText("还没接上电脑").assertExists()
            capture("no-computer-${s.name.lowercase()}-$mode")
            source = Source(notLoggedIn); rule.waitForIdle(); rule.onNodeWithText("Claude Code 还没登录").assertExists()
            capture("not-logged-in-${s.name.lowercase()}-$mode")
        }
        if (!interact) return
        style = AppStyle.Glass
        val current = Source(notLoggedIn)
        source = current
        rule.waitForIdle()
        rule.onNodeWithText("接上模型").performClick()
        assertEquals(ModelPurposeRoute("assistant"), opened.last())
        // "Try it" runs the hub's one-word check and shows its answer.
        rule.onAllNodesWithText("试一句").onLast().performClick()
        rule.waitForIdle()
        assertEquals(1, current.probes)
        rule.onNodeWithText("试过了，回答：你好！").assertExists()
        capture("probed-glass-light")
        assertTrue(opened.size == 1)
    }
}
