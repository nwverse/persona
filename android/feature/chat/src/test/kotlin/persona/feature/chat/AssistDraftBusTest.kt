package persona.feature.chat

import androidx.compose.ui.text.input.TextFieldValue
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.SessionKey

class AssistDraftBusTest {
    private val mainSession = SessionKey("machine-1", "main-session")
    private val screenshot = ComposerAttachment(
        id = "assist-shot-1",
        name = "assist-1.jpg",
        uri = "content://com.example.persona.chat.files/assist/assist-1.jpg",
        image = true,
        size = 2048L,
    )
    private val pageContext = ChatLinkContext(
        kind = "context",
        title = "Docs",
        url = "https://example.com/docs",
        text = "Docs\nhttps://example.com/docs",
        id = "assist-ctx-1",
    )

    @Test
    fun outOfOrderContextAndLateScreenshotPreserveClaimedTarget() {
        var now = 1_000L
        val bus = AssistDraftQueue(clock = { now })

        bus.attachContext("h1", pageContext)
        val claimed = bus.claim(AssistDraftTarget.Session(mainSession))
        assertNotNull(claimed)
        assertEquals(AssistDraftTarget.Session(mainSession), claimed!!.target)
        assertNull(claimed.screenshot)
        assertEquals(pageContext, claimed.context)

        bus.markContextApplied("h1")
        assertFalse(bus.current()!!.hasUnappliedContent)

        now += 200L
        bus.attachScreenshot("h1", screenshot)
        val updated = bus.current()!!
        assertEquals(AssistDraftTarget.Session(mainSession), updated.target)
        assertEquals(screenshot, updated.screenshot)
        assertFalse(updated.screenshotApplied)
        assertTrue(updated.contextApplied)
        assertTrue(updated.hasUnappliedContent)
    }

    @Test
    fun prefillDraftPreservesExistingTextDeduplicatesAndEnforcesMediaCap() {
        val composer = ChatComposerState(ChatComposerDraft(text = "Existing draft question"))
        val contexts = mutableListOf<ChatLinkContext>()
        val draft = PendingAssistDraft(
            id = "h1",
            createdAtMillis = 100L,
            screenshot = screenshot,
            context = pageContext,
            target = AssistDraftTarget.Session(mainSession),
        )

        val first = prefillAssistDraft(composer, draft, existingContextIds = emptySet()) { contexts += it }
        assertTrue(first.screenshotAdded)
        assertFalse(first.screenshotRejectedForLimit)
        assertTrue(first.contextAdded)
        assertEquals("Existing draft question", composer.value.text)
        assertEquals(listOf(screenshot), composer.attachments)
        assertEquals(listOf(pageContext), contexts)

        // Second application with same ids must not duplicate attachment or context.
        val duplicate = prefillAssistDraft(composer, draft, existingContextIds = contexts.map { it.id }.toSet()) { contexts += it }
        assertFalse(duplicate.screenshotAdded)
        assertFalse(duplicate.contextAdded)
        assertEquals(1, composer.attachments.size)
        assertEquals(1, contexts.size)

        // When media slots are full, reject cleanly instead of exceeding MAX_COMPOSER_MEDIA.
        val fullComposer = ChatComposerState(
            ChatComposerDraft(
                attachments = (1..MAX_COMPOSER_MEDIA).map { i ->
                    ComposerAttachment("img-$i", "img-$i.jpg", "content://example/img-$i", image = true)
                },
            ),
        )
        fullComposer.value = TextFieldValue("keep")
        val capped = prefillAssistDraft(fullComposer, draft)
        assertFalse(capped.screenshotAdded)
        assertTrue(capped.screenshotRejectedForLimit)
        assertEquals(MAX_COMPOSER_MEDIA, fullComposer.attachments.size)
        assertEquals("keep", fullComposer.value.text)
    }

    @Test
    fun expiredHandoffIsDroppedOnClaim() {
        var now = 1_000L
        val bus = AssistDraftQueue(clock = { now }, maxAgeMillis = 5_000L)
        bus.publish("stale", screenshot = screenshot, context = pageContext)
        now += 6_000L
        assertNull(bus.claim(AssistDraftTarget.Starter))
        assertNull(bus.current())
    }

    @Test
    fun assistCacheUrisAreCleanedUpWhenLeavingComposerDraft() {
        val pkg = "com.example.persona"
        val assistUri = "content://$pkg.chat.files/assist/assist-123.jpg"
        assertTrue(isCaptureUri(assistUri, pkg))

        val before = listOf(screenshot.copy(uri = assistUri))
        assertEquals(listOf(assistUri), leftCaptures(before, emptyList(), pkg))
    }

    @Test
    fun unsentOverlayTextHandoffAppendsCleanlyWithoutOverwritingExistingDraft() {
        val bus = AssistDraftQueue(clock = { 1_000L })
        bus.publish(id = "h2", screenshot = screenshot, text = "Summarize this chart")
        val claimed = bus.claim(AssistDraftTarget.Session(mainSession))!!
        assertTrue(claimed.hasUnappliedContent)
        assertEquals("Summarize this chart", claimed.text)

        val composer = ChatComposerState(ChatComposerDraft(text = "Earlier note"))
        val applied = prefillAssistDraft(composer, claimed)
        assertTrue(applied.screenshotAdded)
        assertTrue(applied.textAdded)
        assertEquals("Earlier note\nSummarize this chart", composer.value.text)

        bus.markScreenshotApplied("h2")
        bus.markTextApplied("h2")
        assertFalse(bus.current()!!.hasUnappliedContent)
    }
}
