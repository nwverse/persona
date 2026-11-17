package persona.android.ui

import android.app.Application
import android.graphics.Bitmap
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import org.robolectric.shadows.ShadowLooper
import persona.android.navigation.PaneSplits
import persona.android.navigation.sidebarSplit
import persona.core.contracts.Session
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.designsystem.AppStyle
import persona.core.designsystem.JPage
import persona.core.designsystem.PersonaTheme
import persona.feature.assistant.AssistantSidebarContent
import java.io.File

/**
 * The real Assistant column at the foldable widths, Glass, light and dark.
 * Colour blocks live in SidebarColumnDragShotsTest. These frames are the ones a review looks at.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35])
class ColumnDragRealShotsTest {
    @get:Rule val rule = createComposeRule()

    @Config(qualifiers = "en-w673dp-h951dp-mdpi")
    @Test fun overFoldable() = frames(over = true, prefix = "over", windowDp = 673f, widthPx = 673, heightPx = 951)

    @Config(qualifiers = "en-w951dp-h673dp-mdpi")
    @Test fun besideFoldable() = frames(over = false, prefix = "beside", windowDp = 951f, widthPx = 951, heightPx = 673)

    private fun frames(over: Boolean, prefix: String, windowDp: Float, widthPx: Int, heightPx: Int) {
        val choice = AssistantManagerChoice.current.value
        val main = Session("computer", "assistant", title = "Main conversation", assistant = true)
        val recent = main.copy(sessionId = "recent", title = "Weekend plans", updatedAt = "2026-11-12T09:00:00Z")
        val snapshot = AssistantSnapshot(
            mainSession = main.key, conversations = listOf(main, recent),
            manager = choice.manager, managerId = choice.shown,
        )
        val repository = object : AssistantRepository {
            override val assistant = MutableStateFlow(snapshot)
            override suspend fun refresh() = Unit
        }
        var dark by mutableStateOf(false)
        var shown by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = AppStyle.Glass, darkTheme = dark, reduceMotion = false) {
                val scope = rememberCoroutineScope()
                AssistantPaneFrame(
                    regular = true, showSidebar = true, shown = shown, overlay = over, drags = true,
                    detailAtRoot = true, hinges = emptyList(), onDragSettle = { shown = it },
                    modifier = Modifier.testTag("shot"),
                    sidebar = {
                        AssistantSidebarContent(
                            snapshot, repository, main.key, scope, demo = true,
                            onOpen = {}, onAction = {}, onSettings = {}, onInbox = {}, onKnowledge = {},
                            onTodos = null, todoCount = 0, showTodos = false, inboxCount = 0,
                            settingsCount = 0, settingsDot = false, settingsBadge = null,
                            pages = emptyList(), onPage = {}, onRetired = {},
                            fullWindowSearch = true,
                            onFoldColumn = if (over) null else ({ shown = false }),
                        )
                    },
                ) {
                    JPage("Assistant") { padding ->
                        Box(Modifier.fillMaxSize().padding(padding)) {
                            Text("The trip outline is ready.")
                        }
                    }
                }
            }
        }
        val out = File("build/column-drag-real").also { it.mkdirs() }
        println("column-drag-real ${out.absolutePath}")
        val columnDp = if (over) PaneSplits.DefaultLeft else sidebarSplit(windowDp, emptyList()).sidebar
        val columnPx = with(rule.density) { columnDp.dp.toPx() }
        for (isDark in listOf(false, true)) {
            val mode = if (isDark) "dark" else "light"
            rule.mainClock.autoAdvance = true
            rule.runOnIdle { dark = isDark; shown = false }
            rule.waitForIdle()
            rule.onNodeWithText("Weekend plans").assertDoesNotExist()
            rule.onNodeWithText("The trip outline is ready.").assertExists()
            val start = save(out, "$prefix-$mode-start")
            assertEquals("$prefix $mode width", widthPx, start.width)
            assertEquals("$prefix $mode height", heightPx, start.height)
            val drag = columnDrag()
            assertEquals("Idle", drag.step())
            rule.runOnIdle { drag.begin(open = true, over = over, widthPx = columnPx) }
            var i = 0
            while (i < 16 && drag.step() != "Follow") {
                rule.onNodeWithTag("shot").captureToImage()
                rule.waitForIdle()
                ShadowLooper.idleMainLooper()
                i++
            }
            assertEquals("$prefix $mode stuck at ${drag.step()}", "Follow", drag.step())
            rule.runOnIdle { drag.move(columnPx / 2f) }
            rule.waitForIdle()
            rule.onNodeWithText("Weekend plans").assertExists()
            val mid = save(out, "$prefix-$mode-mid")
            assertTrue("$prefix $mode mid matches start", !mid.sameAs(start))
            rule.mainClock.autoAdvance = false
            rule.runOnIdle {
                drag.end(2000f)
                Snapshot.sendApplyNotifications()
            }
            var frames = 0
            while (frames < 80 && drag.step() != "Fade") {
                rule.mainClock.advanceTimeByFrame()
                ShadowLooper.idleMainLooper()
                frames++
            }
            assertEquals("$prefix $mode did not release (${drag.step()})", "Fade", drag.step())
            save(out, "$prefix-$mode-release")
            var fade = 0
            while (fade < 40 && drag.step() != "Idle") {
                rule.mainClock.advanceTimeByFrame()
                ShadowLooper.idleMainLooper()
                fade++
            }
            assertEquals("Idle", drag.step())
            assertTrue("$prefix $mode stayed shut", shown)
            rule.mainClock.autoAdvance = true
            rule.runOnIdle { drag.begin(open = false, over = over, widthPx = columnPx) }
            var shut = 0
            while (shut < 16 && drag.step() != "Follow") {
                rule.onNodeWithTag("shot").captureToImage()
                rule.waitForIdle()
                ShadowLooper.idleMainLooper()
                shut++
            }
            assertEquals("$prefix $mode shut stuck at ${drag.step()}", "Follow", drag.step())
            rule.runOnIdle { drag.move(-columnPx / 2f) }
            rule.waitForIdle()
            save(out, "$prefix-$mode-shut")
            rule.mainClock.autoAdvance = false
            rule.runOnIdle {
                drag.end(-2000f)
                Snapshot.sendApplyNotifications()
            }
            var shutFrames = 0
            while (shutFrames < 80 && drag.step() != "Idle") {
                rule.mainClock.advanceTimeByFrame()
                ShadowLooper.idleMainLooper()
                shutFrames++
            }
            assertEquals("Idle", drag.step())
            assertTrue("$prefix $mode shut did not close", !shown)
        }
    }

    private fun save(out: File, name: String): Bitmap {
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out, "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
        println("wrote ${File(out, "$name.png").absolutePath} ${bitmap.width}x${bitmap.height}")
        return bitmap
    }
}

/** The design-system controller is internal to its module. Tests drive the same begin / move / end the gesture uses. */
private class ColumnDragHandle(private val controller: Any) {
    private val type = controller.javaClass
    fun step(): String = type.getMethod("getStep").invoke(controller).toString()
    fun begin(open: Boolean, over: Boolean, widthPx: Float) {
        val zoneType = Class.forName("persona.core.designsystem.ColumnDragZone")
        val zone = zoneType.enumConstants.first { it.toString() == if (open) "Open" else "Close" }
        type.getMethod("begin", zoneType, java.lang.Boolean.TYPE, java.lang.Float.TYPE)
            .invoke(controller, zone, over, widthPx)
    }
    fun move(dxPx: Float) {
        type.getMethod("move", java.lang.Float.TYPE).invoke(controller, dxPx)
    }
    fun end(velocityPxPerSec: Float) {
        type.getMethod("end", java.lang.Float.TYPE).invoke(controller, velocityPxPerSec)
    }
}

private fun columnDrag(): ColumnDragHandle {
    val holder = Class.forName("persona.core.designsystem.SidebarColumnDragKt")
    val getter = holder.methods.first { it.name.startsWith("getColumnDragControllerForTest") && it.parameterCount == 0 }
    val controller = getter.invoke(null) ?: error("missing column drag controller")
    return ColumnDragHandle(controller)
}
