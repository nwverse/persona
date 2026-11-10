package persona.android.ui

import android.app.Application
import android.content.ComponentName
import android.graphics.Bitmap
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.hasAnyDescendant
import androidx.compose.ui.test.hasClickAction
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createEmptyComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performSemanticsAction
import androidx.compose.ui.unit.Density
import androidx.compose.ui.unit.dp
import androidx.test.core.app.ActivityScenario
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Shadows
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Interaction
import persona.core.contracts.Session
import persona.core.contracts.api.AssistantManagerChoice
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.contracts.api.HubManager
import persona.core.contracts.api.HubManagers
import persona.core.contracts.api.ManagerCode
import persona.core.contracts.api.ManagerRoster
import persona.core.contracts.api.waiting
import persona.core.designsystem.AppStyle
import persona.core.designsystem.JSidebarColumn
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.NavItem
import persona.core.designsystem.PersonaTheme
import persona.core.designsystem.icons.PhIcons
import persona.feature.assistant.AssistantSidebarContent
import persona.feature.settings.AssistantSwitcher
import java.io.File
import java.time.Instant

/**
 * The sidebar with the assistant switcher at its head (docs/assistant/managers.md "Clients"), the real sidebar and the real
 * switcher as the shell puts them together, in every style, light and dark: closed, open (the roster with its badges,
 * the tick, Default, one offline, one still installing, New assistant and Manage assistants…), after switching to
 * another assistant, on an offline one, and with one assistant; the open roster also in English, in Japanese and at the
 * largest text size. The roster is the mock hub's (`scripts/mock/mock_hub.py`). PNGs go to `build/switcher-shots/` (or
 * `-Dpersona.shots=<dir>`). The plain Application keeps Hilt's PersonaApp out of the test.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(application = Application::class, sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class AssistantSwitcherShotsTest {
    @get:Rule val rule = createEmptyComposeRule()
    private val operations = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    @After fun close() { operations.cancel(); AssistantManagerChoice.reset() }

    private fun ago(minutes: Long) = Instant.now().minusSeconds(minutes * 60).toString()

    // The mock hub's roster: the default on the hub's machine, one on a laptop, one offline, one still installing.
    private val managers = listOf(
        HubManager("assistant", "助手", online = true, isDefault = true, builtin = true, host = "server", os = "linux", deploy = "child", inboxNeedsYou = 12),
        HubManager("m-3fa21c", "工作", online = true, host = "MacBook", os = "darwin", deploy = "service", inboxNeedsYou = 2),
        HubManager("m-8b04d2", "家里", online = false, host = "NAS", os = "linux", deploy = "docker", lastSeen = ago(180)),
        HubManager("m-c1e9f0", "测试", installing = Instant.now().plusSeconds(7 * 60 - 5).toString()),
    )

    private class Fake(var list: List<HubManager>) : HubManagers {
        var codes = mutableListOf<String>()
        override suspend fun roster() = ManagerRoster(list, "assistant")
        override suspend fun create(name: String) = error("not used")
        override suspend fun newCode(id: String): ManagerCode { codes += id; error("no code in this test") }
        override suspend fun rename(id: String, name: String) = error("not used")
        override suspend fun makeDefault(id: String) = error("not used")
        override suspend fun delete(id: String) = error("not used")
    }

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/switcher-shots").also { it.mkdirs() }

    /**
     * A tap on what shows [text], by its click action: Robolectric never finishes the platform ripple of a touch (its
     * sparkles stay on the dark surfaces), and the pictures are of the state, not of the press.
     */
    private fun tap(text: String) = rule.onNode(hasClickAction() and (hasText(text) or hasAnyDescendant(hasText(text))))
        .performSemanticsAction(SemanticsActions.OnClick)

    private fun capture(name: String) {
        rule.waitForIdle()
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        File(out(), "$name.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    // 工作 has one agent waiting on a card: its badge is that and its inbox's two.
    private val sessions = listOf(Session(machineId = "mac", sessionId = "s1", manager = "m-3fa21c"))
    private val cards = listOf(Interaction("c1", "mac", "s1"))

    /** The sidebar as the shell's column draws it (its own surface, the compact rows in Glass), its head the switcher. */
    @Composable
    private fun Sidebar(roster: ManagerRoster, fake: Fake, onManage: () -> Unit) {
        val conversations = listOf(
            Session(machineId = "hub", sessionId = "r1", title = "周报整理", status = "idle", updatedAt = ago(30), assistant = true),
            Session(machineId = "hub", sessionId = "r2", title = "订会议室", status = "idle", updatedAt = ago(90), assistant = true),
        )
        val main = Session(machineId = "hub", sessionId = "main", title = "主对话", status = "idle", updatedAt = ago(2), assistant = true)
        val repository = object : AssistantRepository {
            override val assistant = MutableStateFlow(AssistantSnapshot())
            override suspend fun refresh() = Unit
        }
        CompositionLocalProvider(LocalManagerRoster provides roster) {
            val choice = persona.core.designsystem.currentAssistant()
            // What the shown assistant's page read: its own conversations (another's has none here).
            val snapshot = if (choice.id == "assistant") AssistantSnapshot(conversations = listOf(main) + conversations, mainSession = main.key,
                manager = null, managerId = "assistant") else AssistantSnapshot(manager = choice.id, managerId = choice.id)
            val waiting = roster.waiting(cards, sessions)
            Box(Modifier.testTag("shot").width(400.dp).height(900.dp).background(PersonaTheme.palette.background)) {
                JSidebarColumn({
                    AssistantSidebarContent(snapshot, repository, emptySet(), null, operations, demo = false,
                        onOpen = {}, onAction = {}, onSettings = {}, onInbox = {}, onKnowledge = {}, onTodos = {},
                        todoCount = 2, showTodos = true, inboxCount = if (choice.id == "assistant") 12 else 2,
                        settingsCount = 0, settingsDot = false, settingsBadge = null,
                        pages = listOf(NavItem("machines", stringResource(persona.android.R.string.app_machines), PhIcons.Desktop)),
                        onPage = {}, onTogglePin = {}, onRetired = {},
                        switcher = { head, trailing, fallback ->
                            AssistantSwitcher(fake, waiting, hubOnline = true, onManage = onManage, headModifier = head,
                                ticking = false, trailing = trailing, fallback = fallback)
                        })
                }, width = 328.dp, pageStart = 328.dp, shown = true) {}
            }
        }
    }

    private fun launch(content: @Composable () -> Unit): ActivityScenario<ComponentActivity> {
        val app = ApplicationProvider.getApplicationContext<Application>()
        Shadows.shadowOf(app.packageManager).addActivityIfNotPresent(ComponentName(app, ComponentActivity::class.java))
        return ActivityScenario.launch(ComponentActivity::class.java).also { scenario ->
            scenario.onActivity { activity -> activity.setContent(content = content) }
        }
    }

    @Test fun theSidebarSwitchesAssistantsInEveryStyle() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var roster by mutableStateOf(ManagerRoster(managers, "assistant"))
        var managed = 0
        val fake = Fake(managers)
        AssistantManagerChoice.choose("hub", null, "assistant")
        val scenario = launch {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) { Sidebar(roster, fake) { managed++ } }
        }
        fun each(name: String) {
            for (s in AppStyle.entries) for (d in listOf(false, true)) {
                style = s; dark = d
                capture("switcher-$name-${s.name.lowercase()}-${if (d) "dark" else "light"}")
            }
        }
        rule.waitForIdle()
        // Closed: the current assistant with where it runs; the roster is not there.
        rule.onNodeWithText("在线 · server").assertExists()
        rule.onAllNodesWithText("新建助手").assertCountEquals(0)
        each("closed")
        // Open: every assistant, the default marked, the others' badges, the one installing with its code's time.
        tap("在线 · server")
        rule.onNodeWithText("新建助手").assertExists()
        rule.onNodeWithText("管理助手…").assertExists()
        rule.onNodeWithText("默认").assertExists()
        rule.onNodeWithText("在线 · MacBook").assertExists()
        rule.onNodeWithText("离线 · 最后同步于 3 小时前").assertExists()
        rule.onNodeWithText("安装中 · 码 7 分钟后过期").assertExists()
        rule.onNodeWithText("3").assertExists() // 工作: a card and two in its inbox
        rule.onNodeWithContentDescription("当前").assertExists()
        each("open")
        // One still installing opens its install commands (a new code is asked for), and stays out of the choice.
        tap("测试")
        rule.waitForIdle()
        assertEquals(listOf("m-c1e9f0"), fake.codes)
        assertEquals("assistant", AssistantManagerChoice.current.value.shown)
        // Another assistant: it becomes the current one and the roster closes.
        tap("工作")
        rule.waitForIdle()
        assertEquals("m-3fa21c", AssistantManagerChoice.current.value.shown)
        rule.onNodeWithText("在线 · MacBook").assertExists()
        rule.onAllNodesWithText("新建助手").assertCountEquals(0)
        each("switched")
        // An offline one can be picked too.
        tap("在线 · MacBook")
        tap("家里")
        rule.waitForIdle()
        assertEquals("m-8b04d2", AssistantManagerChoice.current.value.shown)
        rule.onNodeWithText("离线 · 最后同步于 3 小时前").assertExists()
        each("offline")
        // Manage assistants… goes to Settings.
        tap("离线 · 最后同步于 3 小时前")
        tap("管理助手…")
        assertEquals(1, managed)
        // One assistant: the switcher is still there, with New assistant; nothing is marked Default.
        AssistantManagerChoice.choose("hub", null, "assistant")
        roster = ManagerRoster(managers.take(1), "assistant")
        rule.waitForIdle()
        tap("在线 · server")
        rule.onNodeWithText("新建助手").assertExists()
        rule.onAllNodesWithText("默认").assertCountEquals(0)
        each("one")
        assertTrue(out().listFiles().orEmpty().size >= 30)
        scenario.close()
    }

    /**
     * The open roster in English and Japanese, to check the words against the other clients', and at the largest text
     * size (Glass, light).
     */
    private fun openRoster(online: String, words: List<String>, name: String, fontScale: Float = 1f) {
        AssistantManagerChoice.choose("hub", null, "assistant")
        val scenario = launch {
            CompositionLocalProvider(LocalDensity provides Density(LocalDensity.current.density, fontScale)) {
                PersonaTheme(darkTheme = false, reduceMotion = true, style = AppStyle.Glass) {
                    Sidebar(ManagerRoster(managers, "assistant"), Fake(managers)) {}
                }
            }
        }
        rule.waitForIdle()
        tap(online)
        words.forEach { rule.onNodeWithText(it).assertExists() }
        capture(name)
        scenario.close()
    }

    @Config(qualifiers = "en-w400dp-h900dp-xhdpi")
    @Test fun theOpenRosterInEnglish() = openRoster("Online · server",
        listOf("New assistant", "Manage assistants…", "Default", "Online · MacBook", "Installing · code expires in 7 minutes"),
        "switcher-open-glass-light-en")

    @Config(qualifiers = "ja-w400dp-h900dp-xhdpi")
    @Test fun theOpenRosterInJapanese() = openRoster("オンライン · server",
        listOf("新しいアシスタント", "アシスタントを管理…", "デフォルト", "オンライン · MacBook", "インストール中 · コードはあと7分で期限切れ"),
        "switcher-open-glass-light-ja")

    @Test fun theOpenRosterAtTheLargestTextSize() = openRoster("在线 · server",
        listOf("新建助手", "管理助手…", "默认", "在线 · MacBook"), "switcher-open-glass-light-large", fontScale = 2f)
}
