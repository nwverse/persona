package persona.feature.knowledge

import android.graphics.Bitmap
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.dp
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.text.TextLayoutResult
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.api.KnowledgeEntry
import persona.core.contracts.api.KnowledgeUpdate
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h860dp-xhdpi")
class PresentedKnowledgePageTest {
    @get:Rule val rule = createComposeRule()
    private val resources get() = InstrumentationRegistry.getInstrumentation().targetContext.resources
    private val note = KnowledgeEntry("docs", "garden", "Community garden")
    private val agent = Session(machineId = "desk", sessionId = "garden", title = "Garden planning", status = "idle")

    private fun capture(name: String) {
        val bitmap = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
        val out = File(System.getProperty("persona.shots") ?: "build/presented-page-shots").also { it.mkdirs() }
        File(out, "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    private fun run(style: AppStyle, dark: Boolean) {
        var page by mutableStateOf("edit")
        val updates = mutableListOf<KnowledgeUpdate>()
        val sent = mutableListOf<String>()
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.fillMaxSize()) {
                    if (page == "edit") KnowledgeTagEditor(note, KnowledgeTagging(), { updates += it; page = "" }, { page = "" })
                    if (page == "handoff") MeetingHandoffSheet(listOf(agent), listOf(Machine("desk", "Desk", online = true)),
                        transcriptOffered = true, again = null, work = KnowledgeMeetingHandoffWork(),
                        onSend = { target, _, _ -> sent += target.sessionId; page = "" }, onDismiss = { page = "" })
                }
            }
        }
        val close = rule.onNodeWithContentDescription(resources.getString(persona.core.designsystem.R.string.designsystem_close))
        val save = rule.onNodeWithContentDescription(resources.getString(R.string.knowledge_save))
        val send = rule.onNodeWithText(resources.getString(R.string.knowledge_handoff_send))
        val language = resources.configuration.locales[0].language
        val prefix = "${style.name.lowercase()}-${if (dark) "dark" else "light"}" + if (resources.configuration.fontScale > 1f) "-$language-large" else ""
        fun checkBar(action: SemanticsNodeInteraction) {
            val exit = close.assertIsEnabled().fetchSemanticsNode().boundsInRoot
            val confirm = action.assertIsEnabled().fetchSemanticsNode().boundsInRoot
            val touch = with(rule.density) { 48.dp.toPx() }
            assertTrue("Close must have a full TalkBack target: $exit", exit.width >= touch && exit.height >= touch)
            assertTrue("Expected a leading exit and trailing action on the same bar: $exit, $confirm",
                exit.right <= confirm.left && kotlin.math.abs(exit.center.y - confirm.center.y) < touch / 2)
        }
        checkBar(save)
        val layouts = mutableListOf<TextLayoutResult>()
        rule.onNodeWithText(resources.getString(R.string.knowledge_tag_title, note.name))
            .performSemanticsAction(SemanticsActions.GetTextLayoutResult) { it(layouts) }
        assertFalse("The editing title must fit at the current font scale", layouts.single().hasVisualOverflow)
        capture("edit-$prefix")
        rule.onNode(hasSetTextAction()).performTextReplacement("discarded")
        close.performClick()
        rule.runOnIdle { assertEquals("", page); assertTrue(updates.isEmpty()); page = "edit" }
        rule.onNode(hasSetTextAction()).performTextReplacement("garden")
        save.performClick()
        rule.runOnIdle { assertEquals(listOf("garden"), updates.single().tags); page = "handoff" }
        checkBar(send)
        capture("verb-$prefix")
        close.performClick()
        rule.runOnIdle { assertTrue(sent.isEmpty()); page = "handoff" }
        send.performClick()
        rule.runOnIdle { assertEquals(listOf(agent.sessionId), sent); assertEquals("", page) }
    }

    @Test @Config(fontScale = 2f) fun glassLightLargeType() = run(AppStyle.Glass, false)
    @Test @Config(fontScale = 2f) fun glassDarkLargeType() = run(AppStyle.Glass, true)
    @Test @Config(fontScale = 2f) fun briefLightLargeType() = run(AppStyle.Brief, false)
    @Test @Config(fontScale = 2f) fun briefDarkLargeType() = run(AppStyle.Brief, true)
    @Test @Config(fontScale = 2f) fun boardLightLargeType() = run(AppStyle.Board, false)
    @Test @Config(fontScale = 2f) fun boardDarkLargeType() = run(AppStyle.Board, true)

    @Test fun glassLight() = run(AppStyle.Glass, false)
    @Test fun glassDark() = run(AppStyle.Glass, true)
    @Test fun briefLight() = run(AppStyle.Brief, false)
    @Test fun briefDark() = run(AppStyle.Brief, true)
    @Test fun boardLight() = run(AppStyle.Board, false)
    @Test fun boardDark() = run(AppStyle.Board, true)
    @Config(sdk = [35], qualifiers = "zh-w400dp-h860dp-xhdpi", fontScale = 2f)
    @Test fun chineseLargeType() = run(AppStyle.Glass, false)
    @Config(sdk = [35], qualifiers = "ja-w400dp-h860dp-xhdpi", fontScale = 2f)
    @Test fun japaneseLargeType() = run(AppStyle.Board, true)
}
