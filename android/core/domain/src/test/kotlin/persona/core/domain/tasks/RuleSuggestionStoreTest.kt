package persona.core.domain.tasks

import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.FeedItem
import persona.core.contracts.FeedResponse
import persona.core.contracts.RuleScope
import persona.core.contracts.RuleSuggestion
import persona.core.contracts.RuleSuggestionMade
import persona.core.contracts.ServerEvent
import persona.core.contracts.TaskPlan
import persona.core.contracts.Todo
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubCodedFailure
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.RequestManager
import persona.core.contracts.api.TasksApi

/**
 * Always and Not now on a card's suggested rule (#4929, docs/assistant/unified-entry.md "A rule after the same answer three
 * times"): what Always made stays with the card it was tapped on, a stale suggestion only loses its line, and any other
 * refusal leaves the line to answer again.
 */
class RuleSuggestionStoreTest {
    private class Connection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.WaitingForNetwork)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    private class Refused(override val status: Int?) : RuntimeException("refused"), HubCodedFailure {
        override val code: String? = null
    }

    private class Api : TasksApi {
        val calls = mutableListOf<Triple<String, String, String?>>()
        var failure: Throwable? = null
        var gate: CompletableDeferred<Unit>? = null
        var made = RuleSuggestionMade("standing_authorization", RuleSuggestion.STANDING_AUTHORIZATION)
        var reads = 0
        override suspend fun ruleAlways(suggestionId: String): RuleSuggestionMade {
            calls += Triple("always", suggestionId, currentCoroutineContext()[RequestManager]?.id)
            gate?.await()
            failure?.let { throw it }
            return made
        }
        override suspend fun ruleNotNow(suggestionId: String) {
            calls += Triple("not-now", suggestionId, currentCoroutineContext()[RequestManager]?.id)
            failure?.let { throw it }
        }
        override suspend fun todos(): List<Todo> { reads++; return emptyList() }
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

    private val merge = RuleSuggestion("rs_merge", "standing_authorization", "You approved this 3 times. Always allow merging?",
        RuleScope("example-node", "Example node", "/code/example", "merge", listOf("ci_green", "no_conflicts"), "Merge the PR now? → Yes"),
        RuleSuggestion.STANDING_AUTHORIZATION, count = 3)

    @Test fun alwaysKeepsWhatWasMadeForTheCardItWasTappedOn() = runTest {
        val api = Api().apply { gate = CompletableDeferred() }
        val store = TasksStore(api, Connection(), backgroundScope)
        store.ruleAlways("todo:t1", merge, { throw it }, manager = "work")
        // one answer at a time: a second tap and Not now wait for the first
        store.ruleAlways("todo:t1", merge, { throw it }, manager = "work")
        store.ruleNotNow(merge, { throw it }, manager = "work")
        runCurrent()
        assertEquals(listOf(Triple("always", "rs_merge", "work")), api.calls)
        assertTrue(ruleSuggestionKey("rs_merge") in store.busy.value)
        assertNull(store.ruleOutcomes.value["rs_merge"])
        api.gate!!.complete(Unit); runCurrent()
        assertEquals(RuleOutcome.Made(api.made), store.ruleOutcomes.value["rs_merge"])
        assertEquals(merge, store.ruleCards.value["todo:t1"])
        assertTrue(store.busy.value.isEmpty())
        // the to-dos are read again: the hub words the decision without its line now
        assertEquals(1, api.reads)
        // answered: neither action is sent again
        store.ruleAlways("todo:t1", merge, { throw it })
        store.ruleNotNow(merge, { throw it })
        runCurrent()
        assertEquals(1, api.calls.size)
    }

    @Test fun notNowPutsTheLineAwayWithoutARule() = runTest {
        val api = Api()
        val store = TasksStore(api, Connection(), backgroundScope)
        store.ruleNotNow(merge, { throw it }); runCurrent()
        assertEquals(listOf(Triple("not-now", "rs_merge", null as String?)), api.calls)
        assertEquals(RuleOutcome.NotNow, store.ruleOutcomes.value["rs_merge"])
        assertTrue(store.ruleCards.value.isEmpty())
    }

    @Test fun aStaleSuggestionIsClearedAndIsNotAnError() = runTest {
        for (action in listOf("always", "not-now")) {
            val api = Api().apply { failure = IllegalStateException("wrapped", Refused(404)) }
            val store = TasksStore(api, Connection(), backgroundScope)
            val errors = mutableListOf<Throwable>()
            if (action == "always") store.ruleAlways("plan:p1", merge, { errors += it }) else store.ruleNotNow(merge, { errors += it })
            runCurrent()
            assertTrue("$action reported a stale suggestion as an error", errors.isEmpty())
            assertEquals(RuleOutcome.Gone, store.ruleOutcomes.value["rs_merge"])
            assertTrue(store.ruleCards.value.isEmpty())
            assertTrue(store.busy.value.isEmpty())
        }
    }

    @Test fun anyOtherRefusalLeavesTheLineToAnswerAgain() = runTest {
        val api = Api()
        val store = TasksStore(api, Connection(), backgroundScope)
        val errors = mutableListOf<Throwable>()
        // the standing authorizations changed meanwhile (409), no full access (403), storage unavailable (503)
        for (status in listOf(409, 403, 503)) {
            api.failure = Refused(status)
            store.ruleAlways("card:q1", merge, { errors += it }); runCurrent()
            assertNull(store.ruleOutcomes.value["rs_merge"])
            assertTrue(store.ruleCards.value.isEmpty())
            assertTrue(store.busy.value.isEmpty())
        }
        assertEquals(listOf(409, 403, 503), errors.map { (it as Refused).status })
        api.failure = null
        store.ruleAlways("card:q1", merge, { throw it }); runCurrent()
        assertEquals(RuleOutcome.Made(api.made), store.ruleOutcomes.value["rs_merge"])
        assertEquals(4, api.calls.size)
    }

    @Test fun aSuggestionWithoutAnIdIsNeverSent() = runTest {
        val api = Api()
        val store = TasksStore(api, Connection(), backgroundScope)
        store.ruleAlways("plan:p1", RuleSuggestion(""), { throw it })
        store.ruleNotNow(RuleSuggestion(" "), { throw it })
        runCurrent()
        assertTrue(api.calls.isEmpty())
    }
}
