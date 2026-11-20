package persona.feature.chat

import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.chatlist.*
import persona.core.contracts.SessionKey
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.RouteSource

@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
class HandoffReadingPositionTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()

    @Test fun theVisibleRowAndItsPixelOffsetReturnAfterNewMessagesWithEitherBackControl() {
        val source = SessionKey("mac~g00000001", "review")
        val target = SessionKey("mac~g00000001", "build")
        val route = taskHandoffRoute(target, source, "Release review", "editor", assistant = true, embedded = true)
        val readingKey = ChatReadingKey("work-pairing", source.path)
        val positions = mutableMapOf<ChatReadingKey, ChatReadingPosition>()
        val store = object : ChatAnchorStore {
            override fun read(key: ChatReadingKey) = positions[key]
            override fun write(key: ChatReadingKey, position: ChatReadingPosition) { positions[key] = position }
        }
        var away by mutableStateOf(false)
        var rows by mutableStateOf((30 downTo 1).map { "Message $it" })
        lateinit var list: ChatListState
        fun visibleTop() = list.list.layoutInfo.visibleItemsInfo.maxBy { it.index }.let { it.key to it.offset }
        fun back() { away = false }
        rule.setContent {
            PersonaTheme(reduceMotion = true) {
                BackHandler(away, ::back)
                if (away) {
                    HandoffReturnBar(route.source as RouteSource.Handoff, ::back)
                } else {
                    Column(Modifier.width(320.dp)) {
                        list = rememberChatListState(readingKey, store)
                        ChatListEffects(list, rows, ready = true, onFreeze = {}, onFollow = {}, loadOlder = {})
                        ChatList(list, Modifier.height(300.dp)) {
                            items(rows, key = { it }) { row -> Box(Modifier.height(67.dp)) { Text(row) } }
                        }
                    }
                }
            }
        }
        rule.waitForIdle()
        repeat(2) { round ->
            val before = rule.runOnIdle {
                val top = visibleTop()
                assertTrue("The source includes a partially visible row", top.second != 0)
                if (round == 0) assertTrue(list.followsEnd)
                assertTrue(list.saveHandoffPosition())
                assertEquals(top.first, positions[readingKey]?.rowId)
                assertEquals(-top.second, positions[readingKey]?.offsetPx)
                assertFalse(positions.getValue(readingKey).followsEnd)
                away = true
                top
            }
            rule.onNodeWithText("Came from Release review · Tap to return").assertIsDisplayed()
            rule.runOnIdle { rows = listOf("New $round A", "New $round B", "New $round C") + rows }
            if (round == 0) rule.onNodeWithText("Came from Release review · Tap to return").performClick()
            else {
                rule.onNodeWithContentDescription("Hide the way back").performClick()
                rule.onNodeWithText("Came from Release review · Tap to return").assertDoesNotExist()
                rule.runOnUiThread { rule.activity.onBackPressedDispatcher.onBackPressed() }
            }
            rule.waitForIdle()
            rule.runOnIdle { assertEquals(before, visibleTop()); assertFalse(list.followsEnd) }
        }
    }
}
