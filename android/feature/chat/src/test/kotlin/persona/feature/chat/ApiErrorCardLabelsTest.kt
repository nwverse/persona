package persona.feature.chat

import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Item
import persona.core.designsystem.PersonaTheme
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.LineItem
import persona.core.domain.lines.chatLines

/** The API error card's detail rows speak the app's language, the session row included (#3847). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ApiErrorCardLabelsTest {
    @get:Rule val rule = createComposeRule()

    private val error = chatLines(listOf(LineItem(Item(id = "e#0", seq = 1, role = "assistant", kind = "text",
        text = "API Error: 502 no available GPU request_id: req_011CUabcdEF session_id: sess_4f2a9c1e",
        ts = "2026-11-02T08:01:05Z")))).lines.last() as ChatLine.ApiError

    private fun openCard() {
        rule.setContent { PersonaTheme(reduceMotion = true) { ApiErrorLine(error) } }
        rule.onNodeWithText("502", substring = true).performClick()
        rule.waitForIdle()
        rule.onNodeWithText("sess_4f2a9c1e").assertExists()
    }

    @Config(qualifiers = "ja")
    @Test fun japanese() {
        openCard()
        rule.onNodeWithText("リクエストID").assertExists()
        rule.onNodeWithText("セッション").assertExists()
        rule.onNodeWithText("時刻").assertExists()
        rule.onNodeWithText("Session").assertDoesNotExist()
        rule.onNodeWithContentDescription("セッションをコピー").assertExists()
    }

    @Test fun chinese() {
        openCard()
        rule.onNodeWithText("会话").assertExists()
        rule.onNodeWithText("Session").assertDoesNotExist()
        rule.onNodeWithContentDescription("复制会话").assertExists()
    }
}
