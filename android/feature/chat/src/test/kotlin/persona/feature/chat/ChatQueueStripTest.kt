package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.api.DraftUploadState

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatQueueStripTest {
    private val res = ApplicationProvider.getApplicationContext<Context>().resources
    @Test fun titleSaysHowManyAndForWhom() {
        assertEquals("排队中 2 条，Claude 忙完会依次处理", queueTitle(2, "Claude", res))
        assertEquals("排队中 1 条，Codex 忙完会依次处理", queueTitle(1, "Codex", res))
    }

    @Test fun pullingOneOutKeepsTheRestInOrder() {
        assertEquals(listOf(1, 2), queueKeep(3, 0))
        assertEquals(listOf(0, 2), queueKeep(3, 1))
        // The only queued message: nothing is put back, and the empty list is what the hub gets.
        assertEquals(emptyList<Int>(), queueKeep(1, 0))
    }

    @Test fun aPulledBackMessageGoesAfterTheDraftOnANewLine() {
        val empty = ChatComposerState()
        empty.restoreQueued("顺便把 README 也更新一下")
        assertEquals("顺便把 README 也更新一下", empty.value.text)
        assertEquals(empty.value.text.length, empty.value.selection.start)

        val typed = ChatComposerState(ChatComposerDraft("先跑测试"))
        typed.restoreQueued("改完以后跑一遍 UI 测试")
        assertEquals("先跑测试\n改完以后跑一遍 UI 测试", typed.value.text)

        // Photos it carried come back as attachments, not as their tag.
        val photos = ChatComposerState(ChatComposerDraft("看下"))
        photos.restoreQueued("截图在这\n\n<attached_images>\n/tmp/shot.png\n</attached_images>")
        assertEquals("看下\n截图在这", photos.value.text)
        assertEquals(listOf("/tmp/shot.png"), photos.attachments.map { it.path })
    }

    @Test fun claudeQueuedPhotosRestoreAfterAnExistingDraftWithoutLosingOtherAttachments() {
        val existing = ComposerAttachment("existing", "original.jpg", "", true, "/tmp/original.jpg",
            uploadState = DraftUploadState.Uploaded)
        val state = ChatComposerState(ChatComposerDraft("Keep this draft", listOf(existing)))
        val video = "<persona-file kind=\"video\" path=\"/tmp/take 1.mov\"/>"
        val context = "<persona-context kind=\"context\" title=\"Notes\">Keep [Image #9] literally</persona-context>"
        val words = "Check these screens $video\n$context"
        state.restoreQueued("[Image #1] [Image #2]\n\n\n$words\n<attached_images>\npath: /tmp/first.jpg\n/tmp/first.jpg\npath: /tmp/second screen.jpg\nnot a path\n</attached_images>")
        assertEquals("Keep this draft\n$words", state.value.text)
        assertEquals(state.value.text.length, state.value.selection.end)
        assertEquals(existing, state.attachments.first())
        assertEquals(listOf("/tmp/original.jpg", "/tmp/first.jpg", "/tmp/second screen.jpg"), state.attachments.map { it.path })
        assertTrue(state.attachments.all { it.uploadState == DraftUploadState.Uploaded })
        assertTrue(state.ready)
        assertEquals("Keep this draft\n$words\n\n<attached_images>\n/tmp/original.jpg\n/tmp/first.jpg\n/tmp/second screen.jpg\n</attached_images>", state.message())
    }

    @Test fun photoOnlyQueueStandInsAreNotRestoredAsUserWords() {
        for (words in listOf("Please look at the image", "Please look at the images", "Please look at the photos",
            "See the images", "请看图片", "画像を見てください")) {
            val state = ChatComposerState().apply { photosOnlyText = "Please look at the photos" }
            state.restoreQueued("[Image #3] $words\n<attached_images>\npath: /tmp/photo.jpg\n</attached_images>")
            assertEquals("", state.value.text)
            assertEquals(listOf("/tmp/photo.jpg"), state.attachments.map { it.path })
            assertEquals("Please look at the photos\n\n<attached_images>\n/tmp/photo.jpg\n</attached_images>", state.message())
            assertEquals("[Photo]", composerMessagePreview("$words\n<attached_images>\npath: /tmp/photo.jpg\n</attached_images>", "[Photo]"))
            val plain = ChatComposerState().apply { restoreQueued(words) }
            assertEquals(words, plain.value.text)
        }
    }

    @Test fun queuePreviewsRemoveOnlyLeadingClaudeImageMarkers() {
        assertEquals("Check these screens\n[Photo]", composerMessagePreview(
            "[Image #1] [Image #2]\n\n\nCheck these screens\n<attached_images>\npath: /tmp/a.jpg\npath: /tmp/b.jpg\n</attached_images>", "[Photo]"))
        val raw = "Keep [Image #7] as text <persona-file kind=\"video\" path=\"/tmp/clip.mov\"/>"
        assertEquals(raw, composerMessagePreview(raw, "[Photo]"))
        val empty = ChatComposerState().apply { restoreQueued("[Image #6]Check the screen\n<attached_images>\n</attached_images>") }
        assertEquals("Check the screen", empty.value.text)
        assertTrue(empty.attachments.isEmpty())
    }
}
