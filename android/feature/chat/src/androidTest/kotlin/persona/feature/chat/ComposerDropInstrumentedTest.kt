package persona.feature.chat

import android.content.ClipData
import android.content.ClipDescription
import android.content.Context
import android.content.pm.ActivityInfo
import android.content.res.Configuration
import android.net.Uri
import android.view.DragEvent
import android.view.View
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onNode
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performKeyInput
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.core.content.FileProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import java.io.IOException
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertSame
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatComposerService
import persona.core.contracts.api.ComposerDelivery
import persona.core.contracts.api.DraftUploadState
import persona.core.designsystem.LocalLargeWindowInput
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
class ComposerDropInstrumentedTest {
    @get:Rule val rule = createAndroidComposeRule<ComposerInstrumentationActivity>()

    private val instrumentation = InstrumentationRegistry.getInstrumentation()
    private val created = mutableListOf<File>()

    @After fun removeSources() {
        created.forEach(File::delete)
    }

    private fun source(name: String, words: String): Uri {
        val file = File(File(rule.activity.cacheDir, "chat-captures").apply { mkdirs() }, name)
        file.writeText(words)
        created += file
        return FileProvider.getUriForFile(rule.activity, "${rule.activity.packageName}.chat.files", file)
    }

    private fun show(
        state: ChatComposerState,
        service: ChatComposerService?,
        large: () -> Boolean = { true },
        enabled: Boolean = true,
        onSend: () -> Unit = {},
    ) {
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides large()) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    LargeWindowComposer(SessionKey("box", "s1"), state, working = false, sending = false,
                        canClear = false, service = service, onSend = onSend, onCommand = {}, onNotice = {},
                        enabled = enabled)
                }
            }
        }
    }

    private fun dispatch(action: Int, description: ClipDescription, data: ClipData?): Boolean {
        val center = rule.onNodeWithTag("composer-drop-target").fetchSemanticsNode().boundsInRoot.center
        var accepted = false
        instrumentation.runOnMainSync {
            val event = newDragEvent(action, center.x, center.y, description, data, action == DragEvent.ACTION_DRAG_ENDED)
            accepted = rule.activity.findViewById<View>(android.R.id.content).dispatchDragEvent(event)
        }
        return accepted
    }

    /** DragEvent's factory is hidden from the SDK; instrumentation still constructs the framework's real event type. */
    private fun newDragEvent(
        action: Int,
        x: Float,
        y: Float,
        description: ClipDescription,
        data: ClipData?,
        result: Boolean,
    ): DragEvent {
        val obtain = DragEvent::class.java.getDeclaredMethod("obtain", Int::class.javaPrimitiveType,
            Float::class.javaPrimitiveType, Float::class.javaPrimitiveType, ClipDescription::class.java,
            ClipData::class.java, Any::class.java, Boolean::class.javaPrimitiveType)
        obtain.isAccessible = true
        return obtain.invoke(null, action, x, y, description, data, null, result) as DragEvent
    }

    private fun drop(
        description: ClipDescription,
        data: ClipData,
        expectHighlight: Boolean = true,
    ): Boolean {
        dispatch(DragEvent.ACTION_DRAG_STARTED, description, null)
        dispatch(DragEvent.ACTION_DRAG_ENTERED, description, null)
        dispatch(DragEvent.ACTION_DRAG_LOCATION, description, null)
        if (expectHighlight) {
            rule.onNodeWithTag("composer-drop-target").assert(SemanticsMatcher.expectValue(
                SemanticsProperties.StateDescription, rule.activity.getString(R.string.chat_composer_drop_target)))
        }
        val accepted = dispatch(DragEvent.ACTION_DROP, description, data)
        dispatch(DragEvent.ACTION_DRAG_ENDED, description, null)
        return accepted
    }

    @Test fun realDragEventsImportMultipleFilesAndSendingCleansOnlyTheirCopies() {
        val state = ChatComposerState()
        val service = RecordingComposerService()
        var sent = 0
        show(state, service, onSend = {
            sent++
            state.clearIfUnchanged(state.draft)
        })
        val description = ClipDescription("files", arrayOf("application/octet-stream"))
        val data = ClipData(description, ClipData.Item(source("one.txt", "one"))).apply {
            addItem(ClipData.Item(source("two.txt", "two")))
        }
        assertTrue(drop(description, data))
        rule.waitUntil(timeoutMillis = 10_000) {
            state.attachments.size == 2 && state.attachments.all { it.path != null } && service.snapshot().size == 2
        }
        assertEquals(listOf("one.txt", "two.txt"), service.snapshot().map { it.name })
        assertEquals(listOf("one", "two"), service.snapshot().map { it.bytes.decodeToString() })
        val owned = state.attachments.map { it.uri }

        rule.onNode(hasSetTextAction()).performClick()
        rule.onNode(hasSetTextAction()).performKeyInput {
            withKeyDown(Key.CtrlLeft) { pressKey(Key.Enter) }
        }
        assertEquals(1, sent)
        assertTrue(state.attachments.isEmpty())
        rule.waitUntil(timeoutMillis = 5_000) { owned.all { !readable(rule.activity, it) } }
        assertEquals(2, service.snapshot().size)
    }

    @Test fun failedUploadUsesTheExistingRetryRowWithoutAnotherImport() {
        val state = ChatComposerState()
        val service = RecordingComposerService().apply { failures.set(1) }
        show(state, service)
        val description = ClipDescription("files", arrayOf("application/octet-stream"))
        val data = ClipData(description, ClipData.Item(source("retry.txt", "retry")))
        assertTrue(drop(description, data))
        rule.waitUntil(timeoutMillis = 10_000) {
            state.attachments.singleOrNull()?.uploadState == DraftUploadState.Failed
        }
        val id = state.attachments.single().id
        rule.onNodeWithText(rule.activity.getString(R.string.chat_attach_retry)).performClick()
        rule.waitUntil(timeoutMillis = 10_000) {
            state.attachments.singleOrNull()?.path != null && service.snapshot().size == 2
        }
        assertEquals(id, state.attachments.single().id)
    }

    @Test fun rotationDuringUploadKeepsTheAttachmentAndOneOperation() {
        val state = ChatComposerState()
        val service = RecordingComposerService().apply { gate = CompletableDeferred() }
        show(state, service)
        val description = ClipDescription("files", arrayOf("application/octet-stream"))
        val data = ClipData(description, ClipData.Item(source("rotate.txt", "rotate")))
        assertTrue(drop(description, data))
        rule.waitUntil(timeoutMillis = 10_000) { service.snapshot().size == 1 }
        val id = state.attachments.single().id
        val activity = rule.activity
        val original = activity.resources.configuration.orientation
        val originalRequest = if (original == Configuration.ORIENTATION_PORTRAIT)
            ActivityInfo.SCREEN_ORIENTATION_PORTRAIT else ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
        val other = if (originalRequest == ActivityInfo.SCREEN_ORIENTATION_PORTRAIT)
            ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE else ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
        instrumentation.runOnMainSync { activity.requestedOrientation = other }
        instrumentation.waitForIdleSync()
        assertSame(activity, rule.activity)
        assertEquals(1, service.snapshot().size)
        instrumentation.runOnMainSync { activity.requestedOrientation = originalRequest }
        instrumentation.waitForIdleSync()
        assertSame(activity, rule.activity)
        assertEquals(id, state.attachments.single().id)
        assertEquals(1, service.snapshot().size)

        service.gate!!.complete(Unit)
        rule.waitUntil(timeoutMillis = 10_000) { state.attachments.single().path != null }
        assertEquals(1, service.snapshot().size)
    }

    @Test fun largeWindowFlagChangesDoNotRestartUploadOrLoseDropCleanup() {
        val state = ChatComposerState().apply { value = TextFieldValue("draft", TextRange(3)) }
        val service = RecordingComposerService().apply { gate = CompletableDeferred() }
        var large by mutableStateOf(true)
        show(state, service, large = { large })
        val description = ClipDescription("files", arrayOf("application/octet-stream"))
        val data = ClipData(description, ClipData.Item(source("resize.txt", "resize")))
        assertTrue(drop(description, data))
        rule.waitUntil(timeoutMillis = 10_000) { service.snapshot().size == 1 }
        val id = state.attachments.single().id
        val owned = state.attachments.single().uri
        val selection = state.value.selection
        repeat(2) {
            large = false
            rule.waitForIdle()
            rule.onNodeWithTag("composer-drop-target").assertDoesNotExist()
            large = true
            rule.waitForIdle()
            assertEquals(1, service.snapshot().size)
            assertEquals(id, state.attachments.single().id)
            assertEquals(selection, state.value.selection)
        }
        service.gate!!.complete(Unit)
        rule.waitUntil(timeoutMillis = 10_000) { state.attachments.single().path != null }
        assertEquals(1, service.snapshot().size)

        val otherUri = source("other.txt", "other").toString()
        state.attachments = state.attachments + ComposerAttachment("other", "other.txt", otherUri, false,
            path = "/remote/other.txt", uploadState = DraftUploadState.Uploaded)
        rule.waitForIdle()
        large = false
        rule.waitForIdle()
        state.attachments = emptyList()
        rule.waitUntil(timeoutMillis = 5_000) { !readable(rule.activity, owned) }
        assertTrue(readable(rule.activity, otherUri))
        large = true
        rule.waitForIdle()
        assertFalse(readable(rule.activity, owned))
        assertTrue(readable(rule.activity, otherUri))
        assertEquals(1, service.snapshot().size)
    }

    @Test fun readOnlyAndPlainTextDropsNeverReachTheUploader() {
        val state = ChatComposerState()
        val service = RecordingComposerService()
        show(state, service, enabled = false)
        val files = ClipDescription("files", arrayOf("application/octet-stream"))
        val fileData = ClipData(files, ClipData.Item(source("readonly.txt", "no")))
        assertFalse(drop(files, fileData, expectHighlight = false))
        assertTrue(service.snapshot().isEmpty())
        assertTrue(state.attachments.isEmpty())
    }

    @Test fun plainTextIsNotConsumedAsAFile() {
        val state = ChatComposerState()
        val service = RecordingComposerService()
        show(state, service)
        val text = ClipDescription("text", arrayOf("text/plain"))
        val data = ClipData(text, ClipData.Item("plain text"))
        drop(text, data, expectHighlight = false)
        assertTrue(service.snapshot().isEmpty())
        assertTrue(state.attachments.isEmpty())
    }

    private fun readable(context: Context, uri: String): Boolean = runCatching {
        context.contentResolver.openInputStream(Uri.parse(uri))?.use { true } == true
    }.getOrDefault(false)
}

private data class RecordedUpload(val name: String, val bytes: ByteArray)

private class RecordingComposerService : ChatComposerService {
    override val deliveries = MutableStateFlow<Map<String, ComposerDelivery>>(emptyMap())
    val failures = AtomicInteger(0)
    var gate: CompletableDeferred<Unit>? = null
    private val lock = Any()
    private val uploads = mutableListOf<RecordedUpload>()

    fun snapshot(): List<RecordedUpload> = synchronized(lock) { uploads.toList() }

    override suspend fun upload(session: SessionKey, name: String, file: File): String {
        val bytes = file.readBytes()
        synchronized(lock) { uploads += RecordedUpload(name, bytes) }
        gate?.await()
        if (failures.getAndUpdate { (it - 1).coerceAtLeast(0) } > 0) throw IOException("synthetic upload failure")
        return "/remote/$name"
    }

    override suspend fun interrupt(session: SessionKey) = Unit

    override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean): ComposerDelivery =
        error("not used by composer drop tests")
}
