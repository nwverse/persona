package persona.feature.inbox

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.InboxEventFields
import persona.core.contracts.InboxFollow
import persona.core.designsystem.AppStyle
import persona.core.designsystem.GroupPosition
import persona.core.designsystem.PersonaTheme
import persona.core.domain.inbox.InboxItem
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator
import java.lang.reflect.Proxy

/**
 * The row with `where` and a shared state word: the state sits at the third line's trailing side, so a long one must not
 * take the whole line away from `where · sender: preview` (`spec/fixtures/inbox/row.json`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w360dp-h800dp-xhdpi")
class InboxRowMetaWidthReviewTest {
    @get:Rule val rule = createComposeRule()
    private val repository = Proxy.newProxyInstance(InboxRepository::class.java.classLoader, arrayOf(InboxRepository::class.java)) { proxy, method, args ->
        when (method.name) {
            "equals" -> proxy === args[0]
            "hashCode" -> System.identityHashCode(proxy)
            "toString" -> "MetaWidthRepository"
            else -> error("Unexpected repository access: ${method.name}")
        }
    } as InboxRepository
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    private val meta = "Launch group · Alex: Can we move it to Friday?"

    private fun record() = InboxRecord(
        InboxItem("meta-1", kind = "group", title = "Confirm the launch date", sender = "Alex",
            preview = "Can we move it to Friday?", tier = "action"),
        source = "chat", isGroup = true, groupName = "Launch group", count = 1,
        event = InboxEventFields(where = "Launch group", now = "confirm_reply"),
    )

    @Test fun aLongStateLeavesTheLineSomeRoomRatherThanNothing() {
        var style by mutableStateOf(AppStyle.Board)
        val longWait = record().let {
            it.copy(item = it.item.copy(status = InboxItem.WORKING), event = it.event.copy(
                status = InboxItem.WORKING, now = "waiting",
                follow = InboxFollow(who = "Alex to confirm the date with the whole launch group",
                    next = "ask whether Friday works for everyone"),
            ))
        }
        rule.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, 1f)) {
                PersonaTheme(style = style, darkTheme = false, reduceMotion = true) {
                    Box(Modifier.width(580.dp).background(PersonaTheme.palette.background)) {
                        InboxItemRow(longWait, repository, navigator, ReplyComposers(repository, rememberCoroutineScope()),
                            GroupPosition.Single, done = false, busy = false, onlineSince = null, onDone = {}, onHandOff = {})
                    }
                }
            }
        }
        rule.waitForIdle()
        // The required third line wins as much as it can; whatever it loses is only what the long state needs.
        val node = rule.onAllNodesWithText(meta, useUnmergedTree = true).fetchSemanticsNodes().single()
        val layouts = mutableListOf<androidx.compose.ui.text.TextLayoutResult>()
        node.config[SemanticsActions.GetTextLayoutResult].action!!(layouts)
        assertEquals(1, layouts.single().lineCount)
        assertTrue("the third line must keep visible width", layouts.single().getBoundingBox(0).width > 0f)
    }
}

/** The row's time with a title: same line at the default text size, under the title once the text grows (from 1.5×). */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w360dp-h800dp-xhdpi")
class InboxRowTitleTimeReviewTest {
    @get:Rule val rule = createComposeRule()
    private val repository = Proxy.newProxyInstance(InboxRepository::class.java.classLoader, arrayOf(InboxRepository::class.java)) { proxy, method, args ->
        when (method.name) {
            "equals" -> proxy === args[0]
            "hashCode" -> System.identityHashCode(proxy)
            "toString" -> "TitleTimeRepository"
            else -> error("Unexpected repository access: ${method.name}")
        }
    } as InboxRepository
    private val navigator = object : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }

    // The row formats in the device's zone (utils), so the expected string is computed the same way.
    private val time = displayTime("2026-11-05T14:30:00Z", java.util.Locale.ENGLISH)
    private fun record() = InboxRecord(
        InboxItem("time-1", kind = "group", title = "Approve the spring launch checklist and confirm every owner",
            sender = "Alex", preview = "Can we move it to Friday?", tier = "action", updatedAt = "2026-11-05T14:30:00Z"),
        source = "chat", isGroup = true, groupName = "Launch group", count = 1,
        event = InboxEventFields(where = "Launch group", now = "confirm_reply"),
    )

    @Test fun theTimeSitsUnderTheTitleOnlyOnceTheTextGrows() {
        var scale by mutableStateOf(1f)
        rule.setContent {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, scale)) {
                PersonaTheme(style = AppStyle.Glass, darkTheme = false, reduceMotion = true) {
                    Box(Modifier.width(360.dp).background(PersonaTheme.palette.background)) {
                        InboxItemRow(record(), repository, navigator, ReplyComposers(repository, rememberCoroutineScope()),
                            GroupPosition.Single, done = false, busy = false, onlineSince = null, onDone = {}, onHandOff = {})
                    }
                }
            }
        }
        assertTrue(time.isNotBlank())
        // Default size: one line for the title and the time at its trailing edge, as every row has always been.
        rule.waitForIdle()
        println("TEXTS:" + rule.onAllNodesWithText(record().item.title, substring = true, useUnmergedTree = true).fetchSemanticsNodes().size)
        val titleOne = rule.onAllNodesWithText(record().item.title, useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        val timeOne = rule.onAllNodesWithText(time, useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        assertTrue("the time shares the title's line at 1x: $titleOne / $timeOne", timeOne.top < titleOne.bottom)

        // At 1.5× and more the time moves below the title rather than squeezing its first glyph away.
        rule.runOnIdle { scale = 2f }
        rule.waitForIdle()
        val titleTwo = rule.onAllNodesWithText(record().item.title, useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        val timeTwo = rule.onAllNodesWithText(time, useUnmergedTree = true).fetchSemanticsNodes().single().boundsInRoot
        assertTrue("the time must sit below the title at 2x: $titleTwo / $timeTwo", timeTwo.top >= titleTwo.bottom)
    }
}
