package persona.core.designsystem

import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.test.assertHasClickAction
import androidx.compose.ui.test.assertHasNoClickAction
import androidx.compose.ui.test.assertIsEnabled
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config

/** The composer's parts answer the taps they promise, in every style, and nothing else. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-mdpi")
class ComposerPartsTest {
    @get:Rule val rule = createComposeRule()

    @org.robolectric.annotation.GraphicsMode(org.robolectric.annotation.GraphicsMode.Mode.NATIVE)
    @Test fun systemFontMetricsDriveGrowthWhenLineHeightIsUnspecified() {
        var scale by mutableStateOf(1f)
        var lines = 0
        rule.setContent {
            val density = androidx.compose.ui.platform.LocalDensity.current
            androidx.compose.runtime.CompositionLocalProvider(androidx.compose.ui.platform.LocalDensity provides
                androidx.compose.ui.unit.Density(density.density, scale)) {
                PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                    assertEquals(androidx.compose.ui.unit.TextUnit.Unspecified, PersonaTheme.type.body.lineHeight)
                    lines = rememberComposerGrowLines()
                }
            }
        }
        rule.waitForIdle()
        val normal = lines
        org.junit.Assert.assertTrue(normal in 3..8)
        scale = 2f
        rule.waitForIdle()
        org.junit.Assert.assertTrue(lines in 3..8)
        org.junit.Assert.assertTrue("larger system text must leave fewer visible lines", lines < normal)
    }

    @Test fun photoThumbnailsRemoveAndRetryOnlyWhenTheyShould() {
        var style by mutableStateOf(AppStyle.Glass)
        var state by mutableStateOf(AttachmentState.Failed)
        val taps = mutableListOf<String>()
        rule.setContent {
            PersonaTheme(style = style, reduceMotion = true) {
                AttachmentThumb(null, "照片 1", state = state, onRemove = { taps += "remove" }, onRetry = { taps += "retry" })
            }
        }
        for (each in AppStyle.entries) {
            style = each
            state = AttachmentState.Failed
            taps.clear()
            rule.onNodeWithContentDescription("照片 1，上传失败").performClick()
            rule.onNodeWithContentDescription("移除这张").performClick()
            assertEquals("$each", listOf("retry", "remove"), taps)
            // Uploading, ready or gone: the photo itself does nothing, only the badge removes it.
            state = AttachmentState.Uploading
            rule.onNodeWithContentDescription("照片 1，正在上传").assertHasNoClickAction()
            state = AttachmentState.Ready
            rule.onNodeWithContentDescription("照片 1").assertHasNoClickAction()
            rule.onNodeWithContentDescription("移除这张").assertHasClickAction()
            state = AttachmentState.Invalid
            rule.onNodeWithContentDescription("照片 1，已失效").assertHasNoClickAction()
        }
    }

    @Test fun aReadyPhotoOpensTheEditorAndOneOnItsWayDoesNot() {
        var state by mutableStateOf(AttachmentState.Ready)
        val taps = mutableListOf<String>()
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, reduceMotion = true) {
                AttachmentThumb(null, "照片 1", state = state, onRemove = { taps += "remove" }, onRetry = { taps += "retry" },
                    onEdit = { taps += "edit" })
            }
        }
        rule.onNodeWithContentDescription("照片 1").performClick()
        rule.onNodeWithContentDescription("移除这张").performClick()
        assertEquals(listOf("edit", "remove"), taps)
        state = AttachmentState.Uploading
        rule.onNodeWithContentDescription("照片 1，正在上传").assertHasNoClickAction()
        // A failed one still retries when tapped, not edits.
        taps.clear()
        state = AttachmentState.Failed
        rule.onNodeWithContentDescription("照片 1，上传失败").performClick()
        assertEquals(listOf("retry"), taps)
    }

    @Test fun suggestionsHandBackWhatWasPicked() {
        var picked: ComposerSuggestion? = null
        val clear = ComposerSuggestion("/clear", "清空上下文，开新会话", destructive = true)
        rule.setContent {
            PersonaTheme(style = AppStyle.Board, reduceMotion = true) {
                ComposerSuggestions(listOf(ComposerSuggestion("/compact", "压缩上下文"), clear), onPick = { picked = it })
            }
        }
        rule.onNodeWithContentDescription("/clear，清空上下文，开新会话").performClick()
        assertEquals(clear, picked)
    }

    @Test fun queueRowsHandBackTheirIndexUnlessBusyOrReadOnly() {
        val picked = mutableListOf<Int>()
        var busy by mutableStateOf(false)
        var enabled by mutableStateOf(true)
        rule.setContent {
            PersonaTheme(style = AppStyle.Brief, reduceMotion = true) {
                QueueStrip("排队中 2 条，Claude 忙完会依次处理", listOf("一", "二"), onItem = { picked += it }, busy = busy, enabled = enabled)
            }
        }
        rule.onNodeWithContentDescription("第 2 条：二").assertIsEnabled().performClick()
        assertEquals(listOf(1), picked)
        busy = true
        rule.onNodeWithContentDescription("第 1 条：一").assertIsNotEnabled()
        busy = false
        enabled = false
        rule.onNodeWithContentDescription("第 1 条：一").assertIsNotEnabled()
    }
}
