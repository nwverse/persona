package persona.feature.agents

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.*
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Before
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.answer.AnswerAction
import persona.core.domain.answer.AnswerDraft

/**
 * A password card's Run on the Agents page (#1581): the typed password goes to the hub once, in the answer, and the
 * page's notice ("Allowed: …"), which outlives the card, keeps no copy of it.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class AgentsSecretRunAnswerTest {
    private lateinit var model: AgentsViewModel
    private val request = Interaction("ix_secret", machineId = "m", sessionId = "s", kind = "permission", toolName = "secret_run",
        fields = listOf(InteractionField(key = "password", label = "Password", secret = true)))
    private val typed = "correct horse battery staple"
    private val sent = mutableListOf<Resolution>()
    private val preferences = AgentsPreferences(object : DataStore<Preferences> {
        override val data = MutableStateFlow<Preferences>(emptyPreferences())
        override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
            transform(data.value).also { data.value = it }
    }, object : HubPreferences {
        override val preferences = MutableStateFlow(HubPreferencesSnapshot())
        override suspend fun refresh() = Unit
        override suspend fun togglePin(path: String, space: Boolean) = Unit
        override suspend fun toggleMute(path: String, space: Boolean) = Unit
        override suspend fun markSeen(session: SessionKey, version: String) = Unit
    })

    @Before fun setMain() { Dispatchers.setMain(StandardTestDispatcher()) }
    @After fun tearDown() {
        if (::model.isInitialized) model.viewModelScope.cancel()
        Dispatchers.resetMain()
    }

    private fun TestScope.start() {
        model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(listOf(Session("m", "s", state = "needs_input")))
            override val machines = MutableStateFlow(listOf(Machine("m", online = true)))
            override suspend fun refresh() = Unit
        }, object : InteractionCenter {
            override val pending = MutableStateFlow(listOf(request))
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) { sent += resolution }
        }, object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }, object : MessageSender {
            override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }, preferences)
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { model.page.collect {} }
        runCurrent()
    }

    @Test fun runSendsThePasswordOnceAndTheNoticeKeepsNoCopy() = runTest {
        start()
        model.answer(request, AnswerAction.Allow, AnswerDraft(password = typed))
        runCurrent()
        assertEquals(listOf(Resolution(allow = true, values = mapOf("password" to typed))), sent)
        assertEquals(RequestDelivery.Sent, model.delivery.value[request.id])
        assertNotNull(model.notice.value)
        val notice = model.notice.value.toString()
        assertFalse(notice, notice.contains(typed))
    }
}
