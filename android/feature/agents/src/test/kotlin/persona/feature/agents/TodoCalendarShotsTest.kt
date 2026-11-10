package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Session
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import java.io.File
import java.time.Instant
import java.util.TimeZone

/**
 * The to-dos' month (#2046) on a phone: List | Calendar, November 2026 from Monday with today (the 5th) circled, a
 * day's to-dos in their state colors (overdue red, done faded and struck through), "+3" on a day of five, the days of
 * October and December faded, "2 without a due date" (whose "See list" goes back to the list); and a day's to-dos in the
 * sheet a tap brings up, one of which opens. Every style, light and dark; the list with the switch above it.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h860dp-xhdpi")
class TodoCalendarShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }

    private val zone = TimeZone.getDefault()
    @Before fun inShanghai() = TimeZone.setDefault(TimeZone.getTimeZone("Asia/Shanghai"))
    @After fun back() = TimeZone.setDefault(zone)

    // 17:30 on Thursday, November 5 2026, in the test's zone
    private val now = Instant.parse("2026-11-05T09:30:00Z")
    private val sessions = listOf(
        Session("mac", "s1", title = "改合同", state = "needs_you", updatedAt = "2026-11-05T09:00:00Z"),
        Session("mac", "s2", title = "修登录页", state = "working", updatedAt = "2026-11-05T09:20:00Z"),
    )
    private fun at(local: String) = java.time.LocalDateTime.parse(local).atZone(java.time.ZoneId.of("Asia/Shanghai")).toInstant().toString()
    private val todos = listOf(
        Todo("t1", title = "交周报", dueDate = "2026-11-03", doneAt = at("2026-11-03T17:00")),
        Todo("t2", title = "续域名", dueDate = "2026-11-04", priority = "high"),
        Todo("t3", title = "发登录修复", dueAt = at("2026-11-05T10:00"), agents = listOf(TodoAgent("mac", "s2"))),
        Todo("t4", title = "回复合同邮件", dueDate = "2026-11-05", agents = listOf(TodoAgent("mac", "s1"))),
        Todo("t5", title = "体检预约", dueAt = at("2026-11-06T09:00"), remindAt = at("2026-11-06T08:00")),
        Todo("t6", title = "评审设计稿", dueDate = "2026-11-12"),
        Todo("t7", title = "季度复盘", dueAt = at("2026-11-12T14:00"), priority = "high"),
        Todo("t8", title = "写发布说明", dueAt = at("2026-11-12T16:00"), agents = listOf(TodoAgent("mac", "s2"))),
        Todo("t9", title = "订机票", dueAt = at("2026-11-12T18:00"), remindAt = at("2026-11-12T12:00")),
        Todo("t10", title = "买礼物", dueAt = at("2026-11-12T20:00"), doneAt = at("2026-11-04T20:00")),
        Todo("t11", title = "交房租", dueDate = "2026-11-18"),
        Todo("t12", title = "整理崩溃报告", dueAt = at("2026-11-20T11:00"), doneAt = at("2026-11-02T11:00")),
        Todo("t13", title = "牙医复诊", dueAt = at("2026-11-26T15:30")),
        Todo("t14", title = "年度总结", dueDate = "2026-12-01"),
        Todo("t15", title = "交水电费", dueDate = "2026-10-30", doneAt = at("2026-10-29T10:00")),
        Todo("t16", title = "轮换 API Key"),
        Todo("t17", title = "读那篇论文"),
    )

    private fun shot(scene: String, style: AppStyle, dark: Boolean) {
        var opened: String? = null
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Box(Modifier.testTag("shot").width(400.dp).height(860.dp).background(PersonaTheme.palette.background)) {
                    TodosScreen(todos, sessions, null, onBack = {}, onOpenTodo = { opened = it }, now = now)
                }
            }
        }
        rule.waitForIdle()
        fun has(text: String) = rule.onAllNodesWithText(text, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        if (scene != "list") {
            rule.onNodeWithText("日历").performClick()
            rule.waitForIdle()
            assertTrue("$scene: no month title", has("2026年11月"))
            assertTrue("$scene: no undated bar", has("2 条没有截止"))
        }
        val node = if (scene == "day") {
            rule.onNodeWithTag("todo-calendar-day-2026-11-12").performClick()
            rule.waitForIdle()
            // the whole day, in the month's order: all day first, then by time, done last at its time
            assertTrue("day: no sheet", has("11月12日"))
            for (t in listOf("评审设计稿", "季度复盘", "写发布说明", "订机票", "买礼物")) assertTrue("day: no $t", has(t))
            rule.onNode(isDialog())
        } else rule.onNodeWithTag("shot")
        val bitmap = node.captureToImage().asAndroidBitmap()
        File(out(), "todo-calendar-$scene-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png").outputStream()
            .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        if (scene == "day") {
            rule.onNodeWithText("订机票").performClick()
            rule.waitForIdle()
            assertEquals("t9", opened)
        }
        if (scene == "month") {
            // "See list" goes back to the list
            rule.onNodeWithText("看列表").performClick()
            rule.waitForIdle()
            assertTrue("month: See list did not show the list", rule.onAllNodesWithTag("todos-summary").fetchSemanticsNodes().isNotEmpty())
        }
    }

    @Test fun monthGlass() = shot("month", AppStyle.Glass, false)
    @Test fun monthGlassDark() = shot("month", AppStyle.Glass, true)
    @Test fun monthBrief() = shot("month", AppStyle.Brief, false)
    @Test fun monthBriefDark() = shot("month", AppStyle.Brief, true)
    @Test fun monthBoard() = shot("month", AppStyle.Board, false)
    @Test fun monthBoardDark() = shot("month", AppStyle.Board, true)

    @Test fun dayGlass() = shot("day", AppStyle.Glass, false)
    @Test fun dayGlassDark() = shot("day", AppStyle.Glass, true)
    @Test fun dayBrief() = shot("day", AppStyle.Brief, false)
    @Test fun dayBriefDark() = shot("day", AppStyle.Brief, true)
    @Test fun dayBoard() = shot("day", AppStyle.Board, false)
    @Test fun dayBoardDark() = shot("day", AppStyle.Board, true)

    // The list with List | Calendar above it
    @Test fun listGlass() = shot("list", AppStyle.Glass, false)
    @Test fun listGlassDark() = shot("list", AppStyle.Glass, true)
}
