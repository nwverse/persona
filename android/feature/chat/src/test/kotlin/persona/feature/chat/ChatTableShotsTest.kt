package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

/** Markdown table captures at a phone's 360 dp width. PNGs are written to `build/chat-table-shots/`. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w360dp-h2200dp-xhdpi")
class ChatTableShotsTest {
    @get:Rule val rule = createComposeRule()

    private data class Scene(val style: AppStyle, val dark: Boolean, val fontScale: Float) {
        val name = "${style.name.lowercase()}-${if (dark) "dark" else "light"}-f${fontScale.toString().replace('.', '-') }"
    }

    private fun out() = File(System.getProperty("persona.shots") ?: "build/chat-table-shots").also { it.mkdirs() }

    @Test fun incomingTablesInEveryStyleAndAtLargeTextSizes() {
        var scene by mutableStateOf(Scene(AppStyle.Glass, dark = false, fontScale = 1f))
        rule.setContent {
            PersonaTheme(style = scene.style, darkTheme = scene.dark, reduceMotion = true) {
                val density = LocalDensity.current
                CompositionLocalProvider(LocalDensity provides Density(density.density, scene.fontScale)) {
                    Column(Modifier.width(360.dp).background(PersonaTheme.palette.background)) {
                        Box(Modifier.testTag("wide")) {
                            MessageContent(Item("wide", role = "assistant", kind = "text", text = wideTable), user = false)
                        }
                        Box(Modifier.testTag("small")) {
                            MessageContent(Item("small", role = "assistant", kind = "text", text = smallTable), user = false)
                        }
                    }
                }
            }
        }
        rule.waitUntil(10_000) {
            rule.onAllNodes(horizontalScroll).fetchSemanticsNodes().singleOrNull()?.let { node ->
                node.config.getOrNull(SemanticsProperties.HorizontalScrollAxisRange)?.maxValue()?.let { it > 0f }
            } == true
        }
        val scenes = AppStyle.entries.flatMap { style ->
            listOf(false, true).map { dark -> Scene(style, dark, 1f) }
        } + listOf(1.3f, 2f).flatMap { scale ->
            listOf(false, true).map { dark -> Scene(AppStyle.Glass, dark, scale) }
        }
        for (current in scenes) {
            scene = current
            rule.waitForIdle()
            scrollWideToStart()
            capture("wide", "wide-${current.name}-start.png")
            if (current.fontScale == 1f) capture("small", "small-${current.name}.png")
            scrollWideToEnd()
            capture("wide", "wide-${current.name}-end.png")
        }
    }

    @Test fun ownBubbleKeepsItsCurrentPlainTextMarkdownControl() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("own").width(360.dp).background(PersonaTheme.palette.background)) {
                    MergedUserBubble(listOf(Item("own", role = "user", kind = "text", text = wideTable, clientMsgId = "own")))
                }
            }
        }
        for (current in AppStyle.entries) for (isDark in listOf(false, true)) {
            style = current
            dark = isDark
            rule.waitForIdle()
            capture("own", "own-${current.name.lowercase()}-${if (isDark) "dark" else "light"}.png")
        }
    }

    private fun scrollWideToStart() {
        rule.onAllNodes(horizontalScroll).fetchSemanticsNodes().firstOrNull() ?: return
        rule.onAllNodes(horizontalScroll)[0].performSemanticsAction(SemanticsActions.ScrollBy) { it(-100_000f, 0f) }
        rule.waitForIdle()
    }

    private fun scrollWideToEnd() {
        rule.onAllNodes(horizontalScroll)[0].performSemanticsAction(SemanticsActions.ScrollBy) { it(100_000f, 0f) }
        rule.waitForIdle()
    }

    private fun capture(tag: String, name: String) {
        val bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()
        File(out(), name).outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    private companion object {
        val horizontalScroll = SemanticsMatcher.keyIsDefined(SemanticsProperties.HorizontalScrollAxisRange)

        val wideTable = """
            | Area | Finding | 中文说明 | Next step |
            | --- | --- | --- | --- |
            | Network retry | The request waits for a connection timeout before trying the next available route. | 网络请求会先等待连接超时，然后再尝试下一条可用路线，因此整个回复需要更长时间。 | Keep the successful route visible and explain each retry. |
            | Rendering | Explanatory sentences need readable columns instead of squeezing every word into a narrow strip. | 表格中的完整句子需要合适的列宽，中文内容也不能被挤压成每行一个字。 | Scroll only the table when every column cannot fit. |
        """.trimIndent()

        val smallTable = """
            | Name | State |
            | --- | --- |
            | Build | Passed |
            | Tests | Ready |
        """.trimIndent()
    }
}
