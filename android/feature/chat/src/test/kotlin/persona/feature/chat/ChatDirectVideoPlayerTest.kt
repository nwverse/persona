package persona.feature.chat

import android.view.View
import android.view.ViewGroup
import androidx.compose.foundation.clickable
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h900dp-mdpi")
class ChatDirectVideoPlayerTest {
    @get:Rule val rule = createComposeRule()

    private lateinit var composeView: View

    @Test fun detectsDirectVideoUrlsWithExtensionsCaseAndQueries() {
        val videoUrls = listOf(
            "http://example.invalid/media/clip.mp4",
            "https://example.invalid/media/CLIP.MP4?token=abc&dl=0",
            "https://example.invalid/media/movie.m4v#t=10",
            "https://example.invalid/media/demo.webm?v=2",
            "https://example.invalid/media/capture.mov",
            "https://example.invalid/media/recording.mkv",
            "https://example.invalid/media/mobile.3gp",
            "https://example.invalid/media/mobile.3gpp",
        )
        videoUrls.forEach { url ->
            assertTrue("Expected direct video URL: $url", isDirectVideoUrl(url))
        }

        val nonVideoUrls = listOf(
            "https://example.invalid/articles/video",
            "https://example.invalid/watch?v=clip.mp4",
            "https://example.invalid/media/clip.mp4.html",
            "https://example.invalid/files/archive.zip",
            "https://example.invalid/files/doc.pdf",
            "https://example.invalid/media/photo.png",
            "file:///sdcard/clip.mp4",
            "javascript:alert(1)",
            "mailto:test@example.invalid",
            "",
        )
        nonVideoUrls.forEach { url ->
            assertFalse("Expected non-video URL: $url", isDirectVideoUrl(url))
        }
    }

    @Test fun extractsReadableVideoTitleAndFormatsTimestamps() {
        assertEquals("clip.mp4", directVideoTitle("http://example.invalid/media/clip.mp4?token=1"))
        assertEquals("my demo.mp4", directVideoTitle("https://example.invalid/v/my%20demo.mp4"))
        assertNull(directVideoTitle("https://example.invalid/"))

        assertEquals("0:00", formatVideoTime(-500))
        assertEquals("0:00", formatVideoTime(0))
        assertEquals("0:09", formatVideoTime(9_400))
        assertEquals("1:05", formatVideoTime(65_000))
        assertEquals("1:01:01", formatVideoTime(3_661_000))
    }

    @Test fun inlineVideoResponseDistinguishesInlineVideoFromAttachmentAndHtml() {
        assertTrue(isInlineVideoResponse(200, "video/mp4", null))
        assertTrue(isInlineVideoResponse(206, "video/mp4; codecs=\"avc1.42E01E\"", "inline; filename=\"clip.mp4\""))
        assertTrue(isInlineVideoResponse(200, "VIDEO/WEBM", "INLINE"))

        assertFalse(isInlineVideoResponse(200, "video/mp4", "attachment; filename=\"clip.mp4\""))
        assertFalse(isInlineVideoResponse(200, "text/html; charset=utf-8", null))
        assertFalse(isInlineVideoResponse(200, "application/octet-stream", null))
        assertFalse(isInlineVideoResponse(404, "video/mp4", null))
        assertFalse(isInlineVideoResponse(302, "video/mp4", null))
    }

    @Test fun probeDirectVideoUrlFollowsRedirectsFallsBackFromHeadAndHonorsAttachment() {
        val requests = mutableListOf<WebDownloadProbeRequest>()
        val prober = WebDownloadProber { req ->
            requests += req
            when (req.url) {
                "https://origin.invalid/share/123" -> WebDownloadProbeResponse(
                    statusCode = 302,
                    location = "https://cdn.invalid/stream/123",
                )
                "https://cdn.invalid/stream/123" -> if (req.method == "HEAD") {
                    WebDownloadProbeResponse(statusCode = 405)
                } else {
                    WebDownloadProbeResponse(
                        statusCode = 206,
                        contentType = "video/mp4",
                        contentDisposition = "inline",
                    )
                }
                "https://origin.invalid/download/123" -> WebDownloadProbeResponse(
                    statusCode = 200,
                    contentType = "video/mp4",
                    contentDisposition = "attachment; filename=\"clip.mp4\"",
                )
                "https://origin.invalid/page" -> WebDownloadProbeResponse(
                    statusCode = 200,
                    contentType = "text/html; charset=utf-8",
                )
                else -> WebDownloadProbeResponse(statusCode = 404)
            }
        }

        val resolved = probeDirectVideoUrl(
            initialUrl = "https://origin.invalid/share/123",
            prober = prober,
            cookieForUrl = { "sid=secret" },
        )
        assertEquals("https://cdn.invalid/stream/123", resolved)
        // Cookie is sent to the initial origin, never leaked across origins.
        assertEquals("sid=secret", requests[0].cookie)
        assertNull(requests[1].cookie)
        assertEquals("HEAD", requests[1].method)
        assertEquals("GET", requests[2].method)

        // Explicit attachment stays on the download path instead of hijacking into the inline video player.
        assertNull(probeDirectVideoUrl("https://origin.invalid/download/123", prober = prober))
        // Normal HTML pages stay in WebView.
        assertNull(probeDirectVideoUrl("https://origin.invalid/page", prober = prober))
    }

    @Test fun directMp4LinkOpensNativeVideoPlayerWithoutCreatingWebView() {
        val downloaded = mutableListOf<ChatWebDownloadRequest>()
        setHost(
            targetUrl = "http://example.invalid/media/sample_clip.mp4",
            prober = ChatDirectVideoProber { null },
            downloader = ChatWebDownloader { _, req, _ ->
                downloaded += req
                ChatWebDownloadResult.Started("sample_clip.mp4", 42L)
            },
        )

        rule.onNodeWithText("Open").performClick()
        rule.waitForIdle()

        rule.onNodeWithTag("chat_direct_video_player").assertIsDisplayed()
        rule.onNodeWithTag("chat_direct_video_controls").assertIsDisplayed()
        rule.onNodeWithTag("chat_direct_video_slider").assertIsDisplayed()
        rule.onNodeWithText("sample_clip.mp4").assertIsDisplayed()
        assertTrue("Direct .mp4 link must not instantiate a WebView", findWebViews(composeView).isEmpty())

        // Tapping download triggers the existing ChatWebDownloader pipeline.
        rule.onNodeWithContentDescription("Download").performClick()
        rule.waitForIdle()
        assertEquals(1, downloaded.size)
        assertEquals("http://example.invalid/media/sample_clip.mp4", downloaded.single().url)

        // Fullscreen toggle opens and exits cleanly.
        rule.onNodeWithContentDescription("Full screen").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("chat_direct_video_fullscreen").assertIsDisplayed()

        rule.onNodeWithContentDescription("Exit full screen").performClick()
        rule.waitForIdle()
        rule.onNodeWithTag("chat_direct_video_player").assertIsDisplayed()
    }

    @Test fun extensionlessInlineVideoLinkSwitchesToNativePlayerWhenProbed() {
        setHost(
            targetUrl = "https://example.invalid/stream/42",
            prober = ChatDirectVideoProber { url -> url },
        )

        rule.onNodeWithText("Open").performClick()
        rule.waitForIdle()

        rule.onNodeWithTag("chat_direct_video_player").assertIsDisplayed()
        assertTrue(findWebViews(composeView).isEmpty())
    }

    @Test fun ordinaryWebPageStaysInWebViewWhenProbeReturnsNull() {
        setHost(
            targetUrl = "https://example.invalid/articles/story",
            prober = ChatDirectVideoProber { null },
        )

        rule.onNodeWithText("Open").performClick()
        rule.waitForIdle()

        assertEquals(1, findWebViews(composeView).size)
    }

    private fun setHost(
        targetUrl: String,
        prober: ChatDirectVideoProber,
        downloader: ChatWebDownloader = ChatWebDownloader { _, _, _ -> ChatWebDownloadResult.Refused },
    ) {
        rule.setContent {
            composeView = LocalView.current
            PersonaTheme {
                CompositionLocalProvider(
                    LocalChatWebFeatures provides NoWebFeatures,
                    LocalChatDirectVideoProber provides prober,
                    LocalChatWebDownloader provides downloader,
                ) {
                    ChatLinkHost {
                        val handler = LocalUriHandler.current
                        Text("Open", Modifier.clickable { handler.openUri(targetUrl) })
                    }
                }
            }
        }
    }

    private fun findWebViews(root: View): List<ChatSelectionWebView> = buildList {
        if (root is ChatSelectionWebView) add(root)
        if (root is ViewGroup) {
            for (i in 0 until root.childCount) addAll(findWebViews(root.getChildAt(i)))
        }
    }
}
