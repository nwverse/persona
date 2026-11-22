package persona.core.domain.tasks

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.*
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
class TaskLaunchReconciliationTest {
    private val key = SessionKey("box", "original")
    private val starting = Todo("task", origin = "main")
    private val linked = starting.copy(agents = listOf(TodoAgent("box", "original", title = "Original agent", cwd = "/code/app")))
    private val running = Session("box", "original", state = "working", rev = 12)

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    private class Api : TasksApi {
        var list = emptyList<Todo>()
        var read: (suspend () -> List<Todo>)? = null
        var agent: Session? = null
        var lookup: (suspend () -> Session?)? = null
        var retries = 0
        var retryGate: CompletableDeferred<Unit>? = null
        override suspend fun todos() = read?.invoke() ?: list
        override suspend fun taskAgent(key: SessionKey) = lookup?.invoke() ?: agent
        override suspend fun retryTask(todoId: String): Todo { retries++; retryGate?.await(); return list.single() }
        override suspend fun feed(conversation: String, since: String?) = FeedResponse(emptyList())
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    @Test fun aDurableBindingOpensTheOriginalAgentOutsideTheFilteredWindow() = runTest {
        val store = TasksStore(Api(), Connection(), backgroundScope)
        val unrelated = listOf(Session("other", "row", state = "working"))
        assertEquals(key, store.lead(linked, unrelated)?.key)
        assertEquals(TaskState.Followup, store.stateOf(linked, unrelated))
        assertEquals("offline", store.lead(linked, unrelated)?.state)
        assertEquals(TaskState.NotStarted, store.stateOf(starting, unrelated))
        assertTrue(store.agentsOf(linked.copy(agents = linked.agents.map { it.copy(dismissedAt = "2026-01-01T00:00:00Z") }), unrelated).isEmpty())
    }

    @Test fun aWaitingReplacementKeepsAnOldInterruptionOutOfTaskState() = runTest {
        val store = TasksStore(Api(), Connection(), backgroundScope)
        val old = Session("box", "original", state = "unread", turnError = "opaque account transition 7A99",
            turnErrorKind = "unrecoverable", turnErrorCode = "compaction_sealed")
        val task = starting.copy(agents = listOf(
            TodoAgent("box", "original"),
            TodoAgent("box", "replacement"),
        ))
        for (on in listOf("agent", "external", "transient")) {
            val replacement = Session("box", "replacement", state = "idle", work = WorkState(phase = "blocked", on = on))
            assertTrue(on, replacement.waitsWithoutBackground())
            assertNotEquals(on, TaskState.Interrupted, store.stateOf(task, listOf(old, replacement)))
        }
    }

    @Test fun aTransientWaitingReplacementWithTurnErrorKeepsAnOldInterruptionOutOfTaskState() = runTest {
        val store = TasksStore(Api(), Connection(), backgroundScope)
        val old = Session("box", "original", state = "idle", turnError = "opaque account transition 7A99",
            turnErrorKind = "unrecoverable", turnErrorCode = "compaction_sealed")
        val replacement = Session("box", "replacement", state = "idle",
            turnError = "API Error: 503 Service Unavailable", turnErrorKind = "transient",
            work = WorkState(phase = "blocked", on = "transient"))
        val task = starting.copy(agents = listOf(
            TodoAgent("box", "original"),
            TodoAgent("box", "replacement"),
        ))
        assertTrue(replacement.waitsWithoutBackground())
        assertEquals(TaskState.Followup, store.stateOf(task, listOf(old, replacement)))
    }

    @Test fun anInvalidAgentWithStaleTransientWorkRemainsInterruptedInTaskState() = runTest {
        val store = TasksStore(Api(), Connection(), backgroundScope)
        val invalid = Session("box", "original", state = "idle", turnError = "opaque account transition 7A99",
            turnErrorKind = "unrecoverable", turnErrorCode = "compaction_sealed",
            work = WorkState(phase = "blocked", on = "transient"))
        assertEquals(TaskState.Interrupted, store.stateOf(linked, listOf(invalid)))
    }

    @Test fun delayedAcknowledgementAndReconnectRecoverTheExistingBinding() = runTest {
        val api = Api().apply { list = listOf(starting) }
        val connection = Connection()
        val store = TasksStore(api, connection, backgroundScope)
        store.reloadTodos(); runCurrent()
        assertEquals(TaskState.NotStarted, store.stateOf(store.todos.value.single(), emptyList()))
        api.list = listOf(linked); api.agent = running
        connection.events.emit(TodosEvent()); runCurrent()
        store.watchAgents(setOf(key)); runCurrent()
        assertEquals(key, store.lead(store.todos.value.single(), emptyList())?.key)
        assertEquals(TaskState.Working, store.stateOf(store.todos.value.single(), emptyList()))
        // The event while disconnected is missed; Online must fetch the reopened row and current metadata.
        api.list = listOf(linked.copy(doneAt = "2026-01-01T00:00:00Z"))
        store.reloadTodos(); runCurrent()
        assertEquals(TaskState.Done, store.stateOf(store.todos.value.single(), emptyList()))
        api.list = listOf(linked)
        connection.state.value = ConnectionState.Online; runCurrent()
        assertEquals(TaskState.Working, store.stateOf(store.todos.value.single(), emptyList()))
        assertEquals(0, api.retries)
    }

    @Test fun concurrentInvalidationsCannotPersistAnOldUnboundOrCompletedSnapshotLast() = runTest {
        for (old in listOf(starting, linked.copy(doneAt = "2026-01-01T00:00:00Z"))) {
            val gate = CompletableDeferred<Unit>()
            var calls = 0
            var active = 0
            var maximum = 0
            var persisted = emptyList<Todo>()
            val cache = object : TodoCache {
                override suspend fun read(): List<Todo>? = null
                override suspend fun write(todos: List<Todo>) { persisted = todos }
            }
            val api = Api().apply { read = {
                val call = ++calls
                maximum = maxOf(maximum, ++active)
                if (call == 1) gate.await()
                active--
                if (call == 1) listOf(old) else listOf(linked)
            } }
            val store = TasksStore(api, Connection(), backgroundScope, cache)
            store.reloadTodos(); runCurrent()
            store.reloadTodos(); runCurrent()
            assertEquals(1, calls)
            gate.complete(Unit); runCurrent()
            assertEquals(1, maximum)
            assertEquals(linked, store.todos.value.single())
            assertEquals(linked, persisted.single())
        }
    }

    @Test fun failedOrTimedOutStartsAreRetryableAndRepeatedTapsSendOneRequest() = runTest {
        for (reason in listOf("launcher missing", "context deadline exceeded")) {
            val failed = starting.copy(doneAt = "2026-01-01T00:00:00Z", startError = reason)
            val gate = CompletableDeferred<Unit>()
            val api = Api().apply { list = listOf(starting); retryGate = gate }
            val store = TasksStore(api, Connection(), backgroundScope)
            assertEquals(TaskState.Failed, store.stateOf(failed, emptyList()))
            store.retryTask(failed) { throw it }; store.retryTask(failed) { throw it }; runCurrent()
            assertEquals(1, api.retries)
            gate.complete(Unit); runCurrent()
        }
    }

    @Test fun aLookupFailureKeepsTheOriginalOpenTargetWithoutPretendingItIsStillWorking() = runTest {
        val api = Api().apply { agent = running }
        val connection = Connection()
        val store = TasksStore(api, connection, backgroundScope)
        store.watchAgents(setOf(key)); runCurrent()
        api.lookup = { throw IllegalStateException("offline") }
        connection.events.emit(SessionsListEvent("r2")); runCurrent()
        assertEquals(TaskState.Followup, store.stateOf(linked, emptyList()))
        assertEquals(key, store.lead(linked, emptyList())?.key)
        assertEquals("offline", store.agentLookupFailures.value[key]?.message)
        assertEquals(0, api.retries)
    }

    @Test fun lookupInvalidationsRefreshWithoutChangingBoundIdentityAndRejectLateUnwatchedRows() = runTest {
        val api = Api().apply { agent = running }
        val connection = Connection()
        val store = TasksStore(api, connection, backgroundScope)
        store.watchAgents(setOf(key)); runCurrent()
        assertEquals(TaskState.Working, store.stateOf(linked, emptyList()))
        api.agent = running.copy(state = "needs_you", rev = 13)
        connection.events.emit(SessionsListEvent("r13")); runCurrent()
        assertEquals(TaskState.NeedsYou, store.stateOf(linked, emptyList()))
        val gate = CompletableDeferred<Session?>()
        api.lookup = { gate.await() }
        connection.events.emit(SessionsListEvent("r14")); runCurrent()
        store.watchAgents(emptySet())
        gate.complete(running); runCurrent()
        assertTrue(store.agentSessions.value.isEmpty())
    }
}
