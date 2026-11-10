package persona.feature.chat

import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.platform.UriHandler
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.click
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.longClick
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.hasAnyAncestor
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.test.swipeDown
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.chatlist.ChatList
import persona.core.chatlist.rememberChatListState
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.designsystem.AppStyle
import persona.core.designsystem.ComposerFrame
import persona.core.designsystem.HomeChrome
import persona.core.designsystem.JPage
import persona.core.designsystem.LocalHomeChrome
import persona.core.designsystem.NavItem
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.icons.PhIcons

/**
 * The conversation's messages under a focused composer (#3056): a tap on a reply's blank width puts the keyboard away,
 * as on the rest of the page; the reply's long press (its menu), its link, the field and a scroll of the list do their
 * own thing and leave it up.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-mdpi")
class ChatKeyboardDismissTest {
    @get:Rule val rule = createComposeRule()

    private var focused by mutableStateOf(false)
    private var style by mutableStateOf(AppStyle.Glass)
    private val opened = mutableListOf<String>()
    private val menu = MessageMenuState()
    private val actions = ChatRowActions(SessionKey("mac", "s1"), fullText = { it.text.orEmpty() }, openPath = null,
        photos = null, pictures = { emptyList() }, openImages = { _, _ -> }, menu = menu, retryLineId = null, onRetry = {})

    private fun setConversation() {
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = style) {
                CompositionLocalProvider(LocalChatRowActions provides actions, LocalUriHandler provides object : UriHandler {
                    override fun openUri(uri: String) { opened += uri }
                }) {
                    var text by remember { mutableStateOf("") }
                    JPage("对话", bottomBar = {
                        ComposerFrame {
                            BasicTextField(text, { text = it }, Modifier.fillMaxWidth().testTag("field").onFocusChanged { focused = it.isFocused })
                        }
                    }) { padding ->
                        ChatList(rememberChatListState(), Modifier.fillMaxSize().testTag("list"),
                            contentPadding = PaddingValues(top = padding.calculateTopPadding(), bottom = padding.calculateBottomPadding())) {
                            item { MessageContent(Item("short", role = "assistant", kind = "text", text = "好的"), user = false) }
                            item { MessageContent(Item("link", role = "assistant", kind = "text", text = "见 [文档](https://example.com/doc)"), user = false) }
                            items(40) { MessageContent(Item("older-$it", role = "assistant", kind = "text", text = "更早的回复 $it"), user = false) }
                        }
                    }
                }
            }
        }
        focusField()
    }

    private fun focusField() {
        rule.onNodeWithTag("field").performTouchInput { click() }
        rule.waitForIdle()
        assertTrue("the field takes focus when tapped", focused)
    }

    @Test fun tapBesideAShortReplyClearsFocusInEveryStyle() {
        setConversation()
        for (each in AppStyle.entries) {
            style = each
            rule.waitForIdle()
            focusField()
            // The reply spans the row; past its words the row is blank.
            rule.onNodeWithText("好的").performTouchInput { click(centerRight.copy(x = centerRight.x + 200.dp.toPx())) }
            rule.waitForIdle()
            assertFalse("a tap on a reply's blank width clears the focus in $style", focused)
            assertNull("a tap opens no menu", menu.target)
        }
    }

    @Test fun longPressOpensTheMenuAndKeepsFocus() {
        setConversation()
        rule.onNodeWithText("好的").performTouchInput { longClick() }
        rule.waitForIdle()
        assertNotNull(menu.target)
        assertEquals("short", menu.target?.id)
        assertTrue(focused)
    }

    // #5724: without it, a screen reader reaches nothing in the menu but "Quote" (and only where quoting is offered).
    @Test fun accessibilityLongClickOpensTheMenu() {
        setConversation()
        rule.onNode(hasAnyDescendant(hasText("好的")) and SemanticsMatcher("long click labelled 更多操作") {
            it.config.getOrNull(SemanticsActions.OnLongClick)?.label == "更多操作"
        }).performSemanticsAction(SemanticsActions.OnLongClick)
        rule.waitForIdle()
        assertEquals("short", menu.target?.id)
    }

    @Test fun linkInAReplyOpensAndKeepsFocus() {
        setConversation()
        // The link's own node: on the JVM a glyph is a pixel or two wide, too narrow to aim at by offset.
        rule.onNode(hasClickAction() and hasAnyAncestor(hasText("见 文档")), useUnmergedTree = true).performTouchInput { click() }
        rule.waitForIdle()
        assertEquals(listOf("https://example.com/doc"), opened)
        assertTrue(focused)
    }

    @Test fun scrollingTheConversationKeepsFocus() {
        setConversation()
        rule.onNodeWithTag("list").performTouchInput { swipeDown() }
        rule.waitForIdle()
        assertTrue(focused)
        assertNull("a scroll opens no menu", menu.target)
    }

    @Test fun tappingTheRealComposerKeepsItsFieldFocused() {
        lateinit var composer: ChatComposerState
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                composer = rememberChatComposerState()
                CompositionLocalProvider(LocalHomeChrome provides HomeChrome(
                    listOf(NavItem("home", "Home", PhIcons.House)), "home", onSelect = {}, leading = {})) {
                    JPage("对话", bottomBar = {
                        ChatComposer(SessionKey("box", "s1"), composer, working = false, sending = false, canClear = false,
                            service = null, onSend = {}, onCommand = {}, onNotice = {}, modeHint = "默认模式",
                            accessory = { Text("建议操作", Modifier.testTag("suggestion")) })
                    }) { _ -> }
                }
            }
        }
        rule.onNodeWithTag("suggestion").assertDoesNotExist()
        rule.onNode(hasSetTextAction()).performTouchInput { click() }
        rule.waitForIdle()
        assertTrue(composer.focused)
        rule.onNodeWithTag("suggestion").assertExists()
        rule.onNode(hasSetTextAction()).performTextInput("a")
        rule.waitForIdle()
        assertEquals("a", composer.value.text)
        assertTrue(composer.focused)
        rule.onNodeWithTag("suggestion").assertExists()
    }
}
