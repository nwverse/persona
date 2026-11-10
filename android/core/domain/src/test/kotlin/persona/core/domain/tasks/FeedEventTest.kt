package persona.core.domain.tasks

import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.buildJsonObject
import org.junit.Assert.assertEquals
import org.junit.Test
import persona.core.contracts.AssistantFeedEvent
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.ServerEvent
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.TasksApi

/** A live assistant_feed item for a conversation not loaded yet must not stand in for its history (#4689). */
class FeedEventTest {
    private fun item(id: String, at: String) = FeedItem(id, "c1", at, "note", "example:1", buildJsonObject {})

    private class Api(val items: List<FeedItem>) : TasksApi {
        val asked = mutableListOf<String?>()
        var taskCards: List<persona.core.contracts.AssistantTaskCard>? = null
        override suspend fun todos() = emptyList<Todo>()
        override suspend fun feed(conversation: String, since: String?): FeedResponse {
            asked += since
            return FeedResponse(items.filter { item -> since?.let { item.at.orEmpty() > it } ?: true }, taskCards = taskCards)
        }
        override suspend fun startPlan(id: String, remember: Boolean): TaskPlan = error("unused")
        override suspend fun dismissPlan(id: String): TaskPlan = error("unused")
        override suspend fun retryTask(todoId: String): Todo = error("unused")
        override suspend fun removeTask(todoId: String) = Unit
        override suspend fun route(conversation: String, todoId: String, text: String): FeedItem = error("unused")
        override suspend fun today(conversation: String): FeedItem = error("unused")
        override suspend fun mainConversation(): String? = null
        override suspend fun inboxReply(itemId: String, text: String, card: String?) = Unit
    }

    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject): Boolean = true
    }

    @Test fun aLiveItemBeforeTheFirstLoadStillPullsTheWholeFeed() = runBlocking {
        val older = item("a1", "2026-11-04T09:00:00Z")
        val live = item("a2", "2026-11-04T10:00:00Z")
        val api = Api(listOf(older, live))
        val connection = Connection()
        val store = TasksStore(api, connection, CoroutineScope(Dispatchers.Unconfined))
        connection.events.emit(AssistantFeedEvent("c1", live))
        store.load("c1")
        assertEquals(listOf<String?>(null), api.asked)
        assertEquals(listOf("a1", "a2"), store.feeds.value["c1"]?.map { it.id })
        store.load("c1") // loaded once: later loads only ask for what came after
        assertEquals(listOf(null, "2026-11-04T10:00:00Z"), api.asked)
    }

    @Test fun taskCardUpsertsSurviveEmptyFailedAndOlderIncrementalReads() {
        val api = Api(emptyList())
        val scope = CoroutineScope(kotlinx.coroutines.SupervisorJob() + Dispatchers.Unconfined)
        val store = TasksStore(api, Connection(), scope)
        val card = persona.core.contracts.AssistantTaskCard("plan:p:0", "t", "p", 0,
            at = "2026-11-10T10:00:00Z", updatedAt = "2026-11-10T10:01:00Z", state = "working")
        try {
            api.taskCards = listOf(card)
            store.load("c1")
            assertEquals(listOf(card), store.taskCards.value["c1"])
            val accepted = card.copy(state = "accepted", updatedAt = "2026-11-10T10:01:00.500Z")
            api.taskCards = listOf(accepted)
            store.load("c1")
            api.taskCards = emptyList()
            store.load("c1")
            api.taskCards = null
            store.load("c1")
            api.taskCards = listOf(card)
            store.load("c1")
            assertEquals(listOf(accepted), store.taskCards.value["c1"])
            assertEquals(null, store.taskCards.value["other"])
        } finally { scope.coroutineContext[kotlinx.coroutines.Job]?.cancel() }
    }

    /**
     * Another manager's live updates (the hub stamps `manager` on them, docs/protocol/protocol.md) reach their conversation
     * whichever manager the assistant page shows: its conversation may be open elsewhere (docs/assistant/managers.md).
     */
    @Test fun anotherManagersLiveUpdatesReachTheirConversation() = runBlocking {
        val api = Api(emptyList())
        val connection = Connection()
        val store = TasksStore(api, connection, CoroutineScope(Dispatchers.Unconfined))
        try {
            persona.core.contracts.api.AssistantManagerChoice.choose("hub", null, "assistant")
            val feed = ServerEvent.decode(
                """{"type":"assistant_feed","manager":"m-2","conversation":"c9","item":{"id":"b1","conversation":"c9","at":"2026-11-05T08:00:00Z","kind":"note","ref":"x"}}""")
            val plan = ServerEvent.decode("""{"type":"task_plan","manager":"m-2","plan":{"id":"p9","conversation":"c9","tasks":[]}}""")
            connection.events.emit(requireNotNull(feed))
            connection.events.emit(requireNotNull(plan))
            assertEquals(listOf("b1"), store.feeds.value["c9"]?.map { it.id })
            assertEquals(setOf("p9"), store.plans.value.keys)
        } finally { persona.core.contracts.api.AssistantManagerChoice.reset() }
    }
}
