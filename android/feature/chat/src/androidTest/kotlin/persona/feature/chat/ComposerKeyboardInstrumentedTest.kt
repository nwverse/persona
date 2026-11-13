package persona.feature.chat

import android.view.KeyEvent
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.hasSetTextAction
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onNode
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import persona.core.contracts.SessionKey
import persona.core.designsystem.LocalLargeWindowInput
import persona.core.designsystem.PersonaTheme

@RunWith(AndroidJUnit4::class)
class ComposerKeyboardInstrumentedTest {
    @get:Rule val rule = createAndroidComposeRule<ComposerInstrumentationActivity>()
    private val instrumentation = InstrumentationRegistry.getInstrumentation()

    private fun show(state: ChatComposerState, onSend: () -> Unit) {
        rule.setContent {
            CompositionLocalProvider(LocalLargeWindowInput provides true) {
                PersonaTheme(darkTheme = false, reduceMotion = true) {
                    LargeWindowComposer(SessionKey("box", "s1"), state, working = false, sending = false,
                        canClear = false, service = null, onSend = onSend, onCommand = {}, onNotice = {})
                }
            }
        }
        rule.onNode(hasSetTextAction()).performClick()
        rule.waitForIdle()
    }

    private fun enter(action: Int, repeatCount: Int, modifiers: Int) {
        instrumentation.runOnMainSync {
            val event = KeyEvent(0, 0, action, KeyEvent.KEYCODE_ENTER, repeatCount, modifiers)
            val target = rule.activity.currentFocus ?: rule.activity.findViewById(android.R.id.content)
            target.dispatchKeyEvent(event)
        }
    }

    @Test fun realMetaAndCtrlEventsSendOnceWithoutRepeatOrKeyUpDuplicates() {
        val state = ChatComposerState()
        state.insert("draft", atSelection = false)
        var sent = 0
        show(state) { sent++ }
        enter(KeyEvent.ACTION_DOWN, 0, KeyEvent.META_CTRL_ON)
        enter(KeyEvent.ACTION_DOWN, 1, KeyEvent.META_CTRL_ON)
        enter(KeyEvent.ACTION_UP, 0, KeyEvent.META_CTRL_ON)
        assertEquals(1, sent)
        enter(KeyEvent.ACTION_DOWN, 0, KeyEvent.META_META_ON)
        enter(KeyEvent.ACTION_UP, 0, KeyEvent.META_META_ON)
        assertEquals(2, sent)
    }

    @Test fun inputConnectionCompositionCommitsBeforeCtrlReturnCanSend() {
        val state = ChatComposerState()
        var sent = 0
        show(state) { sent++ }
        var connection: InputConnection? = null
        instrumentation.runOnMainSync {
            fun locate(view: View): InputConnection? {
                view.onCreateInputConnection(EditorInfo())?.let { return it }
                if (view is ViewGroup) for (index in 0 until view.childCount) {
                    locate(view.getChildAt(index))?.let { return it }
                }
                return null
            }
            connection = locate(rule.activity.window.decorView.rootView)
        }
        assertNotNull(connection)
        instrumentation.runOnMainSync { connection!!.setComposingText("中文", 1) }
        rule.waitForIdle()
        assertNotNull(state.value.composition)
        enter(KeyEvent.ACTION_DOWN, 0, KeyEvent.META_CTRL_ON)
        enter(KeyEvent.ACTION_UP, 0, KeyEvent.META_CTRL_ON)
        assertEquals(0, sent)
        instrumentation.runOnMainSync { connection!!.finishComposingText() }
        rule.waitForIdle()
        assertNull(state.value.composition)
        enter(KeyEvent.ACTION_DOWN, 0, KeyEvent.META_CTRL_ON)
        enter(KeyEvent.ACTION_UP, 0, KeyEvent.META_CTRL_ON)
        assertEquals(1, sent)
        assertEquals("中文", state.value.text)
    }
}
