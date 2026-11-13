package persona.feature.inbox

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.serialization.json.*
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.InboxEventFields
import persona.core.contracts.InboxFollow
import persona.core.contracts.InboxHandling
import persona.core.designsystem.*
import persona.core.domain.inbox.InboxItem
import persona.core.navigation.*
import java.io.File
import java.lang.reflect.Proxy

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w600dp-h900dp-xhdpi")
class InboxGroupNameRowTest {
    @get:Rule val rule = createComposeRule()
    private val repository = Proxy.newProxyInstance(InboxRepository::class.java.classLoader, arrayOf(InboxRepository::class.java)) { proxy, method, args ->
        when (method.name) {
            "equals" -> proxy === args[0]
            "hashCode" -> System.identityHashCode(proxy)
            "toString" -> "RowTestRepository"
            else -> error("Unexpected repository access: ${method.name}")
        }
    } as InboxRepository
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
    private fun record(): InboxRecord {
        val path = System.getenv("PERSONA_INBOX_LIVE_FIXTURE")
        if (path == null) return InboxRecord(InboxItem("event", kind = "group",
            title = "Approve the spring launch checklist and confirm every owner before Friday's release", sender = "Sender",
            summary = "Summary"), feedId = "feed", source = "lark", isGroup = true, groupName = "Source group", count = 1)
        val fixture = Json.parseToJsonElement(File(path).readText()).jsonObject
        val item = fixture.getValue("item").jsonObject
        fun text(key: String) = item[key]?.jsonPrimitive?.contentOrNull
        return InboxRecord(InboxItem(text("id")!!, kind = text("kind")!!, title = text("title")!!, sender = text("sender"),
            preview = text("preview").orEmpty(), summary = text("summary"), updatedAt = text("updated_at")),
            feedId = item.getValue("lark").jsonObject.getValue("feed_id").jsonPrimitive.content,
            source = "lark", isGroup = true, groupName = fixture.getValue("group_name").jsonPrimitive.content, count = 1)
    }

    private fun assertTitleStartsVisibly(title: String, context: String) {
        val node = rule.onNodeWithText(title, useUnmergedTree = true).fetchSemanticsNode()
        val layouts = mutableListOf<androidx.compose.ui.text.TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action!!(layouts)
        val firstGlyph = layouts.single().getBoundingBox(0)
        assertTrue("$context: the title's first glyph must remain visible, not be replaced by an ellipsis", firstGlyph.width > 0f)
    }

    @Test fun eventSenderAndSourceHaveDistinctOrderedLinesInEveryStyle() {
        val record = record()
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scale by mutableStateOf(1f)
        var shown by mutableStateOf(record)
        rule.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, scale)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.testTag("row").width(580.dp).background(PersonaTheme.palette.background)) {
                        InboxItemRow(shown, repository, navigator, ReplyComposers(repository, rememberCoroutineScope()), GroupPosition.Single,
                            done = false, busy = false, onlineSince = null, onDone = {}, onHandOff = {})
                    }
                }
            }
        }
        for (appearance in AppStyle.entries) for (night in listOf(false, true)) for (fontScale in listOf(1f, 2f)) {
            rule.runOnIdle { style = appearance; dark = night; scale = fontScale }
            rule.waitForIdle()
            val context = "$appearance dark=$night scale=$fontScale"
            assertTitleStartsVisibly(record.item.title, context)
            val title = rule.onNodeWithText(record.item.title, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            val sender = rule.onNodeWithText(record.item.sender!!, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            val group = rule.onNodeWithText(record.groupName!!, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            assertTrue("$context: $title / $sender / $group", title.top < sender.top && sender.top < group.top)
            rule.onNodeWithText(record.item.summary!!, useUnmergedTree = true).assertExists()
            val out = File(System.getenv("PERSONA_INBOX_SHOTS") ?: "build/group-name-shots").apply { mkdirs() }
            File(out, "${appearance.name.lowercase()}-${if (night) "dark" else "light"}-${fontScale}x.png").outputStream().use {
                assertTrue(rule.onNodeWithTag("row").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
        rule.runOnIdle { shown = record.copy(groupName = null) }
        rule.onAllNodesWithText(record.item.title, useUnmergedTree = true).assertCountEquals(1)
        rule.onNodeWithText(record.item.sender!!, useUnmergedTree = true).assertExists()
        for (kind in listOf("dm", "mail")) {
            rule.runOnIdle { shown = record.copy(item = record.item.copy(kind = kind), isGroup = false) }
            rule.onNodeWithText(record.groupName!!, useUnmergedTree = true).assertDoesNotExist()
            rule.onAllNodesWithText(record.item.title, useUnmergedTree = true).assertCountEquals(1)
            rule.onNodeWithText(record.item.sender!!, useUnmergedTree = true).assertExists()
        }
    }

    @Test fun eventWithWhereKeepsTheSummaryAndUsesTheSharedThreeContentLines() {
        val base = record()
        val sender = base.item.sender!!
        val group = base.groupName!!
        val followed = base.copy(
            item = base.item.copy(preview = "Latest line", summary = "AI summary"),
            event = InboxEventFields(where = group, now = "confirm_reply"),
        )
        val metaText = if (group == sender) "$sender：Latest line" else "$group · $sender：Latest line"
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var scale by mutableStateOf(1f)
        var shown by mutableStateOf(followed)
        rule.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, scale)) {
                PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                    Box(Modifier.testTag("follow-row").width(580.dp).background(PersonaTheme.palette.background)) {
                        InboxItemRow(shown, repository, navigator, ReplyComposers(repository, rememberCoroutineScope()), GroupPosition.Single,
                            done = shown.item.isDone, busy = false, onlineSince = null, onDone = {}, onHandOff = {})
                    }
                }
            }
        }
        for (appearance in AppStyle.entries) for (night in listOf(false, true)) for (fontScale in listOf(1f, 2f)) {
            rule.runOnIdle { style = appearance; dark = night; scale = fontScale }
            rule.waitForIdle()
            val context = "$appearance dark=$night scale=$fontScale"
            assertTitleStartsVisibly(followed.item.title, context)
            val title = rule.onNodeWithText(followed.item.title, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            val summary = rule.onNodeWithText("AI summary", useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            val meta = rule.onNodeWithText(metaText, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
            assertTrue("$context: $title / $summary / $meta", title.top < summary.top && summary.top < meta.top)
            rule.onNodeWithText("等你确认回复", useUnmergedTree = true).assertExists()
            // The old source-conversation and sender lines would repeat the new meta, so they are not drawn.
            rule.onNodeWithText(group, useUnmergedTree = true).assertDoesNotExist()
            rule.onNodeWithText(sender, useUnmergedTree = true).assertDoesNotExist()
            val out = File(System.getenv("PERSONA_INBOX_FOLLOW_SHOTS") ?: "build/inbox-follow-shots").apply { mkdirs() }
            File(out, "${appearance.name.lowercase()}-${if (night) "dark" else "light"}-${fontScale}x.png").outputStream().use {
                assertTrue(rule.onNodeWithTag("follow-row").captureToImage().asAndroidBitmap().compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }

        // A direct chat says the person once, in the same title / summary / meta order.
        rule.runOnIdle {
            shown = followed.copy(item = followed.item.copy(kind = "dm"), isGroup = false, groupName = null,
                event = followed.event.copy(where = sender, now = "waiting"))
        }
        rule.onNodeWithText("$sender：Latest line", useUnmergedTree = true).assertExists()
        rule.onNodeWithText("等对方回复", useUnmergedTree = true).assertExists()
        rule.onNodeWithText(sender, useUnmergedTree = true).assertDoesNotExist()

        val working = followed.copy(
            item = followed.item.copy(status = InboxItem.WORKING),
            event = followed.event.copy(status = InboxItem.WORKING, handling = InboxHandling(state = "looking"), now = "waiting",
                follow = InboxFollow(who = "A very long wait for the whole review group to answer",
                    next = "Check every remaining owner and then ask for a decision again")),
        )
        rule.runOnIdle { shown = working }
        val boundedMeta = rule.onNodeWithText(metaText, useUnmergedTree = true).fetchSemanticsNode().boundsInRoot
        assertTrue("A long shared state must leave visible width for the required meta text", boundedMeta.width > 0f)
        for (unknownOrMissing in listOf("something_new", null)) {
            rule.runOnIdle { shown = working.copy(event = working.event.copy(now = unknownOrMissing)) }
            // With where but no recognized now, the legacy working mark must not disappear.
            rule.onNodeWithText("AI 正在看", useUnmergedTree = true).assertExists()
        }

        // Without where the legacy group, sender and summary lines come back unchanged; no empty meta row is reserved.
        rule.runOnIdle { shown = base }
        rule.onNodeWithText("AI summary", useUnmergedTree = true).assertDoesNotExist()
        rule.onNodeWithText(base.item.summary!!, useUnmergedTree = true).assertExists()
        rule.onNodeWithText(group, useUnmergedTree = true).assertExists()
        rule.onNodeWithText(sender, useUnmergedTree = true).assertExists()
        rule.onNodeWithText(metaText, useUnmergedTree = true).assertDoesNotExist()
    }
}
