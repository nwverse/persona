package persona.android.assist

import android.graphics.Bitmap
import android.graphics.Color
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatSnapshot
import persona.core.contracts.api.ConnectionState
import persona.feature.chat.ChatLinkContext
import persona.feature.chat.ComposerAttachment

@OptIn(ExperimentalCoroutinesApi::class)
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class AssistOverlayControllerTest {
    private val mainKey = SessionKey("machine-1", "main-1")
    private val sampleContext = ChatLinkContext(
        kind = "context",
        title = "Pricing Page",
        url = "https://example.com/pricing",
        text = "Pricing Page\nhttps://example.com/pricing",
        id = "ctx-1",
    )

    private data class PublishedDraft(
        val id: String,
        val screenshot: ComposerAttachment?,
        val context: ChatLinkContext?,
        val text: String,
    )

    private class Harness(scope: TestScope, timeoutMs: Long = 4_000L) {
        val preparedBitmaps = mutableListOf<Bitmap>()
        var saveCount = 0
        val deletedAttachments = mutableListOf<ComposerAttachment>()
        val sentTexts = mutableListOf<Pair<String, String>>()
        val sentWithScreen = mutableListOf<Triple<String, ComposerAttachment, ChatLinkContext?>>()
        val publishedDrafts = mutableListOf<PublishedDraft>()
        val launchedTargets = mutableListOf<SessionKey?>()
        var wakeCount = 0
        var dismissCount = 0
        private var idSeq = 0

        val controller = AssistOverlayController(
            mainScope = scope,
            prepareScreenshot = { src ->
                Bitmap.createBitmap(src.width, src.height, Bitmap.Config.ARGB_8888).also { preparedBitmaps += it }
            },
            createThumbnail = { _ ->
                Bitmap.createBitmap(32, 32, Bitmap.Config.ARGB_8888)
            },
            saveScreenshot = { _, id ->
                saveCount++
                ComposerAttachment(
                    id = "assist-$id-$saveCount",
                    name = "assist-$id-$saveCount.jpg",
                    uri = "content://com.example.persona.chat.files/assist/assist-$id-$saveCount.jpg",
                    image = true,
                    size = 1024L,
                )
            },
            deleteScreenshot = { deletedAttachments += it },
            sendTextToMain = { prompt, clientId -> sentTexts += prompt to clientId },
            sendWithScreenToMain = { prompt, shot, ctx, _ -> sentWithScreen += Triple(prompt, shot, ctx) },
            publishDraftForFullApp = { id, shot, ctx, text -> publishedDrafts += PublishedDraft(id, shot, ctx, text) },
            onWakeHub = { wakeCount++ },
            onLaunchFullApp = { launchedTargets += it },
            onDismiss = { dismissCount++ },
            screenOnlyDisplayLabel = { "Please look at this screenshot." },
            defaultSendError = { "Couldn't send message" },
            screenshotWaitTimeoutMillis = timeoutMs,
            nextId = { "id-${++idSeq}" },
            log = {},
        )
    }

    private fun sampleBitmap(): Bitmap = Bitmap.createBitmap(120, 80, Bitmap.Config.ARGB_8888).apply {
        eraseColor(Color.BLUE)
    }

    @Test
    fun screenshotStaysInMemoryAndIsNotSavedOrAttachedUntilManualConfirmation() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        controller.onHubUpdate(AssistHubSnapshot(status = AssistHubStatus.Ready, mainSession = mainKey))

        controller.beginSession(supportsScreenshot = true, showFlags = 4)
        assertEquals(1, harness.wakeCount)
        assertEquals(AssistCaptureStatus.Waiting, controller.state.value.captureStatus)

        val shot = sampleBitmap()
        controller.onAssistContextReceived(sampleContext, isFocused = true)
        controller.onScreenshotReceived(shot)
        runCurrent()

        // Never written to disk and never attached by default.
        assertEquals(0, harness.saveCount)
        assertEquals(AssistCaptureStatus.Available, controller.state.value.captureStatus)
        assertNull(controller.state.value.attachedScreenshot)
        assertNull(controller.state.value.attachedContext)
        assertTrue(controller.state.value.hasStagedContext)

        // Manual tap on "Attach current screen" persists to private cache and attaches preview + safe context.
        controller.attachCurrentScreen()
        runCurrent()

        assertEquals(1, harness.saveCount)
        assertEquals(AssistCaptureStatus.Attached, controller.state.value.captureStatus)
        assertNotNull(controller.state.value.attachedScreenshot)
        assertNotNull(controller.state.value.attachedThumbnail)
        assertEquals(sampleContext, controller.state.value.attachedContext)

        shot.recycle()
        controller.endSession()
        runCurrent()
    }

    @Test
    fun removeAttachedScreenDeletesCacheFileClearsContextAndAllowsReattach() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        controller.beginSession(supportsScreenshot = true)

        val shot = sampleBitmap()
        controller.onAssistContextReceived(sampleContext, isFocused = true)
        controller.onScreenshotReceived(shot)
        runCurrent()

        controller.attachCurrentScreen()
        runCurrent()
        val firstSaved = controller.state.value.attachedScreenshot!!

        controller.removeAttachedScreen()
        runCurrent()

        assertEquals(listOf(firstSaved), harness.deletedAttachments)
        assertEquals(AssistCaptureStatus.Available, controller.state.value.captureStatus)
        assertNull(controller.state.value.attachedScreenshot)
        assertNull(controller.state.value.attachedContext)
        assertNull(controller.state.value.attachedThumbnail)

        // User can re-attach before sending.
        controller.attachCurrentScreen()
        runCurrent()
        assertEquals(2, harness.saveCount)
        assertEquals(AssistCaptureStatus.Attached, controller.state.value.captureStatus)

        shot.recycle()
        controller.endSession()
        runCurrent()
    }

    @Test
    fun textOnlySendReleasesStagedBitmapAndNeverSavesOrAttachesScreen() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        controller.onHubUpdate(AssistHubSnapshot(status = AssistHubStatus.Ready, mainSession = mainKey))
        controller.beginSession(supportsScreenshot = true)

        val shot = sampleBitmap()
        controller.onAssistContextReceived(sampleContext, isFocused = true)
        controller.onScreenshotReceived(shot)
        runCurrent()
        assertEquals(1, harness.preparedBitmaps.size)
        assertFalse(harness.preparedBitmaps.single().isRecycled)

        controller.updateDraftText("What is the weather?")
        controller.send()
        runCurrent()

        // Staged bitmap recycled immediately; zero disk writes; pure text sent directly to main chat.
        assertEquals(0, harness.saveCount)
        assertTrue(harness.preparedBitmaps.single().isRecycled)
        assertTrue(harness.sentWithScreen.isEmpty())
        assertEquals(1, harness.sentTexts.size)
        assertEquals("What is the weather?", harness.sentTexts.single().first)
        assertEquals(AssistCaptureStatus.NotSupported, controller.state.value.captureStatus)
        assertTrue(controller.state.value.expanded)
        assertEquals(1, controller.state.value.turns.size)
        assertTrue(controller.state.value.awaitingReply)

        shot.recycle()
        controller.endSession()
        runCurrent()
    }

    @Test
    fun dismissReleasesStagedBitmapAndDeletesUnsentAttachedCacheFile() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        controller.beginSession(supportsScreenshot = true)

        val shot1 = sampleBitmap()
        controller.onScreenshotReceived(shot1)
        runCurrent()
        controller.endSession()
        runCurrent()

        assertEquals(0, harness.saveCount)
        assertTrue(harness.preparedBitmaps[0].isRecycled)

        // Second session: user attaches screen, then dismisses without sending -> temp file must be deleted.
        controller.beginSession(supportsScreenshot = true)
        val shot2 = sampleBitmap()
        controller.onScreenshotReceived(shot2)
        runCurrent()
        controller.attachCurrentScreen()
        runCurrent()
        val saved = controller.state.value.attachedScreenshot!!

        controller.endSession()
        runCurrent()

        assertTrue(harness.preparedBitmaps[1].isRecycled)
        assertEquals(listOf(saved), harness.deletedAttachments)

        shot1.recycle()
        shot2.recycle()
    }

    @Test
    fun flagSecureNullScreenshotDegradesGracefullyWithoutBlockingTextSend() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        controller.onHubUpdate(AssistHubSnapshot(status = AssistHubStatus.Ready, mainSession = mainKey))
        controller.beginSession(supportsScreenshot = true)

        controller.onScreenshotReceived(null)
        runCurrent()

        assertEquals(AssistCaptureStatus.Unavailable, controller.state.value.captureStatus)
        assertEquals(0, harness.saveCount)

        controller.updateDraftText("Summarize my last meeting")
        assertTrue(controller.state.value.canSend)
        controller.send()
        runCurrent()

        assertEquals(1, harness.sentTexts.size)
        assertEquals("Summarize my last meeting", harness.sentTexts.single().first)
        controller.endSession()
        runCurrent()
    }

    @Test
    fun delayedScreenshotArrivalEvenAfterTimeoutStillBecomesAvailable() = runTest {
        val harness = Harness(this, timeoutMs = 1_000L)
        val controller = harness.controller
        controller.beginSession(supportsScreenshot = true)
        assertEquals(AssistCaptureStatus.Waiting, controller.state.value.captureStatus)

        advanceTimeBy(1_500L)
        runCurrent()
        assertEquals(AssistCaptureStatus.Unavailable, controller.state.value.captureStatus)

        // Late onHandleScreenshot from a busy compositor still upgrades seamlessly to Available.
        val shot = sampleBitmap()
        controller.onScreenshotReceived(shot)
        runCurrent()
        assertEquals(AssistCaptureStatus.Available, controller.state.value.captureStatus)
        assertEquals(0, harness.saveCount)

        shot.recycle()
        controller.endSession()
        runCurrent()
    }

    @Test
    fun sendWithConfirmedScreenUploadsDeletesTempFileAndStreamsRepliesInOverlay() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        val earlierItem = Item(id = "old-1", role = "assistant", kind = "text", text = "Earlier history")
        controller.onHubUpdate(
            AssistHubSnapshot(
                status = AssistHubStatus.Ready,
                mainSession = mainKey,
                chatSnapshot = ChatSnapshot(items = listOf(earlierItem), loading = false),
            ),
        )
        controller.beginSession(supportsScreenshot = true)

        val shot = sampleBitmap()
        controller.onAssistContextReceived(sampleContext, isFocused = true)
        controller.onScreenshotReceived(shot)
        runCurrent()
        controller.attachCurrentScreen()
        runCurrent()
        val saved = controller.state.value.attachedScreenshot!!

        controller.updateDraftText("Explain this chart")
        controller.send()
        runCurrent()

        assertEquals(1, harness.sentWithScreen.size)
        assertEquals("Explain this chart", harness.sentWithScreen.single().first)
        assertEquals(saved, harness.sentWithScreen.single().second)
        assertEquals(sampleContext, harness.sentWithScreen.single().third)
        assertEquals(listOf(saved), harness.deletedAttachments)
        assertTrue(harness.preparedBitmaps.single().isRecycled)

        val sentClientId = controller.state.value.turns.single().id.removePrefix("pending:")

        // Streaming assistant reply arrives on the Hub main conversation.
        val userEcho = Item(
            id = "u-1",
            role = "user",
            kind = "text",
            text = "${sampleContext.xml()}\n\nExplain this chart\n\n<attached_images>\n/tmp/a.jpg\n</attached_images>",
            clientMsgId = sentClientId,
        )
        val partialReply = Item(id = "a-1", role = "assistant", kind = "text", text = "The chart shows")
        controller.onHubUpdate(
            AssistHubSnapshot(
                status = AssistHubStatus.Ready,
                mainSession = mainKey,
                mainWorking = true,
                chatSnapshot = ChatSnapshot(
                    items = listOf(earlierItem, userEcho, partialReply),
                    loading = false,
                    streamingItemIds = setOf("a-1"),
                ),
            ),
        )

        // Earlier history stays hidden; overlay shows only this session's turns and live streaming state.
        val streamingTurns = controller.state.value.turns
        assertEquals(2, streamingTurns.size)
        assertTrue(streamingTurns[0].fromUser)
        assertEquals("Explain this chart", streamingTurns[0].text)
        assertTrue(streamingTurns[0].hasScreenAttachment)
        assertFalse(streamingTurns[1].fromUser)
        assertEquals("The chart shows", streamingTurns[1].text)
        assertTrue(streamingTurns[1].streaming)
        assertFalse(controller.state.value.awaitingReply)

        // Reply finishes streaming.
        val finalReply = partialReply.copy(text = "The chart shows steady growth.")
        controller.onHubUpdate(
            AssistHubSnapshot(
                status = AssistHubStatus.Ready,
                mainSession = mainKey,
                mainWorking = false,
                chatSnapshot = ChatSnapshot(
                    items = listOf(earlierItem, userEcho, finalReply),
                    loading = false,
                    streamingItemIds = emptySet(),
                ),
            ),
        )
        assertEquals("The chart shows steady growth.", controller.state.value.turns[1].text)
        assertFalse(controller.state.value.turns[1].streaming)

        shot.recycle()
        controller.endSession()
        runCurrent()
    }

    @Test
    fun openInPersonaHandsOffOnlyManuallyConfirmedDraftAndPreservesItsCacheFile() = runTest {
        val harness = Harness(this)
        val controller = harness.controller
        controller.onHubUpdate(AssistHubSnapshot(status = AssistHubStatus.Ready, mainSession = mainKey))

        // Case 1: unconfirmed screenshot in memory -> must NOT hand off to MainActivity draft.
        controller.beginSession(supportsScreenshot = true)
        val shot1 = sampleBitmap()
        controller.onScreenshotReceived(shot1)
        runCurrent()
        controller.openInPersona()
        controller.endSession()
        runCurrent()

        assertTrue(harness.publishedDrafts.isEmpty())
        assertEquals(listOf(mainKey), harness.launchedTargets)
        assertEquals(1, harness.dismissCount)
        assertTrue(harness.preparedBitmaps[0].isRecycled)

        // Case 2: manually confirmed screenshot + unsent text -> handed off and cache file preserved.
        controller.beginSession(supportsScreenshot = true)
        val shot2 = sampleBitmap()
        controller.onAssistContextReceived(sampleContext, isFocused = true)
        controller.onScreenshotReceived(shot2)
        runCurrent()
        controller.attachCurrentScreen()
        runCurrent()
        val confirmedShot = controller.state.value.attachedScreenshot!!
        controller.updateDraftText("Continue in full app")

        controller.openInPersona()
        controller.endSession()
        runCurrent()

        assertEquals(1, harness.publishedDrafts.size)
        val handoff = harness.publishedDrafts.single()
        assertEquals(confirmedShot, handoff.screenshot)
        assertEquals(sampleContext, handoff.context)
        assertEquals("Continue in full app", handoff.text)
        assertTrue("Handed-off screenshot must not be deleted on overlay close", harness.deletedAttachments.isEmpty())

        shot1.recycle()
        shot2.recycle()
    }

    @Test
    fun wireFormattingAndHubStatusMappingCoverOfflineAndImageCases() {
        val formatted = formatAssistWireMessage(
            prompt = "",
            remoteImagePath = "/tmp/assist-1.jpg",
            contextCard = sampleContext,
            photosOnlyPrompt = "Please look at this screenshot.",
        )
        assertTrue(formatted.startsWith("<persona-context "))
        assertTrue(formatted.contains("Please look at this screenshot."))
        assertTrue(formatted.contains("<attached_images>\n/tmp/assist-1.jpg\n</attached_images>"))

        assertEquals(AssistHubStatus.Ready, mapConnectionStateToHubStatus(ConnectionState.Online, hasBinding = true))
        assertEquals(AssistHubStatus.Connecting, mapConnectionStateToHubStatus(ConnectionState.Connecting, hasBinding = true))
        assertEquals(AssistHubStatus.Offline, mapConnectionStateToHubStatus(ConnectionState.WaitingForNetwork, hasBinding = true))
        assertEquals(AssistHubStatus.Offline, mapConnectionStateToHubStatus(ConnectionState.Revoked, hasBinding = true))
    }
}
