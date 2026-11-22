package persona.feature.chat

import android.graphics.Bitmap
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.Button
import androidx.compose.material3.Text
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.isDialog
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.Machine
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.TodoAgent
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi
import persona.core.designsystem.AppStyle
import persona.core.designsystem.PersonaTheme
import persona.core.domain.tasks.TasksStore
import java.io.File

/** Real receipt and task-sheet Compose clicks; the destination/back shell is deliberately a navigation test double. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h1400dp-xhdpi")
class TaskReceiptNavigationTest {
    @get:Rule val rule = createComposeRule()

    private val binding = TodoAgent("mini", "codex", agent = "codex", title = "检查示例项目构建")
    private val todo = Todo("sample-build", "检查示例项目构建", origin = "main",
        createdAt = "2026-11-10T01:00:00Z", agents = listOf(binding))
    private val session = Session(machineId = "mini", sessionId = "codex", title = todo.title, agent = "codex", state = "idle")
    private var destination by mutableStateOf<SessionKey?>(null)
    private var error by mutableStateOf<String?>(null)
    private lateinit var host: AssistantTaskHost
    private lateinit var store: TasksStore
    private lateinit var api: Api
    private lateinit var connection: Connection

    /** Every mutation available to these task controls is recorded so viewing cannot silently change a task. */
    private class Api(val todo: Todo, val receipt: FeedItem) : TasksApi {
        val writes = mutableListOf<String>()
        private fun write(name: String): Nothing { writes += name; error("unexpected mutation: $name") }
        override suspend fun todos() = listOf(todo)
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(listOf(receipt))
        override suspend fun mainConversation(): String? = "main"
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = write("startPlan")
        override suspend fun dismissPlan(id: String): TaskPlan = write("dismissPlan")
        override suspend fun retryTask(todoId: String): Todo = write("retryTask")
        override suspend fun removeTask(todoId: String): Unit = write("removeTask")
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = write("route")
        override suspend fun today(conversation: String): FeedItem = write("today")
        override suspend fun inboxReply(itemId: String, text: String, card: String?): Unit = write("inboxReply")
        override suspend fun acceptTask(todoId: String, askedAt: String): Unit = write("acceptTask")
        override suspend fun patchTodo(id: String, title: String?, notes: String?, done: Boolean?): Todo = write("patchTodo")
        override suspend fun deleteTodo(id: String): Unit = write("deleteTodo")
        override suspend fun linkTodoAgent(todoId: String, machineId: String, sessionId: String): Unit = write("linkTodoAgent")
        override suspend fun unlinkTodoAgent(todoId: String, machineId: String, sessionId: String): Unit = write("unlinkTodoAgent")
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        val sent = mutableListOf<JsonObject>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean { sent += message; return false }
    }

    private fun render(
        task: Todo = todo,
        sessions: List<Session> = listOf(session),
        insideSheet: Boolean = true,
        style: AppStyle = AppStyle.Glass,
        dark: Boolean = false,
        outcome: String = "done",
    ) {
        val receipt = FeedItem("receipt", "main", at = "2026-11-10T02:00:00Z", kind = "receipt", ref = task.id,
            body = buildJsonObject { put("state", outcome); put("summary", "构建还没完成，请检查对话记录") })
        api = Api(task, receipt)
        connection = Connection()
        store = TasksStore(api, connection, CoroutineScope(Dispatchers.Unconfined))
        store.reloadTodos()
        store.load("main")
        host = AssistantTaskHost(store, "main", sessions, { "Mac mini" },
            { destination = it }, { error = it.message }, { error("unexpected send: $it") },
            sheet = mutableStateOf(if (insideSheet) TaskSheet.Task(task.id) else null),
            machines = listOf(Machine("mini", "Mac mini", online = false)))
        rule.setContent {
            PersonaTheme(darkTheme = dark, reduceMotion = true, style = style) {
                Column {
                    val target = destination
                    if (target != null) {
                        Text("目标对话 ${target.machineId}/${target.sessionId}")
                        Button(onClick = { destination = null }) { Text("返回助手") }
                    } else {
                        Text("助手原对话")
                        if (host.sheet.value == null) TaskLifecycleDetails(task, host)
                        AssistantTaskSheets(host)
                    }
                    error?.let { Text(it) }
                }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("看看").assertIsDisplayed()
    }

    private fun clickAndAssertTarget(expected: SessionKey = binding.key) {
        rule.onNodeWithText("看看").performClick()
        rule.onNodeWithText("目标对话 ${expected.machineId}/${expected.sessionId}").assertIsDisplayed()
        assertEquals(expected, destination)
        assertNull(host.sheet.value)
        assertReadOnly()
    }

    private fun assertReadOnly() {
        assertTrue("navigation mutated a task: ${api.writes}", api.writes.isEmpty())
        assertTrue("navigation sent protocol messages: ${connection.sent}", connection.sent.isEmpty())
        assertEquals(listOf(api.todo), store.todos.value)
        assertNull(store.routes.value["main"])
    }

    @Test fun stoppedReceiptLookOpensAgentWhileOpenChatIsSeparate() {
        render()
        rule.onNodeWithText("打开对话").assertIsDisplayed()
        clickAndAssertTarget()
    }

    @Test fun receiptOutsideDetailsOpensSheetThenLookOpensAgentAndReturnKeepsConversation() {
        render(insideSheet = false)
        rule.onNodeWithText("看看").performClick()
        rule.onNodeWithText("打开对话").assertIsDisplayed()
        assertEquals(TaskSheet.Task(todo.id), host.sheet.value)
        assertNull(destination)
        clickAndAssertTarget()
        rule.onNodeWithText("返回助手").performClick()
        rule.onNodeWithText("助手原对话").assertIsDisplayed()
        assertNull(destination)
        assertNull(host.sheet.value)
        assertReadOnly()
    }

    @Test fun offlineMachineAndSessionAbsentStillOpenDurableBinding() {
        render(sessions = emptyList())
        clickAndAssertTarget()
    }

    @Test fun multipleAgentsUseWaitingLeadInsteadOfFirstBinding() {
        val helper = TodoAgent("mini", "helper", title = "需要确认")
        val waiting = Session(machineId = "mini", sessionId = "helper", title = "需要确认", state = "needs_you")
        render(task = todo.copy(agents = listOf(binding, helper)), sessions = listOf(session, waiting))
        clickAndAssertTarget(helper.key)
    }

    @Test fun missingBindingDisplaysErrorAndKeepsDetails() {
        render(task = todo.copy(agents = emptyList()), sessions = emptyList())
        rule.onNodeWithText("看看").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("没有这个 Agent 了").assertIsDisplayed()
        assertNull("the inline notice must not depend on a global error surface", error)
        assertNull(destination)
        assertEquals(TaskSheet.Task(todo.id), host.sheet.value)
        assertReadOnly()
    }

    @Test fun releasedBindingCannotOpenPreviousExecution() {
        render(task = todo.copy(agents = listOf(binding.copy(releasedAt = "2026-11-10T03:00:00Z"))), sessions = listOf(session))
        rule.onNodeWithText("看看").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("没有这个 Agent 了").assertIsDisplayed()
        assertNull("the inline notice must not depend on a global error surface", error)
        assertNull(destination)
        assertReadOnly()
    }

    @Test fun dismissedBindingCannotOpenHiddenAgent() {
        render(task = todo.copy(agents = listOf(binding.copy(dismissedAt = "2026-11-10T03:00:00Z"))))
        rule.onNodeWithText("看看").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("没有这个 Agent 了").assertIsDisplayed()
        assertNull("the inline notice must not depend on a global error surface", error)
        assertNull(destination)
        assertEquals(TaskSheet.Task(todo.id), host.sheet.value)
        assertReadOnly()
    }

    @Test fun emptyBindingCannotNavigateToAnEmptySession() {
        render(task = todo.copy(agents = listOf(binding.copy(sessionId = ""))), sessions = emptyList())
        rule.onNodeWithText("看看").performClick()
        rule.waitForIdle()
        rule.onNodeWithText("没有这个 Agent 了").assertIsDisplayed()
        assertNull("the inline notice must not depend on a global error surface", error)
        assertNull(destination)
        assertEquals(TaskSheet.Task(todo.id), host.sheet.value)
        assertReadOnly()
    }

    @Test fun openingUnreadAgentDoesNotMarkTaskOrSessionRead() {
        val unread = session.copy(state = "unread")
        render(sessions = listOf(unread))
        clickAndAssertTarget()
        assertEquals(listOf(unread), host.sessions)
    }

    @Test fun interruptedReceiptLookOpensAgentWithoutContinuingIt() {
        val interrupted = session.copy(turnError = "Connection reset by peer")
        render(sessions = listOf(interrupted), outcome = "interrupted")
        rule.onNodeWithText("继续").assertIsDisplayed()
        rule.onNodeWithText("打开对话").assertIsDisplayed()
        clickAndAssertTarget()
        assertEquals(listOf(interrupted), host.sessions)
    }

    @Test fun completedTaskReceiptNavigationPreservesCompletion() {
        render(task = todo.copy(doneAt = "2026-11-10T03:00:00Z", doneBy = "owner"))
        clickAndAssertTarget()
        assertEquals("2026-11-10T03:00:00Z", store.todos.value.single().doneAt)
    }

    private fun screenshot(style: AppStyle, dark: Boolean) {
        render(style = style, dark = dark)
        val directory = File(System.getProperty("persona.shots") ?: "build/i18n-shots").also { it.mkdirs() }
        val name = "task-receipt-look-${style.name.lowercase()}-${if (dark) "dark" else "light"}.png"
        val bitmap = rule.onNode(isDialog()).captureToImage().asAndroidBitmap()
        File(directory, name).outputStream().use { check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        clickAndAssertTarget()
    }

    @Test fun receiptGlass() = screenshot(AppStyle.Glass, false)
    @Test fun receiptGlassDark() = screenshot(AppStyle.Glass, true)
    @Test fun receiptBrief() = screenshot(AppStyle.Brief, false)
    @Test fun receiptBriefDark() = screenshot(AppStyle.Brief, true)
    @Test fun receiptBoard() = screenshot(AppStyle.Board, false)
    @Test fun receiptBoardDark() = screenshot(AppStyle.Board, true)
}
