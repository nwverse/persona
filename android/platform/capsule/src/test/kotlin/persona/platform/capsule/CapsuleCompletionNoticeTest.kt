package persona.platform.capsule

import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertHeightIsEqualTo
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.assertWidthIsEqualTo
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.designsystem.AppFeedback
import persona.core.designsystem.LocalAppFeedback
import persona.core.designsystem.CapsuleTokens
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w411dp-h900dp-xhdpi")
class CapsuleCompletionNoticeTest {
    @get:Rule val rule = createComposeRule()

    @Test fun newMessageNoticeLabelsAndRoutesTheCapturedConversationWithoutOpeningAnInput() {
        val notice = CapsuleCompletion("message-2", "machine/chat-2", "New reply", CapsuleNoticeKind.NewMessage)
        var opened: CapsuleCompletion? = null
        rule.setContent { PersonaTheme(reduceMotion = true) { CapsuleCompletionNotice(notice) { opened = it } } }
        rule.onNodeWithText("· 新消息", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("· 已完成", useUnmergedTree = true).assertDoesNotExist()
        rule.onAllNodes(hasSetTextAction()).assertCountEquals(0)
        rule.onNodeWithTag("capsule.completion").performClick()
        assertEquals(notice, opened)
    }

    @Test fun singleLineNoticeKeepsCompletionVisibleAndClicksTheCapturedTargetWithoutAComposer() {
        val completion = CapsuleCompletion("event-version", "qualified-machine/exact-session", "A very long session name that must be truncated before the completion label")
        var opened: CapsuleCompletion? = null
        rule.setContent { PersonaTheme(reduceMotion = true) { CapsuleCompletionNotice(completion) { opened = it } } }
        rule.onNodeWithTag("capsule.completion").assertWidthIsEqualTo(216.dp).assertHeightIsEqualTo(CapsuleTokens.orb)
        rule.onNodeWithText("· 已完成", useUnmergedTree = true).assertIsDisplayed()
        rule.onAllNodes(hasSetTextAction()).assertCountEquals(0)
        rule.onNodeWithTag("capsule.card").assertDoesNotExist()
        rule.onNodeWithTag("capsule.completion").performClick()
        assertEquals(completion, opened)
        assertEquals("qualified-machine/exact-session", opened?.targetId)
    }

    @Test fun navigationFailureUsesTheExistingFeedbackProvider() {
        var failure: Throwable? = null
        val feedback = object : AppFeedback {
            override fun failure(error: Throwable, operation: Boolean): Boolean { failure = error; return true }
            override fun notice(message: String) = true
        }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                CompositionLocalProvider(LocalAppFeedback provides feedback) {
                    CapsuleCompletionNotice(CapsuleCompletion("gone", "removed/session", "Removed task")) {
                        throw IllegalStateException("会话已不在当前连接中")
                    }
                }
            }
        }
        rule.onNodeWithTag("capsule.completion").performClick()
        assertEquals("会话已不在当前连接中", failure?.message)
    }
}
