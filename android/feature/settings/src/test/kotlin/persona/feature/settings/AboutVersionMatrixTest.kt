package persona.feature.settings

import android.content.ClipboardManager
import android.content.Context
import android.content.res.Configuration
import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.SemanticsNode
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsNodeInteraction
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.AppStyle
import persona.core.designsystem.AppStyleStore
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.JGroupItem
import persona.core.designsystem.JRow
import persona.core.designsystem.LayoutDensity
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.feature.settings.generated.AboutVersionRow
import java.io.File
import java.util.Locale

/**
 * Additional generated/native comparisons and real Compose semantics for the test-only Version row. Widths describe
 * this component fixture, not a tablet window. Native JVM trees, touch bounds and pixels are not TalkBack, an Android
 * accessibility service, a physical device, system navigation or performance evidence. Build and Commit remain native.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w840dp-h1200dp-xhdpi")
class AboutVersionMatrixTest {
    @get:Rule val rule = createComposeRule()

    private val context: Context get() = ApplicationProvider.getApplicationContext()
    private val fullCommit = "0123456789abcdef".repeat(4)
    private val longVersion = "1.42.0-experimental-refract-long-version-0123456789abcdef"

    @Before fun resetDensity() = densityStore().setDensity(LayoutDensity.Default)
    @After fun restoreDefaultDensity() = densityStore().setDensity(LayoutDensity.Default)

    @Test fun widthsDensitiesAndCommitStatesKeepNativeGeneratedPixelsAndSemanticsEqual() {
        var width by mutableStateOf(360.dp)
        var dark by mutableStateOf(false)
        var style by mutableStateOf(AppStyle.Glass)
        var version by mutableStateOf("1.42.0")
        var commit by mutableStateOf<BuildCommit?>(BuildCommit.parse(fullCommit))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.fillMaxSize()) {
                        Surface(modifier = Modifier.width(width), color = PersonaTheme.palette.background) {
                            ComparisonRows(version, commit)
                        }
                    }
            }
        }
        val heights = mutableMapOf<LayoutDensity, MutableMap<Int, Int>>()
        for (selectedWidth in listOf(360, 640)) for (density in LayoutDensity.entries)
            for (largeText in listOf(false, true)) for (present in listOf(true, false))
                for (selectedStyle in AppStyle.entries) for (selectedDark in listOf(false, true)) {
                    rule.runOnIdle {
                        densityStore().setDensity(density)
                        width = selectedWidth.dp
                        style = selectedStyle
                        dark = selectedDark
                        version = if (largeText) longVersion else "1.42.0"
                        commit = if (present) BuildCommit.parse(fullCommit) else null
                    }
                    rule.waitForIdle()
                    val label = "$selectedWidth dp ${density.key} large=$largeText present=$present " +
                        "${selectedStyle.name} dark=$selectedDark"
                    val native = rule.onNodeWithTag("native-row", useUnmergedTree = true)
                    val generated = rule.onNodeWithTag("generated-row", useUnmergedTree = true)
                    native.assertIsDisplayed().assertHasNoClickAction()
                    generated.assertIsDisplayed().assertHasNoClickAction()
                    val nativeSemantics = native.readSemantics()
                    val generatedSemantics = generated.readSemantics()
                    assertEquals("$label Version semantics", nativeSemantics, generatedSemantics)
                    assertEquals("$label full Version value", listOf("Version", version), generatedSemantics.text)
                    assertTrue("$label empty semantics description", generatedSemantics.descriptions.isEmpty())
                    native.assertMinimumTouchTarget(label)
                    generated.assertMinimumTouchTarget(label)
                    for (row in listOf(native, generated)) {
                        assertEquals("$label title lines", 1, row.textLines("Version"))
                        assertEquals("$label value lines", 1, row.textLines(version))
                    }
                    rule.onNodeWithTag("commit-row").let {
                        if (present) it.assertHasClickAction() else it.assertHasNoClickAction()
                    }
                    assertEquals("$label Commit semantics", if (present) listOf("Commit", fullCommit.take(12))
                        else listOf("Commit", "Unknown"), rule.onNodeWithTag("commit-row").readSemantics().text)
                    rule.onNodeWithTag("commit-row").assertIsDisplayed()
                    val nativeImage = capture("native-row")
                    val generatedImage = capture("generated-row")
                    val suffix = "$selectedWidth-${density.key}-large-$largeText-${if (present) "present" else "unknown"}-" +
                        "${selectedStyle.name.lowercase()}-${if (selectedDark) "dark" else "light"}"
                    out().resolve("native-$suffix.png").writePng(nativeImage)
                    out().resolve("generated-$suffix.png").writePng(generatedImage)
                    assertEquals("$label dimensions", nativeImage.width to nativeImage.height,
                        generatedImage.width to generatedImage.height)
                    assertTrue("$label pixels differ", nativeImage.sameAs(generatedImage))
                    rule.onNodeWithTag("native-group").fetchSemanticsNode().boundsInRoot.height.let {
                        heights.getOrPut(density) { mutableMapOf() }[selectedWidth] = it.toInt()
                    }
                }
        for (selectedWidth in listOf(360, 640)) {
            assertTrue("Compact must reduce group height at $selectedWidth dp: $heights",
                heights.getValue(LayoutDensity.Compact).getValue(selectedWidth) <
                    heights.getValue(LayoutDensity.Standard).getValue(selectedWidth))
        }
    }

    @Test fun compactLargeLongValueRetainsFullSemanticsAndEllipsizesOneVisualLine() {
        densityStore().setDensity(LayoutDensity.Compact)
        rule.setContent {
            val base = LocalDensity.current
            CompositionLocalProvider(LocalDensity provides Density(base.density, fontScale = 2f)) {
                PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Brief) {
                    ComparisonRows(longVersion, BuildCommit.parse(fullCommit))
                }
            }
        }
        for (tag in listOf("native-row", "generated-row")) {
            val row = rule.onNodeWithTag(tag, useUnmergedTree = true)
            val semantics = row.readSemantics()
            assertEquals("$tag full Version value", listOf("Version", longVersion), semantics.text)
            assertEquals("$tag title line", 1, row.textLines("Version"))
            val value = row.textLayout(longVersion)
            assertEquals("$tag value line", 1, value.lineCount)
            assertTrue("$tag long value is visually ellipsized", value.isLineEllipsized(0))
            row.assertMinimumTouchTarget(tag)
        }
        assertTrue(capture("native-row").sameAs(capture("generated-row")))
    }

    @Test fun nativeCommitExposesItsCopyLabelAndCopiesTheFullValueWhileUnknownIsInert() {
        val notices = Notices()
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.clearPrimaryClip()
        var commit by mutableStateOf<BuildCommit?>(BuildCommit.parse(fullCommit))
        rule.setContent {
            CompositionLocalProvider(LocalAppFeedback provides notices) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    Column {
                        Box(Modifier.testTag("native")) {
                            JGroupItem(GroupPosition.of(2, 3)) { AboutBuildCommitRow(commit, Modifier.testTag("commit-row")) }
                        }
                    }
                }
            }
        }
        val row = rule.onNodeWithTag("commit-row").assertHasClickAction().assertMinimumTouchTarget("present commit")
        assertEquals("Copy build commit", row.readSemantics().clickLabel)
        assertEquals(listOf("Commit", fullCommit.take(12)), row.readSemantics().text)
        row.performClick()
        rule.waitForIdle()
        assertEquals(fullCommit, clipboard.primaryClip?.getItemAt(0)?.text?.toString())
        assertEquals(listOf("Build commit copied"), notices.shown)
        assertEquals(64, clipboard.primaryClip?.getItemAt(0)?.text?.length)
        clipboard.clearPrimaryClip()
        notices.shown.clear()
        commit = null
        rule.waitForIdle()
        val unknown = rule.onNodeWithTag("commit-row").assertHasNoClickAction().assertMinimumTouchTarget("unknown commit")
        assertNull(unknown.readSemantics().clickLabel)
        assertEquals(listOf("Commit", "Unknown"), unknown.readSemantics().text)
        unknown.performClick()
        rule.waitForIdle()
        assertNull(clipboard.primaryClip)
        assertTrue(notices.shown.isEmpty())
    }

    @Test fun localizedVersionAndUnknownCommitKeepTheirSpokenText() {
        var language by mutableStateOf(language("en"))
        rule.setContent {
            CompositionLocalProvider(LocalContext provides language, LocalResources provides language.resources) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    Column {
                        Box(Modifier.testTag("version")) {
                            JGroupItem(GroupPosition.of(0, 3)) {
                                AboutVersionRow(ImmutableAboutVersionModel(AboutVersionSnapshot(longVersion), language.resources))
                            }
                        }
                        Box(Modifier.testTag("commit")) {
                            JGroupItem(GroupPosition.of(2, 3)) { AboutBuildCommitRow(null) }
                        }
                    }
                }
            }
        }
        for ((tag, expected) in mapOf(
            "en" to listOf("Version", longVersion, "Commit", "Unknown"),
            "zh" to listOf("版本", longVersion, "提交", "未知"),
            "ja" to listOf("バージョン", longVersion, "コミット", "不明"),
        )) {
            language = language(tag)
            rule.waitForIdle()
            val actual = rule.onNodeWithTag("version").readSemantics().text + rule.onNodeWithTag("commit").readSemantics().text
            assertEquals("$tag spoken row text", expected, actual)
        }
    }

    @Composable
    private fun ComparisonRows(version: String, commit: BuildCommit?) {
        Column(Modifier.testTag("native-group").background(PersonaTheme.palette.background).padding(Tokens.Space.m)) {
            Column {
                JGroupItem(GroupPosition.of(0, 3)) {
                    JRow(stringResource(R.string.settings_about_version), value = version,
                        modifier = Modifier.testTag("native-row"))
                }
                JGroupItem(GroupPosition.of(1, 3)) {
                    JRow(stringResource(R.string.settings_about_build),
                        trailing = { Text("42", style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3) })
                }
                JGroupItem(GroupPosition.of(2, 3)) { AboutBuildCommitRow(commit) }
            }
            Column {
                JGroupItem(GroupPosition.of(0, 3)) {
                    AboutVersionRow(ImmutableAboutVersionModel(AboutVersionSnapshot(version), LocalResources.current),
                        modifier = Modifier.testTag("generated-row"))
                }
                JGroupItem(GroupPosition.of(1, 3)) {
                    JRow(stringResource(R.string.settings_about_build),
                        trailing = { Text("42", style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3) })
                }
                JGroupItem(GroupPosition.of(2, 3)) { AboutBuildCommitRow(commit, Modifier.testTag("commit-row")) }
            }
        }
    }

    private fun language(tag: String): Context = context.createConfigurationContext(
        Configuration(context.resources.configuration).apply { setLocale(Locale.forLanguageTag(tag)) })

    private fun densityStore() = AppStyleStore(context)

    private fun capture(tag: String): Bitmap = rule.onNodeWithTag(tag).captureToImage().asAndroidBitmap()

    private fun SemanticsNodeInteraction.readSemantics(): RowSemantics {
        val node = fetchSemanticsNode()
        return RowSemantics(
            text = node.descendants().flatMap { it.config.getOrNull(SemanticsProperties.Text).orEmpty() }.map { it.text },
            descriptions = node.descendants().flatMap { it.config.getOrNull(SemanticsProperties.ContentDescription).orEmpty() },
            clickLabel = node.config.getOrNull(SemanticsActions.OnClick)?.label,
        )
    }

    private fun SemanticsNode.descendants(): List<SemanticsNode> = listOf(this) + children.flatMap { it.descendants() }

    private data class RowSemantics(val text: List<String>, val descriptions: List<String>, val clickLabel: String?)

    private fun SemanticsNodeInteraction.assertMinimumTouchTarget(label: String): SemanticsNodeInteraction {
        val bounds = fetchSemanticsNode().touchBoundsInRoot
        val minimum = with(rule.density) { Tokens.MinTouch.android.toPx() }
        assertTrue("$label touch width ${bounds.width} is below $minimum", bounds.width >= minimum)
        assertTrue("$label touch height ${bounds.height} is below $minimum", bounds.height >= minimum)
        return this
    }

    private fun SemanticsNodeInteraction.textLayout(text: String): TextLayoutResult {
        val node = fetchSemanticsNode().descendants().single {
            it.config.getOrNull(SemanticsProperties.Text).orEmpty().any { value -> value.text == text }
        }
        val layouts = mutableListOf<TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action?.invoke(layouts)
        return layouts.single()
    }

    private fun SemanticsNodeInteraction.textLines(text: String): Int = textLayout(text).lineCount

    private class Notices : AppFeedback {
        val shown = mutableListOf<String>()
        override fun failure(error: Throwable, operation: Boolean) = false
        override fun notice(message: String): Boolean {
            shown += message
            return true
        }
    }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/about-version-matrix-shots").also { it.mkdirs() }

    private fun File.writePng(bitmap: Bitmap) = outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
}
