package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.api.HubChannel
import persona.core.designsystem.*
import java.io.File

/**
 * "From WeChat" under a user message that came from a channel (docs/extensions/channels.md "Source markers"), drawn on the JVM with
 * Robolectric's native graphics, in every style, light and dark: one from WeChat (a monogram), one from the Feishu bot
 * (its mark), one the app sent (no line). PNGs in `build/channel-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1200dp-xhdpi")
class ChannelSourceShotsTest {
    @get:Rule val rule = createComposeRule()

    private val channels = listOf(
        HubChannel("app", "Persona", "app", "builtin"),
        HubChannel("wechat", "微信", "im", "plugin"),
        HubChannel("feishu", "飞书机器人", "im", "builtin"),
    )
    private fun message(id: String, text: String, clientMsgId: String) =
        Item(id, role = "user", kind = "text", text = text, ts = "2026-11-04T10:32:00Z", clientMsgId = clientMsgId)

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/channel-shots").also { it.mkdirs() }

    @Test fun drawsTheSourceLineInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalChannels provides channels) {
                    Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                        MergedUserBubble(listOf(message("u1", "明天上午十点提醒我开周会", "wechat-msg-17")))
                        MergedUserBubble(listOf(message("u2", "把刚才那份周报再精简一点", "feishu-om_1")))
                        MergedUserBubble(listOf(message("u3", "这一条是在 App 里发的", "3f2a9c1d-0b4e-4c55-9a1e-6f0d2c7b8a90")))
                    }
                }
            }
        }
        rule.onNodeWithContentDescription("来自 微信").assertExists()
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val background = bitmap.getPixel(1, 1)
            var other = 0
            for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) other++
            assertTrue("drew nothing", other > 100)
            File(out(), "channel-chat-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
    }
}
