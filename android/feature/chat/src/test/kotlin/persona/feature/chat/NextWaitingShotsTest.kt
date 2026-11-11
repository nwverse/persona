package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasTestTag
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.SessionKey
import persona.core.designsystem.*
import java.io.File

/**
 * "N more waiting for you · Next: … ›" under the answer's notice (#4325), in every style, light and dark, and in English and
 * Japanese: one line, a long name cut at the end, the caret kept, a click TalkBack names. PNGs in
 * `build/next-waiting-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi")
class NextWaitingShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/next-waiting-shots").also { it.mkdirs() }

    private val short = NextWaiting(WaitingRequest(SessionKey("mac", "t"), "r1", "照片同步脚本", "保存为哪种格式？"), 3)
    private val long = NextWaiting(WaitingRequest(SessionKey("mac", "u"), "r2",
        "把整个同步层拆成按来源分开的几个模块并补齐单元测试", "要不要顺便把旧的迁移脚本也删掉，还是先留着给回滚用？"), 1)

    private fun shots(locale: String, expected: String) {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var opened = 0
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(vertical = Tokens.Space.l)) {
                    NextWaitingRow(short, { opened++ })
                    NextWaitingRow(long, {})
                }
            }
        }
        rule.onNodeWithText(expected).assertExists()
        // a long name and question stay on one line: the row is as tall as the short one
        val heights = rule.onAllNodesWithTag("chat-next-waiting").fetchSemanticsNodes().map { it.size.height }
        assertEquals(2, heights.size)
        assertEquals(heights[0], heights[1])
        for (s in AppStyle.entries) for (d in listOf(false, true)) {
            style = s; dark = d
            rule.waitForIdle()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "next-waiting-$locale-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        }
        // after the shots: the press would show in them
        // ChatHint puts the tag on its frame and the click on the capsule inside it
        val row = rule.onAllNodes(hasClickAction() and hasAnyAncestor(hasTestTag("chat-next-waiting")))[0]
        val click = row.fetchSemanticsNode().config.getOrNull(SemanticsActions.OnClick)
        assertTrue("TalkBack names the click", !click?.label.isNullOrBlank())
        row.performClick()
        assertEquals(1, opened)
    }

    @Test fun chinese() = shots("zh", "还有 3 件等你 · 下一件：照片同步脚本 · 保存为哪种格式？")

    @Config(qualifiers = "en-w400dp-h800dp-xhdpi")
    @Test fun english() = shots("en", "3 more need you · Next: 照片同步脚本 · 保存为哪种格式？")

    @Config(qualifiers = "ja-w400dp-h800dp-xhdpi")
    @Test fun japanese() = shots("ja", "ほかに3件が対応待ち · 次：照片同步脚本 · 保存为哪种格式？")

    // An agent with no title or folder comes without a name: the row says "Agent" in the app's language (#3889).
    @Config(qualifiers = "ja-w400dp-h800dp-xhdpi")
    @Test fun unnamedAgentInJapanese() {
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                NextWaitingRow(NextWaiting(WaitingRequest(SessionKey("mac", "v"), "r3", "", "保存为哪种格式？"), 1), {})
            }
        }
        rule.onNodeWithText("ほかに1件が対応待ち · 次：エージェント · 保存为哪种格式？").assertExists()
    }
}
