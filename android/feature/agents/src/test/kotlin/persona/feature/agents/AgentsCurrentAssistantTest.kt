package persona.feature.agents

import androidx.activity.ComponentActivity
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.test.junit4.createAndroidComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.navigation3.runtime.NavKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.LocalManagerRoster
import persona.core.designsystem.PersonaTheme
import persona.core.navigation.AppTab
import persona.core.navigation.Navigator

/**
 * The Agents page lists the current assistant's agents and computers (docs/assistant/managers.md "Clients"): the hub's
 * lists are every manager's, each row with its own; no stamp is the default's. Switching lists the other one's, and
 * the cards that go with them.
 */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35], qualifiers = "en")
class AgentsCurrentAssistantTest {
    @get:Rule val rule = createAndroidComposeRule<ComponentActivity>()
    @After fun reset() = AssistantManagerChoice.reset()

    private val roster = ManagerRoster(listOf(
        HubManager("assistant", "Assistant", online = true, isDefault = true, builtin = true),
        HubManager("m-2", "Work", online = true),
    ), "assistant")

    @Test fun thePageListsTheCurrentAssistantsAgentsAndCards() {
        val stored = MutableStateFlow<Preferences>(emptyPreferences())
        val requests = MutableStateFlow(listOf(
            Interaction("c1", "box", "s1", kind = "question"), // its session's manager: the default's
            Interaction("c2", "laptop", "s2", kind = "question", manager = "m-2"),
        ))
        val model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(listOf(
                Session("box", "s1", title = "Example task", state = "working"),
                Session("laptop", "s2", title = "Work task", state = "working", manager = "m-2"),
                // another group's computers are no manager's of this hub: on every assistant's list
                Session("far~g0123abcd", "s3", title = "Elsewhere task", state = "working", manager = "assistant"),
            ))
            override val machines = MutableStateFlow(listOf(
                Machine("box", "Example computer", online = true),
                Machine("laptop", "Work computer", online = true, manager = "m-2"),
                Machine("far~g0123abcd", "Elsewhere computer", online = true, manager = "assistant"),
            ))
            override suspend fun refresh() = Unit
        }, object : InteractionCenter {
            override val pending = requests
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
        }, AgentsPreferences(object : DataStore<Preferences> {
            override val data = stored
            override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
                transform(stored.value).also { stored.value = it }
        }, object : HubPreferences {
            override val preferences = MutableStateFlow(HubPreferencesSnapshot())
            override suspend fun refresh() = Unit
            override suspend fun togglePin(path: String, space: Boolean) = Unit
            override suspend fun toggleMute(path: String, space: Boolean) = Unit
            override suspend fun markSeen(session: SessionKey, version: String) = Unit
        }))
        AssistantManagerChoice.choose("hub", null, "assistant")
        rule.setContent {
            PersonaTheme(darkTheme = false, reduceMotion = true) {
                CompositionLocalProvider(LocalManagerRoster provides roster) { AgentsScreen(model, Nowhere, AgentsMetadata()) }
            }
        }
        fun ids() = model.page.value.sessions.map { it.sessionId }.sorted()
        fun shown(text: String) = rule.onAllNodesWithText(text, substring = true, useUnmergedTree = true).fetchSemanticsNodes().isNotEmpty()
        rule.waitUntil(5_000) { model.page.value.loaded && ids() == listOf("s1", "s3") }
        assertEquals(listOf("box", "far~g0123abcd"), model.page.value.machines.map { it.id }.sorted())
        rule.waitForIdle()
        // the list draws its computers (their sessions fold under them)
        assertTrue(shown("Example computer"))
        assertFalse(shown("Work computer"))

        rule.runOnIdle { AssistantManagerChoice.pick("m-2") }
        rule.waitUntil(5_000) { ids() == listOf("s2", "s3") }
        assertEquals(listOf("far~g0123abcd", "laptop"), model.page.value.machines.map { it.id }.sorted())
        rule.waitForIdle()
        assertTrue(shown("Work computer"))
        assertFalse(shown("Example computer"))

        // Back on the default: an unstamped row is its, and so is one of a manager the roster no longer lists.
        rule.runOnIdle { AssistantManagerChoice.pick(null) }
        rule.waitUntil(5_000) { ids() == listOf("s1", "s3") }
    }

    private object Nowhere : Navigator {
        override fun navigate(key: NavKey) = Unit
        override fun back() = Unit
        override fun selectTab(tab: AppTab) = Unit
    }
}
