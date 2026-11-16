package persona.feature.agents

import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.setMain
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Assert.assertNull
import org.junit.Before
import org.junit.Test
import persona.core.contracts.api.ConnectionState
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.InteractionCenter
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.SessionsRepository
import persona.core.contracts.Interaction
import persona.core.contracts.Machine
import persona.core.contracts.Resolution
import persona.core.contracts.ServerEvent
import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.domain.sort.AgentGrouping
import persona.core.domain.sort.AgentSession

/** The shell's visible Agent chat drives iOS's held row, not a preference or a second navigation state. */
@OptIn(ExperimentalCoroutinesApi::class)
class AgentsHeldInListTest {
    private val dispatcher: CoroutineDispatcher = UnconfinedTestDispatcher()
    private var model: AgentsViewModel? = null
    private val sessions = MutableStateFlow(listOf(session("held", "unread"), session("working", "working"), session("seen", "idle")))
    private val visible = MutableStateFlow<SessionKey?>(null)

    @Before fun setMain() { Dispatchers.setMain(dispatcher) }
    @After fun tearDown() {
        model?.let { current -> runBlocking {
            withTimeout(10_000) { current.viewModelScope.coroutineContext[Job]?.cancelAndJoin() }
        } }
        Dispatchers.resetMain()
    }

    @Test fun openingAnUnreadChatHoldsItQuietUpdatesThenLeavingReleasesIt() = runBlocking {
        model = model()
        awaitUnread("machine/held")

        visible.value = SessionKey("machine", "held")
        awaitUnread("machine/held")
        sessions.value = sessions.value.map { if (it.sessionId == "held") it.copy(state = "idle") else it }
        awaitUnread("machine/held")

        visible.value = null
        awaitVisible("state/working", "machine/working")
        awaitIdle("machine/held", "machine/seen")
    }

    @Test fun switchingChatsReleasesTheOldHoldAndSeenChatsNeverAcquireOne() = runBlocking {
        model = model()
        visible.value = SessionKey("machine", "seen")
        awaitUnread("machine/held")

        sessions.value = sessions.value.map { if (it.sessionId == "held") it.copy(state = "idle") else it }
        awaitIdle("machine/held", "machine/seen")
        visible.value = SessionKey("machine", "held")
        awaitIdle("machine/held", "machine/seen")

        sessions.value = sessions.value.map { if (it.sessionId == "held") it.copy(state = "unread") else it }
        awaitUnread("machine/held")
        awaitVisible("state/idle")
        visible.value = SessionKey("machine", "working")
        awaitUnread("machine/held")
        awaitVisible("state/idle")
    }

    @Test fun lifecycleFunctionDoesNotRelatchAnAlreadySeenOpenChat() {
        val unreadRows = listOf(AgentSession(session("held", "unread")))
        val quietRows = listOf(AgentSession(session("held", "idle")))
        assertNull(heldInListKey(unreadRows, null, null))
        assertEquals("machine/held", heldInListKey(unreadRows, "machine/held", null))
        assertEquals("machine/held", heldInListKey(quietRows, "machine/held", "machine/held"))
        assertNull(heldInListKey(quietRows, "machine/held", null))
        assertNull(heldInListKey(unreadRows, null, "machine/held"))
        assertNull(heldInListKey(unreadRows, "machine/other", "machine/held"))
    }

    private fun session(id: String, state: String) = Session("machine", id, title = "Example $id", state = state,
        cwd = "/home/user/project", spaceId = "space", spaceName = "Example project")

    private fun model(): AgentsViewModel = AgentsViewModel(object : SessionsRepository {
        override val sessions = this@AgentsHeldInListTest.sessions
        override val machines = MutableStateFlow(listOf(Machine("machine", "Example computer", online = true)))
        override suspend fun refresh() = Unit
    }, object : InteractionCenter {
        override val pending = MutableStateFlow<List<Interaction>>(emptyList())
        override suspend fun refresh() = Unit
        override suspend fun resolve(id: String, resolution: Resolution) = Unit
    }, object : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }, object : MessageSender {
        override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
    }, synchronousPreferences(), visibleChat = visible).also { model = it }

    private fun synchronousPreferences(): AgentsPreferences {
        val store = object : androidx.datastore.core.DataStore<androidx.datastore.preferences.core.Preferences> {
            override val data = MutableStateFlow(androidx.datastore.preferences.core.emptyPreferences())
            override suspend fun updateData(transform: suspend (androidx.datastore.preferences.core.Preferences) -> androidx.datastore.preferences.core.Preferences) =
                transform(data.value).also { data.value = it }
        }
        val hub = object : persona.core.contracts.api.HubPreferences {
            override val preferences = MutableStateFlow(persona.core.contracts.api.HubPreferencesSnapshot())
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }
        return AgentsPreferences(store, hub).also { runBlocking {
            it.grouping(AgentGrouping.State)
            it.range("", AgentRange.All)
        } }
    }

    private suspend fun awaitUnread(id: String) {
        awaitVisible("state/unread", id)
        awaitVisible("state/working", "machine/working")
        awaitIdle("machine/seen")
    }

    private suspend fun awaitIdle(vararg ids: String) {
        val matched = withTimeoutOrNull(10_000) {
            model!!.page.first { page ->
                val group = page.list.groups.singleOrNull { it.id == "state/idle" }
                group?.visibleRows?.isEmpty() == true && group.rows.map { it.id } == ids.toList()
            }
        }
        assertTrue("idle expected $ids; actual " + model!!.page.value.list.groups.map {
            "${it.id}: rows=${it.rows.map(AgentSession::id)}, visible=${it.visibleRows.map(AgentSession::id)}"
        }, matched != null)
    }

    private suspend fun awaitVisible(section: String, vararg ids: String) {
        val expected = ids.toList()
        val matched = withTimeoutOrNull(10_000) {
            model!!.page.first { page ->
                page.list.groups.singleOrNull { it.id == section }?.visibleRows?.map { it.id } == expected
            }
        }
        assertTrue("$section expected $expected; actual " + model!!.page.value.list.groups.map {
            "${it.id}: rows=${it.rows.map(AgentSession::id)}, visible=${it.visibleRows.map(AgentSession::id)}"
        }, matched != null)
    }
}
