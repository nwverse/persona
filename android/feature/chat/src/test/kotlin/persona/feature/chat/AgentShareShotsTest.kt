package persona.feature.chat

import android.graphics.Bitmap
import android.graphics.Canvas
import android.view.View
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.*
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.designsystem.*
import java.io.File

/** Real Compose sheets, captured with Robolectric native graphics, not an emulator or physical device. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h1800dp-xhdpi")
class AgentShareShotsTest {
    @get:Rule val rule = createComposeRule()

    private class PreviewApi : ShareModelApi() {
        init {
            share = syntheticShare().copy(selection = ShareSelectionSummary("individual", 4),
                includeFuture = true, publicationStatus = "paused", pauseReason = "source_incomplete",
                createdAt = "2026-11-10T12:00:00Z", updatedAt = "2026-11-10T12:00:00Z", expiresAt = "2026-11-13T12:00:00Z")
            liveShares = listOf(share, share.copy(shareId = "live", showTools = true, includeFuture = true, publicationStatus = "live"),
                share.copy(shareId = "revoked", status = "revoked", revision = 2))
            insecureTransport = true
        }
        override suspend fun catalog(snapshotId: String?, cursor: String?) = ShareCatalog(
            canShare = true, canIncludeFuture = true, snapshotId = "synthetic-snapshot", snapshotVersion = "v1",
            expiresAt = "2099-01-01T00:00:00Z", complete = true, totalMessages = 4, omittedMessages = 1,
            omissions = listOf("unknown_provenance", "unfinished_turn"), messages = listOf(
                ShareCatalogMessage("first", "user", "Review this synthetic release checklist.", true),
                ShareCatalogMessage("second", "assistant", "The checklist is ready for review.", true),
                ShareCatalogMessage("third", "user", "Include the verification result.", true),
                ShareCatalogMessage("fourth", "assistant", "Verification passed. The checklist is complete.", true),
            ),
        )
        private var previewCount = 0
        var warning: String? = "HTTP sends the bearer link and shared content without encryption. A network observer may obtain them."
        override suspend fun preview(request: SharePreviewRequest): SharePreview {
            previewRequests += request
            val messages = catalog().messages.let { catalog -> when (val selected = request.selection) {
                null, ShareSelection.All -> catalog
                is ShareSelection.From -> catalog.dropWhile { it.messageId != selected.fromMessageId }
                is ShareSelection.Individual -> catalog.filter { it.messageId in selected.messageIds }
            } }.map { ShareMessage(it.messageId, it.role, it.text) }
            previewCount = messages.size
            return SharePreview("synthetic-preview-${previewRequests.size}", "2099-01-01T00:00:00Z", 1, previewCount, 1,
                messages = messages, requiresInsecureTransportAck = true, transportWarning = warning)
        }
        override suspend fun create(request: ShareCreateRequest): ShareCreated {
            val result = super.create(request)
            val mode = when (request.selection) {
                ShareSelection.All -> "all"; is ShareSelection.From -> "from"; is ShareSelection.Individual -> "individual"
            }
            return ShareCreated(share.copy(shareId = "created", includeFuture = request.includeFuture,
                publicationStatus = "frozen", pauseReason = null, selection = ShareSelectionSummary(mode, previewCount)), result.publicUrl)
        }
    }

    private fun shot(style: AppStyle, dark: Boolean) {
        val api = PreviewApi()
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.fillMaxSize().background(PersonaTheme.palette.background)) {
                    AgentShareSheet(SessionKey("synthetic-machine", "synthetic-session"), remember { api }, {})
                }
            }
        }
        rule.onAllNodesWithText("4 selected messages").assertCountEquals(3)
        rule.onNodeWithText("Existing links cannot be retrieved.", substring = true).assertExists()
        rule.onNodeWithText("Execution shown · Adds new messages", substring = true).assertExists()
        rule.onAllNodesWithText("Execution hidden · No new messages", substring = true).assertCountEquals(2)
        capture("list", style, dark)
        rule.onNodeWithText("New link").performClick()
        rule.onNodeWithText("Content that can't be shared").assertExists()
        rule.onNodeWithText("Messages whose sender cannot be verified", substring = true).assertExists()
        rule.onNodeWithText("All history · 4 selected").assertExists()
        // Pick two messages in the real full-screen catalog.
        rule.onNodeWithText("Choose history").performScrollTo().performClick()
        rule.onNodeWithText("Choose messages").performClick()
        rule.onNodeWithText("Review this synthetic release checklist.").performClick()
        rule.onNodeWithText("Verification passed. The checklist is complete.").performClick()
        capture("selection", style, dark)
        rule.onNodeWithText("Done").performScrollTo().performClick()
        rule.onNodeWithText("Choose messages · 2 selected").assertExists()
        rule.onNodeWithText("Prepare preview").performScrollTo().performClick()
        rule.onNodeWithText("Review preview").assertExists()
        rule.onNodeWithText("2 selected · 1 omitted").assertExists()
        rule.onNodeWithText("HTTP sends the bearer link", substring = true).assertExists()
        rule.onNodeWithText("Create link").assertIsNotEnabled()
        capture("settings", style, dark)
        // Drive the actual full-screen preview and consent controls, rather than hand-drawing a lookalike.
        rule.onNodeWithText("Review preview").performScrollTo().performClick()
        rule.onNodeWithText("Verification passed. The checklist is complete.").assertExists()
        rule.onNodeWithText("The checklist is ready for review.").assertDoesNotExist()
        rule.onNodeWithText("Include the verification result.").assertDoesNotExist()
        rule.onNodeWithText("Done").performScrollTo().performClick()
        rule.onNodeWithText("I reviewed this exact preview").performScrollTo().performClick()
        rule.onNodeWithText("Create link").assertIsNotEnabled()
        rule.onNodeWithText("I accept unencrypted sharing").performScrollTo().performClick()
        rule.onNodeWithText("Create link").assertIsEnabled()
        assertEquals(0, api.createCount)
        rule.onNodeWithText("Create link").performScrollTo().performClick()
        rule.onNodeWithText("Link created").assertExists()
        rule.onNodeWithText("http://share.example.test/share/", substring = true).assertExists()
        assertEquals(1, api.createCount)
        assertEquals(ShareSelection.Individual(listOf("first", "fourth")), api.createRequest!!.selection)
        assertTrue(api.createRequest!!.acknowledgement.insecureTransport)
        capture("created", style, dark)
        rule.onNodeWithText("Existing links").performClick()
        rule.onAllNodesWithText("4 selected messages")[0].performClick()
        rule.onNodeWithText("Revoke link").assertExists()
        rule.onNodeWithText("Resume future messages").assertExists()
        rule.onNodeWithText("Save changes").assertIsNotEnabled()
        capture("editor", style, dark)
        rule.onNodeWithText("Resume future messages").performScrollTo().performClick()
        rule.onNodeWithText("Save changes").assertIsNotEnabled()
        rule.onNodeWithText("Prepare preview").performScrollTo().performClick()
        rule.onNodeWithText("4 selected · 1 omitted").assertExists()
        rule.onNodeWithText("I reviewed this exact preview").performScrollTo().performClick()
        rule.onNodeWithText("Save changes").assertIsNotEnabled()
        rule.onNodeWithText("I accept unencrypted sharing").performScrollTo().performClick()
        rule.onNodeWithText("Save changes").performScrollTo().performClick()
        assertEquals(true, api.updateRequest!!.includeFuture)
        assertEquals(1L, api.updateRequest!!.expectedContentVersion)
        assertEquals("synthetic-preview-2", api.updateRequest!!.acknowledgement!!.previewId)
        assertTrue(api.updateRequest!!.acknowledgement!!.insecureTransport)
    }

    /** Draw every actual window, including the sheet decor: captureToImage can crop the Activity for dialogs. */
    @Suppress("UNCHECKED_CAST")
    private fun capture(scene: String, style: AppStyle, dark: Boolean) {
        rule.waitForIdle()
        val bitmap = rule.runOnIdle {
            val global = Class.forName("android.view.WindowManagerGlobal")
            val instance = global.getMethod("getInstance").invoke(null)
            val views = global.getDeclaredField("mViews").apply { isAccessible = true }.get(instance) as List<View>
            assertTrue("No real sheet window", views.size > 1)
            Bitmap.createBitmap(views.first().width, views.first().height, Bitmap.Config.ARGB_8888).also { image ->
                val canvas = Canvas(image)
                views.forEach { view ->
                    val position = IntArray(2).also { view.getLocationOnScreen(it) }
                    canvas.save()
                    canvas.translate(position[0].toFloat(), position[1].toFloat())
                    view.draw(canvas)
                    canvas.restore()
                }
            }
        }
        val background = bitmap.getPixel(1, 1)
        var different = 0
        for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) {
            if (bitmap.getPixel(x, y) != background) different++
        }
        assertTrue("Empty native preview", different > 100)
        val directory = File("build/agent-share-shots").also { it.mkdirs() }
        val file = File(directory, "$scene-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png")
        file.outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        val opposite = File(directory, "$scene-${style.name.lowercase()}-${if (dark) "light" else "dark"}.png")
        if (opposite.exists()) assertFalse("Light and dark previews are identical", file.readBytes().contentEquals(opposite.readBytes()))
    }

    private fun warningFallback(warning: String?) {
        val api = PreviewApi().apply { this.warning = warning }
        rule.setContent {
            PersonaTheme(style = AppStyle.Brief, darkTheme = false, reduceMotion = true) {
                AgentShareSheet(SessionKey("synthetic-machine", "synthetic-session"), remember { api }, {})
            }
        }
        rule.onNodeWithText("New link").performClick()
        rule.onNodeWithText("Prepare preview").performScrollTo().performClick()
        rule.onNodeWithText("This link uses HTTP.", substring = true).assertExists()
        rule.onNodeWithText("I accept unencrypted sharing").assertExists()
        rule.onNodeWithText("Create link").assertIsNotEnabled()
    }

    @Test fun missingTransportWarningUsesLocalizedFallbackWithoutRemovingConsent() = warningFallback(null)
    @Test fun oversizedTransportWarningUsesLocalizedFallbackWithoutRemovingConsent() = warningFallback("x".repeat(1001))

    @Test fun glassLight() = shot(AppStyle.Glass, false)
    @Test fun glassDark() = shot(AppStyle.Glass, true)
    @Test fun briefLight() = shot(AppStyle.Brief, false)
    @Test fun briefDark() = shot(AppStyle.Brief, true)
    @Test fun boardLight() = shot(AppStyle.Board, false)
    @Test fun boardDark() = shot(AppStyle.Board, true)
}
