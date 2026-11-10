package persona.core.domain.tasks

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*

class TaskAcceptanceTest {
    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }
    private class Api(var rows: List<Todo>) : TasksApi {
        var delayed: CompletableDeferred<List<Todo>>? = null
        val accepted = mutableListOf<Pair<String, String>>()
        override suspend fun todos(): List<Todo> = delayed?.let { held -> delayed = null; held.await() } ?: rows
        override suspend fun acceptTask(todoId: String, askedAt: String) { accepted += todoId to askedAt }
        override suspend fun feed(conversation: String, since: String?) = FeedResponse()
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    @Test fun acceptedRoundCannotResurrectFromDelayedReadButANewAskSurvives() = runTest {
        val original = Todo("task", origin = "main", ask = TodoAsk(verdict = "Check the deployed page", askedAt = "2026-11-01T09:00:00Z"))
        val api = Api(listOf(original))
        val store = TasksStore(api, Connection(), backgroundScope)
        store.reloadTodos(); runCurrent()
        val held = CompletableDeferred<List<Todo>>()
        api.delayed = held
        store.reloadTodos(); runCurrent()
        var changed = 0
        store.acceptTask(original, { throw it }) { changed++ }; runCurrent()
        assertEquals(listOf(original.id to original.ask!!.askedAt), api.accepted)
        assertNull(store.todos.value.single().ask)
        held.complete(listOf(original)); runCurrent()
        assertNull(store.todos.value.single().ask)
        val latest = original.copy(ask = original.ask!!.copy(askedAt = "2026-11-02T09:00:00Z", verdict = "Check the new release"))
        api.rows = listOf(latest)
        store.reloadTodos(); runCurrent()
        assertEquals(latest.ask, store.todos.value.single().ask)
        store.acceptTask(original, { throw it }) { changed++ }; runCurrent()
        assertEquals(1, changed)
        assertEquals(1, api.accepted.size)
    }
}
