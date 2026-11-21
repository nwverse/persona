package persona.feature.chat

import android.content.Context
import android.content.Intent
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows.shadowOf
import org.robolectric.annotation.Config
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.domain.lines.chatTurns
import persona.feature.chat.media.MediaShare
import java.io.File

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class SelectedMessageExportTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private fun item(id: String, seq: Int, user: Boolean, text: String = id) =
        Item(id, seq, role = if (user) "user" else "assistant", kind = "text", text = text)
    private fun Item.target() = MessageTarget(id, text.orEmpty(), role == "user")

    @Test fun bottomMenuSeparatesPickingAndExcludesLocalOutbox() {
        val row = MessageTarget("answer", "Reply", false)
        val extras = MessageExtras(pick = true)
        val actions = messageMenuActions(row, false, true, context.resources, extras) {}
        assertEquals(context.getString(R.string.chat_forward_select_messages), actions.last().label)
        assertTrue(actions.last().separatorBefore)
        assertFalse(MessageAction.Pick in messageActions(row.copy(pending = persona.core.contracts.api.DeliveryState.Queued), true, true, extras))
        assertFalse(MessageAction.Pick in messageActions(row.copy(text = ""), true, true, extras))
    }

    @Test fun allActionsUseSameTimelineOrderWithConsecutiveQuestionsAndWorkNarration() {
        val rows = listOf(item("q1", 1, true), item("q2", 2, true), item("progress", 3, false),
            Item("tool", 4, kind = "tool_use", toolName = "Read"), item("reply", 5, false), item("q3", 6, true), item("later", 7, false))
        val questions = pickQuestions(chatTurns(rows.map { it.asLineItem() }))
        assertEquals(listOf("q1", "q2"), questions.getValue("progress").map { it.id })
        assertEquals(listOf("q1", "q2"), questions.getValue("reply").map { it.id })
        assertEquals(listOf("q3"), questions.getValue("later").map { it.id })
        val pick = ForwardPick().apply { readQuestions = { questions }; start(rows.last().target()); toggle(rows[4].target()); toggle(rows[0].target()) }
        assertEquals(3, pick.selected?.size)
        assertTrue(pick.isBrought("q2"))
        for (action in PickAction.entries) {
            pick.choose(rows.reversed(), action)
            assertEquals(listOf("q1", "q2", "reply", "q3", "later"), pick.sheet?.map { it.id })
            assertEquals(action, pick.action)
            pick.dismiss()
        }
        pick.setQuestions(false)
        pick.choose(rows, PickAction.Copy)
        assertEquals(listOf("q1", "reply", "later"), pick.sheet?.map { it.id })
    }

    @Test fun rememberQuestionsAcrossSessionsAndRejectAnOverLimitUnion() {
        val question = MessageTarget("q", "Question", true)
        val answer = MessageTarget("a", "Answer", false)
        var stored = true
        fun newPick() = ForwardPick().apply {
            readQuestions = { mapOf("a" to listOf(question)) }
            readInclude = { stored }
            saveInclude = { stored = it }
            start(answer)
        }
        val pick = newPick()
        assertTrue(pick.include)
        pick.setQuestions(false)
        pick.end()
        val next = newPick()
        assertFalse(next.include)
        repeat(ForwardMessage.MOST - 1) { next.toggle(answer.copy(id = "other$it")) }
        next.setQuestions(true)
        assertTrue(next.refused)
        assertFalse(stored)
        next.toggle(answer.copy(id = "other0"))
        next.setQuestions(true)
        assertTrue(stored)
    }

    @Test fun relaysLoopAndToolOutputAreNotYourQuestions() {
        val rows = listOf(item("loop", 1, true, "/loop 10m"), item("relay", 2, true).copy(from = "other"),
            item("empty", 3, true, ""), item("q", 4, true), item("a", 5, false))
        val questions = pickQuestions(chatTurns(rows.map { it.asLineItem() }))
        assertEquals(listOf("q"), questions.getValue("a").map { it.id })
    }

    @Test fun wholeCopyRecordsAttributionAndReadsOnlyClippedItems() = runTest {
        val key = SessionKey("laptop", "chat")
        val source = ForwardMessage.Source(key, "q", ForwardMessage.Role.User, "unused", "Walk")
        val drafts = listOf(ForwardDraft(source, "Where?"), ForwardDraft(source.copy(item = "a", role = ForwardMessage.Role.Agent, author = "Claude"), "cut", "a"))
        val read = mutableListOf<String>()
        val messages = pickedMessages(drafts, context.resources) { read += it; "At the gate." }
        assertEquals(listOf("a"), read)
        assertEquals("Me:\nWhere?\n\nClaude:\nAt the gate.", copiedMessages(messages, context.resources))
        val broken = runCatching { pickedMessages(drafts, context.resources) { throw java.io.IOException("offline") } }
        assertTrue(broken.exceptionOrNull() is java.io.IOException)
    }

    @Test fun wholeBlocksStartOnNextPageAndOverlongBlocksContinueWithoutAGap() {
        assertTrue(exportPagePlan(listOf(0), 8, MAX_EXPORT_HEIGHT).isEmpty())
        val slices = exportPagePlan(listOf(100, MAX_EXPORT_HEIGHT - 108, MAX_EXPORT_HEIGHT + 9, 10), 8, MAX_EXPORT_HEIGHT)
        assertEquals(listOf(listOf(ExportSlice(0, 0, 100, 0), ExportSlice(1, 0, MAX_EXPORT_HEIGHT - 108, 8)),
            listOf(ExportSlice(2, 0, MAX_EXPORT_HEIGHT, 0)), listOf(ExportSlice(2, MAX_EXPORT_HEIGHT, MAX_EXPORT_HEIGHT + 9, 0),
                ExportSlice(3, 0, 10, 8))), slices)
        assertEquals(listOf(0, 0), exportPagePlan(listOf(120, 90), 3, 200).map { it.single().gap })
    }

    @Test fun oversizedFencesRemainCodeAcrossMeasuredBlocksAndUserTextStaysLiteral() {
        val marker = "~~~~"
        val lines = (1..700).map { "line($it)" }
        val original = (listOf(marker + "kotlin") + lines + marker + "After the snippet").joinToString("\n")
        val parts = pictureParts(original, markdown = true)
        assertEquals(2, parts.size)
        assertTrue(parts.first().endsWith(marker))
        assertTrue(parts.last().startsWith(marker + "kotlin\n"))
        assertEquals(lines, parts.flatMap { part -> part.lines().filter { it.startsWith("line(") } })
        assertEquals(original, pictureParts(original, markdown = false).joinToString("\n"))
    }

    @Test fun headingUsesBothDatesAndTheChatAgent() {
        val early = java.time.Instant.parse("2026-06-01T12:00:00Z")
        val late = java.time.Instant.parse("2026-06-08T12:00:00Z")
        val first = PickedMessage("date", true, "Me", "Hello", early, emptyList())
        val locale = java.util.Locale.US
        assertEquals("Jun 1, 2026 – Jun 8, 2026 · Codex", exportSubtitle(listOf(first.copy(time = late), first), "Codex", locale))
        assertEquals("Jun 1, 2026", exportSubtitle(listOf(first, first), "", locale))
        assertEquals("Jun 8, 2026 · Claude", exportSubtitle(emptyList(), "Claude", locale, now = late))
    }

    @Test fun picturesUseOnlyPermittedHubReadsAndSkipHiddenGalleryTiles() = runTest {
        val policy = persona.core.contracts.api.ReplyImageSource
        val requests = mutableListOf<String>()
        val source = object : persona.core.contracts.api.ReplyImageSource {
            override val features = kotlinx.coroutines.flow.MutableStateFlow(emptySet<String>())
            override suspend fun load(url: String, size: Int, allowDirect: Boolean): ByteArray {
                assertFalse(allowDirect)
                requests += url
                throw java.io.IOException("missing fixture")
            }
        }
        val text = (1..8).joinToString("\n") { "![Waypoint $it](https://example.invalid/$it.png)" }
        val doc = requireNotNull(ReplyImageDocument.parse(text))
        val assets = mutableMapOf<String, android.graphics.Bitmap?>()
        val older = exportGallery(doc, true, source, assets, 360)
        assertFalse(older.canLoad(doc.pictures.first()))
        source.features.value = setOf(policy.FEATURE)
        val relayed = exportGallery(doc, false, source, assets, 360)
        assertFalse(relayed.canLoad(doc.pictures.first()))
        assertTrue(requests.isEmpty())
        val own = exportGallery(doc, true, source, assets, 360)
        assertTrue(own.canLoad(doc.pictures.first()))
        assertEquals(doc.pictures.take(ReplyImageLayout.mostShown).map { it.url }, requests)
        exportGallery(doc, true, source, assets, 360)
        assertEquals(ReplyImageLayout.mostShown, requests.size)
    }

    @Test fun shareUsesAReadOnlyContentGrantForEveryPageInOrder() {
        val root = File(context.cacheDir, "shared").apply { mkdirs() }
        val files = listOf(File(root, "picked-1.png").apply { writeBytes(byteArrayOf(1)) }, File(root, "picked-2.png").apply { writeBytes(byteArrayOf(2)) })
        for (selected in listOf(files.take(1), files)) {
            assertTrue(MediaShare.sharePngs(context, selected))
            val chooser = shadowOf(context as android.app.Application).nextStartedActivity
            assertEquals(Intent.ACTION_CHOOSER, chooser.action)
            val intent = chooser.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)!!
            assertEquals(if (selected.size == 1) Intent.ACTION_SEND else Intent.ACTION_SEND_MULTIPLE, intent.action)
            assertEquals("image/png", intent.type)
            assertEquals(Intent.FLAG_GRANT_READ_URI_PERMISSION, intent.flags and (Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION))
            val uris = (0 until intent.clipData!!.itemCount).map { intent.clipData!!.getItemAt(it).uri }
            assertEquals(selected.map { it.name }, uris.map { it.lastPathSegment })
            assertTrue(uris.all { it.scheme == "content" })
            if (selected.size == 1) assertEquals(uris.single(), intent.getParcelableExtra(Intent.EXTRA_STREAM, android.net.Uri::class.java))
            else assertEquals(uris, intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, android.net.Uri::class.java))
            assertEquals(selected.indices.map { it + 1 }, uris.map { context.contentResolver.openInputStream(it)!!.use { stream -> stream.read() } })
        }
    }
}
