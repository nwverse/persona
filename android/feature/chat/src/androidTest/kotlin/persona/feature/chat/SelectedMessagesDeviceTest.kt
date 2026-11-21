package persona.feature.chat

import android.app.Instrumentation
import android.content.Context
import android.content.Intent
import android.accessibilityservice.AccessibilityService
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.rememberCompositionContext
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.util.concurrent.atomic.AtomicReference

/** Drives production ChatScreen and its attached-window PNG renderer with synthetic history. */
@RunWith(AndroidJUnit4::class)
class SelectedMessagesDeviceTest {
    @get:Rule val rule = createAndroidComposeRule<ComposerInstrumentationActivity>()
    private val session = Session("studio", "trail", title = "Saturday field notes", agent = "claude", status = "idle", state = "idle")
    private val question = "Where should we meet on Saturday?"
    private val reply = "Meet at the east trailhead at nine. Bring the printed map and a water bottle."
    private val rows = listOf(Item("question", 1, "2026-06-09T09:00:00Z", "user", "text", question),
        Item("reply", 2, "2026-06-09T09:01:00Z", "assistant", "text", reply))
    private val transcript = MutableStateFlow(ChatSnapshot(items = rows, loading = false))
    private var wholeReply: String? = null
    private val reads = mutableListOf<String>()
    private val repository = object : ChatRepository {
        override fun observe(session: SessionKey) = transcript
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
        override suspend fun fullText(session: SessionKey, itemId: String): String {
            assertEquals(this@SelectedMessagesDeviceTest.session.key, session)
            reads += itemId
            return requireNotNull(wholeReply)
        }
    }
    private val sessions = object : SessionsRepository {
        override val sessions = MutableStateFlow(listOf(session))
        override val machines = MutableStateFlow(listOf(Machine("studio", "Studio", online = true)))
        override suspend fun refresh() = Unit
    }
    private val interactions = object : InteractionCenter {
        override val pending = MutableStateFlow(emptyList<Interaction>())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }
    private val sender = object : MessageSender {
        override val outbox = MutableStateFlow(emptyList<OutboxMessage>())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = error("no send in export")
    }

    private val evidence get() = File(rule.activity.getExternalFilesDir(null), "picked-messages").apply { mkdirs() }
    private fun capture(name: String) {
        rule.waitForIdle()
        val automation = InstrumentationRegistry.getInstrumentation().uiAutomation
        automation.waitForIdle(350, 3_000)
        val image = automation.takeScreenshot()
        try { File(evidence, "$name.png").outputStream().use { assertTrue(image.compress(Bitmap.CompressFormat.PNG, 100, it)) } }
        finally { image.recycle() }
    }

    private fun showChat(style: AppStyle, dark: Boolean) {
        rule.activity.getSharedPreferences("chat-pick", Context.MODE_PRIVATE).edit().clear().commit()
        val forwarding = ChatForwarding(sessions.sessions, sessions.machines, MutableStateFlow(AssistantSnapshot()), { true },
            { it.title }, { null }, { _, _ -> }, {}, {})
        rule.setContent {
            CompositionLocalProvider(LocalChatForwarding provides forwarding) {
                PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                    Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background)) {
                        ChatScreen(session.key, repository, sender, interactions, sessions, onBack = {})
                    }
                }
            }
        }
    }

    private fun tag(name: String) = rule.onNodeWithTag(name, useUnmergedTree = true)
    private fun selectReply() {
        rule.waitUntil(10_000) { rule.onAllNodesWithText(reply, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText(reply, substring = true, useUnmergedTree = true).performTouchInput { longClick() }
        rule.onNodeWithText(rule.activity.getString(R.string.chat_forward_select_messages)).performClick()
        tag("forward-pick-reply").assertIsOn()
    }

    private fun run(style: AppStyle, dark: Boolean) {
        showChat(style, dark)
        selectReply()
        val suffix = style.name.lowercase() + (if (dark) "-dark" else "-light") +
            (if (rule.activity.resources.configuration.fontScale > 1.5f) "-large" else "")
        tag("pick-questions").assertIsOn()
        tag("forward-picked").assertIsEnabled()
        tag("copy-picked").assertIsEnabled()
        tag("export-picked").assertIsEnabled()
        val selected = rule.onNodeWithText(rule.activity.getString(R.string.chat_forward_selected, 1)).fetchSemanticsNode().boundsInRoot
        assertTrue("The selected count fits on a line at this text size: $selected", selected.width > selected.height)
        capture("pick-$suffix")
        if (style == AppStyle.Glass && !dark) {
            tag("pick-questions").performClick().assertIsOff()
            rule.onNodeWithText(rule.activity.getString(R.string.chat_dialog_cancel)).performClick()
            selectReply()
            tag("pick-questions").assertIsOff().performClick().assertIsOn()
        }
        tag("export-picked").performClick()
        rule.waitUntil(30_000) { rule.onAllNodesWithTag("export-page-1", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        val label = rule.activity.getString(R.string.chat_pick_page, 1, 1)
        rule.waitUntil(10_000) { rule.onAllNodesWithContentDescription(label, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        tag("export-share").assertIsEnabled()
        capture("preview-$suffix")
        val png = File(rule.activity.cacheDir, "shared").listFiles()!!.filter { it.name.startsWith("persona-page-") }.maxBy { it.lastModified() }
        png.copyTo(File(evidence, "image-$suffix.png"), overwrite = true)
        if (style == AppStyle.Board && dark) systemShare(1) { tag("export-share").performClick() }
        if (style == AppStyle.Glass && !dark) {
            rule.onNodeWithText(rule.activity.getString(R.string.chat_pick_heading)).performClick()
            rule.waitUntil(20_000) {
                File(rule.activity.cacheDir, "shared").listFiles().orEmpty().any { it.name.startsWith("persona-page-") && it != png && it.lastModified() > png.lastModified() }
            }
            rule.onNodeWithText(rule.activity.getString(R.string.chat_delegated_close)).performClick()
            tag("copy-picked").performClick()
            rule.waitUntil(10_000) { rule.onAllNodesWithTag("copy-picked").fetchSemanticsNodes().isEmpty() }
            val clipboard = rule.activity.getSystemService(android.content.ClipboardManager::class.java)
            val text = rule.runOnIdle { clipboard.primaryClip?.getItemAt(0)?.text.toString() }
            assertEquals("Me:\n$question\n\nClaude:\n$reply", text)
        }
    }

    @Test fun glassLight() = run(AppStyle.Glass, false)
    @Test fun glassDark() = run(AppStyle.Glass, true)
    @Test fun briefLight() = run(AppStyle.Brief, false)
    @Test fun briefDark() = run(AppStyle.Brief, true)
    @Test fun boardLight() = run(AppStyle.Board, false)
    @Test fun boardDark() = run(AppStyle.Board, true)

    private fun systemShare(pageCount: Int, open: () -> Unit) {
        val instrumentation = InstrumentationRegistry.getInstrumentation()
        val receipt = AtomicReference<Intent>()
        val monitor = object : Instrumentation.ActivityMonitor() {
            override fun onStartActivity(intent: Intent): Instrumentation.ActivityResult? {
                if (intent.action == Intent.ACTION_CHOOSER) receipt.set(intent)
                return null
            }
        }
        instrumentation.addMonitor(monitor)
        try {
            open()
            rule.waitUntil(10_000) { receipt.get() != null }
            val chooser = receipt.get()
            val inner = chooser.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)!!
            assertEquals(if (pageCount == 1) Intent.ACTION_SEND else Intent.ACTION_SEND_MULTIPLE, inner.action)
            val uris = (0 until inner.clipData!!.itemCount).map { inner.clipData!!.getItemAt(it).uri }
            assertEquals(pageCount, uris.size)
            assertTrue(inner.flags and Intent.FLAG_GRANT_READ_URI_PERMISSION != 0)
            uris.forEachIndexed { i, uri ->
                assertTrue(uri.lastPathSegment!!.startsWith("persona-page-${i + 1}-"))
                rule.activity.contentResolver.openInputStream(uri)!!.use { assertEquals(0x89, it.read()) }
            }
            val resolver = chooser.resolveActivity(rule.activity.packageManager)!!.packageName
            rule.waitUntil(10_000) { instrumentation.uiAutomation.rootInActiveWindow?.packageName?.toString() == resolver }
            capture(if (pageCount == 1) "system-share-one" else "system-share-pages")
            instrumentation.uiAutomation.performGlobalAction(AccessibilityService.GLOBAL_ACTION_BACK)
        } finally { instrumentation.removeMonitor(monitor) }
    }

    @Test fun userPhotoAndAgentPictureReachTheActualPng() {
        val blue = android.graphics.Color.rgb(19, 88, 181)
        val green = android.graphics.Color.rgb(24, 175, 71)
        fun png(color: Int): ByteArray {
            val bitmap = Bitmap.createBitmap(48, 32, Bitmap.Config.ARGB_8888).apply { eraseColor(color) }
            return java.io.ByteArrayOutputStream().use { out ->
                bitmap.compress(Bitmap.CompressFormat.PNG, 100, out)
                bitmap.recycle()
                out.toByteArray()
            }
        }
        val files = object : SessionFiles {
            override suspend fun file(machineId: String, cwd: String, path: String): SessionFileContent {
                assertEquals(Triple("studio", "/trail", "/photo/map.png"), Triple(machineId, cwd, path))
                return SessionFileContent(path, "map.png", mime = "image/png", data = java.util.Base64.getEncoder().encodeToString(png(blue)))
            }
            override suspend fun directory(machineId: String, cwd: String, path: String) = error("not used")
            override suspend fun status(machineId: String, cwd: String) = error("not used")
            override suspend fun diff(machineId: String, cwd: String, path: String, staged: Boolean) = error("not used")
            override suspend fun log(machineId: String, cwd: String, skip: Int, limit: Int) = error("not used")
            override suspend fun show(machineId: String, cwd: String, hash: String) = error("not used")
        }
        val source = object : ReplyImageSource {
            override val features = MutableStateFlow(setOf(ReplyImageSource.FEATURE))
            override suspend fun load(url: String, size: Int, allowDirect: Boolean): ByteArray {
                assertEquals("https://example.invalid/trail.png", url)
                assertFalse(allowDirect)
                return png(green)
            }
        }
        val messages = listOf(PickedMessage("map", true, "Me", "My map", null, listOf("/photo/map.png")),
            PickedMessage("trail", false, "Claude", "![Trail](https://example.invalid/trail.png)", null, emptyList(), ownReply = true))
        val result = AtomicReference<Result<List<ExportPage>>>()
        rule.setContent {
            val view = LocalView.current
            val parent = rememberCompositionContext()
            LaunchedEffect(Unit) { result.set(runCatching {
                renderPickedPages(view, parent, AppStyle.Brief, false, messages, "Trail", false,
                    AttachedPhotos(files, "studio", "/trail"), "Claude", source)
            }) }
        }
        rule.waitUntil(30_000) { result.get() != null }
        val page = result.get().getOrThrow().single()
        val bitmap = BitmapFactory.decodeFile(page.file.path)
        val pixels = IntArray(bitmap.width * bitmap.height)
        bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
        bitmap.recycle()
        assertTrue("user photo missing from PNG", pixels.count { it == blue } > 50)
        assertTrue("agent image missing from PNG", pixels.count { it == green } > 50)
    }

    @Test fun overlongClippedReplyPreviewsAndSharesEveryPageInOrder() {
        wholeReply = (1..640).joinToString("\n\n") { "Field observation $it." }
        transcript.value = ChatSnapshot(items = listOf(rows.first(), rows.last().copy(fullSize = wholeReply!!.length)), loading = false)
        val cache = File(rule.activity.cacheDir, "shared")
        val previous = cache.listFiles().orEmpty().toSet()
        showChat(AppStyle.Board, false)
        selectReply()
        tag("export-picked").performClick()
        rule.waitUntil(60_000) { rule.onAllNodesWithTag("export-page-1", useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty() }
        val pages = cache.listFiles().orEmpty().filter { it !in previous && it.name.startsWith("persona-page-") }.sortedBy { it.name }
        assertEquals("heading and question, then two pages of the same reply", 3, pages.size)
        assertEquals(listOf("reply"), reads)
        rule.onNode(hasScrollToIndexAction() and hasAnyAncestor(hasTestTag("export-preview")), useUnmergedTree = true)
            .performScrollToIndex(pages.lastIndex)
        rule.onNodeWithText(rule.activity.getString(R.string.chat_pick_page, pages.size, pages.size)).assertIsDisplayed()
        for ((index, page) in pages.withIndex()) {
            val options = BitmapFactory.Options().apply { inJustDecodeBounds = true }
            BitmapFactory.decodeFile(page.path, options)
            assertEquals(780, options.outWidth)
            assertTrue(options.outHeight in 1..MAX_EXPORT_HEIGHT)
            if (index == 1) assertEquals("the first slice of the overlong reply fills a page", MAX_EXPORT_HEIGHT, options.outHeight)
            assertTrue(page.name.startsWith("persona-page-${index + 1}-"))
            page.copyTo(File(evidence, "long-${index + 1}.png"), overwrite = true)
        }
        capture("long-preview")
        systemShare(pages.size) { tag("export-share").performClick() }
    }
}
