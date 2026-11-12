package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshots.Snapshot
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import coil3.ImageLoader
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flowOf
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.Subagent
import persona.core.contracts.TaskNote
import persona.core.contracts.WorkflowTool
import persona.core.contracts.api.ChatRepository
import persona.core.contracts.api.ChatSnapshot
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.Tokens
import persona.core.domain.lines.ChatLine
import persona.core.domain.lines.gridAgents
import java.io.File

/**
 * Subagents in an Agent's chat as iOS draws them in Glass (#4339 C2): a running one's card, folded and open (its brief,
 * its report so far, its latest steps, View Subagent); a finished and a failed one as the tool's row; an older hub's
 * background one with how it went; parallel ones in a grid, running and finished. Light and dark; PNGs in
 * `build/subagent-cards-shots/` (or `-Dpersona.shots=<dir>`).
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h2000dp-xhdpi")
class SubagentCardsShotsTest {
    @get:Rule val rule = createComposeRule()
    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)
    private var scene by mutableStateOf("running")

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/subagent-cards-shots").also { it.mkdirs() }

    private fun user(seq: Int, text: String) = Item("u$seq", seq = seq, role = "user", kind = "text", text = text, ts = at(seq))
    private fun use(seq: Int, id: String, name: String, vararg input: Pair<String, String>) = Item("c$seq", seq = seq, role = "assistant",
        kind = "tool_use", toolUseId = id, toolName = name, ts = at(seq), toolInput = buildJsonObject { input.forEach { (k, v) -> put(k, v) } })
    private fun agent(seq: Int, id: String, title: String, prompt: String = "Look into $title and report back.") =
        use(seq, id, "Agent", "description" to title, "prompt" to prompt, "subagent_type" to "general-purpose")
    private fun result(seq: Int, id: String, text: String?, error: Boolean = false, task: String? = null) = Item("r$seq", seq = seq,
        role = "user", kind = "tool_result", toolUseId = id, text = text, isError = error, ts = at(seq),
        task = task?.let { TaskNote(kind = "agent", status = it, title = "Report", toolUseId = id) })
    private fun at(seq: Int) = "2026-11-12T10:%02d:%02dZ".format(seq * 20 / 60, seq * 20 % 60)

    private val listed = mapOf(
        "run" to Subagent("a-run", "s1@a-run", "general-purpose", "Run the unit tests", "run", running = true, steps = 7,
            lastTool = WorkflowTool(name = "Bash", summary = "Running the tests")),
        "old" to Subagent("a-old", "s1@a-old", "Explore", "Map the old screens", "old", running = false, background = true,
            startedAt = "2026-11-12T10:00:00Z", updatedAt = "2026-11-12T10:01:20Z", steps = 7),
        "p" to Subagent("a-p", "s1@a-p", "Explore", toolUseId = "p", running = true, steps = 4, lastTool = WorkflowTool(name = "Read", summary = "Reading Lexer.kt")),
        "q" to Subagent("a-q", "s1@a-q", "Explore", toolUseId = "q", steps = 3),
        "r" to Subagent("a-r", "s1@a-r", "Explore", toolUseId = "r", steps = 5),
        "x" to Subagent("a-x", "s1@a-x", "Explore", toolUseId = "x", steps = 3),
        "y" to Subagent("a-y", "s1@a-y", "Explore", toolUseId = "y", steps = 4),
        "z" to Subagent("a-z", "s1@a-z", "Explore", toolUseId = "z", steps = 5),
    )

    // What the running subagent's own conversation holds: its latest three calls show in its open card.
    private val child = listOf(
        use(0, "k0", "Read", "file_path" to "/repo/settings.gradle.kts"),
        use(1, "k1", "Read", "file_path" to "/repo/core/domain/build.gradle.kts"), result(2, "k1", "ok"),
        use(3, "k2", "Grep", "pattern" to "SubagentStatus"), result(4, "k2", "3 matches"),
        use(5, "k3", "Bash", "command" to "./gradlew :core:domain:test"),
    )

    private val repository = object : ChatRepository {
        override fun observe(session: SessionKey): Flow<ChatSnapshot> = flowOf(ChatSnapshot(items = child, loading = false))
        override suspend fun loadOlder(session: SessionKey) = Unit
        override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int): List<Item> = emptyList()
    }

    private val scenes = mapOf(
        "running" to listOf(
            user(1, "Check the release before tagging it"),
            use(2, "read", "Read", "file_path" to "/repo/CHANGELOG.md"), result(3, "read", "ok"),
            agent(4, "run", "Run the unit tests", "Run the unit tests in core/domain and core/data, then report each failure with its cause."),
            result(5, "run", "Async agent launched successfully.\nagentId: a-run", task = "running"),
            result(6, "run", "Half way: core/domain passed, core/data is running.", task = "running"),
        ),
        "done" to listOf(
            user(1, "Look at the crash and the old screens"),
            agent(2, "done", "Find the crash"), result(3, "done", "The crash comes from a null session in ChatScreen.kt:142."),
            use(4, "sh", "Bash", "command" to "git log -3 --oneline"), result(5, "sh", "ok"),
            agent(6, "bad", "Reproduce on a tablet"), result(7, "bad", "No tablet emulator is installed.", error = true),
            use(8, "sh2", "Bash", "command" to "ls"), result(9, "sh2", "ok"),
            // An older hub: the receipt is the result, a notice says how it went.
            use(10, "old", "Agent", "description" to "Map the old screens", "prompt" to "List the screens", "run_in_background" to "true"),
            result(11, "old", "Async agent launched successfully.\nagentId: a-old"),
            Item("n12", seq = 12, kind = "notice", text = "done", ts = at(12), task = TaskNote(kind = "agent", status = "completed", title = "Map the old screens", toolUseId = "old")),
        ),
        "grid" to listOf(
            user(1, "Review the three modules"),
            agent(2, "p", "Review the parser"), agent(3, "q", "Review the renderer"), agent(4, "r", "Review the docs"),
            result(5, "q", "The renderer is fine."), result(6, "r", "Two broken links in README.md.", error = true),
        ),
        "grid-done" to listOf(
            user(1, "Review the three modules"),
            agent(2, "x", "Review the parser"), agent(3, "y", "Review the renderer"), agent(4, "z", "Review the docs"),
            result(5, "x", "The parser is fine."), result(6, "y", "The renderer is fine."), result(7, "z", "The docs are fine."),
        ),
    )

    @Test fun subagentsReadAsOnIOS() {
        val opened = mutableListOf<SessionKey>()
        val session = SessionKey("box", "s1")
        rule.mainClock.autoAdvance = false // spinners never settle
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                val context = LocalContext.current
                val loader = remember { ImageLoader.Builder(context).build() }
                DisposableEffect(loader) { onDispose { loader.shutdown() } }
                val timeline = remember(scene) { ChatTimeline() }
                val folds = remember(scene) { SubagentFolds() }
                val snapshot = timeline.lines(scenes.getValue(scene), live = scene != "done" && scene != "grid-done", open = setOf("w-u1"))
                val lines = if (style == AppStyle.Glass) gridAgents(snapshot.lines) else snapshot.lines
                val groups = lines.filterIsInstance<ChatLine.Work>().associateBy { it.id }
                val links = ChatSubagents(listed.mapValues { SessionKey("box", it.value.sessionId) }, { opened += it }, listed, repository)
                CompositionLocalProvider(LocalChatTaskIndex provides snapshot.tasks, LocalChatSubagents provides links,
                    LocalSubagentFolds provides folds) {
                    // Each scene starts as the reader would find it: nothing opened in another scene carries over.
                    Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) { key(scene) {
                        lines.forEach { line ->
                            ChatLineContent(line, session, loader, null, onToggle = {}, onFold = {}, folding = emptySet(),
                                foldErrors = emptyMap(), onFullText = {},
                                workRunning = line.workGroup()?.let { groups[it]?.live } == true,
                                lastWorkStep = when (line) {
                                    is ChatLine.Step -> groups[line.groupId]?.steps?.lastOrNull()?.id == line.step.id
                                    is ChatLine.Agents -> groups[line.groupId]?.steps?.lastOrNull()?.id == line.calls.last().id
                                    else -> false
                                })
                        }
                    } }
                }
            }
        }
        frame()
        // Running: its card says what it is doing and how far it got; Starting… nowhere.
        rule.onNodeWithText("Run the unit tests").assertIsDisplayed()
        rule.onNodeWithText("Running the tests… · 7 steps").assertIsDisplayed()
        rule.onNodeWithText("Half way", substring = true).assertDoesNotExist()
        capture("running")
        style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("Run the unit tests").performClick()
        frame()
        rule.onNodeWithText("Run the unit tests in core/domain", substring = true).assertIsDisplayed()
        rule.onNodeWithText("Half way: core/domain passed, core/data is running.").assertIsDisplayed()
        rule.onNodeWithTag("chat.subagent-steps", useUnmergedTree = true).assertIsDisplayed()
        rule.onNodeWithText("./gradlew :core:domain:test", substring = true).assertIsDisplayed()
        rule.onNodeWithText("settings.gradle.kts", substring = true).assertDoesNotExist() // only the latest three: not the first
        rule.onNodeWithText("View Subagent").performClick()
        assertEquals(listOf(SessionKey("box", "s1@a-run")), opened)
        capture("running-open")

        // Finished: the tool's row, as iOS draws it once the subagent reported; an older hub's background one keeps its card.
        scene = "done"; style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("Find the crash").assertIsDisplayed()
        rule.onNodeWithText("Reproduce on a tablet").assertIsDisplayed()
        rule.onAllNodesWithTag("chat.subagent-card", useUnmergedTree = true).assertCountEquals(1)
        rule.onNodeWithText("Done · 7 steps · 1 min").assertIsDisplayed()
        capture("done")

        // Parallel: one grid under "Running 1 agent", in launch order.
        scene = "grid"; style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("Running 1 agent").assertIsDisplayed()
        rule.onNodeWithText("Reading Lexer.kt… · 4 steps").assertIsDisplayed()
        rule.onAllNodesWithTag("chat.subagent-grid", useUnmergedTree = true).assertCountEquals(1)
        capture("grid", glassOnly = true)
        scene = "grid-done"; style = AppStyle.Glass; dark = false
        frame()
        rule.onNodeWithText("Ran 3 agents · 12 steps").assertIsDisplayed()
        listOf("parser", "renderer", "docs").forEach { rule.onNodeWithText("Review the $it").assertIsDisplayed() }
        capture("grid-done", glassOnly = true)
    }

    private fun frame() {
        Snapshot.sendApplyNotifications()
        rule.mainClock.advanceTimeBy(1_000) // a card opening settles
        repeat(3) { rule.mainClock.advanceTimeByFrame() }
    }

    /** Glass light and dark, and (unless [glassOnly]) Brief and Board to show they are as they were. */
    private fun capture(name: String, glassOnly: Boolean = false) {
        val styles = if (glassOnly) listOf(AppStyle.Glass) else AppStyle.entries
        for (s in styles) {
            val corners = mutableListOf<Int>()
            for (d in listOf(false, true)) {
                style = s; dark = d
                frame()
                val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
                corners += bitmap.getPixel(2, 2)
                File(out(), "subagents-$name-${s.name.lowercase()}-${if (d) "dark" else "light"}.png").outputStream()
                    .use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
            }
            assertNotEquals("$s dark capture must differ from light", corners[0], corners[1])
        }
    }
}
