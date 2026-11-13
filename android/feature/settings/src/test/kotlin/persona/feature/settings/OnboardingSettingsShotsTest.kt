package persona.feature.settings

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onFirst
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.flowOf
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/**
 * The onboarding pages in Settings (docs/assistant/onboarding.md 4.2 / 4.3): the provider wizard's three steps and the gateway's
 * test, and "Connect a new device", in every style, light and dark. Written to `build/onboarding-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1300dp-xhdpi")
class OnboardingSettingsShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/onboarding-shots").also { it.mkdirs() }

    private val presets = listOf(
        ProviderPreset("builtin/anthropic", ProviderPreset.BUILTIN, "Anthropic", description = "Claude 官方，海外直接开通", baseUrl = "https://api.anthropic.example",
            model = "claude-sonnet", signupUrl = "https://console.anthropic.example/keys"),
        ProviderPreset("builtin/deepseek", ProviderPreset.BUILTIN, "DeepSeek", description = "国内直接开通，按量付费", baseUrl = "https://api.deepseek.example/anthropic",
            model = "deepseek-chat", auth = "bearer", signupUrl = "https://platform.deepseek.example/keys"),
        ProviderPreset("builtin/grok", ProviderPreset.BUILTIN, "Grok", description = "xAI 的 Responses API", kind = "api", api = "responses",
            baseUrl = "https://api.x.example/v1", model = "grok-example", signupUrl = "https://console.x.example/keys"),
        ProviderPreset("builtin/zhipu", ProviderPreset.BUILTIN, "Zhipu GLM", description = "国内直接开通", baseUrl = "https://open.zhipu.example/anthropic",
            model = "glm-4.6", displayName = "智谱 GLM"),
        ProviderPreset("example-gateway/team", "example-gateway", "示例网关 · 团队", baseUrl = "https://gateway.example.com", model = "example-pro"),
    )

    private class Providers(val presets: List<ProviderPreset>) : ProviderSetup {
        override val builtinPresets = flowOf(true)
        override suspend fun presets() = presets
        override suspend fun add(provider: NewProvider) {}
        override suspend fun create(provider: NewProvider) =
            if (provider.preset == "builtin/grok") ProviderAdded("grok", "Grok")
            else ProviderAdded("deepseek", "DeepSeek", assigned = listOf("assistant"), kept = listOf(KeptPurpose("agent.new", "customized")))
        override suspend fun test(probe: ProviderProbe) =
            ProviderTest(ok = true, ms = 412, model = if (probe.preset == "builtin/grok") "grok-example" else "deepseek-chat")
        override suspend fun discover(probe: ProviderProbe) = ProviderDiscovery(listOf(PresetModel("gw-large"), PresetModel("gw-small")))
    }

    private val readiness = object : AssistantReadinessSource {
        override val available = flowOf(true)
        override suspend fun readiness(probe: Boolean) = AssistantReadiness(mode = "setup", ready = false, machine = ReadinessMachine("mbp", "工作电脑"),
            steps = listOf(ReadinessStep("computer", "ok"), ReadinessStep("claude", "problem", code = "claude_missing"), ReadinessStep("model", "unknown")))
        override suspend fun dismiss(dismissed: Boolean) {}
    }

    /** A QR-looking pattern: three finder squares and a deterministic scatter. */
    private val rows = (0 until 25).map { y ->
        (0 until 25).joinToString("") { x ->
            val finder = listOf(0 to 0, 18 to 0, 0 to 18).any { (fx, fy) -> x in fx..fx + 6 && y in fy..fy + 6 &&
                (x == fx || x == fx + 6 || y == fy || y == fy + 6 || (x in fx + 2..fx + 4 && y in fy + 2..fy + 4)) }
            val inFinder = listOf(0 to 0, 18 to 0, 0 to 18).any { (fx, fy) -> x in fx..fx + 7 && y in fy..fy + 7 }
            if (finder || (!inFinder && (x * 7 + y * 13 + x * y) % 3 == 0)) "1" else "0"
        }
    }

    private val devices = object : DeviceSettings {
        override suspend fun devices() = PairedDevices(listOf(PairedDevice("me", "这台手机", "phone", "full")), "me")
        override suspend fun revoke(credentialId: String) {}
        override suspend fun preferences() = DeviceNotificationPreferences()
        override suspend fun savePreferences(preferences: DeviceNotificationPreferences) = preferences
        override suspend fun pairCode() = AppPairCode("jp_example", System.currentTimeMillis() + 598_000, "persona://pair?token=jp_example",
            QrImage(25, rows), "https://hub.example.com/#token=jp_example", QrImage(25, rows.reversed()))
    }

    private enum class Page { Wizard, Gateway, Connect }

    @Test fun light() = shots(false)

    @Config(sdk = [35], qualifiers = "zh-w400dp-h1300dp-night-xhdpi")
    @Test fun dark() = shots(true)

    private fun shots(dark: Boolean) {
        var style by mutableStateOf(AppStyle.Glass)
        var page by mutableStateOf(Page.Wizard)
        var run by mutableIntStateOf(0)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                key(run) {
                    Box(Modifier.testTag("shot").width(400.dp).height(1250.dp).background(PersonaTheme.palette.background)) {
                        when (page) {
                            Page.Wizard, Page.Gateway -> NewProviderPage(Providers(presets), onBack = {}, onSaved = {}, readiness = readiness)
                            Page.Connect -> ConnectDevicePage(devices, onBack = {}, onPaired = {})
                        }
                    }
                }
            }
        }
        val mode = if (dark) "dark" else "light"
        fun capture(name: String) {
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "settings-$name-${style.name.lowercase()}-$mode.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        for (s in AppStyle.entries) {
            style = s
            page = Page.Wizard; run++
            rule.waitForIdle()
            rule.onNodeWithText("智谱 GLM").assertExists()
            capture("provider-pick")
            rule.onNodeWithText("DeepSeek").performClick()
            rule.waitForIdle()
            rule.onAllNodes(hasSetTextAction()).onFirst().performTextInput("sk-example-0123456789")
            rule.mainClock.advanceTimeBy(1_000)
            rule.waitForIdle()
            rule.onNodeWithText("能用：deepseek-chat 回复了（412 毫秒）").assertExists()
            capture("provider-key")
            rule.onNodeWithText("保存").performClick()
            rule.waitForIdle()
            rule.onNodeWithText("DeepSeek 接好了").assertExists()
            capture("provider-done")

            page = Page.Wizard; run++
            rule.waitForIdle()
            rule.onNodeWithText("Grok").performClick()
            rule.waitForIdle()
            rule.onAllNodes(hasSetTextAction()).onFirst().performTextInput("sk-example-0123456789")
            rule.mainClock.advanceTimeBy(1_000)
            rule.waitForIdle()
            rule.onNodeWithText("主对话和新开的 Agent 都用它").assertDoesNotExist()
            rule.onNodeWithText("主对话和 Agent 不能用它", substring = true).assertExists()
            capture("provider-direct-key")
            rule.onNodeWithText("保存").performClick()
            rule.waitForIdle()
            rule.onNodeWithText("Grok 接好了").assertExists()
            rule.onNodeWithText("主对话和 Agent 不能用它", substring = true).assertExists()
            rule.onNodeWithText("要让主对话或 Agent 用它", substring = true).assertDoesNotExist()
            rule.onNodeWithText("Claude Code", substring = true).assertDoesNotExist()
            capture("provider-direct-done")

            page = Page.Gateway; run++
            rule.waitForIdle()
            rule.onNodeWithText("自定义兼容网关").performClick()
            rule.waitForIdle()
            val fields = rule.onAllNodes(hasSetTextAction())
            fields[0].performTextInput("my_gateway")
            fields[1].performTextInput("https://gateway.example.com")
            fields[2].performTextInput("sk-example-key")
            rule.onNodeWithText("获取模型列表").performClick()
            rule.waitForIdle()
            rule.onNodeWithText("测试连接").performClick()
            rule.waitForIdle()
            capture("provider-gateway")

            page = Page.Connect; run++
            rule.waitForIdle()
            rule.onAllNodesWithText("拷贝配对链接").onFirst().assertExists()
            capture("connect-app")
            rule.onNodeWithText("浏览器").performClick()
            rule.waitForIdle()
            capture("connect-web")
        }
    }
}
